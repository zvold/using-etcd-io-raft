## 04-detour-node-refactor
The code from [`02-single-node-proposals/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/02-single-node-proposals/the_test.go)  is refactored so that each node keeps track of its own `Storage` and committed entries. This should make writing code for a 2-node cluster easier.

The refactored test lives in [`04-detour-node-refactor/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/04-detour-node-refactor/the_test.go).

The main changes it introduces are:
- defining a `node struct` which extends `raft.Node` but keeps track of its own `MemoryStorage` and FSM state,
- moving some functionality into convenience methods like `node::newNode` and `node::runNode`.

Next: [05-two-node-cluster](05-two-node-cluster).