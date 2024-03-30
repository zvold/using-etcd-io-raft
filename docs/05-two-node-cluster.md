This section builds on top of [04-detour-node-refactor](04-detour-node-refactor) to set up a two-node cluster, where a second node joins an existing single-node cluster and receives the full log from the leader. This, in turn, should set us up for implementing and testing log compaction later.

The test at [`05-two-node-cluster/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/05-two-node-cluster/the_test.go) :
- Starts a single-node cluster, and waits for the node to win the election.
- Proposes a few "command" entries, and wait for each of them to be committed (appear in `rd.CommittedEntries`).
     Each committed "command" carries a single byte, which is collected by the FSM in a list.
- Verifies that all proposed log entries to get committed on the single node of the cluster.
- Adds a second node to the cluster.
- Waits for the second node to receive the same committed log entries from the first node.

To make this work, the "state machine handling" loop needs to be updated to pass messages between the nodes.

Next: [06-two-node-snapshot](06-two-node-snapshot).