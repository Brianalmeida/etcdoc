package evaluator

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/brian/etcd-reliability-tool/internal/config"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type Evaluator struct {
	cfg               *config.Config
	lastLeaderChanges float64
	lastCheckTime     time.Time
	
	// Thread-safe storage for the last known state
	mu        sync.RWMutex
	lastState HealthState
}

type Alert struct {
	Metric  string `json:"metric"`
	Message string `json:"message"`
}

type HealthState struct {
	Status    string    `json:"status"`
	LastCheck time.Time `json:"last_check"`
	Alerts    []Alert   `json:"alerts,omitempty"`
}

func New(cfg *config.Config) *Evaluator {
	return &Evaluator{
		cfg: cfg,
		lastState: HealthState{
			Status: "initializing",
		},
	}
}

func (e *Evaluator) GetLastState() HealthState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastState
}

func (e *Evaluator) Evaluate(metricsBody string) ([]Alert, error) {
	reader := strings.NewReader(metricsBody)
	// Use NewTextParser with LegacyValidation to avoid panic in v0.66.0+
	parser := expfmt.NewTextParser(model.LegacyValidation)
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse etcd metrics payload: %w", err)
	}

	var alerts []Alert

	// 1. Check for Leader
	if mf, ok := metricFamilies["etcd_server_has_leader"]; ok {
		for _, m := range mf.GetMetric() {
			val := m.GetGauge().GetValue()
			slog.Debug("Checking etcd_server_has_leader", "value", val)
			if val == 0 {
				alerts = append(alerts, Alert{
					Metric:  "etcd_server_has_leader",
					Message: "Member has no leader",
				})
			}
		}
	}

	// 2. Check Fsync Latency (WAL)
	if mf, ok := metricFamilies["etcd_disk_wal_fsync_duration_seconds"]; ok {
		for _, m := range mf.GetMetric() {
			hist := m.GetHistogram()
			if hist.GetSampleCount() > 0 {
				avg := hist.GetSampleSum() / float64(hist.GetSampleCount())
				slog.Debug("Checking etcd_disk_wal_fsync_duration_seconds", "avg", avg, "threshold", e.cfg.Thresholds.FsyncLatencySeconds)
				if avg > e.cfg.Thresholds.FsyncLatencySeconds {
					alerts = append(alerts, Alert{
						Metric:  "etcd_disk_wal_fsync_duration_seconds",
						Message: fmt.Sprintf("High average WAL fsync latency: %.3fs (threshold: %.3fs)", avg, e.cfg.Thresholds.FsyncLatencySeconds),
					})
				}
			}
		}
	}

	// 3. Check Pending Proposals
	if mf, ok := metricFamilies["etcd_server_proposals_pending"]; ok {
		for _, m := range mf.GetMetric() {
			val := m.GetGauge().GetValue()
			slog.Debug("Checking etcd_server_proposals_pending", "value", val, "threshold", e.cfg.Thresholds.MaxPendingProposals)
			if val > float64(e.cfg.Thresholds.MaxPendingProposals) {
				alerts = append(alerts, Alert{
					Metric:  "etcd_server_proposals_pending",
					Message: fmt.Sprintf("High number of pending proposals: %.0f (threshold: %d)", val, e.cfg.Thresholds.MaxPendingProposals),
				})
			}
		}
	}

	// 4. Leader Changes
	if mf, ok := metricFamilies["etcd_server_leader_changes_seen_total"]; ok {
		for _, m := range mf.GetMetric() {
			current := m.GetCounter().GetValue()
			if !e.lastCheckTime.IsZero() {
				diff := current - e.lastLeaderChanges
				slog.Debug("Checking etcd_server_leader_changes_seen_total", "diff", diff, "threshold", e.cfg.Thresholds.MaxLeaderChanges5m)
				if diff >= float64(e.cfg.Thresholds.MaxLeaderChanges5m) {
					alerts = append(alerts, Alert{
						Metric:  "etcd_server_leader_changes_seen_total",
						Message: fmt.Sprintf("Frequent leader changes detected: %.0f (threshold: %d)", diff, e.cfg.Thresholds.MaxLeaderChanges5m),
					})
				}
			}
			e.lastLeaderChanges = current
		}
	}

	e.lastCheckTime = time.Now()
	
	// Update last state
	e.mu.Lock()
	e.lastState.LastCheck = e.lastCheckTime
	e.lastState.Alerts = alerts
	if len(alerts) > 0 {
		e.lastState.Status = "unhealthy"
	} else {
		e.lastState.Status = "healthy"
	}
	e.mu.Unlock()

	return alerts, nil
}
