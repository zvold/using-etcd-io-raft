## 06-two-node-snapshot
In this section, we let one node of the cluster receive the full FSM state from another node via a snapshot + remaining entries.

This requires implementing the remaining steps of the "state machine handling loop":
- Applying snapshots to FSM, and notifying raft library via `ReportSnapshot()` ([link](https://github.com/etcd-io/raft/blob/d475d7e4/doc.go#L84C75-L85C73)).
- Remembering `ConfChange` returned from `ApplyConfChange()` ([link](https://github.com/etcd-io/raft/blob/d475d7e4/storage.go#L225C1-L226C75)).

The test [`06-two-node-snapshot/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/06-two-node-snapshot/the_test.go) is similar to the previous [05-two-node-cluster](05-two-node-cluster), except before creating the second node, it performs a log compaction on node 1. So that when node 2 joins the cluster, it has to recover the FSM state from the snapshot.

Note that the `Snapshot` is not coming from a "committed" channel. When `Ready` struct contains a Snapshot, we apply it to both `MemoryStorage` and to the FSM.

Next: [07-detour-change-fsm](07-detour-change-fsm).