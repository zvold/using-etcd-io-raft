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
	confState *raftpb.ConfState // Stores ConfState returned by ApplyConfChange().
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
//
// Details of the app-specific state machine for this test:
//  - Log entries contain "commands" (a single byte).
//  - The FSM adds bytes from committed entries into an ordered list.
//  - The commands are sent sequentially, starting with 0.
//  - To take a snapshot of the FSM, we choose a committed entry, compact everything up to it,
//    and store that entry's command byte in the snapshot.
//  - To recover the FSM state from a snapshot, we take the byte D from the snapshot,
//    and re-create the ordered list of "command" bytes, from 0 to D.
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
				data := rd.Snapshot.Data[0]
				// Note this restores just the FSM state (which happens to be a list).
				// This process doesn't re-create the log entries in the log.
				fmt.Printf("Node %d: (R) restoring FSM state [0, %d] from the snapshot.\n", n.id(), data)
				var i byte
				for i = 0; i <= data; i++ {
					n.committed = append(n.committed, i)
				}
			}

			// 3. ... apply CommittedEntries to the state machine.
			for _, entry := range rd.CommittedEntries {
				if entry.Type == raftpb.EntryConfChange {
					var cc raftpb.ConfChange
					cc.Unmarshal(entry.Data)
					fmt.Printf("Node %d: (C)\n\tEntryConfChange: %+v\n", n.id(), cc)
					n.confState = n.ApplyConfChange(cc)
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
//      The commands are sequential in the range [0, 20].
//   4. Expects them to arrive on node.Ready() channel as CommittedEntries.
//   5. Makes a Snapshot (and compacts MemoryStorage) up until the entry with command=10.
//   5. Announces a new node, via node.ProposeConfChange().
//   6. Creates a second node, and starts its handling loop.
//   7. Expects the new node to join the cluster, and:
//      7a. Receive the snapshot and restore commands [0, 10] from it.
//      7b. Receive the remaining committed entries (commands [11, 20]).
func Test_TwoNodeCluster_Snapshot(t *testing.T) {
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
		t.Fatalf("Node %d hasn't reached StateLeader: %s", nodes[1].id(), nodes[1])
	}

	// Construct the expected list of committed "commands".
	expected := make([]byte, 0)

	// Propose a bunch of "command" entries, all at once.
	for i := 0; i <= 20; i++ {
		nodes[1].Propose(context.TODO(), []byte{byte(i)})
		expected = append(expected, byte(i))
	}

	// Wait for all "commands" to arrive to node 1 via CommittedEntries.
	if !util.WaitWithTicks(
		func() bool {
			result := slices.Equal(expected, nodes[1].committed)
			if result {
				fmt.Printf("Node %d: all expected command entries were committed.\n", nodes[1].id())
			}
			return result
		}, tick) {
		t.Fatalf("Node %d hasn't received expected command entries", nodes[1].id())
	}

	// Find the log entry with command=10 and compact everything up to that entry.
	firstIndex, _ := nodes[1].storage.FirstIndex()
	lastIndex, _ := nodes[1].storage.LastIndex()
	if lastIndex < 20 {
		t.Fatalf("Node 1: expected to have at least 20 log entries")
	}
	for i := firstIndex; i <= lastIndex; i++ {
		entries, _ := nodes[1].storage.Entries(i, i+1, 100) // Read the entry at index 'i'.
		if len(entries[0].Data) != 0 && entries[0].Data[0] == 10 {
			fmt.Printf("Node %d: compacting log up to index %d.\n", nodes[1].id(), i)
			nodes[1].storage.CreateSnapshot(i, nodes[1].confState, []byte{10})
			nodes[1].storage.Compact(i)
			break
		}
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

	// Wait for all "commands" [0, 20] to arrive to node 2 via CommittedEntries.
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
