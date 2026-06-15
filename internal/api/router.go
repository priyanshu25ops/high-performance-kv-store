package api

import (
	"high-performance-kv-store/internal/cluster"
	"high-performance-kv-store/internal/config"
	"high-performance-kv-store/internal/metrics"
	"high-performance-kv-store/internal/store"
	"net/http"
	"os"
	"path/filepath"
)

type API struct {
	store      *store.Store
	pm         *cluster.PeerManager
	gossip     *cluster.GossipManager
	replicator *cluster.Replicator
	metrics    *metrics.MetricsCollector
	cfg        *config.Config
}

func NewAPI(store *store.Store, pm *cluster.PeerManager, gossip *cluster.GossipManager, replicator *cluster.Replicator, metrics *metrics.MetricsCollector, cfg *config.Config) *API {
	return &API{
		store:      store,
		pm:         pm,
		gossip:     gossip,
		replicator: replicator,
		metrics:    metrics,
		cfg:        cfg,
	}
}

func NewHandler(api *API) http.Handler {
	mux := http.NewServeMux()

	// Public REST API
	mux.HandleFunc("GET /v1/kv/{key}", api.handleGetKey)
	mux.HandleFunc("PUT /v1/kv/{key}", api.handlePutKey)
	mux.HandleFunc("DELETE /v1/kv/{key}", api.handleDeleteKey)
	mux.HandleFunc("GET /v1/keys", api.handleListKeys)

	// Cluster Endpoints
	mux.HandleFunc("GET /v1/cluster/peers", api.handleClusterPeers)
	mux.HandleFunc("GET /v1/cluster/status", api.handleClusterStatus)
	mux.HandleFunc("GET /healthz", api.handleHealthz)
	mux.HandleFunc("GET /metrics", api.handleMetrics)

	// Internal Replication & Gossip
	mux.HandleFunc("GET /internal/ping", api.handleInternalPing)
	mux.HandleFunc("POST /internal/replicate", api.handleInternalReplicate)

	// Static Dashboard Serving
	dashboardDir := getDashboardDir()
	
	// Redirect /dashboard to /dashboard/
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusMovedPermanently)
	})
	
	// Serve files
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", http.FileServer(http.Dir(dashboardDir))))

	// Chain middlewares: Recovery -> Logging & Metrics
	handler := RecoveryMiddleware(mux)
	handler = LoggingAndMetricsMiddleware(api.metrics, handler)

	return handler
}

func getDashboardDir() string {
	// Check typical locations for dashboard folder.
	// 1. Current working dir + web/dashboard
	// 2. Relative to executable path
	pwd, err := os.Getwd()
	if err == nil {
		path := filepath.Join(pwd, "web", "dashboard")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "./web/dashboard"
}
