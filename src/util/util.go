package util

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.etcd.io/raft/v3"
)

// Send "ticks" to the 'tick' channel, until 'predicate()' returns true.
// Returns 'false' if the predicate is not satisfied after 100 ticks.
func WaitWithTicks(predicate func() bool, tick chan<- struct{}) bool {
	for i := 0; i < 100; i++ {
		if predicate() {
			return true
		}
		tick <- struct{}{}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// Don't send "ticks", just wait until 'predicate()' returns true.
// Returns 'false' if the predicate is not satisfied after 100 ticks.
func WaitNoTicks(predicate func() bool) bool {
	for i := 0; i < 200; i++ {
		if predicate() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func IntToBytes(i int) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutVarint(buf, int64(i))
	return buf[:n]
}

func BytesToInt(buf []byte) int {
	r, _ := binary.Varint(buf)
	return int(r)
}

// Helper for pretty-printing raft.Ready structs.
func ReadyToStr(rd *raft.Ready) (s string) {
	if !raft.IsEmptyHardState(rd.HardState) {
		s += fmt.Sprintf("\n\tHardState: %+v", rd.HardState)
	}
	if len(rd.Entries) != 0 {
		s += fmt.Sprintf("\n\tEntries:   ")
		prefix := ""
		for _, e := range rd.Entries {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeEntry(e, nil))
			prefix = "\n\t\t   "
		}
	}
	if !raft.IsEmptySnap(rd.Snapshot) {
		s += fmt.Sprintf("\n\tSnapshot:  %s", raft.DescribeSnapshot(rd.Snapshot))
	}
	if len(rd.Messages) != 0 {
		s += fmt.Sprint("\n\tMessages:  ")
		prefix := ""
		for _, m := range rd.Messages {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeMessage(m, nil))
			prefix = "\n\t\t   "
		}
	}
	if len(rd.CommittedEntries) != 0 {
		s += fmt.Sprint("\n\tCommitted: ")
		prefix := ""
		for _, e := range rd.CommittedEntries {
			s += fmt.Sprintf("%s%s", prefix, raft.DescribeEntry(e, nil))
			prefix = "\n\t\t   "
		}
	}
	return
}
