---
permalink: /
---
> {% octicon alert height:24 %} Note:
> 
> Work in progress, current status: step 8 out of approx. 12 steps.

This is a series of blog posts describing how to build a small program that uses the [`etcd-io/raft`](https://github.com/etcd-io/raft) Go library.

The README of `etcd-io/raft` links to a "simple example application", [_raftexample_](https://github.com/etcd-io/etcd/tree/main/contrib/raftexample), which I found too complex to follow. For example, it pulls dependencies from [`etcd-io/etcd`](https://github.com/etcd-io/etcd), which shouldn't be necessary for a minimal example.

Therefore, I'm attempting to build my own "simple example application", starting by following the ["usage"](https://github.com/etcd-io/raft#usage) section of the `etcd-io/raft` README to build a minimal program that runs a single-node raft cluster: [01-single-node-cluster](01-single-node-cluster).