package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brian/etcdoc/internal/config"
	"github.com/brian/etcdoc/internal/evaluator"
	"github.com/brian/etcdoc/internal/notifier"
	"github.com/brian/etcdoc/internal/scraper"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var (
	isLeader int32 // atomic boolean for leadership status
)

func buildConfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
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
	once := flag.Bool("once", false, "Run exactly one health check and exit (diagnostic mode)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	initLogger(cfg)
	slog.Info("Starting etcdoc", "interval", *interval, "config", *configPath, "once", *once)

	s, err := scraper.New(cfg)
	if err != nil {
		slog.Error("Failed to initialize scraper", "error", err)
		os.Exit(1)
	}

	e := evaluator.New(cfg)
	n := notifier.New(cfg)

	if *once {
		runDiagnosticMode(s, e)
		return
	}

	// Set up context and interrupts
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("Received termination signal, shutting down")
		cancel()
	}()

	// Set up leader election
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "etcdoc-standalone"
	}

	kubeCfg, err := buildConfig()
	if err != nil {
		slog.Warn("Could not build kube config, running without leader election (always leader)", "error", err)
		atomic.StoreInt32(&isLeader, 1)
	} else {
		clientset, err := kubernetes.NewForConfig(kubeCfg)
		if err != nil {
			slog.Error("Failed to create kubernetes client", "error", err)
			os.Exit(1)
		}

		lock := &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      "etcdoc-leader",
				Namespace: "kube-system",
			},
			Client: clientset.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: podName,
			},
		}

		go leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   15 * time.Second,
			RenewDeadline:   10 * time.Second,
			RetryPeriod:     2 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					slog.Info("Started leading")
					atomic.StoreInt32(&isLeader, 1)
				},
				OnStoppedLeading: func() {
					slog.Info("Stopped leading")
					atomic.StoreInt32(&isLeader, 0)
				},
				OnNewLeader: func(identity string) {
					if identity == podName {
						slog.Info("I am the new leader", "identity", identity)
					} else {
						slog.Info("New leader elected", "identity", identity)
					}
				},
			},
		})
	}

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

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down main loop")
			return
		case <-ticker.C:
			run(s, e, n)
		}
	}
}

func run(s *scraper.Scraper, e *evaluator.Evaluator, n *notifier.Notifier) {
	body, err := s.Scrape()
	if err != nil {
		slog.Warn("Scrape failed", "error", err)
		scrapeErrorsTotal.Inc()
		return
	}

	report, err := e.Evaluate(body)
	if err != nil {
		slog.Error("Evaluation failed", "error", err)
		evalErrorsTotal.Inc()
		return
	}

	lastSuccessTimestamp.SetToCurrentTime()

	if len(report.Alerts) > 0 {
		slog.Info("Threshold violations detected", "count", len(report.Alerts))
		for _, a := range report.Alerts {
			alertsDispatchedTotal.WithLabelValues(a.Metric).Inc()
		}

		if atomic.LoadInt32(&isLeader) == 1 {
			n.Notify(report.Alerts)
		} else {
			slog.Debug("Skipping notification, not the leader")
		}
	} else {
		slog.Debug("etcd member healthy")
	}
}

func runDiagnosticMode(s *scraper.Scraper, e *evaluator.Evaluator) {
	fmt.Println("\n=== etcdoc Diagnostic Report ===")
	body, err := s.Scrape()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: Could not scrape etcd metrics: %v\n", err)
		os.Exit(1)
	}

	report, err := e.Evaluate(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: Evaluation error: %v\n", err)
		os.Exit(1)
	}

	for _, check := range report.Checks {
		fmt.Printf("\n[%s] %s\n", check.Status, check.Name)
		fmt.Printf("       Current: %s\n", check.Current)
		fmt.Printf("     Threshold: %s\n", check.Threshold)
		fmt.Printf("       Details: %s\n", check.Description)
	}
	
	fmt.Println("\n================================")

	if len(report.Alerts) > 0 {
		fmt.Println("STATUS: UNHEALTHY")
		fmt.Println("One or more metrics exceeded acceptable thresholds.")
		os.Exit(1)
	}

	fmt.Println("STATUS: HEALTHY")
	fmt.Println("All monitored metrics are within acceptable thresholds.")
	os.Exit(0)
}
