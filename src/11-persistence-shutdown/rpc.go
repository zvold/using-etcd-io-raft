package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// NodeRpc receives RPC requests and calls appropriate methods on the
// underlying node.
type NodeRpc struct {
	n *node
}

// ProposeConfChange RPC is called by nodes wishing to join the cluster. They send
// a ConfChange describing itself to one of the existing nodes of the cluster.
func (h *NodeRpc) ProposeConfChange(cc raftpb.ConfChange, reply *struct{}) error {
	fmt.Printf("Node %d: received ConfChange: %+v\n", h.n.id(), cc)
	h.n.ProposeConfChange(context.TODO(), cc)
	return nil
}

// ProcessMessage RPC is called by a node which needs to deliver a message to
// another node. On the server side, if the message is addressed to the server's
// node, it's processed directly, otherwise it's relayed further via node.Send().
func (h *NodeRpc) ProcessMessage(m raftpb.Message, reply *struct{}) error {
	if m.Type != raftpb.MsgHeartbeat && m.Type != raftpb.MsgHeartbeatResp {
		fmt.Printf("Node %d: received Message: %s\n", h.n.id(), raft.DescribeMessage(m, nil))
	}

	if m.To == h.n.id() {
		// Message is addressed to this node, process it directly.
		h.n.Step(context.TODO(), m)
	} else {
		// The sender couldn't reach destination directly, try relaying it for them.
		fmt.Printf("Node %d: relaying a message for node %d\n", h.n.id(), m.From)
		// TODO(zvold): drop the messages after one or a few relay attempts, otherwise they
		// can bounce like this forever.
		h.n.send([]raftpb.Message{m})
	}
	return nil
}

// Starts RPC server and returns the underlying listener, so we could close it properly.
func startRpcServer(n *node, port int) net.Listener {
	serv := rpc.NewServer()
	r := NodeRpc{n: n}
	serv.Register(&r)

	// Without this, it's not possible to start several RPC servers (one per node)
	// in a test. See https://github.com/golang/go/issues/13395
	oldMux := http.DefaultServeMux
	// Override 'DefaultServeMux' temporarily (serv.HandleHTTP is not parametrized)
	mux := http.NewServeMux()
	http.DefaultServeMux = mux
	serv.HandleHTTP(rpc.DefaultRPCPath, rpc.DefaultDebugPath)
	// Restore old mux.
	http.DefaultServeMux = oldMux

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal("Cannot start RPC server, error: ", err)
	}
	go http.Serve(l, mux)

	return l
}
