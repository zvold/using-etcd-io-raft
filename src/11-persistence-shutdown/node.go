package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/rpc"
	"time"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	// Rand instance for choosing the log compaction threshold.
	r = rand.New(rand.NewSource(time.Now().UnixNano()))

	// Threshold for initiating log compaction.
	compactN = r.Intn(20) + 10
)

// node extends raft.Node by keeping track of its storage and committed entries.
type node struct {
	raft.Node

	ticker    *time.Ticker        // Periodic "ticks" to drive the node forward.
	stop      chan struct{}       // Signals node handling gorouting to stop.
	storage   *raft.MemoryStorage // In-memory storage for raft log entries.
	fsmState  *FsmState           // FSM state: the sum of committed "command" entries.
	confState *raftpb.ConfState   // Stores ConfState returned by ApplyConfChange().
	listener  net.Listener        // Listener for the RPC server, so we can close it.

	toAppend chan raftpb.Message // "Append" channel for async. storage writes.
	toApply  chan raftpb.Message // "Apply" channel for async. storage writes.
}

// Convenience method for getting node id.
func (n *node) id() uint64 {
	return n.Status().ID
}

// Convenience method for getting node state.
func (n *node) state() raft.StateType {
	return n.Status().RaftState
}

// Pretty-print the node.
func (n *node) String() string {
	return fmt.Sprintf("Node %d: %+v\n\t%s", n.id(), n.Status(), n.fsmState)
}

// Returns the port on which underlying RPC server listens.
func (n *node) port() int {
	return n.listener.Addr().(*net.TCPAddr).Port
}

// Close() the underlying net.Listener and Stop() the raft.Node.
// Persist MemoryStorage to file '.memory-N', where N = node id.
func (n *node) shutdown() {
	log.Printf("Node %d: shutting down, memory persisted to '.memory-%d'.", n.id(), n.id())
	writeBlob(toBlob(n.storage), n.id())

	// Close the underlying listener of the HTTP server handling RPCs.
	n.listener.Close()
	n.Stop()
}

// Create the raft node and start node handling goroutines.
func newNode(id uint64, port int, peers []raft.Peer, restart bool, stop chan struct{}) *node {
	storage := raft.NewMemoryStorage()
	config := raft.Config{
		ID:                 id,
		ElectionTick:       10,
		HeartbeatTick:      1,
		Storage:            storage,
		MaxSizePerMsg:      4096,
		MaxInflightMsgs:    256,
		AsyncStorageWrites: true,
	}
	var n node
	if restart {
		// For a new node joining the cluster, RestartNode() is used.
		n = node{
			Node:     raft.RestartNode(&config),
			ticker:   time.NewTicker(100 * time.Millisecond),
			stop:     stop,
			storage:  storage,
			fsmState: NewFsmState(),
			toAppend: make(chan raftpb.Message),
			toApply:  make(chan raftpb.Message),
		}
	} else {
		n = node{
			Node:     raft.StartNode(&config, peers),
			ticker:   time.NewTicker(100 * time.Millisecond),
			stop:     stop,
			storage:  storage,
			fsmState: NewFsmState(),
			toAppend: make(chan raftpb.Message),
			toApply:  make(chan raftpb.Message),
		}
	}
	n.listener = startRpcServer(&n, port)

	go n.runNode()
	go n.handleApplyThread()
	go n.handleAppendThread()
	return &n
}

// Restart raft node from persisted Storage and start node handling goroutines.
func recoverNode(id uint64, port int, storage *raft.MemoryStorage, stop chan struct{}) *node {
	fsm := NewFsmState()
	s, err := storage.Snapshot()
	if err != nil {
		log.Fatalf("Cannot restore FSM from persisted storage (%s).", err)
	}
	fsm.decode(s.Data)

	config := raft.Config{
		ID:                 id,
		ElectionTick:       10,
		HeartbeatTick:      1,
		Storage:            storage,
		MaxSizePerMsg:      4096,
		MaxInflightMsgs:    256,
		AsyncStorageWrites: true,
	}
	n := node{
		Node:     raft.RestartNode(&config),
		ticker:   time.NewTicker(100 * time.Millisecond),
		stop:     stop,
		storage:  storage,
		fsmState: fsm,
		toAppend: make(chan raftpb.Message),
		toApply:  make(chan raftpb.Message),
	}
	n.listener = startRpcServer(&n, port)

	go n.runNode()
	go n.handleApplyThread()
	go n.handleAppendThread()
	return &n
}

func (n *node) saveToStorage(m *raftpb.Message) {
	// Note: Snapshot has to be applied before Entries, otherwise Append() might
	// fail due to a "gap" between LastIndex() and "first index" of the new entries.
	if m.Snapshot != nil && len(m.Snapshot.Data) != 0 {
		// ApplySnapshot() is no-op for Snapshots older than storage.Snapshot().
		err := n.storage.ApplySnapshot(*m.Snapshot)
		if err != raft.ErrSnapOutOfDate {
			// Note that we modify the FSM state here as well.
			n.fsmState.decode(m.Snapshot.Data)
			fmt.Printf("Node %d: (R)\n\trestored fsm state = %s.\n", n.id(), n.fsmState)
		}
	}
	hs := raftpb.HardState{Term: m.Term, Vote: m.Vote, Commit: m.Commit}
	if !raft.IsEmptyHardState(hs) {
		n.storage.SetHardState(hs)
	}
	n.storage.Append(m.Entries)

	b := toBlob(n.storage)
	fmt.Printf("Modified blob:\n%s\n", b.Verbose())
}

