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

// This test:
//   1. Creates a single-node raft cluster.
//   2. Waits for the node to become a leader.
//   3. Proposes a bunch of "command" entries via node.Propose().
//   4. Expects them to arrive on node.Ready() channel as CommittedEntries.
func Test_SingleNodeCluster_Propose(t *testing.T) {
	// App-specific FSM state is the list of received "commands".
	// Each "command" entry carries a byte, which we record here.
	committed := make([]byte, 0)

	// Create a node as described in the etcd-io/raft docs.
	storage := raft.NewMemoryStorage()
	n := raft.StartNode(
		&raft.Config{
			ID:              0x1,
			ElectionTick:    10,
			HeartbeatTick:   1,
			Storage:         storage,
			MaxSizePerMsg:   4096,
			MaxInflightMsgs: 256,
		}, []raft.Peer{{ID: 0x1}})

	// The 'tick' channel is used to drive the node forward in time.
	tick := make(chan struct{})
	defer close(tick)

	// The 'stop' channel signals the state machine handling goroutine (below) to finish.
	stop := make(chan struct{})
	defer close(stop)

	// Start the "state machine handling loop" as described in the etcd-io/raft docs.
	// Some of the steps are irrelevant for this single-node cluster and not implemented:
	//   - No need to pass messages between nodes (step 2).
	//   - No need to apply snapshots to the state machine (step 3).
	//
	// However, we make sure to collect the "commands" coming from CommittedEntries.
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-tick:
				n.Tick()
			case rd := <-n.Ready():
				fmt.Printf("Node %d: (R) %s\n", n.Status().ID, util.ReadyToStr(&rd))

				// 1. Write Entries, HardState and Snapshot to persistent storage in order.
				// ApplySnapshot() is no-op for Snapshots older than storage.Snapshot().
				storage.ApplySnapshot(rd.Snapshot)
				storage.SetHardState(rd.HardState)
				storage.Append(rd.Entries)

				// These steps are skipped for the single-node cluster:
				//
				// 2. Send all Messages to the nodes named in the To field.
				// 3. Apply Snapshot (if any) to the state machine.

				// 3. ...apply CommittedEntries to the state machine.
				for _, entry := range rd.CommittedEntries {
					if entry.Type == raftpb.EntryConfChange {
						var cc raftpb.ConfChange
						cc.Unmarshal(entry.Data)
						fmt.Printf("Node %d: (C)\n\tEntryConfChange: %+v\n", n.Status().ID, cc)
						n.ApplyConfChange(cc)
					}
					// Collect all committed "commands" for a later verification.
					if entry.Type == raftpb.EntryNormal && len(entry.Data) != 0 {
						committed = append(committed, entry.Data[0])
					}
				}

				// 4. Call Node.Advance() to signal readiness for the next batch of updates.
				n.Advance()
			}
		}
	}()

	// Wait for the node to reach StateLeader.
	if !util.WaitWithTicks(func() bool { return n.Status().RaftState == raft.StateLeader }, tick) {
		t.Fatalf("The node hasn't reached StateLeader: %s", nodeToStr(n))
	}

	// Construct the expected list of committed "commands".
	expected := make([]byte, 0)

	// Propose a bunch of "command" entries and wait for them to get committed.
	for i := 0; i < 10; i++ {
		n.Propose(context.TODO(), []byte{byte(i)})
		expected = append(expected, byte(i))
		if !util.WaitWithTicks(
			func() bool {
				// Check the latest committed command, since we're sending them one by one.
				return len(committed) != 0 && committed[len(committed)-1] == byte(i)
			}, tick) {
			t.Fatalf("Node %d: command %v hasn't been committed.", n.Status().ID, i)
		}
	}

	if !slices.Equal(expected, committed) {
		t.Fatalf("Committed entries don't match expectation:\ncommitted:%v\nexpected:%v",
			committed, expected)
	}

	// Wait for the node to stop (raft library sets node ID to 0).
	n.Stop()
	if !util.WaitWithTicks(func() bool { return n.Status().ID == 0 }, tick) {
		t.Fatalf("The node hasn't stopped: %s", nodeToStr(n))
	}
}

// Helper for pretty-printing raft.Node.
func nodeToStr(n raft.Node) string {
	return fmt.Sprintf("Node %d: %+v", n.Status().ID, n.Status())
}
