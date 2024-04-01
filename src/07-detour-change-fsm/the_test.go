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

func (s syncNodes) add(n *node) {
	s.Lock()
	defer s.Unlock()
	s.nodes[int(n.id())] = n
}

func (s syncNodes) get(id int) *node {
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

	storage   *raft.MemoryStorage // In-memory storage for raft log entries.
	fsmState  int                 // FSM state: the sum of "commands" from committed entries.
	confState *raftpb.ConfState   // Stores ConfState returned by ApplyConfChange().
}

// saveToStorage applies snapshot/hardstate/entries from raft.Ready to node storage.
func (n *node) saveToStorage(rd *raft.Ready) {
	// Note: Snapshot has to be applied before Entries, otherwise Append() might
	// fail due to a "gap" between LastIndex() and "first index" of the new entries.

	// ApplySnapshot() is no-op for Snapshots older than storage.Snapshot().
	n.storage.ApplySnapshot(rd.Snapshot)
	n.storage.SetHardState(rd.HardState)
	n.storage.Append(rd.Entries)
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

// Create a node as described in the etcd-io/raft docs.
func newNode(id uint64, peers []raft.Peer, restart bool) *node {
	storage := raft.NewMemoryStorage()
	config := raft.Config{
		ID:              id,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         storage,
		MaxSizePerMsg:   4096,
		MaxInflightMsgs: 256,
	}
	if restart {
		// For a new node joining the cluster, RestartNode() is used.
		return &node{
			Node:    raft.RestartNode(&config),
			storage: storage,
		}
	} else {
		return &node{
			Node:    raft.StartNode(&config, peers),
			storage: storage,
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
func (n *node) runNode(tick, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-tick:
			n.Tick()
		case rd := <-n.Ready():
			fmt.Printf("Node %d: (R) %s\n", n.id(), util.ReadyToStr(&rd))

			// 1. Write Entries, HardState and Snapshot to persistent storage in order.
			n.saveToStorage(&rd)

			// 2. Send all Messages to the nodes named in the To field.
			for _, message := range rd.Messages {
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

			// 3. Apply Snapshot (if any) to the state machine.
			if !raft.IsEmptySnap(rd.Snapshot) && len(rd.Snapshot.Data) != 0 {
				n.fsmState = util.BytesToInt(rd.Snapshot.Data)
				fmt.Printf("Node %d: (R) restored fsm state = %d from the snapshot.\n", n.id(), n.fsmState)
			}

			// 3. ... apply CommittedEntries to the state machine.
			for _, entry := range rd.CommittedEntries {
				if entry.Type == raftpb.EntryConfChange {
					var cc raftpb.ConfChange
					cc.Unmarshal(entry.Data)
					fmt.Printf("Node %d: (C)\n\tEntryConfChange: %+v\n", n.id(), cc)
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

			// 4. Call Node.Advance() to signal readiness for the next batch of updates.
			n.Advance()
		}
	}
}

// This test:
//   1. Creates a single-node raft cluster.
//   2. Waits for the node to become a leader.
//   3. Proposes a bunch of random "command" entries via node.Propose().
//   4. Keeps "ticking" in the background.
//   5. Periodically makes Snapshots (and compacts the log) up to some committed index.
//   5. Announces a new node, via node.ProposeConfChange().
//   6. Creates a second node, and starts its handling loop.
//   7. Expects the new node to join the cluster, and catch up to the 1st node,
//      by reaching the same FSM state value.
func Test_TwoNodeCluster_FSM(t *testing.T) {
	// The 'tick' channel is used to drive the node forward in time.
	tick := make(chan struct{})

	// The 'stop' channel signals the state machine handling goroutine (below) to finish.
	stop := make(chan struct{})
	defer close(stop)

	// Send ticks in the background.
	go func() {
		for {
			select {
			case <-stop:
				return
			case tick <- struct{}{}:
				time.Sleep(5 * time.Millisecond)
			}
		}
		close(tick)
	}()

	// Create and start a node as described in the etcd-io/raft docs.
	nodes.add(newNode(
		0x1,
		[]raft.Peer{{ID: 0x1}},
		/*restart=*/ false))
	go nodes.get(1).runNode(tick, stop)

	// Wait for node 1 to reach StateLeader.
	if !util.WaitNoTicks(func() bool { return nodes.get(1).state() == raft.StateLeader }) {
		t.Fatalf("Node %d hasn't reached StateLeader: %s", nodes.get(1).id(), nodes.get(1))
	}

	// Propose a bunch of "command" entries to node 1.
	for i := 0; i < 25; i++ {
		nodes.get(1).Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
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

	// Make node 2 join the cluster, following etcd-io/raft docs.
	nodes.get(1).ProposeConfChange(
		context.TODO(),
		raftpb.ConfChange{
			Type:   raftpb.ConfChangeAddNode,
			NodeID: 0x2,
		})

	// Create and start the second node.
	nodes.add(newNode(0x2, nil, true /*restart=*/))
	go nodes.get(2).runNode(tick, stop)

	// Keep proposing entries, now to both nodes.
	for i := 0; i < 25; i++ {
		nodes.get(1).Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
		nodes.get(2).Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
		if i%10 == 0 {
			// Artificial delay so not all messages are proposed in bulk (see above).
			time.Sleep(1 * time.Millisecond)
		}
	}

	// Wait until both nodes reach the same FSM state.
	if !util.WaitNoTicks(
		func() bool {
			result := nodes.get(1).fsmState == nodes.get(2).fsmState
			if result {
				t.Logf("Node %d and node %d fsm state is the same: %d.\n",
					nodes.get(1).id(), nodes.get(2).id(), nodes.get(1).fsmState)
			}
			return result
		}) {
		t.Fatalf("Node %d and node %d fsm state is not the same: %d vs %d.\n",
			nodes.get(1).id(), nodes.get(2).id(), nodes.get(1).fsmState, nodes.get(2).fsmState)
	}

	// Wait for all nodes to stop (raft library sets node ID to 0).
	nodes.doEach(func(n *node) { n.Stop() })
	nodes.doEach(func(n *node) {
		if !util.WaitNoTicks(func() bool { return n.id() == 0 }) {
			t.Fatalf("The node %d hasn't stopped: %s", n.id(), n)
		}
	})
}