// Note: this makes extra calls for messages involving snapshots. Quote:
// "If any Message has type MsgSnap, call Node.ReportSnapshot() after it has been sent"
func (n *node) send(msgs []raftpb.Message) {
	for _, m := range msgs {
		if m.To == n.id() {
			// Message to self.
			n.Step(context.TODO(), m)
			if m.Snapshot != nil {
				// TODO(zvold): SnapshotFinish is a no-op, make sure to report errors properly instead.
				n.ReportSnapshot(m.To, raft.SnapshotFinish)
			}
			continue
		}

		// Look up target node address in the FSM state.
		addr := n.fsmState.getPeer(m.To)
		if addr == "" {
			// We don't know the target node address.
			// Select some known target and relay the message through it.
			for k, v := range n.fsmState.peers() {
				// Relaying through yourself won't work - we don't know the address.
				// Relaying through sender is also likely to fail.
				if k != int(n.id()) && k != int(m.From) {
					addr = v
					fmt.Printf("Node %d: unknown destination node %d, relaying via node %d\n",
						n.id(), m.To, k)
					break
				}
			}
		}

		client, err := rpc.DialHTTP("tcp", addr)
		if err != nil {
			fmt.Printf("Node %d: cannot connect to '%s' (%s), ignoring.\n", n.id(), addr, err)
			if m.Snapshot != nil {
				n.ReportSnapshot(m.To, raft.SnapshotFailure)
			}
			continue
		}
		defer client.Close()

		var reply struct{}
		err = client.Call("NodeRpc.ProcessMessage", m, &reply)
		if err != nil {
			fmt.Printf("Node %d: message delivery failure: %s.\n", n.id(), err)
			if m.Snapshot != nil {
				n.ReportSnapshot(m.To, raft.SnapshotFailure)
			}
		}
		// TODO(zvold): reply should indicate if recepient couldn't process the message
		// for example a snapshot. In which case we need to report the error back.
		if m.Snapshot != nil {
			n.ReportSnapshot(m.To, raft.SnapshotFinish)
		}
	}
}

func (n *node) handleAppendThread() {
	for {
		select {
		case m := <-n.toAppend:
			fmt.Printf("Node %d: (A)\n\t%s\n", n.id(), raft.DescribeMessage(m, nil))
			n.saveToStorage(&m)
			n.send(m.Responses)
		case <-n.stop:
			return
		}
	}
}

func (n *node) handleApplyThread() {
	for {
		select {
		case m := <-n.toApply:
			fmt.Printf("Node %d: (C)\n\t%s\n", n.id(), raft.DescribeMessage(m, nil))
			for _, entry := range m.Entries {
				if entry.Type == raftpb.EntryConfChange {
					var cc raftpb.ConfChange
					cc.Unmarshal(entry.Data)
					n.confState = n.ApplyConfChange(cc)
					fmt.Printf("Node %d: (C)\n\tConfChange: %+v\n", n.id(), cc)

					switch cc.Type {
					case raftpb.ConfChangeAddNode:
						fallthrough
					case raftpb.ConfChangeUpdateNode:
						if len(cc.Context) != 0 {
							var p peer
							p.decode(cc.Context)
							n.fsmState.applyConfChange(p)
							fmt.Printf("Node %d: added peer %+v to the FSM state.\n", n.id(), p)
						}
					case raftpb.ConfChangeRemoveNode:
						n.fsmState.deletePeer(cc.NodeID)
						fmt.Printf("Node %d: removed node %d address\n", n.id(), cc.NodeID)
					}
				}
				// Mutate the FSM state based on committed "commands".
				if entry.Type == raftpb.EntryNormal && len(entry.Data) != 0 {
					n.fsmState.applyCommand(entry.Data[0])
					// Periodically compact everything up to the last committed index. Note: this deliberately
					// compacts different nodes' logs at different indexes, since compacting at the same index
					// is not required for cluster operation.
					if n.fsmState.State%(compactN+int(n.id())) == 0 {
						fmt.Printf("Node %d: compacting log up to index %d; fsm state = %s.\n",
							n.id(), entry.Index, n.fsmState)
						n.storage.CreateSnapshot(entry.Index, n.confState, n.fsmState.encode())
						n.storage.Compact(entry.Index)
					}
				}
			}
			n.send(m.Responses)
		case <-n.stop:
			return
		}
	}
}

// runNode implements "state machine handling loop" as described in the etcd-io/raft docs.
// This is the "asynchronous storage writes" version of the loop.
func (n *node) runNode() {
	for {
		select {
		case <-n.stop:
			return
		case <-n.ticker.C:
			n.Tick()
		case rd := <-n.Ready():
			// Save previous n.confState in case it's updated by ConfChange in this Ready batch.
			prevConfState := n.confState

			for _, m := range rd.Messages {
				switch m.To {
				case raft.LocalAppendThread:
					n.toAppend <- m
				case raft.LocalApplyThread:
					n.toApply <- m
				default:
					if m.Type != raftpb.MsgHeartbeat && m.Type != raftpb.MsgHeartbeatResp {
						fmt.Printf("Node %d: --> %s\n", n.id(), raft.DescribeMessage(m, nil))
					}
					// Snapshot metadata overwrite, see https://github.com/etcd-io/etcd/issues/13741
					if m.Snapshot != nil && prevConfState != nil {
						m.Snapshot.Metadata.ConfState = *prevConfState
					}
					n.send([]raftpb.Message{m})
				}
			}
		}
	}
}
