package api

import (
	"bytes"
	"encoding/json"
	"high-performance-kv-store/internal/cluster"
	"high-performance-kv-store/internal/config"
	"high-performance-kv-store/internal/metrics"
	"high-performance-kv-store/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func setupTestAPI(t *testing.T) (*API, func()) {
	cfg := &config.Config{
		NodeID:          "test-node",
		Port:            8080,
		GossipInterval:  1 * time.Second,
		PingTimeout:     200 * time.Millisecond,
		SuspicionWindow: 2 * time.Second,
	}

	s := store.NewStore(nil)
	pm := cluster.NewPeerManager()
	rep := cluster.NewReplicator(cfg.NodeID, pm, 100*time.Millisecond)
	g := cluster.NewGossipManager(cfg.NodeID, "localhost:8080", "", 1*time.Second, 100*time.Millisecond, 2*time.Second, pm)

	mc := metrics.NewMetricsCollector(
		s.Size,
		func() int64 { return 0 },
		func() map[string]int { return map[string]int{"alive": 0} },
	)

	api := NewAPI(s, pm, g, rep, mc, cfg)

	return api, func() {
		s.Close()
	}
}

func TestAPIHealthz(t *testing.T) {
	api, cleanup := setupTestAPI(t)
	defer cleanup()

	handler := NewHandler(api)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	if rr.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got '%s'", rr.Body.String())
	}
}

func TestAPIKeysCRUD(t *testing.T) {
	api, cleanup := setupTestAPI(t)
	defer cleanup()

	handler := NewHandler(api)

	// 1. PUT key
	putReq := PutRequest{
		Value: "hello-world",
	}
	body, _ := json.Marshal(putReq)
	req := httptest.NewRequest("PUT", "/v1/kv/mykey", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("PUT failed: code %d, body %s", rr.Code, rr.Body.String())
	}

	var putResp PutResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("failed to decode PUT response: %v", err)
	}
	if putResp.Version == 0 {
		t.Errorf("expected version to be set")
	}

	// 2. GET key
	req = httptest.NewRequest("GET", "/v1/kv/mykey", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET failed: code %d, body %s", rr.Code, rr.Body.String())
	}

	var getResp GetResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode GET response: %v", err)
	}
	if getResp.Value != "hello-world" {
		t.Errorf("expected value 'hello-world', got '%s'", getResp.Value)
	}
	if getResp.Version != putResp.Version {
		t.Errorf("expected version %d, got %d", putResp.Version, getResp.Version)
	}

	// 3. DELETE key
	req = httptest.NewRequest("DELETE", "/v1/kv/mykey", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE failed: code %d", rr.Code)
	}

	var delResp DeleteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &delResp); err != nil {
		t.Fatalf("failed to decode DELETE response: %v", err)
	}
	if !delResp.Deleted {
		t.Errorf("expected deleted to be true")
	}

	// 4. GET key again (should be 404)
	req = httptest.NewRequest("GET", "/v1/kv/mykey", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestAPIMetrics(t *testing.T) {
	api, cleanup := setupTestAPI(t)
	defer cleanup()

	handler := NewHandler(api)

	// Make some requests to increment counters
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Fetch metrics
	req = httptest.NewRequest("GET", "/metrics", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("failed to get metrics: code %d", rr.Code)
	}

	metricsStr := rr.Body.String()
	// Should contain our http_requests_total and other custom metrics
	if !bytes.Contains([]byte(metricsStr), []byte("http_requests_total")) {
		t.Errorf("expected metrics to contain http_requests_total")
	}
	if !bytes.Contains([]byte(metricsStr), []byte("kv_store_size_keys")) {
		t.Errorf("expected metrics to contain kv_store_size_keys")
	}
}
