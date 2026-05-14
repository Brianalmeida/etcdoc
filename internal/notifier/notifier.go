package notifier

import (
	"log/slog"
	"time"

	"github.com/brian/etcdoc/internal/config"
	"github.com/brian/etcdoc/internal/evaluator"
)

type Notifier struct {
	cfg        *config.Config
	lastAlerts map[string]time.Time
}

func New(cfg *config.Config) *Notifier {
	return &Notifier{
		cfg:        cfg,
		lastAlerts: make(map[string]time.Time),
	}
}

func (n *Notifier) Notify(alerts []evaluator.Alert) {
	for _, alert := range alerts {
		// Debounce: 5 minute cooldown for the same metric logging
		if last, ok := n.lastAlerts[alert.Metric]; ok && time.Since(last) < 5*time.Minute {
			continue
		}

		slog.Error("ETCD_ALERT", "metric", alert.Metric, "message", alert.Message)

		n.lastAlerts[alert.Metric] = time.Now()
	}
}