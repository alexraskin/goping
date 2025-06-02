package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	logger *slog.Logger

	retryClient = retryablehttp.NewClient()

	pingRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goping_requests_total",
			Help: "Total number of ping requests made",
		},
		[]string{"status"},
	)

	pingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "goping_request_duration_seconds",
			Help:    "Duration of ping requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	pingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goping_errors_total",
			Help: "Total number of ping errors",
		},
		[]string{"error_type"},
	)

	uptime = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goping_uptime_seconds_total",
			Help: "Total uptime of the application in seconds",
		},
		[]string{},
	)
)

func init() {
	prometheus.MustRegister(pingRequestsTotal)
	prometheus.MustRegister(pingDuration)
	prometheus.MustRegister(pingErrors)
	prometheus.MustRegister(uptime)

	retryClient.RetryWaitMin = 2 * time.Second
	retryClient.RetryWaitMax = 10 * time.Second
	retryClient.RetryMax = 5
	retryClient.Backoff = retryablehttp.DefaultBackoff
	retryClient.CheckRetry = retryablehttp.DefaultRetryPolicy
}

func getEnv(key string) string {
	value := os.Getenv(key)

	if strings.HasPrefix(value, "/") {
		if _, err := os.Stat(value); err == nil {
			data, err := os.ReadFile(value)
			if err != nil {
				logger.Error("Failed to read secret file", "error", err)
			}
			return strings.TrimSpace(string(data))
		}
	}

	return strings.TrimSpace(value)
}

func ping(url string) {
	start := time.Now()

	r, err := retryablehttp.NewRequest("GET", url, nil)
	if err != nil {
		logger.Error("Failed to create request", "error", err)
		pingErrors.WithLabelValues("request_creation").Inc()
		return
	}

	resp, err := retryClient.Do(r)
	duration := time.Since(start).Seconds()

	if err != nil {
		logger.Error("Failed to send request", "error", err)
		pingRequestsTotal.WithLabelValues("error").Inc()
		pingDuration.WithLabelValues("error").Observe(duration)
		pingErrors.WithLabelValues("request_failed").Inc()
		return
	}

	defer resp.Body.Close()

	status := "success"
	if resp.StatusCode >= 400 {
		status = "client_error"
		if resp.StatusCode >= 500 {
			status = "server_error"
		}
		logger.Warn("Request returned non-success status", "status_code", resp.StatusCode, "url", url)
	} else {
		logger.Info("Ping successful", "status_code", resp.StatusCode, "duration", duration)
	}

	pingRequestsTotal.WithLabelValues(status).Inc()
	pingDuration.WithLabelValues(status).Observe(duration)
}

func startMetricsServer(port string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		logger.Info("Starting metrics server", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Metrics server failed", "error", err)
		}
	}()

	return server
}

func main() {
	debug := flag.Bool("debug", false, "enable debug logging")
	metricsPort := flag.String("metrics-port", "8080", "port to listen on for metrics")
	flag.Parse()

	// Initialize logger once
	logger = setupLogger(*debug)

	// Load environment variables
	if err := godotenv.Load(); err != nil {
		logger.Info("No .env file found, continuing with system environment", "error", err)
	}

	webhookURL := getEnv("WEBHOOK_URL")
	if webhookURL == "" {
		logger.Error("WEBHOOK_URL is not set")
		os.Exit(1)
	}

	metricsServer := startMetricsServer(*metricsPort)

	go func() {
		uptimeTicker := time.NewTicker(1 * time.Second)
		defer uptimeTicker.Stop()
		for range uptimeTicker.C {
			uptime.WithLabelValues().Inc()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		logger.Info("Shutting down gracefully...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shutdown metrics server", "error", err)
		}
	}()

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	ping(webhookURL)

	for {
		select {
		case <-ctx.Done():
			logger.Info("goping stopped")
			return
		case <-ticker.C:
			ping(webhookURL)
		}
	}
}

func setupLogger(debug bool) *slog.Logger {
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: func() slog.Level {
			if debug {
				return slog.LevelDebug
			}
			return slog.LevelInfo
		}(),
	}))

	slog.SetDefault(logger)

	return logger
}
