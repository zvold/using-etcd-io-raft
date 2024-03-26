The code for this section (see [`01-single-node-cluster/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/01-single-node-cluster/the_test.go)) follows the [instructions](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L75-L81) from the `etcd-io/raft` README to start a single-node cluster, and implements the ["state machine handling loop"](https://github.com/etcd-io/raft/blob/ffe5efcf/README.md?plain=1#L136-L162), to the extent necessary for the cluster to function.

Specifically, for this minimal example, the implementation skips the following steps of the loop:
  - sending messages to other nodes, as there's only one node in the cluster;
  - applying snapshots, as nothing creates snapshots yet.

The single-node cluster is expected to go through the following stages:
  - The node starts in the 'follower' state — the test verifies this before continuing.
  - It starts the voting process and wins the vote — the test verifies that the node reaches the 'leader' state.

 The test can be run with:
 ```bash
 mkdir -p /tmp/01 && cd /tmp/01
 go mod init tmp
 go get  -t github.com/zvold/using-etcd-io-raft/src/01-single-node-cluster@latest
 go test -v github.com/zvold/using-etcd-io-raft/src/01-single-node-cluster
 ```
 
 Or, if this repository is cloned locally, directly from the `src` directory:
 ```bash
 go test -v ./01-single-node-cluster
 ```

Next: [02-single-node-proposals](02-single-node-proposals).