package main

import (
	"testing"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Creates a MemoryStorage with entries in [1, 3] range.
func s_1_3() *raft.MemoryStorage {
	s := raft.NewMemoryStorage()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 1, Data: []byte{'a'}},
		raftpb.Entry{Index: 2, Data: []byte{'a'}},
		raftpb.Entry{Index: 3, Data: []byte{'a'}},
	})
	return s
}

// Creates a MemoryStorage with entries in [11, 13] range, and snapshot until index 10.
func s_11_13() *raft.MemoryStorage {
	s := s_1_3()
	s.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 10}})
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 11, Data: []byte{'a'}},
		raftpb.Entry{Index: 12, Data: []byte{'a'}},
		raftpb.Entry{Index: 13, Data: []byte{'a'}},
	})
	return s
}

func Test_EmptyStorage(t *testing.T) {
	s := raft.NewMemoryStorage()
	assertConversion(s, t)
}

func Test_Append(t *testing.T) {
	s := s_1_3()
	assertConversion(s, t)
}

func Test_Append_Grow(t *testing.T) {
	s := s_1_3()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
		raftpb.Entry{Index: 6, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_Append_Overlap(t *testing.T) {
	s := s_1_3()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 2, Data: []byte{'f'}},
		raftpb.Entry{Index: 3, Data: []byte{'f'}},
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_Append_FullOverlap(t *testing.T) {
	s := s_1_3()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 0, Data: []byte{'f'}},
		raftpb.Entry{Index: 1, Data: []byte{'f'}},
		raftpb.Entry{Index: 2, Data: []byte{'f'}},
		raftpb.Entry{Index: 3, Data: []byte{'f'}},
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_ApplySnapshot_EmptyStorage(t *testing.T) {
	s := s_1_3()
	s.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 10}})
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append(t *testing.T) {
	s := s_11_13()
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append_MinimalOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 9, Data: []byte{'f'}},
		raftpb.Entry{Index: 10, Data: []byte{'f'}},
		raftpb.Entry{Index: 11, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append_NoOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
		raftpb.Entry{Index: 6, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append_FullOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 8, Data: []byte{'f'}},
		raftpb.Entry{Index: 9, Data: []byte{'f'}},
		raftpb.Entry{Index: 10, Data: []byte{'f'}},
		raftpb.Entry{Index: 11, Data: []byte{'f'}},
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
		raftpb.Entry{Index: 13, Data: []byte{'f'}},
		raftpb.Entry{Index: 14, Data: []byte{'f'}},
		raftpb.Entry{Index: 15, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append_InsideOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 14, Data: []byte{'a'}},
	})
	assertConversion(s, t)

	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
		raftpb.Entry{Index: 13, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_ApplySnapshot_Append_RightOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 14, Data: []byte{'a'}},
	})
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
		raftpb.Entry{Index: 13, Data: []byte{'f'}},
		raftpb.Entry{Index: 14, Data: []byte{'f'}},
		raftpb.Entry{Index: 15, Data: []byte{'f'}},
		raftpb.Entry{Index: 16, Data: []byte{'f'}},
	})
	assertConversion(s, t)
}

func Test_CreateSnapshot_Compact(t *testing.T) {
	s := s_11_13()
	s.CreateSnapshot(12, nil, []byte{'x'})
	// CreateSnapshot doesn't remove old entries, but they're effectively invisible for serialization,
	// as MemoryStorage.Entries won't return them. That's why there's no assertConversion() call here.
	s.Compact(12)
	assertConversion(s, t)
}

func Test_CreateSnapshot_Compact_All(t *testing.T) {
	s := s_11_13()
	s.CreateSnapshot(13, nil, []byte{'x'})
	s.Compact(13)
	assertConversion(s, t)
}

func equal(m1, m2 *raft.MemoryStorage) bool {
	return toBlob(m1).isEqual(toBlob(m2))
}

func assertConversion(s *raft.MemoryStorage, t *testing.T) {
	b := toBlob(s)

	var b2 blob
	b2.decode(b.encode())
	if !b.isEqual(&b2) {
		t.Errorf("encode/decode doesn't preserve the blob for:\n%+v\n", s)
	}

	s2 := fromBlob(b)
	if !equal(s, s2) {
		t.Errorf("s != fromBlob( toBlob(s) ):\nwas:%+v\nhas:%+v\n", s, s2)
	}
}
