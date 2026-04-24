package config

import (
	"fmt"
	"os"

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
		FsyncLatencySeconds        float64 `yaml:"fsync_latency_seconds"`
		BackendCommitLatencySeconds float64 `yaml:"backend_commit_latency_seconds"`
		MaxLeaderChanges5m         int     `yaml:"max_leader_changes_5m"`
		MaxPendingProposals        int     `yaml:"max_pending_proposals"`
	} `yaml:"thresholds"`

	Notifications struct {
		Slack struct {
			Enabled bool   `yaml:"enabled"`
			Token   string `yaml:"-"` // Loaded from env
			Channel string `yaml:"channel"`
		} `yaml:"slack"`
		PagerDuty struct {
			Enabled    bool   `yaml:"enabled"`
			RoutingKey string `yaml:"-"` // Loaded from env
		} `yaml:"pagerduty"`
		Webhook struct {
			Enabled bool   `yaml:"enabled"`
			URL     string `yaml:"url"`
		} `yaml:"webhook"`
	} `yaml:"notifications"`

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

	// Override with environment variables for secrets
	if token := os.Getenv("SLACK_TOKEN"); token != "" {
		cfg.Notifications.Slack.Token = token
	}
	if key := os.Getenv("PAGERDUTY_KEY"); key != "" {
		cfg.Notifications.PagerDuty.RoutingKey = key
	}

	return cfg, nil
}
