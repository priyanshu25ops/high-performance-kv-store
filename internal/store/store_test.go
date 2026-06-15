package store

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestStoreSetGetDelete(t *testing.T) {
	s := NewStore(nil)
	defer s.Close()

	// 1. Test Set & Get
	version, err := s.Set("key1", []byte("value1"), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error setting key: %v", err)
	}
	if version == 0 {
		t.Fatalf("expected positive version, got 0")
	}

	val, ver, found := s.Get("key1")
	if !found {
		t.Fatalf("expected key1 to be found")
	}
	if !bytes.Equal(val, []byte("value1")) {
		t.Errorf("expected value1, got %s", val)
	}
	if ver != version {
		t.Errorf("expected version %d, got %d", version, ver)
	}

	// 2. Test conflict resolution (LWW)
	olderVersion := version - 1
	newerVersion, err := s.Set("key1", []byte("value-old"), 0, olderVersion)
	if err != nil {
		t.Fatalf("failed older version set: %v", err)
	}
	// The store should NOT overwrite because olderVersion < current version
	val, ver, _ = s.Get("key1")
	if bytes.Equal(val, []byte("value-old")) {
		t.Errorf("expected value to remain value1, but got overwritten")
	}

	futureVersion := version + 100
	_, err = s.Set("key1", []byte("value-new"), 0, futureVersion)
	if err != nil {
		t.Fatalf("failed future version set: %v", err)
	}
	val, ver, _ = s.Get("key1")
	if !bytes.Equal(val, []byte("value-new")) {
		t.Errorf("expected key1 to be updated to value-new")
	}
	if ver != futureVersion {
		t.Errorf("expected version to be %d, got %d", futureVersion, ver)
	}

	// 3. Test Delete
	_, deleted, err := s.Delete("key1", futureVersion+1)
	if err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}
	if !deleted {
		t.Errorf("expected deleted to be true")
	}

	_, _, found = s.Get("key1")
	if found {
		t.Errorf("expected key1 to be deleted")
	}
}

func TestStoreConcurrency(t *testing.T) {
	s := NewStore(nil)
	defer s.Close()

	wg := sync.WaitGroup{}
	numWorkers := 50
	opsPerWorker := 100

	// Concurrent writers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := fmt.Sprintf("key-%d-%d", workerID, i)
				val := fmt.Sprintf("val-%d-%d", workerID, i)
				_, _ = s.Set(key, []byte(val), 0, 0)
			}
		}(w)
	}

	wg.Wait()

	if s.Size() != int64(numWorkers*opsPerWorker) {
		t.Errorf("expected size %d, got %d", numWorkers*opsPerWorker, s.Size())
	}

	// Concurrent readers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := fmt.Sprintf("key-%d-%d", workerID, i)
				val, _, found := s.Get(key)
				if !found {
					t.Errorf("expected key %s to exist", key)
					return
				}
				expected := fmt.Sprintf("val-%d-%d", workerID, i)
				if string(val) != expected {
					t.Errorf("expected %s, got %s", expected, val)
				}
			}
		}(w)
	}

	wg.Wait()
}

func TestStoreExpiry(t *testing.T) {
	s := NewStore(nil)
	defer s.Close()

	_, _ = s.Set("temp", []byte("tempval"), 50*time.Millisecond, 0)
	val, _, found := s.Get("temp")
	if !found || string(val) != "tempval" {
		t.Fatalf("expected key to exist")
	}

	time.Sleep(60 * time.Millisecond)

	_, _, found = s.Get("temp")
	if found {
		t.Errorf("expected key to be expired and not returned")
	}
}

func TestStoreReaper(t *testing.T) {
	s := NewStore(nil)
	s.StartReaper(10 * time.Millisecond)
	defer s.Close()

	_, _ = s.Set("temp1", []byte("val1"), 15*time.Millisecond, 0)
	_, _ = s.Set("temp2", []byte("val2"), 150*time.Millisecond, 0)

	if s.Size() != 2 {
		t.Fatalf("expected 2 keys, got %d", s.Size())
	}

	time.Sleep(40 * time.Millisecond)

	// After 40ms, temp1 should be reaped. temp2 should still be there.
	if s.Size() != 1 {
		t.Errorf("expected size 1, got %d", s.Size())
	}

	_, _, found := s.Get("temp1")
	if found {
		t.Errorf("expected temp1 to be gone")
	}

	_, _, found = s.Get("temp2")
	if !found {
		t.Errorf("expected temp2 to still be alive")
	}
}

func TestStoreScan(t *testing.T) {
	s := NewStore(nil)
	defer s.Close()

	// Set sorted keys
	for i := 1; i <= 10; i++ {
		key := fmt.Sprintf("user:%03d", i)
		_, _ = s.Set(key, []byte(fmt.Sprintf("data-%d", i)), 0, 0)
	}
	// Add dummy keys
	_, _ = s.Set("admin:01", []byte("adm"), 0, 0)

	// Scan prefix "user:" with limit 4
	keys, cursor := s.Scan("user:", 4, "")
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(keys))
	}
	if keys[0] != "user:001" || keys[3] != "user:004" {
		t.Errorf("unexpected scan keys: %v", keys)
	}
	if cursor != "user:004" {
		t.Errorf("expected cursor 'user:004', got '%s'", cursor)
	}

	// Scan second page
	keys, cursor = s.Scan("user:", 4, cursor)
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys on second page, got %d", len(keys))
	}
	if keys[0] != "user:005" || keys[3] != "user:008" {
		t.Errorf("unexpected second page keys: %v", keys)
	}
	if cursor != "user:008" {
		t.Errorf("expected cursor 'user:008', got '%s'", cursor)
	}

	// Scan remaining page
	keys, cursor = s.Scan("user:", 4, cursor)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys on last page, got %d", len(keys))
	}
	if keys[0] != "user:009" || keys[1] != "user:010" {
		t.Errorf("unexpected third page keys: %v", keys)
	}
	if cursor != "" {
		t.Errorf("expected empty cursor for end of list, got '%s'", cursor)
	}
}
