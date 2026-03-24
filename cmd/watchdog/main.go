package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jpmc/ingress-poc/pkg/middleware"
	pkgotel "github.com/jpmc/ingress-poc/pkg/otel"
)

var (
	port             string
	managementAPIURL string
	probeInterval    int
)

// Probe state protected by a mutex.
var (
	mu                  sync.Mutex
	probeHistory        []map[string]interface{}
	consecutiveFailures int
	lastSuccess         float64
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8006"
	}
	managementAPIURL = os.Getenv("MANAGEMENT_API_URL")
	if managementAPIURL == "" {
		managementAPIURL = "http://management-api:8003"
	}
	probeInterval = 10
	if v := os.Getenv("PROBE_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			probeInterval = n
		}
	}
}

func computeStatus() string {
	// Caller must hold mu.
	if consecutiveFailures == 0 {
		return "healthy"
	} else if consecutiveFailures <= 2 {
		return "degraded"
	}
	return "offline"
}

func probeLoop() {
	client := &http.Client{Timeout: 5 * time.Second}
	ticker := time.NewTicker(time.Duration(probeInterval) * time.Second)
	defer ticker.Stop()

	// Run an initial probe immediately, then on tick.
	for {
		entry := map[string]interface{}{
			"ts":         float64(time.Now().UnixMilli()) / 1000.0,
			"status":     "unknown",
			"latency_ms": float64(0),
		}

		start := time.Now()
		resp, err := client.Get(managementAPIURL + "/health")
		latency := float64(time.Since(start).Milliseconds()) + float64(time.Since(start).Microseconds()%1000)/1000.0
		latency = float64(time.Since(start).Nanoseconds()) / 1e6
		// Round to 1 decimal
		latency = float64(int(latency*10+0.5)) / 10

		entry["latency_ms"] = latency

		mu.Lock()
		if err != nil {
			consecutiveFailures++
			entry["status"] = "error"
			errMsg := err.Error()
			if len(errMsg) > 100 {
				errMsg = errMsg[:100]
			}
			entry["error"] = errMsg
		} else {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				consecutiveFailures = 0
				lastSuccess = float64(time.Now().UnixMilli()) / 1000.0
				entry["status"] = "ok"
			} else {
				consecutiveFailures++
				entry["status"] = fmt.Sprintf("http_%d", resp.StatusCode)
			}
		}

		probeHistory = append(probeHistory, entry)
		// Keep last 30 entries (5 minutes at 10s interval)
		for len(probeHistory) > 30 {
			probeHistory = probeHistory[1:]
		}
		mu.Unlock()

		<-ticker.C
	}
}

func main() {
	tp, _ := pkgotel.InitOTEL("watchdog")
	defer tp.Shutdown(nil)

	// Start the background probe goroutine.
	go probeLoop()

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(pkgotel.Middleware("watchdog"))

	r.Get("/status", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		status := computeStatus()
		failures := consecutiveFailures
		ls := lastSuccess

		var uptimeSeconds float64
		if len(probeHistory) > 0 {
			firstTS, _ := probeHistory[0]["ts"].(float64)
			uptimeSeconds = (float64(time.Now().UnixMilli()) / 1000.0) - firstTS
		}

		// Last 10 entries
		start := 0
		if len(probeHistory) > 10 {
			start = len(probeHistory) - 10
		}
		history := make([]map[string]interface{}, len(probeHistory[start:]))
		copy(history, probeHistory[start:])
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"management_api_status":  status,
			"management_api_url":     managementAPIURL,
			"consecutive_failures":   failures,
			"last_successful_probe":  ls,
			"uptime_seconds":         uptimeSeconds,
			"probe_history":          history,
		})
	})

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		status := computeStatus()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":                "ok",
			"service":              "watchdog",
			"management_api_status": status,
		})
	})

	log.Printf("watchdog starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
