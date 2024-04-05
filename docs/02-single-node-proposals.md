## 02-single-node-proposals
[Previous section](01-single-node-cluster) described how to start a single-node raft cluster. The next step is to send some proposals in this cluster and observe them arriving via the 'commit' channel on the sole node.

In general, there are two kinds of log entries that can be proposed: "command" entries that are relevant for the application-specific FSM, and "configuration change" entries that are necessary in any raft cluster (for communicating things like nodes joining/leaving the cluster).

As described in the ["state machine handling loop"](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L136-L162), the proposed log entries are expected to arrive as [`Ready`](https://github.com/etcd-io/raft/blob/ffe5efcf/node.go#L52) structs on the [`Node::Ready()`](https://github.com/etcd-io/raft/blob/ffe5efcf/node.go#L151) channel, first via `rd.Entries` and then via `rd.CommittedEntries`:
- Log entries arriving via `rd.Entries` just need to be appended to the node's log.
- Committed entries (coming via `rd.CommittedEntries`) are handled in two ways:
	1. "Configuration change" entries like `ConfChange` are applied by calling `Node::ApplyConfChange()`.
	1. "Command" entries are applied to the FSM.

This example uses an FSM which simply keeps all received "commands" in an ordered list. In other words, the state of this FSM is fully described by the list of commands received so far — the FSM state *is* the list.

The test at [`02-single-node-proposals/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/02-single-node-proposals/the_test.go) :
- Starts a single-node cluster, and waits for the node to win the election.
- Proposes a few "command" entries, and wait for each of them to be committed (appear in `rd.CommittedEntries`).
  Each committed "command" carries a single byte, which is collected by the FSM in a list.
- Verifies that in the end, all proposed "commands" get committed.

Next: [03-detour-memory-storage](03-detour-memory-storage).