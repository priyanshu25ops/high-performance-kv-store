package store

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	SnapshotMagic   = "KVSN"
	SnapshotVersion = 1
)

// CreateSnapshot dumps the current in-memory state to a snapshot file and truncates the WAL.
func (s *Store) CreateSnapshot() error {
	if s.wal == nil {
		return fmt.Errorf("cannot snapshot without WAL configuration")
	}

	// 1. Lock the WAL and all shards to ensure a consistent point-in-time state.
	// We lock the WAL first, then shards in order of index to prevent deadlocks.
	s.wal.mu.Lock()
	defer s.wal.mu.Unlock()

	for i := 0; i < ShardCount; i++ {
		s.shards[i].mu.Lock()
		defer s.shards[i].mu.Unlock()
	}

	// 2. Open temporary snapshot file
	dataDir := filepath.Dir(s.wal.Path())
	tmpPath := filepath.Join(dataDir, "snapshot.bin.tmp")
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp snapshot file: %w", err)
	}
	defer func() {
		file.Close()
		os.Remove(tmpPath) // clean up if we exit without renaming
	}()

	// 3. Write snapshot header: Magic (4B) + Version (2B)
	header := make([]byte, 6)
	copy(header[0:4], SnapshotMagic)
	binary.BigEndian.PutUint16(header[4:6], SnapshotVersion)
	if _, err := file.Write(header); err != nil {
		return fmt.Errorf("failed to write snapshot header: %w", err)
	}

	// 4. Iterate over shards and write all non-expired entries
	now := time.Now()
	for i := 0; i < ShardCount; i++ {
		for k, entry := range s.shards[i].db {
			if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
				continue
			}

			// Write entry using the same WAL format
			keyBytes := []byte(k)
			keyLen := len(keyBytes)
			valLen := len(entry.Value)

			payloadSize := 1 + 8 + 8 + 2 + keyLen + valLen
			recordSize := 4 + 4 + payloadSize
			buf := make([]byte, recordSize)

			binary.BigEndian.PutUint32(buf[0:4], uint32(payloadSize))
			payload := buf[8:]
			payload[0] = RecordTypeSet
			binary.BigEndian.PutUint64(payload[1:9], entry.Version)

			var expNano int64
			if !entry.ExpiresAt.IsZero() {
				expNano = entry.ExpiresAt.UnixNano()
			}
			binary.BigEndian.PutUint64(payload[9:17], uint64(expNano))
			binary.BigEndian.PutUint16(payload[17:19], uint16(keyLen))
			copy(payload[19:19+keyLen], keyBytes)
			if valLen > 0 {
				copy(payload[19+keyLen:], entry.Value)
			}

			checksum := crc32.ChecksumIEEE(payload)
			binary.BigEndian.PutUint32(buf[4:8], checksum)

			if _, err := file.Write(buf); err != nil {
				return fmt.Errorf("failed to write snapshot record: %w", err)
			}
		}
	}

	// 5. Sync snapshot file and close it
	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync snapshot file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close snapshot file: %w", err)
	}

	// 6. Rename temp file to final snapshot file
	snapPath := filepath.Join(dataDir, "snapshot.bin")
	if err := os.Rename(tmpPath, snapPath); err != nil {
		return fmt.Errorf("failed to commit snapshot file: %w", err)
	}

	// 7. Truncate the WAL
	if err := s.wal.file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate WAL file: %w", err)
	}
	if _, err := s.wal.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek WAL file: %w", err)
	}
	s.wal.size = 0

	return nil
}

// LoadSnapshot restores the in-memory state from the snapshot.bin file if it exists.
func (s *Store) LoadSnapshot(snapPath string) (bool, error) {
	file, err := os.Open(snapPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Snapshot doesn't exist, which is fine
		}
		return false, fmt.Errorf("failed to open snapshot file: %w", err)
	}
	defer file.Close()

	header := make([]byte, 6)
	if _, err := io.ReadFull(file, header); err != nil {
		return false, fmt.Errorf("failed to read snapshot header: %w", err)
	}

	if string(header[0:4]) != SnapshotMagic {
		return false, fmt.Errorf("invalid snapshot magic header")
	}

	version := binary.BigEndian.Uint16(header[4:6])
	if version != SnapshotVersion {
		return false, fmt.Errorf("unsupported snapshot version: %d", version)
	}

	headerBuf := make([]byte, 8)
	var loadedCount int64

	for {
		_, err := io.ReadFull(file, headerBuf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return false, fmt.Errorf("failed to read snapshot record header: %w", err)
		}

		payloadSize := binary.BigEndian.Uint32(headerBuf[0:4])
		expectedCrc := binary.BigEndian.Uint32(headerBuf[4:8])

		payload := make([]byte, payloadSize)
		if _, err := io.ReadFull(file, payload); err != nil {
			return false, fmt.Errorf("failed to read snapshot payload: %w", err)
		}

		actualCrc := crc32.ChecksumIEEE(payload)
		if actualCrc != expectedCrc {
			return false, ErrChecksumMismatch
		}

		if len(payload) < 19 {
			return false, ErrCorruptRecord
		}

		recType := payload[0]
		entryVer := binary.BigEndian.Uint64(payload[1:9])
		expNano := int64(binary.BigEndian.Uint64(payload[9:17]))
		keyLen := int(binary.BigEndian.Uint16(payload[17:19]))

		if len(payload) < 19+keyLen {
			return false, ErrCorruptRecord
		}

		key := string(payload[19 : 19+keyLen])
		var val []byte
		if len(payload) > 19+keyLen {
			val = payload[19+keyLen:]
		}

		var expiresAt time.Time
		if expNano > 0 {
			expiresAt = time.Unix(0, expNano)
		}

		if recType == RecordTypeSet {
			// Apply directly to shard without WAL logging
			sh := s.getShard(key)
			sh.mu.Lock()
			if current, exists := sh.db[key]; !exists || entryVer > current.Version {
				if !exists {
					loadedCount++
				}
				sh.db[key] = &Entry{
					Value:     val,
					Version:   entryVer,
					ExpiresAt: expiresAt,
				}
				if entryVer > s.lastVersion {
					s.lastVersion = entryVer
				}
			}
			sh.mu.Unlock()
		}
	}

	s.keyCount = loadedCount
	return true, nil
}
