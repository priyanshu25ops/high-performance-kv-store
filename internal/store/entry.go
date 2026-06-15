package store

import "time"

// Entry represents a value in the key-value store.
type Entry struct {
	Value     []byte
	Version   uint64
	ExpiresAt time.Time
}

// IsExpired returns true if the entry has an expiry set and it has passed.
func (e *Entry) IsExpired() bool {
	if e.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.ExpiresAt)
}
