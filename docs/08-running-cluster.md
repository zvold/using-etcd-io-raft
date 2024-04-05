## 08-running-cluster
This section changes the cluster to have 3 nodes, and starts all nodes at once (as opposed to proposing `ConfChange`s to join the cluster). Again, we just follow the [instructions](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L59-L73) from the `etcd-io/raft` README.

The test at [`08-running-cluster/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/08-running-cluster/the_test.go):
- Creates a 3-node cluster (still running in the same process).
- Waits for one of them to be elected leader.
- Sends random proposals to random nodes.
- Has each node creating snapshots and compacting its log as it wishes.
- Lets it bake and verifies that all 3 nodes have the same FSM state at the end.

It also simulates an unreliable network by sometimes dropping messages addressed to a random target node.

Next: [09-async-storage-write](09-async-storage-write).