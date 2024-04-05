## 11-persistence-shutdown
This section addresses some leftovers from the [previous](10-separate-process) one:
- On shutdown, nodes write their `MemoryStorage` to a file, and remove themselves from the cluster, by sending a `ConfChange` proposal.
- On startup, nodes recover the state from the file, if present.
- Also, the nodes are responsible for starting their own RPC servers and closing them in the end.

As explained in [03-detour-memory-storage](03-detour-memory-storage#implementation-overview), `MemoryStorage` is fully described by just 3 fields. For serialization, we store them in a `blob struct` (another option could be using the `Message` protobuf):
```go
type blob struct {
  HardState raftpb.HardState  // Storage::InitialState
  Snapshot  raftpb.Snapshot   // Storage::Snapshot
  Entries   []raftpb.Entry    // Storage::Entries
}
```

The file `blob.go` implements the necessary functions for writing / reading blobs and for conversion between blobs and `MemoryStorage`. The test cases from [`03-detour-memory-storage/memorystorage_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/03-detour-memory-storage/memorystorage_test.go) come handy here, and are reused for testing that different `MemoryStorage` instances can be correctly persisted to disk and recovered.

> {% octicon alert height:24 %} Note:
>
> The nodes should really persist data to disk on any modifications to `MemoryStorage`, so that they'll be able to recover in case the process crashes. For simplicity, writing to disk happens only on node shutdown — this is enough for the testing purposes.

The test in [`11-persistence-shutdown/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/11-persistence-shutdown/the_test.go) now checks that a node can be shut down and restarted from a persisted state:
1. Start a 3-node cluster, where nodes communicate over the network.
2. Issue some command proposals, and then shutdown node 3.
3. Node 3 persists the state to a file named `.memory-3` and removes itself from the cluster.
4. Issue some more commands to the remaining nodes, so they can advance the FSM.
5. Restart node 3. It recovers the state from `.memory-3` file and re-joins the cluster.
6. After a while, verify what all 3 nodes have the same FSM state — that is, node 3 has caught up with the other nodes.
