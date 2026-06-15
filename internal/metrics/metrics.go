package metrics

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Buckets in seconds for the latency histogram.
var LatencyBuckets = []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}

type requestKey struct {
	method string
	path   string
	status string
}

type histogramKey struct {
	method string
	path   string
}

type bucketCounters struct {
	counts [10]uint64 // Matches length of LatencyBuckets
	inf    uint64
	sum    uint64 // in nanoseconds, stored as uint64
	count  uint64
}

type MetricsCollector struct {
	mu           sync.RWMutex
	reqCounts    map[requestKey]*uint64
	histograms   map[histogramKey]*bucketCounters
	storeSizeFn  func() int64
	peerCountFn  func() map[string]int // returns map of state -> count
	walSizeFn    func() int64
	startTime    time.Time
}

func NewMetricsCollector(storeSizeFn, walSizeFn func() int64, peerCountFn func() map[string]int) *MetricsCollector {
	return &MetricsCollector{
		reqCounts:   make(map[requestKey]*uint64),
		histograms:  make(map[histogramKey]*bucketCounters),
		storeSizeFn: storeSizeFn,
		peerCountFn: peerCountFn,
		walSizeFn:   walSizeFn,
		startTime:   time.Now(),
	}
}

// Uptime returns the duration the server has been running.
func (mc *MetricsCollector) Uptime() time.Duration {
	return time.Since(mc.startTime)
}

// RecordRequest increments the request counter.
func (mc *MetricsCollector) RecordRequest(method, path, status string) {
	key := requestKey{method: method, path: path, status: status}

	mc.mu.RLock()
	counter, exists := mc.reqCounts[key]
	mc.mu.RUnlock()

	if !exists {
		mc.mu.Lock()
		// Double check under write lock
		counter, exists = mc.reqCounts[key]
		if !exists {
			var val uint64
			counter = &val
			mc.reqCounts[key] = counter
		}
		mc.mu.Unlock()
	}

	atomic.AddUint64(counter, 1)
}

// RecordLatency registers the response latency for an operation.
func (mc *MetricsCollector) RecordLatency(method, path string, duration time.Duration) {
	key := histogramKey{method: method, path: path}

	mc.mu.RLock()
	hist, exists := mc.histograms[key]
	mc.mu.RUnlock()

	if !exists {
		mc.mu.Lock()
		hist, exists = mc.histograms[key]
		if !exists {
			hist = &bucketCounters{}
			mc.histograms[key] = hist
		}
		mc.mu.Unlock()
	}

	secs := duration.Seconds()
	atomic.AddUint64(&hist.count, 1)
	atomic.AddUint64(&hist.sum, uint64(duration.Nanoseconds()))

	placed := false
	for i, limit := range LatencyBuckets {
		if secs <= limit {
			atomic.AddUint64(&hist.counts[i], 1)
			placed = true
			break
		}
	}
	if !placed {
		atomic.AddUint64(&hist.inf, 1)
	}
}

// WritePrometheus formats and writes the metrics in Prometheus exposition format.
func (mc *MetricsCollector) WritePrometheus(w io.Writer) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Write request counter metrics
	fmt.Fprintln(w, "# HELP http_requests_total Total number of HTTP requests processed.")
	fmt.Fprintln(w, "# TYPE http_requests_total counter")
	for k, counterVal := range mc.reqCounts {
		fmt.Fprintf(w, "http_requests_total{method=\"%s\",path=\"%s\",status=\"%s\"} %d\n",
			k.method, k.path, k.status, atomic.LoadUint64(counterVal))
	}

	// Write histogram metrics
	fmt.Fprintln(w, "# HELP http_request_duration_seconds HTTP request latencies in seconds.")
	fmt.Fprintln(w, "# TYPE http_request_duration_seconds histogram")
	for k, hist := range mc.histograms {
		sumSecs := float64(atomic.LoadUint64(&hist.sum)) / 1e9
		count := atomic.LoadUint64(&hist.count)

		var cumulative uint64
		for i, limit := range LatencyBuckets {
			cumulative += atomic.LoadUint64(&hist.counts[i])
			fmt.Fprintf(w, "http_request_duration_seconds_bucket{method=\"%s\",path=\"%s\",le=\"%.5f\"} %d\n",
				k.method, k.path, limit, cumulative)
		}
		cumulative += atomic.LoadUint64(&hist.inf)
		fmt.Fprintf(w, "http_request_duration_seconds_bucket{method=\"%s\",path=\"%s\",le=\"+Inf\"} %d\n",
			k.method, k.path, cumulative)

		fmt.Fprintf(w, "http_request_duration_seconds_sum{method=\"%s\",path=\"%s\"} %.6f\n",
			k.method, k.path, sumSecs)
		fmt.Fprintf(w, "http_request_duration_seconds_count{method=\"%s\",path=\"%s\"} %d\n",
			k.method, k.path, count)
	}

	// Write database size gauge
	if mc.storeSizeFn != nil {
		fmt.Fprintln(w, "# HELP kv_store_size_keys Current number of keys in the store.")
		fmt.Fprintln(w, "# TYPE kv_store_size_keys gauge")
		fmt.Fprintf(w, "kv_store_size_keys %d\n", mc.storeSizeFn())
	}

	// Write WAL size gauge
	if mc.walSizeFn != nil {
		fmt.Fprintln(w, "# HELP kv_wal_size_bytes Size of the Write-Ahead Log on disk in bytes.")
		fmt.Fprintln(w, "# TYPE kv_wal_size_bytes gauge")
		fmt.Fprintf(w, "kv_wal_size_bytes %d\n", mc.walSizeFn())
	}

	// Write cluster peer counts
	if mc.peerCountFn != nil {
		fmt.Fprintln(w, "# HELP kv_cluster_peers_total Current count of cluster members.")
		fmt.Fprintln(w, "# TYPE kv_cluster_peers_total gauge")
		counts := mc.peerCountFn()
		for state, count := range counts {
			fmt.Fprintf(w, "kv_cluster_peers_total{state=\"%s\"} %d\n", state, count)
		}
	}
}
