package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Etcd struct {
		MetricsURL string `yaml:"metrics_url"`
		CertFile   string `yaml:"cert_file"`
		KeyFile    string `yaml:"key_file"`
		CAFile     string `yaml:"ca_file"`
	} `yaml:"etcd"`

	Thresholds struct {
		FsyncLatencySeconds         float64 `yaml:"fsync_latency_seconds"`
		BackendCommitLatencySeconds float64 `yaml:"backend_commit_latency_seconds"`
		MaxLeaderChanges5m          int     `yaml:"max_leader_changes_5m"`
		MaxPendingProposals         int     `yaml:"max_pending_proposals"`
		MaxDBSizeBytes              float64 `yaml:"max_db_size_bytes"`
	} `yaml:"thresholds"`

	Logging struct {
		Level  string `yaml:"level"`  // debug, info, warn, error
		Format string `yaml:"format"` // json, text
	} `yaml:"logging"`

	Observability struct {
		Enabled     bool `yaml:"enabled"`
		MetricsPort int  `yaml:"metrics_port"`
	} `yaml:"observability"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{}

	// Set defaults
	cfg.Etcd.MetricsURL = "https://127.0.0.1:2381/metrics"
	cfg.Etcd.CertFile = "/var/lib/rancher/rke2/server/tls/etcd/server-etcd.crt"
	cfg.Etcd.KeyFile = "/var/lib/rancher/rke2/server/tls/etcd/server-client.key"
	cfg.Etcd.CAFile = "/var/lib/rancher/rke2/server/tls/etcd/server-ca.crt"
	
	cfg.Thresholds.FsyncLatencySeconds = 0.5
	cfg.Thresholds.BackendCommitLatencySeconds = 1.0
	cfg.Thresholds.MaxLeaderChanges5m = 3
	cfg.Thresholds.MaxPendingProposals = 100
	cfg.Thresholds.MaxDBSizeBytes = 8589934592 // 8GB default
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	
	cfg.Observability.Enabled = true
	cfg.Observability.MetricsPort = 8080

	// Load file if exists
	if _, err := os.Stat(path); err == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("failed to open config file: %w", err)
		}
		defer f.Close()

		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("failed to decode config file: %w", err)
		}
	}

	// Override thresholds with environment variables
	if v := os.Getenv("FSYNC_LATENCY_SECONDS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Thresholds.FsyncLatencySeconds = f
		}
	}
	if v := os.Getenv("BACKEND_COMMIT_LATENCY_SECONDS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Thresholds.BackendCommitLatencySeconds = f
		}
	}
	if v := os.Getenv("MAX_LEADER_CHANGES_5M"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Thresholds.MaxLeaderChanges5m = i
		}
	}
	if v := os.Getenv("MAX_PENDING_PROPOSALS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.Thresholds.MaxPendingProposals = i
		}
	}
	if v := os.Getenv("MAX_DB_SIZE_BYTES"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Thresholds.MaxDBSizeBytes = f
		}
	}
	return cfg, nil
}
