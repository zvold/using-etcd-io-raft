package main

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/zvold/using-etcd-io-raft/src/util"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// A global map for finding the node object by node id.
// It is used to find the destination node when we route messages.
var nodes = make(map[int]*node)

// node extends raft.Node by keeping track of its storage and committed entries.
type node struct {
	raft.Node

	storage *raft.MemoryStorage // In-memory storage for raft log entries.
	// App-specific FSM state is the list of received "commands".
	// Each "command" entry carries a byte, which we record here.
	committed []byte
}

// saveToStorage applies snapshot/hardstate/entries from raft.Ready to node storage.
func (n *node) saveToStorage(rd *raft.Ready) {
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
			Node:      raft.RestartNode(&config),
			storage:   storage,
			committed: make([]byte, 0),
		}
	} else {
		return &node{
			Node:      raft.StartNode(&config, peers),
			storage:   storage,
			committed: make([]byte, 0),
		}
	}
}

// runNode implements "state machine handling loop" as described in the etcd-io/raft docs.
// Some of the steps are irrelevant for this single-node cluster and not implemented:
//   - No need to apply snapshots to the state machine (step 3).
//
// However, we make sure to collect the "commands" coming from CommittedEntries.
// And, since we have a multi-node cluster, we need to pass messages between the nodes.
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
				target, ok := nodes[int(message.To)]
				if !ok {
					panic("'nodes' is fully initialized before nodes start operating.")
				}
				target.Step(context.TODO(), message)
			}

			// This step is still skipped (nodes make no snapshots yet):
			//
			// 3. Apply Snapshot (if any) to the state machine.

			// 3. ... apply CommittedEntries to the state machine.
			for _, entry := range rd.CommittedEntries {
				if entry.Type == raftpb.EntryConfChange {
					var cc raftpb.ConfChange
					cc.Unmarshal(entry.Data)
					fmt.Printf("Node %d: (C)\n\tEntryConfChange: %+v\n", n.id(), cc)
					n.ApplyConfChange(cc)
				}
				// Collect all committed "commands" for a later verification.
				if entry.Type == raftpb.EntryNormal && len(entry.Data) != 0 {
					n.committed = append(n.committed, entry.Data[0])
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
//   3. Proposes a bunch of "command" entries via node.Propose().
//   4. Expects them to arrive on node.Ready() channel as CommittedEntries.
//   5. Announces a new node, via node.ProposeConfChange().
//   6. Creates a second node, and starts its handling loop.
//   7. Expects the new node to join the cluster, and receive the same committed entries.
func Test_TwoNodeCluster_Join(t *testing.T) {
	// The 'tick' channel is used to drive the node forward in time.
	tick := make(chan struct{})
	defer close(tick)

	// The 'stop' channel signals the state machine handling goroutine (below) to finish.
	stop := make(chan struct{})
	defer close(stop)

	// Create and start a node as described in the etcd-io/raft docs.
	nodes[1] = newNode(
		0x1,
		[]raft.Peer{{ID: 0x1}},
		/*restart=*/ false)
	go nodes[1].runNode(tick, stop)

	// Wait for node 1 to reach StateLeader.
	if !util.WaitWithTicks(func() bool { return nodes[1].state() == raft.StateLeader }, tick) {
		t.Fatalf("Node %d node hasn't reached StateLeader: %s", nodes[1].id(), nodes[1])
	}

	// Construct the expected list of committed "commands".
	expected := make([]byte, 0)

	// Propose a bunch of "command" entries, all at once.
	for i := 0; i < 50; i++ {
		nodes[1].Propose(context.TODO(), []byte{byte(i)})
		expected = append(expected, byte(i))
	}

	// Wait for all "commands" to arrive to node 1 via CommittedEntries.
	if !util.WaitWithTicks(
		func() bool {
			result := slices.Equal(expected, nodes[1].committed)
			if result {
				t.Logf("Node %d: all expected command entries were committed.\n", nodes[1].id())
			}
			return result
		}, tick) {
		t.Fatalf("Node %d hasn't received expected command entries", nodes[1].id())
	}

	// Make node 2 join the cluster, following etcd-io/raft docs.
	nodes[1].ProposeConfChange(
		context.TODO(),
		raftpb.ConfChange{
			Type:   raftpb.ConfChangeAddNode,
			NodeID: 0x2,
		})

	// Create and start the second node.
	nodes[2] = newNode(
		0x2,
		nil,
		/*restart=*/ true)
	go nodes[2].runNode(tick, stop)

	// Wait for all "commands" to arrive to node 2 via CommittedEntries.
	if !util.WaitWithTicks(
		func() bool {
			result := slices.Equal(expected, nodes[2].committed)
			if result {
				t.Logf("Node %d: all expected command entries were committed.\n", nodes[2].id())
			}
			return result
		}, tick) {
		t.Fatalf("Node %d hasn't received expected command entries", nodes[2].id())
	}

	// Wait for all nodes to stop (raft library sets node ID to 0).
	for i, n := range nodes {
		n.Stop()
		if !util.WaitWithTicks(func() bool { return n.id() == 0 }, tick) {
			t.Fatalf("The node %d hasn't stopped: %s", i, n)
		}
	}
}
