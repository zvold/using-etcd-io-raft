package main

import (
	"fmt"
	"testing"
	"time"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// This test:
//   1. Creates a single-node raft cluster.
//   2. Expects the node to start voting process and become a leader.
func Test_SingleNodeCluster_Start(t *testing.T) {
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
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-tick:
				n.Tick()
			case rd := <-n.Ready():
				fmt.Printf("Node %d: (R) %s\n", n.Status().ID, readyToStr(&rd))

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
				}

				// 4. Call Node.Advance() to signal readiness for the next batch of updates.
				n.Advance()
			}
		}
	}()

	// Wait for the node to reach StateFollower.
	if !waitWithTicks(func() bool { return n.Status().RaftState == raft.StateFollower }, tick) {
		t.Fatalf("The node hasn't reached StateFollower: %s", nodeToStr(n))
	}

	// Wait for the node to reach StateLeader.
	if !waitWithTicks(func() bool { return n.Status().RaftState == raft.StateLeader }, tick) {
		t.Fatalf("The node hasn't reached StateLeader: %s", nodeToStr(n))
	}

	// Wait for the node to stop (raft library sets node ID to 0).
	n.Stop()
	if !waitWithTicks(func() bool { return n.Status().ID == 0 }, tick) {
		t.Fatalf("The node hasn't stopped: %s", nodeToStr(n))
	}
}

// Send "ticks" to the 'tick' channel, until 'predicate()' returns true.
// Returns 'false' if the predicate is not satisfied after 100 ticks.
func waitWithTicks(predicate func() bool, tick chan<- struct{}) bool {
	for i := 0; i < 100; i++ {
		if predicate() {
			return true
		}
		tick <- struct{}{}
		time.Sleep(1 * time.Millisecond)
	}
	return false
}

// Helper for pretty-printing raft.Ready structs.
func readyToStr(rd *raft.Ready) (s string) {
	if !raft.IsEmptyHardState(rd.HardState) {
		s += fmt.Sprintf("\n\tHardState: %+v", rd.HardState)
	}
	if len(rd.Entries) != 0 {
		s += fmt.Sprintf("\n\tEntries:   ")
		prefix := ""
		for _, e := range rd.Entries {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeEntry(e, nil))
			prefix = "\n\t\t   "
		}
	}
	if !raft.IsEmptySnap(rd.Snapshot) {
		s += fmt.Sprintf("\n\tSnapshot:  %s", raft.DescribeSnapshot(rd.Snapshot))
	}
	if len(rd.Messages) != 0 {
		s += fmt.Sprint("\n\tMessages:  ")
		prefix := ""
		for _, m := range rd.Messages {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeMessage(m, nil))
			prefix = "\n\t\t   "
		}
	}
	if len(rd.CommittedEntries) != 0 {
		s += fmt.Sprint("\n\tCommitted: ")
		prefix := ""
		for _, e := range rd.CommittedEntries {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeEntry(e, nil))
			prefix = "\n\t\t   "
		}
	}
	return
}

// Helper for pretty-printing raft.Node.
func nodeToStr(n raft.Node) string {
	return fmt.Sprintf("Node %d: %+v", n.Status().ID, n.Status())
}
