package evaluator

import (
	"strings"
	"testing"
	"time"

	"github.com/brian/etcdoc/internal/config"
)

const healthyLeaderPayload = `
# TYPE etcd_server_has_leader gauge
etcd_server_has_leader 1
# TYPE etcd_server_is_leader gauge
etcd_server_is_leader 1
# TYPE etcd_server_id gauge
etcd_server_id{server_id="aaa"} 1
# TYPE etcd_network_peer_sent_bytes_total counter
etcd_network_peer_sent_bytes_total{To="bbb"} 100
etcd_network_peer_sent_bytes_total{To="ccc"} 200
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

const healthyFollowerPayload = `
# TYPE etcd_server_has_leader gauge
etcd_server_has_leader 1
# TYPE etcd_server_is_leader gauge
etcd_server_is_leader 0
# TYPE etcd_network_peer_received_bytes_total counter
etcd_network_peer_received_bytes_total{From="aaa"} 500
etcd_network_peer_received_bytes_total{From="ccc"} 100
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

	tests := []struct {
		name              string
		payload           string
		preCheckTime      time.Time
		preLeaderChanges  float64
		expectedAlerts    int
		expectedDescMatch string // check if description contains this string for leader check
	}{
		{
			name:              "Healthy Leader",
			payload:           healthyLeaderPayload,
			expectedAlerts:    0,
			expectedDescMatch: "(This node is the leader: aaa). Peers: bbb, ccc",
		},
		{
			name:              "Healthy Follower",
			payload:           healthyFollowerPayload,
			expectedAlerts:    0,
			expectedDescMatch: "(Follower. Leader is: aaa). Peers: ccc",
		},
		{
			name:           "Unhealthy Scenarios",
			payload:        unhealthyPayload,
			expectedAlerts: 8,
		},
		{
			name:             "Stateful Leader Changes Detected",
			payload:          "# TYPE etcd_server_has_leader gauge\netcd_server_has_leader 1\n# TYPE etcd_server_leader_changes_seen_total counter\netcd_server_leader_changes_seen_total 5\n",
			preCheckTime:     time.Now(),
			preLeaderChanges: 1, // 5 - 1 = 4 changes, which is > threshold of 3
			expectedAlerts:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(cfg)
			if !tt.preCheckTime.IsZero() {
				e.lastCheckTime = tt.preCheckTime
				e.lastLeaderChanges = tt.preLeaderChanges
			}

			report, err := e.Evaluate(strings.NewReader(tt.payload))
			if err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			if len(report.Alerts) != tt.expectedAlerts {
				t.Errorf("Expected %d alerts, got %d", tt.expectedAlerts, len(report.Alerts))
				for _, a := range report.Alerts {
					t.Logf("Alert: %s - %s", a.Metric, a.Message)
				}
			}

			if tt.expectedDescMatch != "" {
				found := false
				for _, c := range report.Checks {
					if c.Name == "Leader Status" && strings.Contains(c.Description, tt.expectedDescMatch) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected Leader Status check description to contain '%s', but it did not. Checks: %+v", tt.expectedDescMatch, report.Checks)
				}
			}
		})
	}
}
