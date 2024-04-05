package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/zvold/using-etcd-io-raft/src/util"
)

func Test_SeparateProcesses(t *testing.T) {
	defer func() {
		for i := 1; i <= 3; i++ {
			os.Remove(fmt.Sprintf(".memory-%d", i))
		}
	}()

	stop := make(chan struct{})
	defer close(stop)

	nodes := make(map[uint64]*node)

	nodes[0x1] = runBootstrapNode(nil /*storage*/, 0x1, 0 /*port*/, stop)

	address := fmt.Sprintf("1=localhost:%d", nodes[0x1].port())

	nodes[0x2] = runJoiningNode(nil /*storage*/, 0x2, 0 /*port*/, address, stop)
	nodes[0x3] = runJoiningNode(nil /*storage*/, 0x3, 0 /*port*/, address, stop)

	if !util.WaitNoTicks(func() bool {
		for _, n := range nodes {
			// We expect the FSM state of (1<<1 + 1<<2 + 1<<3)
			if n.fsmState.State != 14 {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("The nodes haven't received all commands: \n%+v", nodes)
	}

	// Shut down node 3 - this creates '.memory-3' file.
	nodes[0x3].shutdown()
	if !util.WaitNoTicks(func() bool { return nodes[0x3].id() == 0 }) {
		log.Fatalf("Node 3 hasn't stopped: %s", nodes[0x3])
	}
	delete(nodes, 0x3)

	// Make some proposals on remaining nodes so they change the state.
	for i := 0; i < 10; i++ {
		nodes[uint64(r.Intn(2)+1)].Propose(context.TODO(), []byte{byte(r.Intn(10) + 1)})
		time.Sleep(50 * time.Millisecond)
	}

	// Recover node 3 from '.memory-3'.
	var b *blob
	b = readBlob(0x3)
	if b == nil {
		t.Fatalf("Cannot recover the node from expected '.memory-3' file.")
	}
	nodes[0x3] = runJoiningNode(fromBlob(b), 0x3, 0 /*port*/, address, stop)

	// Wait until all nodes reach the same FSM state.
	if !util.WaitNoTicks(
		func() bool {
			states := make(map[int]bool)
			for _, n := range nodes {
				states[n.fsmState.State] = true
			}
			return len(states) == 1
		}) {
		t.Fatalf("Node fsm states are not the same: %s, %s, %s.\n",
			nodes[0x1].fsmState, nodes[0x2].fsmState, nodes[0x3].fsmState)
	}
	t.Logf("The nodes have reached the same fsm state = %d.\n", nodes[0x1].fsmState.State)

	for _, v := range nodes {
		v.shutdown()
	}

	if !util.WaitNoTicks(func() bool {
		for _, n := range nodes {
			if n.id() != 0 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("The nodes haven't stopped cleanly.")
	}
}
