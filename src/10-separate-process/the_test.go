package main

import (
	"fmt"
	"net"
	"testing"

	"github.com/zvold/using-etcd-io-raft/src/util"
)

type pair struct {
	n *node
	l net.Listener
}

func (p pair) String() string {
	return fmt.Sprintf("%s\n", p.n)
}

func Test_SeparateProcesses(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)

	list := make([]pair, 0)

	n, l := runBootstrapNode(0x1, "localhost:0", stop)
	list = append(list, pair{n, l})

	address := fmt.Sprintf("localhost:%d", l.Addr().(*net.TCPAddr).Port)

	n, l = runJoiningNode(0x2, address, stop)
	list = append(list, pair{n, l})

	n, l = runJoiningNode(0x3, address, stop)
	list = append(list, pair{n, l})

	if !util.WaitNoTicks(func() bool {
		for _, v := range list {
			// We expect the FSM state of (1<<1 + 1<<2 + 1<<3)
			if v.n.fsmState.State != 14 {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("The nodes haven't received all commands: \n%+v", list)
	}

	for _, v := range list {
		v.l.Close()
		v.n.Stop()
	}

	if !util.WaitNoTicks(func() bool {
		for _, v := range list {
			if v.n.id() != 0 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("The nodes haven't stopped cleanly.")
	}
}
