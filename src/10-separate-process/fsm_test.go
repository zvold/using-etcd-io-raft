package main

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func Test_FsmState_ApplyCommand(t *testing.T) {
	f := NewFsmState()
	f.applyCommand(40)
	f.applyCommand(2)
	if f.State != 42 {
		t.Fatalf("Expected FsmState.State: 42, was: %d", f.State)
	}
}

func Test_FsmState_ApplyConfChange(t *testing.T) {
	f := NewFsmState()
	f.applyConfChange(peer{Id: 42, Address: "host:port"})
	if len(f.Peers) != 1 {
		t.Fatalf("Expected len(FsmState.Peers) = 1, was: %d", len(f.Peers))
	}
	if f.Peers[42] != "host:port" {
		t.Fatalf("Expected f.Peers[42] = 'host:port', was: %+v", f.Peers[42])
	}
}

func Test_FsmState_EncodeDecode(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 1000; i++ {
		f := NewFsmState()
		f.applyCommand(byte(r.Intn(200)))
		for j := 0; j < 10; j++ {
			f.applyConfChange(
				peer{Id: r.Intn(100), Address: fmt.Sprintf("host:%d", r.Intn(1000))},
			)
		}
		var f2 FsmState
		f2.decode(f.encode())
		if !Equal(f, &f2) {
			t.Fatalf("FsmState encoding/decoding failure:\noriginal: %s\nrestored:%s", f, &f2)
		}
	}
}

func Test_Peer_EncodeDecode(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 1000; i++ {
		p := peer{Id: r.Intn(100), Address: fmt.Sprintf("host:%d", r.Intn(1000))}
		var p2 peer
		p2.decode(p.encode())
		if p != p2 {
			t.Fatalf("Peer encoding/decoding failure:\noriginal: %+v\nrestored:%+v", p, p2)
		}
	}
}
