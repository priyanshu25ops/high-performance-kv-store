package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration settings for the node.
type Config struct {
	NodeID             string
	Port               int
	DataDir            string
	SeedAddress        string
	GossipInterval     time.Duration
	PingTimeout        time.Duration
	SuspicionWindow    time.Duration
	ReplicationTimeout time.Duration
	ReapInterval       time.Duration
	AdvertiseAddr      string
}

// Load loads the configuration from command-line flags and environment variables.
func Load() (*Config, error) {
	cfg := &Config{}

	// Define command line flags
	flag.StringVar(&cfg.NodeID, "node-id", getEnv("KV_NODE_ID", ""), "Unique identifier for this node")
	flag.IntVar(&cfg.Port, "port", getEnvInt("KV_PORT", 8080), "HTTP API port")
	flag.StringVar(&cfg.DataDir, "data-dir", getEnv("KV_DATA_DIR", "./data"), "Directory for WAL and snapshots")
	flag.StringVar(&cfg.SeedAddress, "seed-addr", getEnv("KV_SEED_ADDR", ""), "Seed node address to join cluster")
	flag.StringVar(&cfg.AdvertiseAddr, "advertise-addr", getEnv("KV_ADVERTISE_ADDR", ""), "Address advertised to peers (e.g. host:port)")

	gossipSec := flag.Int("gossip-interval-sec", getEnvInt("KV_GOSSIP_INTERVAL_SEC", 1), "Gossip ping interval in seconds")
	pingMs := flag.Int("ping-timeout-ms", getEnvInt("KV_PING_TIMEOUT_MS", 200), "Ping timeout in milliseconds")
	suspSec := flag.Int("suspicion-window-sec", getEnvInt("KV_SUSPICION_WINDOW_SEC", 5), "Suspicion window in seconds")
	replMs := flag.Int("replication-timeout-ms", getEnvInt("KV_REPLICATION_TIMEOUT_MS", 500), "Replication request timeout in milliseconds")
	reapSec := flag.Int("reap-interval-sec", getEnvInt("KV_REAP_INTERVAL_SEC", 5), "TTL reap interval in seconds")

	flag.Parse()

	cfg.GossipInterval = time.Duration(*gossipSec) * time.Second
	cfg.PingTimeout = time.Duration(*pingMs) * time.Millisecond
	cfg.SuspicionWindow = time.Duration(*suspSec) * time.Second
	cfg.ReplicationTimeout = time.Duration(*replMs) * time.Millisecond
	cfg.ReapInterval = time.Duration(*reapSec) * time.Second

	// Validate config
	if cfg.NodeID == "" {
		// Fallback to hostname if NodeID is empty
		hostname, err := os.Hostname()
		if err == nil {
			cfg.NodeID = hostname
		} else {
			cfg.NodeID = fmt.Sprintf("node-%d", cfg.Port)
		}
	}

	if cfg.AdvertiseAddr == "" {
		hostname, err := os.Hostname()
		if err == nil {
			cfg.AdvertiseAddr = fmt.Sprintf("%s:%d", hostname, cfg.Port)
		} else {
			cfg.AdvertiseAddr = fmt.Sprintf("localhost:%d", cfg.Port)
		}
	}

	// Ensure DataDir exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory %s: %w", cfg.DataDir, err)
	}

	return cfg, nil
}

// Helper to get environment string value or fallback
func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

// Helper to get environment int value or fallback
func getEnvInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}
