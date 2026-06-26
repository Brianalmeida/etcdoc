package evaluator

import (
	"strings"
	"testing"

	"github.com/brian/etcdoc/internal/config"
)

const healthyPayload = `
# HELP etcd_server_has_leader etcd server has a leader. 1 is yes, 0 is no.
# TYPE etcd_server_has_leader gauge
etcd_server_has_leader 1
# TYPE etcd_disk_wal_fsync_duration_seconds histogram
etcd_disk_wal_fsync_duration_seconds_bucket{le="0.001"} 10
etcd_disk_wal_fsync_duration_seconds_sum 0.1
etcd_disk_wal_fsync_duration_seconds_count 10
# TYPE etcd_server_proposals_pending gauge
etcd_server_proposals_pending 5
# TYPE etcd_mvcc_db_total_size_in_bytes gauge
etcd_mvcc_db_total_size_in_bytes 10485760
# TYPE etcd_disk_defrag_inflight gauge
etcd_disk_defrag_inflight 0
# TYPE etcd_disk_backend_commit_duration_seconds histogram
etcd_disk_backend_commit_duration_seconds_bucket{le="0.001"} 10
etcd_disk_backend_commit_duration_seconds_sum 0.1
etcd_disk_backend_commit_duration_seconds_count 10
# TYPE etcd_server_is_learner gauge
etcd_server_is_learner 0
# TYPE etcd_server_leader_changes_seen_total counter
etcd_server_leader_changes_seen_total 1
# TYPE etcd_network_active_peers gauge
etcd_network_active_peers{peer="1"} 1
etcd_network_active_peers{peer="2"} 1
etcd_network_active_peers{peer="3"} 1
# TYPE etcd_network_known_peers gauge
etcd_network_known_peers{peer="1"} 1
etcd_network_known_peers{peer="2"} 1
etcd_network_known_peers{peer="3"} 1
`

const unhealthyPayload = `
# TYPE etcd_server_has_leader gauge
etcd_server_has_leader 0
# TYPE etcd_disk_wal_fsync_duration_seconds histogram
etcd_disk_wal_fsync_duration_seconds_bucket{le="0.001"} 10
etcd_disk_wal_fsync_duration_seconds_sum 10.0
etcd_disk_wal_fsync_duration_seconds_count 10
# TYPE etcd_server_proposals_pending gauge
etcd_server_proposals_pending 500
# TYPE etcd_mvcc_db_total_size_in_bytes gauge
etcd_mvcc_db_total_size_in_bytes 10000000000
# TYPE etcd_disk_defrag_inflight gauge
etcd_disk_defrag_inflight 1
# TYPE etcd_disk_backend_commit_duration_seconds histogram
etcd_disk_backend_commit_duration_seconds_bucket{le="0.001"} 10
etcd_disk_backend_commit_duration_seconds_sum 20.0
etcd_disk_backend_commit_duration_seconds_count 10
# TYPE etcd_server_is_learner gauge
etcd_server_is_learner 1
# TYPE etcd_server_leader_changes_seen_total counter
etcd_server_leader_changes_seen_total 10
# TYPE etcd_network_active_peers gauge
etcd_network_active_peers{peer="1"} 1
# TYPE etcd_network_known_peers gauge
etcd_network_known_peers{peer="1"} 1
etcd_network_known_peers{peer="2"} 1
`

func TestEvaluator(t *testing.T) {
	cfg := &config.Config{}
	cfg.Thresholds.FsyncLatencySeconds = 0.5
	cfg.Thresholds.BackendCommitLatencySeconds = 1.0
	cfg.Thresholds.MaxPendingProposals = 100
	cfg.Thresholds.MaxDBSizeBytes = 8589934592 // 8GB
	cfg.Thresholds.MaxLeaderChanges5m = 3

	e := New(cfg)

	// Test 1: Healthy
	report, err := e.Evaluate(strings.NewReader(healthyPayload))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(report.Alerts) > 0 {
		t.Errorf("Expected 0 alerts, got %d", len(report.Alerts))
		for _, a := range report.Alerts {
			t.Logf("Unexpected Alert: %s - %s", a.Metric, a.Message)
		}
	}

	// Test 2: Leader Changes Stateful test
	// We feed a second payload where leader changes jumped by 4 (threshold is 3)
	const statefulPayload = `
# TYPE etcd_server_leader_changes_seen_total counter
etcd_server_leader_changes_seen_total 5
`
	report2, _ := e.Evaluate(strings.NewReader(statefulPayload))
	hasLeaderAlert := false
	for _, a := range report2.Alerts {
		if a.Metric == "etcd_server_leader_changes_seen_total" {
			hasLeaderAlert = true
			break
		}
	}
	if !hasLeaderAlert {
		t.Errorf("Expected alert for leader changes")
	}

	// Test 3: Unhealthy
	e = New(cfg) // reset state
	report3, err := e.Evaluate(strings.NewReader(unhealthyPayload))
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedAlertCount := 8 // Leader, Fsync, Pending, DB Size, Defrag, Backend Commit, Learner, Peer Health
	if len(report3.Alerts) != expectedAlertCount {
		t.Errorf("Expected %d alerts, got %d", expectedAlertCount, len(report3.Alerts))
		for _, a := range report3.Alerts {
			t.Logf("Alert: %s - %s", a.Metric, a.Message)
		}
	}

	// Test 4: 5-Member Cluster (Valid)
	const fiveMemberPayload = healthyPayload + `
etcd_network_active_peers{peer="4"} 1
etcd_network_active_peers{peer="5"} 1
etcd_network_known_peers{peer="4"} 1
etcd_network_known_peers{peer="5"} 1
`
	e = New(cfg) // reset state
	report4, _ := e.Evaluate(strings.NewReader(fiveMemberPayload))
	if len(report4.Alerts) > 0 {
		t.Errorf("Expected 0 alerts for 5-member cluster, got %d", len(report4.Alerts))
		for _, a := range report4.Alerts {
			t.Logf("Unexpected Alert (5-member): %s - %s", a.Metric, a.Message)
		}
	}

	// Test 5: 7-Member Cluster (Valid, Max)
	const sevenMemberPayload = fiveMemberPayload + `
etcd_network_active_peers{peer="6"} 1
etcd_network_active_peers{peer="7"} 1
etcd_network_known_peers{peer="6"} 1
etcd_network_known_peers{peer="7"} 1
`
	e = New(cfg) // reset state
	report5, _ := e.Evaluate(strings.NewReader(sevenMemberPayload))
	if len(report5.Alerts) > 0 {
		t.Errorf("Expected 0 alerts for 7-member cluster, got %d", len(report5.Alerts))
		for _, a := range report5.Alerts {
			t.Logf("Unexpected Alert (7-member): %s - %s", a.Metric, a.Message)
		}
	}
}
