package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"log"
	"maps"
	"sync"
)

// =====================================================================
// peer stores id->address mapping, and is used in ConfChange proposals.
type peer struct {
	Id      int    // Raft node id.
	Address string // Node's host:port where it listens for messages.
}

// Encodes the peer into a byte slice.
func (p *peer) encode() []byte {
	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)
	err := encoder.Encode(*p)
	if err != nil {
		log.Fatalf("Failed to encode peer: %+v, err: %s", *p, err)
	}
	return buf.Bytes()
}

// Decodes an peer from the byte slice.
func (p *peer) decode(data []byte) {
	buf := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buf)
	err := decoder.Decode(p)
	if err != nil {
		log.Fatalf("Failed to decode peer: %s, err: %s", data, err)
	}
}

// ==========================================================================
// FsmState encapsulates the FSM state, including "configuration" state (e.g.
// how to reach peers over the network).
type FsmState struct {
	sync.Mutex                // Protects FsmState, as it's accessed from different goroutines.
	State      int            // Simple internal state is just an int.
	Peers      map[int]string // Maps (node id) --> (peer's host:port).
}

// Constructs ready to use FsmState.
func NewFsmState() *FsmState {
	return &FsmState{Peers: make(map[int]string)}
}

// Pretty-print the FsmState.
func (f *FsmState) String() string {
	f.Lock()
	defer f.Unlock()

	var s string = fmt.Sprintf("FSM { State = %d", f.State)
	for k, v := range f.Peers {
		s += fmt.Sprintf("\n\t      node %d: %s", k, v)
	}
	s += " }"
	return s
}

// Returns a copy of Peers map.
func (f *FsmState) peers() map[int]string {
	f.Lock()
	defer f.Unlock()
	return maps.Clone(f.Peers)
}

func (f *FsmState) setPeer(id uint64, address string) {
	f.Lock()
	defer f.Unlock()
	f.Peers[int(id)] = address
}

// Returns peer node's address or "" if peer is unknown.
func (f *FsmState) getPeer(id uint64) string {
	f.Lock()
	defer f.Unlock()
	return f.Peers[int(id)]
}

// Deletes the specified node from the Peers map.
func (f *FsmState) deletePeer(id uint64) {
	f.Lock()
	defer f.Unlock()
	delete(f.Peers, int(id))
}

// Compares two FsmStates.
func Equal(f1, f2 *FsmState) bool {
	if f1 == nil || f2 == nil {
		return f1 == f2
	}

	f1.Lock()
	defer f1.Unlock()
	f2.Lock()
	defer f2.Unlock()

	return f1.State == f2.State && maps.Equal(f1.Peers, f2.Peers)
}

// Applies a "command" log entry. This simple FSM just sums the byte "commands".
func (f *FsmState) applyCommand(b byte) {
	f.Lock()
	defer f.Unlock()
	f.State += int(b)
}

// Applies a ConfChange log entry, which currently means adding a new peer.
// The nodes don't leave the cluster in this example.
func (f *FsmState) applyConfChange(p peer) {
	f.Lock()
	defer f.Unlock()
	f.Peers[p.Id] = p.Address
}

// Fully encodes the FsmState in a byte slice.
func (f *FsmState) encode() []byte {
	f.Lock()
	defer f.Unlock()

	// Same as FsmState, but without the mutex (which fails serialization).
	type FsmState2 struct {
		State int
		Peers map[int]string
	}
	f2 := FsmState2{State: f.State, Peers: f.Peers}

	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)
	err := encoder.Encode(f2)
	if err != nil {
		log.Fatalf("Failed to encode FsmState: %+v, err: %s", f2, err)
	}
	return buf.Bytes()
}

// Decodes an FsmState from the byte slice.
func (f *FsmState) decode(data []byte) {
	f.Lock()
	defer f.Unlock()

	if len(data) == 0 {
		return
	}

	buf := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buf)
	err := decoder.Decode(f)
	if err != nil {
		log.Fatalf("Failed to decode FsmState: %s, err: %s", data, err)
	}
}
