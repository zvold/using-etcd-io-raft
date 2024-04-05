package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/rpc"
	"os"
	"strconv"
	"strings"

	"github.com/zvold/using-etcd-io-raft/src/util"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	idFlag   = flag.Uint64("id", 0x1, "Node id, default is 1.")
	portFlag = flag.Int("port", 0, "Overrides port on which this node will listen for RPCs.")
	joinFlag = flag.String("join", "1=localhost:8866", "Specifies one of the nodes in the cluster "+
		"to join, format: node_id=host:port. If not set, this node is assumed to be the first "+
		"(bootstrap) node of the cluster.")
	bootstrapFlag = flag.Bool("bootstrap", false,
		"Set to true if this is the first node of a cluster.")
)

// Starts the first node of raft cluster (id=1), returns the constructed node.
func runBootstrapNode(
	storage *raft.MemoryStorage,
	id uint64,
	port int,
	stop chan struct{},
) *node {
	var node *node
	if storage == nil {
		node = newNode(id, port, []raft.Peer{{ID: id}}, false /*restart*/, stop)
	} else {
		node = recoverNode(id, port, storage, stop)
	}

	log.Printf("Node %d: listening on port %d.\n", node.id(), node.port())

	// Propose a ConfChange with details on how to contact this node.
	p := peer{Id: int(node.id()), Address: fmt.Sprintf("localhost:%d", node.port())}
	node.ProposeConfChange(
		context.TODO(),
		raftpb.ConfChange{
			Type:    raftpb.ConfChangeUpdateNode,
			NodeID:  node.id(),
			Context: p.encode(),
		})

	// Propose a command with value 1<<1.
	node.Propose(context.TODO(), []byte{1 << node.id()})

	return node
}

// Starts a new node that joins the existing cluster (node 1) at 'address'.
// Returns true if it joined successfully and observed its command (1<<id).
func runJoiningNode(
	storage *raft.MemoryStorage,
	id uint64,
	port int,
	address string,
	stop chan struct{},
) *node {
	var node *node
	if storage == nil {
		node = newNode(id, port, nil /*peers*/, true /*restart*/, stop)
	} else {
		node = recoverNode(id, port, storage, stop)
	}

	// When joining the cluster, the node will have to respond to heartbeats from the leader.
	// even before replicating the state. Save the address of the only node we know about, to
	// be able to talk back.
	chunks := strings.Split(address, "=")
	if len(chunks) != 2 {
		log.Fatalf("Incorrectly formatted --join flag: %s", address)
	}
	id2, err := strconv.Atoi(chunks[0])
	if err != nil {
		log.Fatalf("Invalid target node id %s", chunks[0])
	}
	node.fsmState.setPeer(uint64(id2), chunks[1])

	// Start RPC server, passing incoming messages to the node.
	log.Printf("Node %d: listening on port %d.\n", node.id(), node.port())

	// Prepare a ConfChange proposal announcing this new node to the cluster.
	p := peer{Id: int(node.id()), Address: fmt.Sprintf("localhost:%d", node.port())}
	cc := raftpb.ConfChange{
		Type:    raftpb.ConfChangeAddNode,
		NodeID:  node.id(),
		Context: p.encode(),
	}

	client, err := rpc.DialHTTP("tcp", chunks[1])
	if err != nil {
		log.Fatalf("Node %d: cannot connect to '%s' (%s).\n", node.id(), chunks[1], err)
	}
	defer client.Close()

	// Send the ConfChange over network.
	var reply struct{}
	err = client.Call("NodeRpc.ProposeConfChange", cc, &reply)
	if err != nil {
		log.Fatalf("Node %d: ConfChange proposal failure: %s.\n", node.id(), err)
	}

	// Propose a command (1<<id) to this node directly.
	node.Propose(context.TODO(), []byte{1 << node.id()})

	return node
}

func main() {
	flag.Parse()

	stop := make(chan struct{})
	defer close(stop)

	var node *node

	// This is either 'nil' or a MemoryStorage recovered '.memory-N' file, if present.
	storage := fromBlob(readBlob(*idFlag))

	if *bootstrapFlag {
		// This process runs the first node of the cluster.
		fmt.Println("bootstrap")
		node = runBootstrapNode(storage, *idFlag, *portFlag, stop)
	} else {
		fmt.Println("joining")
		// This process runs a node that joins the cluster.
		node = runJoiningNode(storage, *idFlag, *portFlag, *joinFlag, stop)
	}

	// 's' prints debug info, 'p' proposes a command, otherwise stop the node.
	reader := bufio.NewReader(os.Stdin)
	for {
		b, _ := reader.ReadString(byte('\n'))
		if b == "s\n" {
			fmt.Println(node)
		} else if b == "p\n" {
			node.Propose(context.TODO(), []byte{3})
		} else {
			break
		}
	}

	// Remove node from the cluster, unless it's the last node.
	if len(node.fsmState.Peers) > 1 {
		node.ProposeConfChange(
			context.TODO(),
			raftpb.ConfChange{
				Type:   raftpb.ConfChangeRemoveNode,
				NodeID: node.id(),
			})
		if !util.WaitNoTicks(func() bool { return node.fsmState.getPeer(node.id()) == "" }) {
			log.Fatalf("ConfChangeRemoveNode isn't committed: %s", node)
		}
	}

	node.shutdown()
	if !util.WaitNoTicks(func() bool { return node.id() == 0 }) {
		log.Fatalf("The node hasn't stopped: %s", node)
	}

	log.Println(node)
}
