package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
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

func initLogger(cfg *config.Config, logFile *os.File) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
		level = slog.LevelInfo
	}

	var out io.Writer = os.Stdout
	if logFile != nil {
		out = io.MultiWriter(os.Stdout, logFile)
	}

	var handler slog.Handler
	if cfg.Logging.Format == "text" {
		handler = slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
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

	logPath := "/var/lib/rancher/rke2/server/tls/etcd/etcdoc-diag.log"
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open diagnostic log file, writing to stdout only: %v\n", err)
	} else {
		defer logFile.Close()
	}

	initLogger(cfg, logFile)
	slog.Info("Starting etcdoc", "interval", *interval, "config", *configPath, "once", *once)

	s, err := scraper.New(cfg)
	if err != nil {
		slog.Error("Failed to initialize scraper", "error", err)
		os.Exit(1)
	}

	e := evaluator.New(cfg)

	if *once {
		runDiagnosticMode(s, e, logFile)
		return
	}

	// Set up context and interrupts
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Set up leader election and event recording
	var recorder record.EventRecorder
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = "etcdoc-standalone"
	}
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "kube-system"
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

		broadcaster := record.NewBroadcaster()
		broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events(namespace)})
		recorder = broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "etcdoc"})

		lock := &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      "etcdoc-leader",
				Namespace: namespace,
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
						slog.Info("New etcdoc pod leader elected", "identity", identity)
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

	diagInterval, err := time.ParseDuration(cfg.Logging.DiagnosticInterval)
	if err != nil {
		slog.Warn("Invalid diagnostic_interval, defaulting to 6h", "error", err)
		diagInterval = 6 * time.Hour
	}
	diagTicker := time.NewTicker(diagInterval)
	defer diagTicker.Stop()

	// Startup Diagnostic Report
	if _, err := generateDiagnosticReport(s, e, logFile, "Startup Diagnostic Report"); err != nil {
		slog.Error("Failed to generate startup diagnostic report", "error", err)
	}

	n := notifier.New(cfg, recorder, podName, namespace)
	heartbeatTicker := time.NewTicker(1 * time.Hour)
	defer heartbeatTicker.Stop()

	// Initial run
	n.Heartbeat()
	run(s, e, n)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down main loop")
			return
		case <-ticker.C:
			run(s, e, n)
		case <-heartbeatTicker.C:
			if atomic.LoadInt32(&isLeader) == 1 {
				n.Heartbeat()
			}
		case <-diagTicker.C:
			generateDiagnosticReport(s, e, logFile, "Periodic Diagnostic Report generated")
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
	defer body.Close()

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

func writeDiagnosticReport(w io.Writer, report evaluator.Report) {
	fmt.Fprintf(w, "\n=== etcdoc Diagnostic Report ===\n")
	for _, check := range report.Checks {
		fmt.Fprintf(w, "\n[%s] %s\n", check.Status, check.Name)
		fmt.Fprintf(w, "       Current: %s\n", check.Current)
		fmt.Fprintf(w, "     Threshold: %s\n", check.Threshold)
		fmt.Fprintf(w, "       Details: %s\n", check.Description)
	}

	fmt.Fprintf(w, "\n================================\n")

	if len(report.Alerts) > 0 {
		fmt.Fprintf(w, "STATUS: UNHEALTHY\n")
		fmt.Fprintf(w, "One or more metrics exceeded acceptable thresholds.\n")
	} else {
		fmt.Fprintf(w, "STATUS: HEALTHY\n")
		fmt.Fprintf(w, "All monitored metrics are within acceptable thresholds.\n")
	}
}

func runDiagnosticMode(s *scraper.Scraper, e *evaluator.Evaluator, logFile *os.File) {
	report, err := generateDiagnosticReport(s, e, nil, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}

	writeDiagnosticReport(os.Stdout, *report)

	if len(report.Alerts) > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}

func generateDiagnosticReport(s *scraper.Scraper, e *evaluator.Evaluator, logFile *os.File, title string) (*evaluator.Report, error) {
	body, err := s.Scrape()
	if err != nil {
		return nil, fmt.Errorf("scrape failed: %w", err)
	}
	defer body.Close()

	report, err := e.Evaluate(body)
	if err != nil {
		return nil, fmt.Errorf("evaluate failed: %w", err)
	}

	if title != "" {
		slog.Info(title, "checks", report.Checks)
	}
	if logFile != nil {
		writeDiagnosticReport(logFile, report)
		logFile.Sync()
	}

	return &report, nil
}
