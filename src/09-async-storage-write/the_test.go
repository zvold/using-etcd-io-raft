package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/zvold/using-etcd-io-raft/src/util"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	// Rand instance for sending random command bytes.
	r = rand.New(rand.NewSource(time.Now().UnixNano()))

	// Threshold for initiating log compaction.
	compactN = r.Intn(20) + 10

	// A global map for finding the node object by node id.
	// It is used to find the destination node when we route messages.
	nodes = syncNodes{
		nodes: make(map[int]*node),
	}
)

type syncNodes struct {
	sync.RWMutex
	nodes map[int]*node
}

func (s *syncNodes) add(n *node) {
	s.Lock()
	defer s.Unlock()
	s.nodes[int(n.id())] = n
}

func (s *syncNodes) get(id int) *node {
	s.RLock()
	defer s.RUnlock()
	return s.nodes[id]
}

func (s syncNodes) checkEach(f func(n *node) bool) bool {
	s.RLock()
	defer s.RUnlock()
	for _, n := range s.nodes {
		if f(n) {
			return true
		}
	}
	return false
}

func (s syncNodes) doEach(f func(n *node)) {
	s.checkEach(func(n *node) bool {
		f(n)
		return false
	})
}

// node extends raft.Node by keeping track of its storage and committed entries.
type node struct {
	raft.Node

	ticker    *time.Ticker        // Periodic "ticks" to drive the node forward.
	stop      chan struct{}       // Signals node handling gorouting to stop.
	storage   *raft.MemoryStorage // In-memory storage for raft log entries.
	fsmState  int                 // FSM state: the sum of committed "command" entries.
	confState *raftpb.ConfState   // Stores ConfState returned by ApplyConfChange().

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
	return fmt.Sprintf("Node %d: %+v", n.id(), n.Status())
}

// Create the raft node and start node handling goroutines.
func newNode(id uint64, peers []raft.Peer, restart bool, stop chan struct{}) *node {
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
			storage:  storage,
			ticker:   time.NewTicker(5 * time.Millisecond),
			stop:     stop,
			toAppend: make(chan raftpb.Message),
			toApply:  make(chan raftpb.Message),
		}
	} else {
		n = node{
			Node:     raft.StartNode(&config, peers),
			storage:  storage,
			ticker:   time.NewTicker(5 * time.Millisecond),
			stop:     stop,
			toAppend: make(chan raftpb.Message),
			toApply:  make(chan raftpb.Message),
		}
	}
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
			n.fsmState = util.BytesToInt(m.Snapshot.Data)
			fmt.Printf("Node %d: (R)\n\trestored fsm state = %d from the snapshot.\n", n.id(), n.fsmState)
		}
	}
	hs := raftpb.HardState{Term: m.Term, Vote: m.Vote, Commit: m.Commit}
	if !raft.IsEmptyHardState(hs) {
		n.storage.SetHardState(hs)
	}
	n.storage.Append(m.Entries)
}

