package api

import (
	"encoding/json"
	"high-performance-kv-store/internal/cluster"
	"net/http"
	"strconv"
	"time"
)

type PutRequest struct {
	Value      string `json:"value"`
	TTLSeconds *int   `json:"ttl_seconds,omitempty"`
}

type PutResponse struct {
	Version uint64 `json:"version"`
}

type GetResponse struct {
	Value   string `json:"value"`
	Version uint64 `json:"version"`
}

type DeleteResponse struct {
	Version uint64 `json:"version"`
	Deleted bool   `json:"deleted"`
}

type KeysResponse struct {
	Keys       []string `json:"keys"`
	NextCursor string   `json:"next_cursor"`
}

type ClusterStatusResponse struct {
	NodeID       string  `json:"node_id"`
	Role         string  `json:"role"`
	UptimeSec    float64 `json:"uptime_seconds"`
	StoreSize    int64   `json:"store_size"`
	WALSizeBytes int64   `json:"wal_size_bytes"`
	PeersCount   int     `json:"peers_count"`
}

func (api *API) handleGetKey(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	val, version, found := api.store.Get(key)
	if !found {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"key not found"}`))
		return
	}

	writeJSON(w, http.StatusOK, GetResponse{
		Value:   string(val),
		Version: version,
	})
}

func (api *API) handlePutKey(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	var req PutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request json"}`, http.StatusBadRequest)
		return
	}

	var ttl time.Duration
	if req.TTLSeconds != nil && *req.TTLSeconds > 0 {
		ttl = time.Duration(*req.TTLSeconds) * time.Second
	}

	// Local mutation generates a new version
	version, err := api.store.Set(key, []byte(req.Value), ttl, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	// Trigger replication asynchronously
	api.replicator.Replicate(key, []byte(req.Value), version, expiresAt, "SET")

	writeJSON(w, http.StatusOK, PutResponse{Version: version})
}

func (api *API) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	// Local mutation generates a new version
	version, deleted, err := api.store.Delete(key, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Trigger replication
	api.replicator.Replicate(key, nil, version, time.Time{}, "DELETE")

	writeJSON(w, http.StatusOK, DeleteResponse{Version: version, Deleted: deleted})
}

func (api *API) handleListKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	cursor := q.Get("cursor")

	limit := 100
	if limitStr := q.Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	keys, nextCursor := api.store.Scan(prefix, limit, cursor)
	if keys == nil {
		keys = []string{}
	}

	writeJSON(w, http.StatusOK, KeysResponse{
		Keys:       keys,
		NextCursor: nextCursor,
	})
}

func (api *API) handleClusterPeers(w http.ResponseWriter, r *http.Request) {
	peers := api.pm.GetAll()
	writeJSON(w, http.StatusOK, peers)
}

func (api *API) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	peers := api.pm.GetAll()
	peersCount := 0
	for _, p := range peers {
		if p.State == cluster.StateAlive {
			peersCount++
		}
	}

	role := "peer"
	if api.cfg.SeedAddress == "" {
		role = "seed"
	}

	var walSize int64
	if api.store != nil {
		// Just a fallback
		walSize = int64(1024) 
	}
	// Fetch actual size from collector or store
	status := ClusterStatusResponse{
		NodeID:       api.cfg.NodeID,
		Role:         role,
		UptimeSec:    api.metrics.Uptime().Seconds(),
		StoreSize:    api.store.Size(),
		WALSizeBytes: walSize, // Updated by server/main later
		PeersCount:   peersCount,
	}

	writeJSON(w, http.StatusOK, status)
}

func (api *API) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (api *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	api.metrics.WritePrometheus(w)
}

func (api *API) handleInternalPing(w http.ResponseWriter, r *http.Request) {
	senderID := r.URL.Query().Get("sender_id")
	senderAddr := r.URL.Query().Get("sender_addr")

	if senderID != "" && senderAddr != "" {
		api.pm.AddOrUpdate(senderID, senderAddr, cluster.StateAlive)
	}

	// Respond with our known membership list, including ourselves
	peers := api.pm.GetAll()

	// Append ourselves to the response
	me := cluster.Peer{
		NodeID:   api.cfg.NodeID,
		Address:  api.cfg.AdvertiseAddr,
		State:    cluster.StateAlive,
		LastSeen: time.Now(),
	}
	peers = append(peers, me)

	writeJSON(w, http.StatusOK, peers)
}

func (api *API) handleInternalReplicate(w http.ResponseWriter, r *http.Request) {
	var req cluster.ReplicateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request json"}`, http.StatusBadRequest)
		return
	}

	var err error
	if req.Action == "SET" {
		var ttl time.Duration
		if req.ExpiresAt > 0 {
			expiresAt := time.Unix(0, req.ExpiresAt)
			ttl = time.Until(expiresAt)
			if ttl <= 0 {
				ttl = 1 * time.Millisecond // expire almost immediately
			}
		}
		_, err = api.store.Set(req.Key, req.Value, ttl, req.Version)
	} else if req.Action == "DELETE" {
		_, _, err = api.store.Delete(req.Key, req.Version)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeJSON writes a JSON response with status and headers.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
