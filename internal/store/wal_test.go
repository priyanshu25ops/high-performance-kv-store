package store

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWALAppendAndReplay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wal, err := NewWAL(tmpDir, "wal.log")
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	// Append a SET record
	expiresAt := time.Now().Add(10 * time.Second)
	err = wal.Append(RecordTypeSet, "testkey", []byte("testvalue"), 100, expiresAt)
	if err != nil {
		t.Fatalf("failed to append SET: %v", err)
	}

	// Append a DELETE record
	err = wal.Append(RecordTypeDelete, "testkey", nil, 101, time.Time{})
	if err != nil {
		t.Fatalf("failed to append DELETE: %v", err)
	}

	wal.Close()

	// Re-open and replay
	wal2, err := NewWAL(tmpDir, "wal.log")
	if err != nil {
		t.Fatalf("failed to re-open WAL: %v", err)
	}
	defer wal2.Close()

	var records []struct {
		recType   byte
		key       string
		val       []byte
		version   uint64
		expiresAt time.Time
	}

	err = wal2.Replay(func(recType byte, key string, value []byte, version uint64, expiresAt time.Time) error {
		records = append(records, struct {
			recType   byte
			key       string
			val       []byte
			version   uint64
			expiresAt time.Time
		}{recType, key, value, version, expiresAt})
		return nil
	})

	if err != nil {
		t.Fatalf("failed to replay WAL: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify first record
	if records[0].recType != RecordTypeSet {
		t.Errorf("expected RecordTypeSet, got %d", records[0].recType)
	}
	if records[0].key != "testkey" {
		t.Errorf("expected testkey, got %s", records[0].key)
	}
	if !bytes.Equal(records[0].val, []byte("testvalue")) {
		t.Errorf("expected testvalue, got %s", records[0].val)
	}
	if records[0].version != 100 {
		t.Errorf("expected version 100, got %d", records[0].version)
	}
	if records[0].expiresAt.UnixNano() != expiresAt.UnixNano() {
		t.Errorf("expected expiry %v, got %v", expiresAt, records[0].expiresAt)
	}

	// Verify second record
	if records[1].recType != RecordTypeDelete {
		t.Errorf("expected RecordTypeDelete, got %d", records[1].recType)
	}
	if records[1].key != "testkey" {
		t.Errorf("expected testkey, got %s", records[1].key)
	}
	if len(records[1].val) != 0 {
		t.Errorf("expected nil value, got %s", records[1].val)
	}
	if records[1].version != 101 {
		t.Errorf("expected version 101, got %d", records[1].version)
	}
}

func TestWALCorruptRecord(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal-test-corrupt")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wal, err := NewWAL(tmpDir, "wal.log")
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	err = wal.Append(RecordTypeSet, "corrupt", []byte("val"), 1, time.Time{})
	if err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	wal.Close()

	// Corrupt the file by writing trash at the end
	f, err := os.OpenFile(filepath.Join(tmpDir, "wal.log"), os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to open file for corruption: %v", err)
	}
	f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Close()

	// Reopen and replay
	wal2, err := NewWAL(tmpDir, "wal.log")
	if err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	defer wal2.Close()

	err = wal2.Replay(func(recType byte, key string, value []byte, version uint64, expiresAt time.Time) error {
		return nil
	})

	// Replay should fail or handle EOF nicely if corruption is at boundary,
	// but since we wrote random bytes, it will read them as length/crc and fail with checksum/corrupt error.
	if err == nil {
		t.Errorf("expected replay error for corrupt WAL file, got nil")
	}
}

func TestSnapshotAndCompaction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "snapshot-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wal, err := NewWAL(tmpDir, "wal.log")
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	s := NewStore(wal)
	defer s.Close()

	// Add keys
	s.Set("key1", []byte("value1"), 0, 10)
	s.Set("key2", []byte("value2"), 0, 20)
	s.Set("key3", []byte("value3"), 5*time.Millisecond, 30) // will expire

	time.Sleep(10 * time.Millisecond) // wait for key3 to expire

	// Check WAL size before snapshot
	if wal.Size() == 0 {
		t.Fatalf("expected WAL to have non-zero size")
	}

	// Create Snapshot
	err = s.CreateSnapshot()
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// WAL should be truncated to 0 size
	if wal.Size() != 0 {
		t.Errorf("expected WAL size to be 0 after compaction, got %d", wal.Size())
	}

	// Create a new store to load from snapshot
	s2 := NewStore(nil)
	loaded, err := s2.LoadSnapshot(filepath.Join(tmpDir, "snapshot.bin"))
	if err != nil {
		t.Fatalf("failed to load snapshot: %v", err)
	}
	if !loaded {
		t.Fatalf("expected snapshot to load")
	}

	// Verify loaded keys
	val, ver, found := s2.Get("key1")
	if !found || string(val) != "value1" || ver != 10 {
		t.Errorf("key1 loaded incorrectly: found=%t, val=%s, ver=%d", found, val, ver)
	}

	val, ver, found = s2.Get("key2")
	if !found || string(val) != "value2" || ver != 20 {
		t.Errorf("key2 loaded incorrectly")
	}

	_, _, found = s2.Get("key3")
	if found {
		t.Errorf("key3 was loaded, but it should have been skipped (expired)")
	}
}
