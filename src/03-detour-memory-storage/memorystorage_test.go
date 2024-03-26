package main

import (
	"slices"
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
	// Empty memory storage (has only a dummy entry).
	assertRange(s, 1, 0, t)
}

func Test_Append(t *testing.T) {
	s := s_1_3()
	// Test that s_1_3() constructs the expected MemoryStorage.
	assertRange(s, 1, 3, t)       // Entries in [1, 3] are available.
	assertValues(s, 1, 3, 'a', t) // All entries contain command 'a'.
}

func Test_Append_Grow(t *testing.T) {
	s := s_1_3()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
		raftpb.Entry{Index: 6, Data: []byte{'f'}},
	})
	// index:    0123456789
	// storage:   aaa
	// entries:      fff
	// result:    aaafff
	assertRange(s, 1, 6, t)
	assertValues(s, 1, 3, 'a', t)
	assertValues(s, 4, 6, 'f', t)
}

func Test_Append_Overlap(t *testing.T) {
	s := s_1_3()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 2, Data: []byte{'f'}},
		raftpb.Entry{Index: 3, Data: []byte{'f'}},
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
	})
	// index:    0123456789
	// storage:   aaa
	// entries:    ffff
	// result:    affff
	assertRange(s, 1, 5, t)
	assertValues(s, 1, 1, 'a', t)
	assertValues(s, 2, 5, 'f', t)
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
	// indeX:    0123456789
	// storage:   aaa
	// entries:  ffffff
	// result:    fffff
	assertRange(s, 1, 5, t)
	assertValues(s, 1, 5, 'f', t)
}

func Test_Append_Gap(t *testing.T) {
	s := s_1_3()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Appending [5, 7] to a [1, 3] storage should panic")
		}
	}()
	// index:    0123456789
	// storage:   aaa
	// entries:       fff
	// result:    (panic)
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 5},
		raftpb.Entry{Index: 6},
		raftpb.Entry{Index: 7},
	})
}

func Test_ApplySnapshot_EmptyStorage(t *testing.T) {
	s := s_1_3()
	s.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 10}})
	// ApplySnapshot() overrides the MemoryStorage with a dummy entry - the storage is empty.
	assertRange(s, 11, 10, t)

	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() returned error: %v", err)
	}
	if snapshot.Metadata.Index != 10 {
		t.Fatalf("Snapshot() should have index=10, was: %v", snapshot)
	}
}

func Test_ApplySnapshot_Append(t *testing.T) {
	s := s_11_13()
	// Assert that s_11_13() constructs the expected MemoryStorage.
	assertRange(s, 11, 13, t)       // Entries in [11, 13] range are available.
	assertValues(s, 11, 13, 'a', t) // All entries contain command 'a'.

	// Everything up up index 10 is archived in the snapshot.
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() returned error: %v", err)
	}
	if snapshot.Metadata.Index != 10 {
		t.Fatalf("Snapshot() should have index=10, was: %v", snapshot)
	}
}

func Test_Entries_Compacted(t *testing.T) {
	s := s_11_13()
	// Entry with index=10 is compacted in the s_11_13 MemoryStorage.
	_, err := s.Entries(10, 11, 100)
	if err != raft.ErrCompacted {
		t.Fatalf("Entries() should return ErrCompacted, was %v", err)
	}
}

func Test_ApplySnapshot_Append_MinimalOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 9, Data: []byte{'f'}},
		raftpb.Entry{Index: 10, Data: []byte{'f'}},
		raftpb.Entry{Index: 11, Data: []byte{'f'}},
	})
	// index:    4567890123456789
	// storage:  [snap.]aaa
	// entries:       fff
	// result:          f
	assertRange(s, 11, 11, t)
	assertValues(s, 11, 11, 'f', t)
}

func Test_ApplySnapshot_Append_NoOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 4, Data: []byte{'f'}},
		raftpb.Entry{Index: 5, Data: []byte{'f'}},
		raftpb.Entry{Index: 6, Data: []byte{'f'}},
	})
	// index:    4567890123456789
	// storage:  [snap.]aaa
	// entries:  fff
	// result:          aaa
	assertRange(s, 11, 13, t)
	assertValues(s, 11, 13, 'a', t)
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
	// index:    4567890123456789
	// storage:  [snap.]aaa
	// entries:      ffffffff
	// result:          fffff
	assertRange(s, 11, 15, t)
	assertValues(s, 11, 15, 'f', t)
}

func Test_ApplySnapshot_Append_LeftOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 14, Data: []byte{'a'}},
	})
	assertRange(s, 11, 14, t)
	assertValues(s, 11, 14, 'a', t)

	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 8, Data: []byte{'f'}},
		raftpb.Entry{Index: 9, Data: []byte{'f'}},
		raftpb.Entry{Index: 10, Data: []byte{'f'}},
		raftpb.Entry{Index: 11, Data: []byte{'f'}},
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
	})
	// index:    4567890123456789
	// storage:  [snap.]aaaa
	// entries:      fffff
	// result:          ff
	assertRange(s, 11, 12, t)
	assertValues(s, 11, 12, 'f', t)
}

