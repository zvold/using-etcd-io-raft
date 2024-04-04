package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"strings"

	"github.com/zvold/using-etcd-io-raft/src/util"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

var (
	joinFlag = flag.Bool("join", false,
		"Specify if this node is joining, or it's the first one in the cluster.")
	idFlag = flag.Uint64("id", 0x1,
		"Node id, must be set if --join=true. Must be 1 (default) for the bootstrap node.")
	addressFlag = flag.String("address", "localhost:8866",
		"Address of some node of the cluster.")
)

// Starts first node of the raft cluster (id=1), listening on the default port 8866.
// Returns true if it observed its own command (1<<id) being committed.
func runBootstrapNode(id uint64, address string, stop chan struct{}) (*node, net.Listener) {
	// This process runs the first node of the cluster.
	if id != 0x1 {
		log.Fatal("The bootstrap node must have id=1 (default for the --id flag).")
	}

	node := newNode(0x1, []raft.Peer{{ID: 0x1}}, false /*restart*/, stop)

	port, err := strconv.Atoi(strings.Split(address, ":")[1])
	if err != nil {
		log.Fatalf("Can't extract port from '%s'.", address)
	}

	// Start RPC server, passing incoming messages to the node.
	l := startRpcServer(node, port)
	port = l.Addr().(*net.TCPAddr).Port
	fmt.Printf("Listening on port %d.\n", port)

	// Propose a ConfChange with details on how to contact this node.
	p := peer{Id: 1, Address: fmt.Sprintf("localhost:%d", port)}
	node.ProposeConfChange(
		context.TODO(),
		raftpb.ConfChange{
			Type:    raftpb.ConfChangeUpdateNode,
			NodeID:  0x1,
			Context: p.encode(),
		})

	// Propose a command with value 1<<1.
	node.Propose(context.TODO(), []byte{1 << node.id()})

	return node, l
}

// Starts a new node that joins the existing cluster (node 1) at 'address'.
// Returns true if it joined successfully and observed its command (1<<id).
func runJoiningNode(id uint64, address string, stop chan struct{}) (*node, net.Listener) {
	if id == 0x1 {
		log.Fatal("When --join=true, the flag --id=N is required, where N!=1.")
	}

	node := newNode(id, nil, true /*restart*/, stop)

	// When joining the cluster, the node will have to respond to heartbeats from the leader.
	// even before replicating the state. Save the address of the only node we know about, to
	// be able to talk back.
	node.fsmState.setPeer(0x1, address)

	// Start RPC server, passing incoming messages to the node.
	l := startRpcServer(node, 0)
	port := l.Addr().(*net.TCPAddr).Port
	fmt.Printf("Listening on port %d.\n", port)

	// Prepare a ConfChange proposal announcing this new node to the cluster.
	p := peer{Id: int(node.id()), Address: fmt.Sprintf("localhost:%d", port)}
	cc := raftpb.ConfChange{
		Type:    raftpb.ConfChangeAddNode,
		NodeID:  node.id(),
		Context: p.encode(),
	}

	client, err := rpc.DialHTTP("tcp", address)
	if err != nil {
		log.Fatalf("Node %d: cannot connect to '%s' (%s).\n", node.id(), address, err)
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

	return node, l
}

// Stops the node and the listener and waits for it to actually stop.
func shutdown(n *node, l net.Listener) {
	// Close the underlying listener of the HTTP server handling RPCs.
	l.Close()
	n.Stop()

	if !util.WaitNoTicks(func() bool { return n.id() == 0 }) {
		log.Fatalf("The node hasn't stopped: %s", n)
	}
}

func main() {
	flag.Parse()

	stop := make(chan struct{})
	defer close(stop)

	var node *node
	var l net.Listener
	if !*joinFlag {
		// This process runs the first node of the cluster.
		node, l = runBootstrapNode(*idFlag, *addressFlag, stop)
	} else {
		// This process runs a node that joins the cluster.
		node, l = runJoiningNode(*idFlag, *addressFlag, stop)
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

	shutdown(node, l)
	log.Println(node)
}
