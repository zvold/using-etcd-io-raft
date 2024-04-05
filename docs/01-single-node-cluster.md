## 01-single-node-cluster
The code for this section (see [`01-single-node-cluster/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/01-single-node-cluster/the_test.go)) follows the [instructions](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L75-L81) from the `etcd-io/raft` README to start a single-node cluster, and implements the ["state machine handling loop"](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L136-L162), to the extent necessary for the cluster to function.

Specifically, for this minimal example, the implementation skips the following steps of the loop:
- sending messages to other nodes, as there's only one node in the cluster;
- applying snapshots, as nothing creates snapshots yet.

The single-node cluster is expected to go through the following stages:
- The node starts in the 'follower' state — the test verifies this before continuing.
- It starts the voting process and wins the vote — the test verifies that the node reaches the 'leader' state.

> {% octicon alert height:24 %} Note:
> 
> Step 1 of "the loop" mentions:
> 
> *Write Entries, HardState and Snapshot to *persistent* storage in order, i.e. Entries first, then HardState and Snapshot if they are not empty. If persistent storage supports atomic writes then all of them can be written together.*
> 
> However, `the_test.go` doesn't follow this and applies the `Snapshot` first:
>``` go
>n.storage.ApplySnapshot(rd.Snapshot)
>n.storage.SetHardState(rd.HardState)
>n.storage.Append(rd.Entries)
>```
>This is because the above quote refers to _persistent_ storage, addressing concerns of the node crashing after persisting the data partially and later restarting from that date. This doesn't apply to the `MemoryStorage` used in this test (and following ones). In fact, for `MemoryStorage` it's important to apply the `Snapshot` first. It will be more clear as to why from [07-detour-change-fsm](07-detour-change-fsm).

See [these instructions](index.md#running-the-code) on how to run the code.

Next: [02-single-node-proposals](02-single-node-proposals).