func Test_ApplySnapshot_Append_InsideOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 14, Data: []byte{'a'}},
	})
	assertRange(s, 11, 14, t)

	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
		raftpb.Entry{Index: 13, Data: []byte{'f'}},
	})
	// index:    4567890123456789
	// storage:  [snap.]aaaa
	// entries:          ff
	// result:          aff
	assertRange(s, 11, 13, t)
	assertValues(s, 11, 11, 'a', t)
	assertValues(s, 12, 13, 'f', t)
}

func Test_ApplySnapshot_Append_RightOverlap(t *testing.T) {
	s := s_11_13()
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 14, Data: []byte{'a'}},
	})
	assertRange(s, 11, 14, t)

	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 12, Data: []byte{'f'}},
		raftpb.Entry{Index: 13, Data: []byte{'f'}},
		raftpb.Entry{Index: 14, Data: []byte{'f'}},
		raftpb.Entry{Index: 15, Data: []byte{'f'}},
		raftpb.Entry{Index: 16, Data: []byte{'f'}},
	})
	// index:    4567890123456789
	// storage:  [snap.]aaaa
	// entries:          fffff
	// result:          afffff
	assertRange(s, 11, 16, t)
	assertValues(s, 11, 11, 'a', t)
	assertValues(s, 12, 16, 'f', t)
}

func Test_ApplySnapshot_Append_Gap(t *testing.T) {
	s := s_11_13()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Appending [15, 16] to a [11, 13] storage should panic")
		}
	}()
	// index:    4567890123456789
	// storage:  [snap.]aaa
	// entries:             ff
	// result:          (panic)
	s.Append([]raftpb.Entry{
		raftpb.Entry{Index: 15},
		raftpb.Entry{Index: 16},
	})
}

func Test_CreateSnapshot(t *testing.T) {
	s := s_11_13()
	s.CreateSnapshot(12, nil, []byte{'x'})

	// Everything up until index=12 is archived in the snapshot.
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() returned error: %v", err)
	}
	if snapshot.Metadata.Index != 12 {
		t.Fatalf("Snapshot() should have index=12, was: %v", snapshot)
	}

	// However, CreateSnapshot() leaves all entries accessible.
	assertRange(s, 11, 13, t)
	assertValues(s, 11, 13, 'a', t)
}

func Test_CreateSnapshot_Compact(t *testing.T) {
	s := s_11_13()
	s.CreateSnapshot(12, nil, []byte{'x'})
	// CreateSnapshot() doesn't change entries' availability.
	assertRange(s, 11, 13, t)       // All entries in [11, 13] range are available.
	assertValues(s, 11, 13, 'a', t) // Each entry contain command 'a'.

	s.Compact(12)
	// But Compact(12) does: only entries with index >= 13 are available now.
	assertRange(s, 13, 13, t)
	assertValues(s, 13, 13, 'a', t)

	// Entries with index<=12 are now compacted.
	_, err := s.Entries(12, 13, 100)
	if err != raft.ErrCompacted {
		t.Fatalf("Entries() should return ErrCompacted, was %v", err)
	}
}

func Test_CreateSnapshot_Compact_All(t *testing.T) {
	s := s_11_13()
	s.CreateSnapshot(13, nil, []byte{'x'})
	s.Compact(13)
	assertRange(s, 14, 13, t) // Effectively empty storage (hi < lo).

	// Entries with index<=13 are now compacted.
	_, err := s.Entries(13, 14, 100)
	if err != raft.ErrCompacted {
		t.Fatalf("Entries() should return ErrCompacted, was %v", err)
	}
}

// Assert that MemoryStorage 's' has entries in the range [first, last] available.
func assertRange(s *raft.MemoryStorage, first, last uint64, t *testing.T) {
	firstIndex, err := s.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() failed: %v", err)
	}
	if first != firstIndex {
		t.Fatalf("FirstIndex() returned %v, expected %v", firstIndex, first)
	}

	lastIndex, err2 := s.LastIndex()
	if err2 != nil {
		t.Fatalf("LastIndex() failed: %v", err2)
	}
	if last != lastIndex {
		t.Fatalf("LastIndex() returned %v, expected %v", lastIndex, last)
	}
}

// Assert that all entries in the [first, last] range have the single-byte data equal to 'value'.
func assertValues(s *raft.MemoryStorage, first, last uint64, value byte, t *testing.T) {
	for i := first; i <= last; i++ {
		entries, err := s.Entries(i, i+1, 100)
		if err != nil {
			t.Fatalf("Entries(%v, %v) failed: %v", i, i+1, err)
		}
		if len(entries) != 1 {
			t.Fatalf("Entries(%v, %v) expected to return 1 element, returned %v", i, i+1, len(entries))
		}
		if entries[0].Type != raftpb.EntryNormal {
			t.Fatalf("Entry %v should have type EntryNormal, was: %+v", i, entries[0])
		}
		if !slices.Equal([]byte{value}, entries[0].Data) {
			t.Fatalf("Entry data expected to be %v, was %v", []byte{value}, entries[0].Data)
		}
	}
}
