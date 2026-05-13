package evaluator

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/brian/etcdoc/internal/config"
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

type CheckResult struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // PASS, FAIL, SKIP, WARN
	Current     string `json:"current"`
	Threshold   string `json:"threshold"`
	Description string `json:"description"`
}

type Report struct {
	Alerts []Alert       `json:"alerts"`
	Checks []CheckResult `json:"checks"`
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

func (e *Evaluator) Evaluate(metricsBody string) (Report, error) {
	reader := strings.NewReader(metricsBody)
	// Use NewTextParser with LegacyValidation to avoid panic in v0.66.0+
	parser := expfmt.NewTextParser(model.LegacyValidation)
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return Report{}, fmt.Errorf("failed to parse etcd metrics payload: %w", err)
	}

	var alerts []Alert
	var checks []CheckResult

	// 1. Check for Leader
	if mf, ok := metricFamilies["etcd_server_has_leader"]; ok {
		for _, m := range mf.GetMetric() {
			val := m.GetGauge().GetValue()
			res := CheckResult{
				Name:      "Leader Status",
				Current:   fmt.Sprintf("%.0f", val),
				Threshold: "1",
			}
			if val == 0 {
				res.Status = "FAIL"
				res.Description = "Member has no leader"
				alerts = append(alerts, Alert{Metric: "etcd_server_has_leader", Message: res.Description})
			} else {
				res.Status = "PASS"
				res.Description = "Member has an active leader"
			}
			checks = append(checks, res)
		}
	} else {
		checks = append(checks, CheckResult{Name: "Leader Status", Status: "FAIL", Description: "Metric missing"})
		alerts = append(alerts, Alert{Metric: "etcd_server_has_leader", Message: "Metric missing"})
	}

	// 2. Check Fsync Latency (WAL)
	if mf, ok := metricFamilies["etcd_disk_wal_fsync_duration_seconds"]; ok {
		for _, m := range mf.GetMetric() {
			hist := m.GetHistogram()
			if hist.GetSampleCount() > 0 {
				avg := hist.GetSampleSum() / float64(hist.GetSampleCount())
				res := CheckResult{
					Name:      "WAL Fsync Latency",
					Current:   fmt.Sprintf("%.4fs", avg),
					Threshold: fmt.Sprintf("%.4fs", e.cfg.Thresholds.FsyncLatencySeconds),
				}
				if avg > e.cfg.Thresholds.FsyncLatencySeconds {
					res.Status = "FAIL"
					res.Description = "High average WAL fsync latency"
					alerts = append(alerts, Alert{Metric: "etcd_disk_wal_fsync_duration_seconds", Message: res.Description})
				} else {
					res.Status = "PASS"
					res.Description = "WAL fsync latency within bounds"
				}
				checks = append(checks, res)
			}
		}
	}

	// 3. Check Pending Proposals
	if mf, ok := metricFamilies["etcd_server_proposals_pending"]; ok {
		for _, m := range mf.GetMetric() {
			val := m.GetGauge().GetValue()
			res := CheckResult{
				Name:      "Pending Proposals",
				Current:   fmt.Sprintf("%.0f", val),
				Threshold: fmt.Sprintf("%d", e.cfg.Thresholds.MaxPendingProposals),
			}
			if val > float64(e.cfg.Thresholds.MaxPendingProposals) {
				res.Status = "FAIL"
				res.Description = "High number of pending proposals"
				alerts = append(alerts, Alert{Metric: "etcd_server_proposals_pending", Message: res.Description})
			} else {
				res.Status = "PASS"
				res.Description = "Pending proposals within bounds"
			}
			checks = append(checks, res)
		}
	}

	// 4. DB Size Check
	if mf, ok := metricFamilies["etcd_mvcc_db_total_size_in_bytes"]; ok {
		for _, m := range mf.GetMetric() {
			val := m.GetGauge().GetValue()
			res := CheckResult{
				Name:      "Database Size",
				Current:   fmt.Sprintf("%.2f MB", val/(1024*1024)),
				Threshold: fmt.Sprintf("%.2f MB", e.cfg.Thresholds.MaxDBSizeBytes/(1024*1024)),
			}
			if val > e.cfg.Thresholds.MaxDBSizeBytes {
				res.Status = "FAIL"
				res.Description = "Database size exceeds maximum threshold"
				alerts = append(alerts, Alert{Metric: "etcd_mvcc_db_total_size_in_bytes", Message: res.Description})
			} else {
				res.Status = "PASS"
				res.Description = "Database size within bounds"
			}
			checks = append(checks, res)
		}
	}

	// 5. Leader Changes
	if mf, ok := metricFamilies["etcd_server_leader_changes_seen_total"]; ok {
		for _, m := range mf.GetMetric() {
			current := m.GetCounter().GetValue()
			res := CheckResult{
				Name:      "Leader Changes",
				Threshold: fmt.Sprintf("%d (per check interval)", e.cfg.Thresholds.MaxLeaderChanges5m),
			}
			if !e.lastCheckTime.IsZero() {
				diff := current - e.lastLeaderChanges
				res.Current = fmt.Sprintf("%.0f", diff)
				if diff >= float64(e.cfg.Thresholds.MaxLeaderChanges5m) {
					res.Status = "FAIL"
					res.Description = "Frequent leader changes detected"
					alerts = append(alerts, Alert{Metric: "etcd_server_leader_changes_seen_total", Message: res.Description})
				} else {
					res.Status = "PASS"
					res.Description = "Leader stability is normal"
				}
			} else {
				res.Status = "SKIP"
				res.Current = "N/A"
				res.Description = "Requires two samples to calculate changes"
			}
			checks = append(checks, res)
			e.lastLeaderChanges = current
		}
	}

	// 6. Cluster Peer Health
	if mfActive, okActive := metricFamilies["etcd_network_active_peers"]; okActive {
		if mfKnown, okKnown := metricFamilies["etcd_network_known_peers"]; okKnown {
			var activePeers, knownPeers float64
			for _, m := range mfActive.GetMetric() {
				// Each remote peer has an entry. Sum the active ones
				if m.GetGauge().GetValue() == 1 {
					activePeers++
				}
			}
			for _, m := range mfKnown.GetMetric() {
				// Known peers includes self (sometimes), but we count entries that are 1
				if m.GetGauge().GetValue() == 1 {
					knownPeers++
				}
			}
			
			// Known peers often includes self in some etcd versions, but remote in others.
			// The most reliable check is: if we have disconnected peers, we are degraded.
			if mfDisconnected, okDisc := metricFamilies["etcd_network_disconnected_peers_total"]; okDisc {
				for _, _ = range mfDisconnected.GetMetric() {
					// Any disconnected peer entry means at some point a peer disconnected.
					// A better real-time check is active vs known.
				}
			}

			// We use active < (known - 1) heuristic or active == 0 when known > 1
			// Actually, just check if any known peer is not active
			
			res := CheckResult{
				Name:      "Cluster Peer Health",
				Current:   fmt.Sprintf("%.0f active / %.0f known", activePeers, knownPeers),
				Threshold: "Active == Known peers",
			}
			
			if knownPeers > 1 && activePeers < (knownPeers - 1) { 
				// -1 because known peers usually includes the local node itself
				res.Status = "WARN"
				res.Description = "Cluster is degraded. One or more peers are unreachable."
				alerts = append(alerts, Alert{Metric: "etcd_network_active_peers", Message: res.Description})
			} else {
				res.Status = "PASS"
				res.Description = "All known peers are active."
			}
			checks = append(checks, res)
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

	return Report{Alerts: alerts, Checks: checks}, nil
}