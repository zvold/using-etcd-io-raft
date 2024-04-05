package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"log"
	"math"
	"os"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// blob stores all information necessary to store/restore MemoryStorage to/from disk.
type blob struct {
	HardState raftpb.HardState
	Snapshot  raftpb.Snapshot
	Entries   []raftpb.Entry
}

func (b *blob) String() string {
	return fmt.Sprintf("%+v %+v %+v", b.HardState, b.Snapshot, b.Entries)
}

func (b *blob) Verbose() string {
	var fsm FsmState
	if len(b.Snapshot.Data) != 0 {
		fsm.decode(b.Snapshot.Data)
	}

	s := fmt.Sprintf(`
======== blob ============
HardState.Term:      %d
HardState.Vote:      %d
HardState.Commit:    %d

Snapshot.Term:       %d
Snapshot.Index:      %d
Snapshot.Data(len):  %d
	%s

Entries: `,
		b.HardState.Term,
		b.HardState.Vote,
		b.HardState.Commit,
		b.Snapshot.Metadata.Term,
		b.Snapshot.Metadata.Index,
		len(b.Snapshot.Data),
		&fsm)
	if len(b.Entries) == 0 {
		s += "NONE\n==========================\n"
		return s
	}
	f := func(data []byte) string {
		if len(data) == 0 {
			return "none"
		}
		return fmt.Sprintf("%d", data[0])
	}
	for _, e := range b.Entries {
		s += fmt.Sprintf("\n\t%s", raft.DescribeEntry(e, f))
	}
	s += "\n==========================\n"
	return s
}

// Poor man's blob comparison, which is good enough for tests.
func (b *blob) isEqual(b2 *blob) bool {
	return b.String() == b2.String()
}

// Writes contents of the 'blob' to a file named '.memory-N', where N = id.
func writeBlob(b *blob, id uint64) {
	tmpfile := fmt.Sprintf(".memory-%d.tmp", id)
	os.Remove(tmpfile) // Ignore errors for tmpfile.

	err := os.WriteFile(tmpfile, b.encode(), 0666)
	if err != nil {
		log.Fatalf("Cannot write to '%s' (%s).", tmpfile, err)
	}

	filename := fmt.Sprintf(".memory-%d", id)
	err = os.Rename(tmpfile, filename)
	if err != nil {
		log.Fatalf("Rename to '%s' failed (%s).", filename, err)
	}

	fmt.Printf("Blob written:\n%s\n", b.Verbose())
}

// Reads contents of the blob from the file named '.memory-N', where N = id.
func readBlob(id uint64) *blob {
	filename := fmt.Sprintf(".memory-%d", id)
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Printf("Couldn't restore node state from %s, continuing\n", filename)
		return nil
	}
	var b blob
	b.decode(data)

	fmt.Printf("Blob read:\n%s\n", b.Verbose())
	return &b
}

// Encodes the blob into a byte slice.
func (b *blob) encode() []byte {
	var buf bytes.Buffer
	encoder := gob.NewEncoder(&buf)
	err := encoder.Encode(*b)
	if err != nil {
		log.Fatalf("Failed to encode blob: %+v, err: %s", *b, err)
	}
	return buf.Bytes()
}

// Decodes a blob from the byte slice.
func (b *blob) decode(data []byte) {
	buf := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buf)
	err := decoder.Decode(b)
	if err != nil {
		log.Fatalf("Failed to decode blob: %s, err: %s", data, err)
	}
}

// Converts a MemoryStorage to a blob.
func toBlob(m *raft.MemoryStorage) *blob {
	hs, _, _ := m.InitialState()
	snapshot, _ := m.Snapshot()
	first, _ := m.FirstIndex()
	last, _ := m.LastIndex()
	entries, _ := m.Entries(first, last+1, math.MaxInt32)

	return &blob{
		HardState: hs,
		Snapshot:  snapshot,
		Entries:   entries,
	}
}

// Converts a blob into a MemoryStorage.
func fromBlob(b *blob) *raft.MemoryStorage {
	if b == nil {
		return nil
	}
	m := raft.NewMemoryStorage()
	m.ApplySnapshot(b.Snapshot)
	m.SetHardState(b.HardState)
	m.Append(b.Entries)
	return m
}
