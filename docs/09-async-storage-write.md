## 09-async-storage-write
Documentation for `etcd-io/raft` package describes the [Asynchronous Storage Writes](https://pkg.go.dev/go.etcd.io/raft/v3#hdr-Usage_with_Asynchronous_Storage_Writes) feature:

>*...an alternate interface for local storage writes that can provide better performance in the presence of high proposal concurrency by minimizing interference between proposals...*

Independent of performance benefits, I find this approach leads to a cleaner separation of code handling "appended" and "committed" entries. We're following the skeleton of the new "state machine handling loop", as [provided](https://github.com/etcd-io/raft/blob/a02bb0ff/doc.go#L200-L258) in the documentation: [`09-async-storage-write/the_test.go`](https://github.com/zvold/using-etcd-io-raft/blob/main/src/09-async-storage-write/the_test.go).

Some changes that were necessary to make this work:
- The `Snapshot` proto now comes from a `Message`, and can be unset (`nil`), so this requires an explicit check.
- The `HardState` needs to be reconstructed from fields of the `Message` (as opposed to being a separate field in `Ready`).

> {% octicon alert height:24 %} Note:
> 
> The test disables the simulation of failed message delivery (introduced in [08-running-cluster](08-running-cluster), because sometimes a message containing the `ConfChange` entry isn't delivered and the new node never joins the cluster.
> 
> To re-enable message delivery failures, the test would need to detect that a configuration change proposal was lost and retry.

Next: [10-separate-process](10-separate-process).