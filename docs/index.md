---
permalink: /
---
> {% octicon alert height:24 %} Note:
> 
> Work in progress, current status: step 10 out of approx. 12 steps.

**What:** this is a series of blog posts about building a small program that uses the [`etcd-io/raft`](https://github.com/etcd-io/raft) Go library.

**Why:** the README of `etcd-io/raft` links to a "simple example application", [raftexample](https://github.com/etcd-io/etcd/tree/main/contrib/raftexample), which I found too complex. For instance, it pulls dependencies from [`etcd-io/etcd`](https://github.com/etcd-io/etcd), which shouldn't be necessary for a minimal example. So, I'm attempting to build my own "simple example application", starting by following the ["usage"](https://github.com/etcd-io/raft#usage) section.

**How:** The first post describes a minimal program, running a single-node raft cluster. Then, after a sequence of incremental changes, we end up with a CLI program similar to `raftexample`: each instance runs one raft node, instances communicate over network, and together they maintain a replicated FSM.

## Table of contents
- [01-single-node-cluster](01-single-node-cluster) \
  A node starts up and becomes the leader of a single-node raft cluster. Minimally implements the "state machine handling loop".
- [02-single-node-proposals](02-single-node-proposals) \
  The leader of a single-node cluster sends proposals and receives them as committed log entries. Introduces a simple FSM which remembers the "commands" from all committed entries.
- [03-detour-memory-storage](03-detour-memory-storage) \
  Overview of `etcd-io/raft` "memory storage" interface and implementation, plus some unit tests.
- [04-detour-node-refactor](04-detour-node-refactor) \
  Refactoring of the single-node code, to simplify implementing a two-node cluster later. Functionally equivalent to `02-single-node-proposals`.
- [05-two-node-cluster](05-two-node-cluster) \
  A new node joins running single-node cluster, and receives all committed messages from the leader. This expands the "loop" implementation to route messages between the nodes.
- [06-two-node-snapshot](06-two-node-snapshot) \
  The leader of a single-node cluster makes a snapshot and compacts the log. When a new node joins the cluster, it restores the state from the snapshot and remaining committed entries.
- [07-detour-change-fsm](07-detour-change-fsm) \
  Change the FSM to sum all byte "commands" into a single integer "state" — snapshots now just need to store this value. Run a raft cluster, making random proposals / snapshots, in the end verify that all nodes have the same FSM state.
- [08-running-cluster](08-running-cluster) \
  Set up a 3-node cluster using the new FSM and make message delivery unreliable by dropping 25% of them. Send a bunch of proposals, make nodes periodically snapshot / compact the log, in the end verify consistency of all FSM states.
- [09-async-storage-write](09-async-storage-write) \
  Run a 3-node cluster similar to `08-running-cluster`, with snapshots, log compaction, and so on. However, the "state machine handling loop" is implemented using the "asynchronous storage writes" mechanism.
- [10-separate-process](10-separate-process) \
  Introduce a CLI program which runs one node. Several instances of the program can form a cluster. The running instances deliver messages to each other over network via RPCs.