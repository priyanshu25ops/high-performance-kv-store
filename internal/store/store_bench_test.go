package store

import (
	"fmt"
	"testing"
)

func BenchmarkStoreParallelWrites(b *testing.B) {
	s := NewStore(nil)
	defer s.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench-key-%d", i)
			_, _ = s.Set(key, []byte("bench-value"), 0, 0)
			i++
		}
	})
}

func BenchmarkStoreParallelReads(b *testing.B) {
	s := NewStore(nil)
	defer s.Close()

	// Pre-fill keys
	for i := 0; i < 10000; i++ {
		_, _ = s.Set(fmt.Sprintf("bench-key-%d", i), []byte("bench-value"), 0, 0)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench-key-%d", i%10000)
			_, _, _ = s.Get(key)
			i++
		}
	})
}
