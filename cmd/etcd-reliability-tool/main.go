package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/brian/etcd-reliability-tool/internal/config"
	"github.com/brian/etcd-reliability-tool/internal/evaluator"
	"github.com/brian/etcd-reliability-tool/internal/notifier"
	"github.com/brian/etcd-reliability-tool/internal/scraper"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	scrapeErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "etcd_reliability_scrape_errors_total",
		Help: "Total number of errors encountered while scraping etcd metrics",
	})
	evalErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "etcd_reliability_eval_errors_total",
		Help: "Total number of errors encountered while evaluating metrics",
	})
	alertsDispatchedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "etcd_reliability_alerts_dispatched_total",
		Help: "Total number of alerts dispatched, partitioned by metric name",
	}, []string{"metric"})
	lastSuccessTimestamp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "etcd_reliability_last_success_timestamp_seconds",
		Help: "Unix timestamp of the last successful scrape and evaluation",
	})
)

func initLogger(cfg *config.Config) {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if cfg.Logging.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	slog.SetDefault(slog.New(handler))
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	interval := flag.Duration("interval", 30*time.Second, "Scrape interval")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	initLogger(cfg)
	slog.Info("Starting etcd-reliability-tool", "interval", *interval, "config", *configPath)

	s, err := scraper.New(cfg)
	if err != nil {
		slog.Error("Failed to initialize scraper", "error", err)
		os.Exit(1)
	}

	e := evaluator.New(cfg)
	n := notifier.New(cfg)

	// Start the HTTP server for /health and optionally /metrics
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Observability.MetricsPort)
		slog.Info("Starting HTTP server", "addr", addr)
		
		mux := http.NewServeMux()
		
		// Always expose /health
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			state := e.GetLastState()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(state)
		})
		
		// Conditionally expose /metrics
		if cfg.Observability.Enabled {
			slog.Info("Prometheus metrics enabled at /metrics")
			mux.Handle("/metrics", promhttp.Handler())
		}

		server := &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  120 * time.Second,
		}

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Initial run
	run(s, e, n)

	for range ticker.C {
		run(s, e, n)
	}
}

func run(s *scraper.Scraper, e *evaluator.Evaluator, n *notifier.Notifier) {
	body, err := s.Scrape()
	if err != nil {
		slog.Warn("Scrape failed", "error", err)
		scrapeErrorsTotal.Inc()
		return
	}

	alerts, err := e.Evaluate(body)
	if err != nil {
		slog.Error("Evaluation failed", "error", err)
		evalErrorsTotal.Inc()
		return
	}

	lastSuccessTimestamp.SetToCurrentTime()

	if len(alerts) > 0 {
		slog.Info("Threshold violations detected", "count", len(alerts))
		for _, a := range alerts {
			alertsDispatchedTotal.WithLabelValues(a.Metric).Inc()
		}
		n.Notify(alerts)
	} else {
		slog.Debug("etcd member healthy")
	}
}
