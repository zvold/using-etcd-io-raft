Up to this point, the FSM state was represented by a *full* list of "command" log entries (introduced in [02-single-node-proposals](02-single-node-proposals)).

So first, we make the FSM more realistic, by introducing a state which is still mutated using the commands, but doesn't remember the *full* history. Each individual "command" entry still carries a single byte of data, but the FSM now simply adds committed commands to its state, an integer value.

This means that a snapshot of the FSM can be created by remembering a single integer (the FSM state), and snapshot recovery operation then initializes the FSM state to the integer stored in the snapshot.

Second, we also make the cluster behaviour more realistic, in two ways:
1. Instead of making a single log compaction on the leader, all nodes are creating snapshots and compacting their logs periodically.
2. And, instead of sending the "ticks" explicitly in the test, we send them in a background goroutine, independent of other test operations.

> {% octicon alert height:24 %} Note:
> 
> When creating a snapshot, it's important to store the FSM state (and compact the log) only up to an already *committed* entry. The node making the snapshot might have more entries available in the log, but cannot safely snapshot at an index beyond what is known (to this node) to be committed.

At this step it becomes important to apply the `Snapshot` and `Entries` from the `Ready()` channel in the correct order (see the note in [01-single-node-cluster](01-single-node-cluster)). The nodes might receive a `Ready` struct with both `Snapshot` and `Entries` populated, so we have to apply the `Snapshot` first (see [03-detour-memory-storage](03-detour-memory-storage#codeappendcode) for more details about the "gap"):
``` go
// Apply snapshot first or appending the entries might fail because of the "gap":
// panic: missing log entry [last: 0, append at: 92]:
n.storage.ApplySnapshot(rd.Snapshot)
n.storage.SetHardState(rd.HardState)
n.storage.Append(rd.Entries)
```

> {% octicon alert height:24 %} Note:
> 
> Another thing is that the test also triggers the following error intermittently:
> ```
> "tocommit(11) is out of range [lastIndex(3)]. Was the raft log corrupted, truncated, or lost?"
> ```
>This seems to be caused by proposing a lot of entries at once (i.e. between ticks).
>
>The error itself is well known (see [1](https://github.com/etcd-io/etcd/issues/13509), [2](https://github.com/etcd-io/etcd/issues/16220), [3](https://github.com/etcd-io/etcd/issues/13509)) and is addressed by [this](https://github.com/etcd-io/raft/pull/139) pull request (I verified that PR by using a patched `raft` library instead of `v3`). To work around the issue, until the pull request is merged, the test adds some delays when proposing new entries to reduce the number of new entries per tick. 