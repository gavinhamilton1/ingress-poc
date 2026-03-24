package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/httputil"
	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

var (
	port            string
	gatewayEnvoyURL string
	gatewayKongURL  string
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8011"
	}
	gatewayEnvoyURL = os.Getenv("GATEWAY_ENVOY_URL")
	if gatewayEnvoyURL == "" {
		gatewayEnvoyURL = "http://mock-psaas:8012"
	}
	gatewayKongURL = os.Getenv("GATEWAY_KONG_URL")
	if gatewayKongURL == "" {
		gatewayKongURL = "http://mock-psaas:8012"
	}
}

// wafCheck simulates a WAF check. Returns (blocked, ruleName).
func wafCheck(path string, query string, headers http.Header) (bool, string) {
	if strings.Contains(path, "<script>") || strings.Contains(query, "<script>") {
		return true, "xss"
	}
	if strings.Contains(path, "../") {
		return true, "path-traversal"
	}
	if headers.Get("User-Agent") == "" {
		return true, "bot-check"
	}
	return false, ""
}

func main() {
	tp, tracer := otel.InitOTEL("akamai.edge")
	defer tp.Shutdown(nil)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("akamai.edge"))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "mock-akamai-edge",
		})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "akamai.edge.request")
		defer span.End()

		requestID := req.Header.Get("X-Akamai-Request-Id")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		span.SetAttributes(
			attribute.String("akamai.service", "edge"),
			attribute.String("akamai.request_id", requestID),
			attribute.String("akamai.edge_ip", "23.40.11.5"),
			attribute.String("akamai.country", "GB"),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		path := strings.TrimPrefix(req.URL.Path, "/")

		// WAF check
		queryString := req.URL.RawQuery
		blocked, wafRule := wafCheck("/"+path, queryString, req.Header)
		span.SetAttributes(
			attribute.Bool("akamai.waf.checked", true),
			attribute.Bool("akamai.waf.blocked", blocked),
		)

		if blocked {
			span.SetAttributes(attribute.String("akamai.waf.block_reason", wafRule))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":      "WAF blocked",
				"rule":       wafRule,
				"request_id": requestID,
			})
			return
		}

		// Cache simulation (20% of GETs)
		cacheHit := req.Method == "GET" && rand.Float64() < 0.2
		span.SetAttributes(attribute.Bool("akamai.cache.hit", cacheHit))

		// Static routing: /api/* -> Kong (via PSaaS), everything else -> Envoy (via PSaaS)
		var targetURL string
		if path == "api" || strings.HasPrefix(path, "api/") {
			targetURL = gatewayKongURL
			span.SetAttributes(attribute.String("akamai.forward_to", "gateway-kong"))
		} else {
			targetURL = gatewayEnvoyURL
			span.SetAttributes(attribute.String("akamai.forward_to", "gateway-envoy"))
		}

		// Build extra headers
		cacheStatus := "MISS"
		if cacheHit {
			cacheStatus = "HIT"
		}

		extra := map[string]string{
			"X-Akamai-Request-Id":    requestID,
			"X-Akamai-Edgescape":     "georegion=263,country_code=GB,city=LONDON,lat=51.50,long=-0.12",
			"X-True-Client-Ip":       "203.0.113.42",
			"X-Forwarded-For":        "203.0.113.42, 23.40.11.5",
			"X-Akamai-Cache-Status":  cacheStatus,
			"X-Akamai-Waf-Status":    "PASS",
			"Tracestate":             fmt.Sprintf("akamai=%s", requestID),
		}

		// Preserve original Host for subdomain routing
		forwardedHost := req.Header.Get("X-Forwarded-Host")
		if forwardedHost == "" {
			forwardedHost = req.Host
			if forwardedHost != "" {
				extra["X-Forwarded-Host"] = forwardedHost
			}
		}

		fullURL := fmt.Sprintf("%s/%s", targetURL, path)

		resp, err := httputil.ForwardRequest(ctx, httputil.DefaultClient, req, fullURL, extra)
		if err != nil {
			span.SetAttributes(attribute.Int("http.status_code", 502), attribute.String("error", err.Error()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(502)
			fmt.Fprintf(w, `{"error": "Upstream unreachable: %s"}`, err.Error())
			return
		}
		defer resp.Body.Close()

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		httputil.CopyResponseHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	log.Printf("mock-akamai-edge starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
