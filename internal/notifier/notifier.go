package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/brian/etcd-reliability-tool/internal/config"
	"github.com/brian/etcd-reliability-tool/internal/evaluator"
)

type Notifier struct {
	cfg        *config.Config
	httpClient *http.Client
	lastAlerts map[string]time.Time
}

func New(cfg *config.Config) *Notifier {
	return &Notifier{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		lastAlerts: make(map[string]time.Time),
	}
}

func (n *Notifier) Notify(alerts []evaluator.Alert) {
	for _, alert := range alerts {
		// Debounce: 5 minute cooldown for the same metric
		if last, ok := n.lastAlerts[alert.Metric]; ok && time.Since(last) < 5*time.Minute {
			continue
		}

		fmt.Printf("ALERT: [%s] %s\n", alert.Metric, alert.Message)

		if n.cfg.Notifications.Slack.Enabled {
			n.sendSlack(alert)
		}
		if n.cfg.Notifications.PagerDuty.Enabled {
			n.sendPagerDuty(alert)
		}
		if n.cfg.Notifications.Webhook.Enabled {
			n.sendWebhook(alert)
		}

		n.lastAlerts[alert.Metric] = time.Now()
	}
}

func (n *Notifier) sendSlack(alert evaluator.Alert) {
	if n.cfg.Notifications.Slack.Token == "" {
		return
	}
	payload := map[string]interface{}{
		"channel": n.cfg.Notifications.Slack.Channel,
		"text":    fmt.Sprintf("*ETCD Alert:* %s\n> %s", alert.Metric, alert.Message),
	}
	n.postJSON("https://slack.com/api/chat.postMessage", payload, map[string]string{
		"Authorization": "Bearer " + n.cfg.Notifications.Slack.Token,
	})
}

func (n *Notifier) sendPagerDuty(alert evaluator.Alert) {
	if n.cfg.Notifications.PagerDuty.RoutingKey == "" {
		return
	}
	payload := map[string]interface{}{
		"routing_key":  n.cfg.Notifications.PagerDuty.RoutingKey,
		"event_action": "trigger",
		"payload": map[string]interface{}{
			"summary":  alert.Message,
			"source":   "etcd-reliability-tool",
			"severity": "critical",
			"component": alert.Metric,
		},
	}
	n.postJSON("https://events.pagerduty.com/v2/enqueue", payload, nil)
}

func (n *Notifier) sendWebhook(alert evaluator.Alert) {
	if n.cfg.Notifications.Webhook.URL == "" {
		return
	}
	n.postJSON(n.cfg.Notifications.Webhook.URL, alert, nil)
}

func (n *Notifier) postJSON(url string, payload interface{}, headers map[string]string) {
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := n.httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to send notification to %s: %v\n", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		fmt.Printf("Notification to %s failed with status: %d\n", url, resp.StatusCode)
	}
}
