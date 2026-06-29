package notifier

import (
	"log/slog"
	"time"

	"github.com/brian/etcdoc/internal/config"
	"github.com/brian/etcdoc/internal/evaluator"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

type Notifier struct {
	cfg        *config.Config
	lastAlerts map[string]time.Time
	recorder   record.EventRecorder
	objRef     *corev1.ObjectReference
}

func New(cfg *config.Config, recorder record.EventRecorder, podName, namespace string) *Notifier {
	return &Notifier{
		cfg:        cfg,
		lastAlerts: make(map[string]time.Time),
		recorder:   recorder,
		objRef: &corev1.ObjectReference{
			Kind:      "Pod",
			Name:      podName,
			Namespace: namespace,
		},
	}
}

func (n *Notifier) Notify(alerts []evaluator.Alert) {
	for _, alert := range alerts {
		// Debounce: 5 minute cooldown for the same metric logging
		if last, ok := n.lastAlerts[alert.Metric]; ok && time.Since(last) < 5*time.Minute {
			continue
		}

		slog.Error("ETCD_ALERT", "metric", alert.Metric, "message", alert.Message)

		if n.recorder != nil {
			n.recorder.Event(n.objRef, corev1.EventTypeWarning, "EtcdAlert", alert.Message)
		}

		n.lastAlerts[alert.Metric] = time.Now()
	}
}

func (n *Notifier) Heartbeat() {
	slog.Info("ETCD_HEARTBEAT", "message", "etcdoc is actively monitoring etcd health")
	if n.recorder != nil {
		n.recorder.Event(n.objRef, corev1.EventTypeNormal, "EtcdHealthCheck", "etcdoc is actively monitoring etcd health")
	}
}
