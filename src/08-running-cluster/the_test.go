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

// Create the raft node and start the main handling goroutine.
func newNode(id uint64, peers []raft.Peer, restart bool, stop chan struct{}) *node {
	storage := raft.NewMemoryStorage()
	config := raft.Config{
		ID:              id,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         storage,
		MaxSizePerMsg:   4096,
		MaxInflightMsgs: 256,
	}
	var n node
	if restart {
		// For a new node joining the cluster, RestartNode() is used.
		n = node{
			Node:    raft.RestartNode(&config),
			storage: storage,
			ticker:  time.NewTicker(5 * time.Millisecond),
			stop:    stop,
		}
	} else {
		n = node{
			Node:    raft.StartNode(&config, peers),
			storage: storage,
			ticker:  time.NewTicker(5 * time.Millisecond),
			stop:    stop,
		}
	}
	go n.runNode()
	return &n
}

// saveToStorage applies snapshot/hardstate/entries from raft.Ready to node storage.
func (n *node) saveToStorage(rd *raft.Ready) {
	// Note: Snapshot has to be applied before Entries, otherwise Append() might
	// fail due to a "gap" between LastIndex() and "first index" of the new entries.
	if !raft.IsEmptySnap(rd.Snapshot) && len(rd.Snapshot.Data) != 0 {
		// ApplySnapshot() is no-op for Snapshots older than storage.Snapshot().
		err := n.storage.ApplySnapshot(rd.Snapshot)
		if err != raft.ErrSnapOutOfDate {
			// Note that we modify the FSM state here as well.
			n.fsmState = util.BytesToInt(rd.Snapshot.Data)
			fmt.Printf("Node %d: (R) restored fsm state = %d from the snapshot.\n", n.id(), n.fsmState)
		}
	}
	if !raft.IsEmptyHardState(rd.HardState) {
		n.storage.SetHardState(rd.HardState)
	}
	n.storage.Append(rd.Entries)
}

func (n *node) sendMessages(messages []raftpb.Message) {
	// For 20% of Ready messages, select the node to which message delivery fails.
	dropTarget := 100
	if r.Intn(5) == 0 {
		dropTarget = 1 + r.Intn(3)
	}

	for _, message := range messages {
		if message.To == uint64(dropTarget) {
			// Drop all messages addressed to 'dropTarget'.
			fmt.Printf("\t!!! Dropping messages *->%d.\n", dropTarget)
			if message.Type == raftpb.MsgSnap {
				// Make sure to report snapshot failure if we cannot deliver the snapshot.
				// https://github.com/etcd-io/raft/blob/ffe5efcf/node.go#L195C30-L197C33
				n.ReportSnapshot(message.To, raft.SnapshotFailure)
			}
			continue
		}

		target := nodes.get(int(message.To))
		if target == nil {
			continue // Cannot deliver? No problem, deliver next time.
		}

		if message.Type == raftpb.MsgSnap {
			// See https://github.com/etcd-io/etcd/issues/13741
			message.Snapshot.Metadata.ConfState = *n.confState
		}
		target.Step(context.TODO(), message)
		if message.Type == raftpb.MsgSnap {
			// "If any Message has type MsgSnap, call Node.ReportSnapshot() after it has been sent"
			n.ReportSnapshot(message.To, raft.SnapshotFinish)
		}
	}
}

func (n *node) applyCommitted(committed []raftpb.Entry) {
	for _, entry := range committed {
		switch entry.Type {
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			cc.Unmarshal(entry.Data)
			fmt.Printf("Node %d: (C)\n\tEntryConfChange: %+v\n", n.id(), cc)
			n.confState = n.ApplyConfChange(cc)
		case raftpb.EntryNormal:
			if len(entry.Data) == 0 {
				continue
			}
			// Mutate the FSM state based on committed "commands".
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
}

// runNode implements "state machine handling loop" as described in the etcd-io/raft docs.
//
// Details of the app-specific state machine for this test:
//  - Log entries contain "commands" (a single byte).
//  - The FSM applies each "command" by adding its value to the state (an integer).
//  - To take a snapshot of the FSM, we compact everything up to the latest committed entry,
//    and records the integer FSM state as snapshot data.
//  - To recover the FSM state from a snapshot, we simply read the integer state from it.
func (n *node) runNode() {
	for {
		select {
		case <-n.stop:
			n.ticker.Stop()
			return
		case <-n.ticker.C:
			n.Tick()
		case rd := <-n.Ready():
			fmt.Printf("Node %d: (R) %s\n", n.id(), util.ReadyToStr(&rd))
			// 1. Write Entries, HardState and Snapshot to persistent storage in order.
			// 3. Apply Snapshot (if any) to the state machine.
			n.saveToStorage(&rd)
			// 2. Send all Messages to the nodes named in the To field.
			n.sendMessages(rd.Messages)
			// 3. ...apply CommittedEntries to the state machine.
			n.applyCommitted(rd.CommittedEntries)
			// 4. Call Node.Advance() to signal readiness for the next batch of updates.
			n.Advance()
		}
	}
}

// This test:
//   1. Creates a 3-node raft cluster right away.
//   2. Keeps "ticking" in the background.
//   3. Waits for one of the nodes to become a leader.
//   4. Proposes a bunch of random "command" entries via node.Propose() to all nodes.
//   5. Periodically makes Snapshots (and compacts MemoryStorage) up to some committed index.
//   6. Simulates unreliable message delivery (for 20% of Ready messages).
//   7. After a while, asserts that all 3 nodes agree on the FSM state.
func Test_RunningCluster(t *testing.T) {
	// The 'stop' channel signals the state machine handling goroutine (below) to finish.
	stop := make(chan struct{})
	defer close(stop)

	// Create 3-node cluster. Note that each node lists itself as a peer.
	for i := 1; i <= 3; i++ {
		nodes.add(newNode(
			uint64(i),
			[]raft.Peer{{ID: 0x1}, {ID: 0x2}, {ID: 0x3}},
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
		nodes.get(r.Intn(3)+1).Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
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
