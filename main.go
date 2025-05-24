package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"log/slog"

	"github.com/joho/godotenv"
)

func init() {
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found, continuing with system environment", "error", err)
	}
}

func getEnv(key string) string {
	value := os.Getenv(key)

	if strings.HasPrefix(value, "/") {
		if _, err := os.Stat(value); err == nil {
			data, err := os.ReadFile(value)
			if err != nil {
				slog.Error("Failed to read secret file", "error", err)
			}
			return strings.TrimSpace(string(data))
		}
	}

	return strings.TrimSpace(value)
}

func pingWithRetry(url string, maxRetries int) {
	for i := range maxRetries {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			slog.Debug("Webhook sent", "status", resp.Status)
			return
		}
		slog.Warn("Ping failed", "attempt", i+1, "error", err)
		time.Sleep(5 * time.Second)
	}
	slog.Error("All retries failed")
}

func main() {
	webhookURL := getEnv("WEBHOOK_URL")
	if webhookURL == "" {
		slog.Error("WEBHOOK_URL is not set")
		os.Exit(1)
	}

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	pingWithRetry(webhookURL, 3)

	for range ticker.C {
		pingWithRetry(webhookURL, 3)
	}
}
