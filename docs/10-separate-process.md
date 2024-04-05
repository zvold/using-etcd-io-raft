This section describes a program which runs a single node of a raft cluster, and communicates with other instances of the same program via network. This is a relatively big change compared to the [09-async-storage-write](09-async-storage-write) example, so here's an overview of how the functionality is split between different files:
```
10-separate-process/
 ├──── fsm.go       // FSM implementation, including creating/restoring snapshots.
 ├──── node.go      // The familiar 'node struct' extracted into a file.
 ├──── rpc.go       // RPC server, handles incoming requests by calling the node methods.
 ├──── server.go    // Main program, capable of bootstrapping or joining a cluster.
 └──── the_test.go  // Runs a 3-node cluster using functionality from server.go.
```
#### `fsm.go`
Up until now, the FSM state was represented by just an integer. The FSM was simply adding the committed command entries together. Here, we're changing it to include the information on how to contact peer nodes over the network:
``` go
type FsmState struct {
  state   int             // The integer FSM state, sums committed commands.
  peers   map[int]string  // Maps (node id) --> (another node's host:port).
}
```

When a node runs as a separate process, it learns about new peers through raft log entries (specifically `ConfChange`). Just like the regular "command" entries, these mutate the FSM state (by adding the new node's listening address) when committed.

The `ConfChange` protobuf conveniently includes a `context` field, which can be used to pass arbitrary data. We use it to encode the following struct, which describes how to contact a peer node:
```go
type peer struct {
  Id      int    // Raft node id.
  Address string // Node's host:port where it listens for messages.
}
```

The `FsmState` still handles normal command (single-byte) entries by summing them up into the `state` field. This makes it easy to write assertions on committed entries when testing.
#### `node.go`
This file contains the `node struct` implementation from the previous sections, with a few small changes.

First, to represent the FSM state, it uses the `FsmState` (from [`fsm.go`](#codefsmgocode) above) instead of a simple `int`, and makes sure to modify it accordingly when handling committed configuration changes. The latter are now expected to contain the `peer` encoded in the `context` field. Also, when creating a snapshot (for log compaction) or restoring the log from a snapshot, it uses the `encode()/decode()` methods of the `FsmState`.

Second, sending messages to other nodes is necessarily modified (the code cannot just call `Node.Step` directly, since the target is now in a separate process). So a message addressed to a node N is sent out by looking up the target node's address (in `FsmState.peers`) and making an RPC.

> {% octicon alert height:24 %} Note:
>
> The `net.Client` should really be reused for the node's lifetime (one client per peer), but for simplicity it's created and closed immediately for each RPC sent. 

Importantly, when the node doesn't know the destination address, it sends the message to any other node from `FsmState.peers`. The idea here is that the message gets relayed to the proper destination (see the note [below](#codeservergocode) for more details).
#### `rpc.go`
As messages from other nodes now arrive via network, we need some way of receiving and handling the messages. This file provides the `startRpcServer()` function to start an RPC server, implemented by using the standard Go package `net/rpc`.

The first RPC `NodeRpc.ProcessMessage` implements a way for calling `Node.Step` remotely on another node. On the server side, it receives a `raftpb.Message` as a request, and simply calls `Node.Step` on the local node.

The second RPC `NodeRpc.ProposeConfChange` is a way for the cluster to learn that a new node is about to join. The joining node sends a `ConfChange` proposal as an RPC request to an existing node of the cluster.
#### `server.go`
This is the main CLI program, which operates in two modes.

**Bootstrapping mode.** This is as simple as:
1. Starting a raft node ­— implemented by `node struct`. The first node's id is always 1.
2. Starting an RPC server to handle received messages — implemented by `startRpcServer()`.
3. Making sure the node's `FsmState` contains the mapping b/w the node's id and its RPC server address. This can be achieved in two ways: (a) initializing the `FsmState` directly to have the mapping, or (b) proposing a `ConfChange` locally with an appropriate `context` message. The bootstrapping implementation uses the second approach.

**Joining mode.** In this mode, the program:
1. Starts a raft node and an RPC server the same way as in the bootstrapping mode. For the nodes joining a cluster, the ids are >1.
2. Announces that a new node wishes to join the cluster by making a `NodeRpc.ProposeConfChange` RPC on one of the cluster nodes. The `context` on the `ConfChange` request contains the joining node's address. In practice, these RPCs are sent to the node 1. Most likely, the node 1 by now is also the leader, but it doesn't matter — this proposal can be sent to any node of the cluster.
3. Since the node might have to respond to heartbeats from the leader before it receives the latest FSM state, in order to be able to send the messages back, we directly initialize the `FsmState` with the address of node 1 (the only known node).

In both modes, the program proposes a "command" entry to the local node with a byte value of  `1 << node_id`. This is done for the testing purposes — to verify that the log is replicated properly across the cluster.

> {% octicon alert height:24 %} Note:
>
> When a node joins the cluster, it knows the address of some node on the cluster and sends a `ConfChange` proposal there. It's possible that this target node is not the leader at the moment. However, log replication messages, heartbeat messages, etc. will come back from the leader. This is problematic, because then the joining node won't know where to send the responses.
>
> Here's an example sequence:
>   1. Node 1 runs in a single-node cluster and is the leader.
>   2. Node 2 joins by sending proposals to node 1.
>       When instructed (by raft) to send messages to node 1, it sends them to node 1's address.
>   3. Node 2 happens to become the leader.
>   4. Node 3 wants to join the cluster. It knows only node 1's address, and sends its proposals there.
>   5. Node 3 receives messages (heartbeats, etc.) from node 2 (the leader), but cannot respond, as it doesn't know node 2 address.
>       The problem is that node 3 knows only its own listening address and the address of node 1.
>
> To address this, we're allow "indirect" delivery of messages. In the above example, if node 3 doesn't know how to deliver a `node 3 --> node 2` message, it just sends it to node 1. Node 1 of course knows how to contact node 2 (they've been in the cluster for a while), and relays the message on behalf of node 3. Eventually, node 3 gets enough of the log replicated (populating peer addresses in the FSM state) and is able deliver the messages directly.
#### `the_test.go`
The [test](https://github.com/zvold/using-etcd-io-raft/blob/main/src/10-separate-process/the_test.go) utilizes `server.go` to run a 3-node cluster. For the test purposes, the nodes still run in the same process, but they do communicate over network:
1. Start node 1, listening on the default address `localhost:8866`.
   Propose a command entry `1 << 1` to the (local) node 1 directly.
2. Start node 2, initialize `FsmState` with the node address, and send `ProposeConfChange` RPC to node 1 over the network.
   The program proposes a command `1 << 2` to node 2 locally.
4. Start node 3, initialize `FsmState` with the node address, and send `ProposeConfChange` RPC to node 1 over the network.
   The program proposes a command `1 << 3` to node 3 locally.
5. Verify that the FSM integer state on all 3 nodes is equal to 14 (the sum of all proposed commands, `0x1110`).

Next: [11-persistence-shutdown](11-persistence-shutdown).