func (n *node) send(msgs []raftpb.Message) {
	for _, m := range msgs {
		target := nodes.get(int(m.To))
		if target == nil {
			continue // Cannot deliver? No problem, deliver next time.
		}
		target.Step(context.TODO(), m)
		if m.Snapshot != nil {
			// "If any Message has type MsgSnap, call Node.ReportSnapshot() after it has been sent"
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
				}
				// Mutate the FSM state based on committed "commands".
				if entry.Type == raftpb.EntryNormal && len(entry.Data) != 0 {
					n.fsmState += int(entry.Data[0])
					// Periodically compact everything up to the last committed index. Note: this deliberately
					// compacts different nodes' logs at different indexes, since compacting at the same index
					// is not required for cluster operation.
					if n.fsmState%(compactN+int(n.id())) == 0 {
						fmt.Printf("Node %d: compacting log up to index %d; fsm state = %d.\n",
							n.id(), entry.Index, n.fsmState)
						n.storage.CreateSnapshot(entry.Index, n.confState, util.IntToBytes(n.fsmState))
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
					fmt.Printf("Node %d: --> %s\n", n.id(), raft.DescribeMessage(m, nil))
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

// This test:
//   0. Uses nodes working with "asynchronous storage writes" mechanism.
//   1. Creates a 2-node raft cluster.
//   2. Keeps "ticking" in the background.
//   3. Waits for one of the nodes to become a leader.
//   4. Proposes a bunch of random "command" entries via node.Propose() to all nodes.
//   5. Periodically makes Snapshots (and compacts MemoryStorage) up to some committed index.
//   6. Announces a new node, via node.ProposeConfChange().
//   7. Creates the new node, and starts its handling loop.
//   8. After a while, asserts that all 3 nodes agree on the FSM state.
func Test_RunningCluster(t *testing.T) {
	// The 'stop' channel signals the "state machine handling" goroutine (below) to finish.
	stop := make(chan struct{})
	defer close(stop)

	// Create 2-node cluster. Note that each node lists itself as a peer.
	for i := 1; i <= 2; i++ {
		nodes.add(newNode(
			uint64(i),
			[]raft.Peer{{ID: 0x1}, {ID: 0x2}},
			/*restart=*/ false,
			stop))
	}

	// Wait for some node to reach StateLeader.
	if !util.WaitNoTicks(func() bool {
		return nodes.checkEach(
			func(n *node) bool {
				if n.state() == raft.StateLeader {
					t.Logf("Node %d is the leader.\n", n.id())
					return true
				}
				return false
			})
	}) {
		t.Fatal("No nodes have reached StateLeader.")
	}

	// Keep proposing entries to all nodes.
	for i := 0; i < 50; i++ {
		nodes.get(r.Intn(2)+1).Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
		// Note: it appears that when too many proposals are applied in bulk (without any delays),
		// it seems to trigger this bug:
		// "tocommit(11) is out of range [lastIndex(3)]. Was the raft log corrupted, truncated, or lost?"
		//
		// see https://github.com/etcd-io/etcd/issues/13509
		// and https://github.com/etcd-io/etcd/issues/16220
		// and https://github.com/etcd-io/etcd/issues/13509
		//
		// this pull request seems to fix the bug: https://github.com/etcd-io/raft/pull/139,
		// but it's not merged yet
		if i%10 == 0 {
			// Artificial delay so not all messages are proposed in bulk.
			time.Sleep(1 * time.Millisecond)
		}
	}

	// Make node 3 join the cluster, following etcd-io/raft docs.
	nodes.get(1).ProposeConfChange(
		context.TODO(),
		raftpb.ConfChange{
			Type:   raftpb.ConfChangeAddNode,
			NodeID: 0x3,
		})

	nodes.add(newNode(
		0x3,
		nil,
		/*restart=*/ true,
		stop))

	// Wait for node 3 to reach StateFollower.
	if !util.WaitNoTicks(func() bool {
		return nodes.get(3).state() == raft.StateFollower
	}) {
		t.Fatalf("Node 3 hasn't reached StateFollower: %s", nodes.get(3))
	}

	// Wait until all nodes reach the same FSM state.
	if !util.WaitNoTicks(
		func() bool {
			states := make(map[int]bool)
			nodes.doEach(func(n *node) { states[n.fsmState] = true })
			return len(states) == 1
		}) {
		t.Fatalf("Node fsm states are not the same: %d, %d, %d.\n",
			nodes.get(1).fsmState, nodes.get(2).fsmState, nodes.get(3).fsmState)
	}
	t.Logf("The nodes have reached the same fsm state = %d.\n", nodes.get(1).fsmState)

	// Wait for all nodes to stop (raft library sets node ID to 0).
	nodes.doEach(func(n *node) { n.Stop() })
	nodes.doEach(func(n *node) {
		if !util.WaitNoTicks(func() bool { return n.id() == 0 }) {
			t.Fatalf("The node %d hasn't stopped: %s", n.id(), n)
		}
	})
}
