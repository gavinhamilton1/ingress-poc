// mock-cf-edge simulates the Cloudflare edge (WAF Rulesets + Cache Rules),
// converging on the same downstream as mock-akamai-edge (mock-psaas ->
// gateway-envoy/kong) so both CDN paths reach the same real platform.
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
		port = "8031"
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

// wafCheck simulates a Cloudflare WAF Ruleset evaluation. Returns (blocked, ruleName).
// Same checks as mock-akamai-edge's wafCheck — this models each CDN running
// broadly equivalent baseline managed rules, not a difference in detection
// capability between providers.
func wafCheck(path string, query string, headers http.Header) (bool, string) {
	if strings.Contains(path, "<script>") || strings.Contains(query, "<script>") {
		return true, "cf-managed-ruleset-xss"
	}
	if strings.Contains(path, "../") {
		return true, "cf-managed-ruleset-path-traversal"
	}
	if headers.Get("User-Agent") == "" {
		return true, "cf-bot-fight-mode"
	}
	return false, ""
}

func main() {
	tp, tracer := otel.InitOTEL("cf.edge")
	defer tp.Shutdown(nil)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("cf.edge"))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "mock-cf-edge",
		})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "cf.edge.request")
		defer span.End()

		requestID := req.Header.Get("Cf-Ray")
		if requestID == "" {
			requestID = strings.ReplaceAll(uuid.New().String(), "-", "") + "-edge"
		}

		span.SetAttributes(
			attribute.String("cf.service", "edge"),
			attribute.String("cf.ray", requestID),
			attribute.String("cf.colo", "LHR"),
			attribute.String("cf.country", "GB"),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		path := strings.TrimPrefix(req.URL.Path, "/")

		// WAF Ruleset check
		queryString := req.URL.RawQuery
		blocked, wafRule := wafCheck("/"+path, queryString, req.Header)
		span.SetAttributes(
			attribute.Bool("cf.waf.checked", true),
			attribute.Bool("cf.waf.blocked", blocked),
		)

		if blocked {
			span.SetAttributes(attribute.String("cf.waf.block_reason", wafRule))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":      "WAF blocked",
				"rule":       wafRule,
				"request_id": requestID,
			})
			return
		}

		// Cache Rules simulation (20% of GETs, same hit rate as the Akamai
		// path so the two providers are comparable for demo purposes)
		cacheHit := req.Method == "GET" && rand.Float64() < 0.2
		span.SetAttributes(attribute.Bool("cf.cache.hit", cacheHit))

		// Static routing: /api/* -> Kong (via PSaaS), everything else -> Envoy (via PSaaS)
		var targetURL string
		if path == "api" || strings.HasPrefix(path, "api/") {
			targetURL = gatewayKongURL
			span.SetAttributes(attribute.String("cf.forward_to", "gateway-kong"))
		} else {
			targetURL = gatewayEnvoyURL
			span.SetAttributes(attribute.String("cf.forward_to", "gateway-envoy"))
		}

		cacheStatus := "DYNAMIC"
		if cacheHit {
			cacheStatus = "HIT"
		}

		extra := map[string]string{
			"Cf-Ray":           requestID,
			"Cf-Ipcountry":     "GB",
			"Cf-Connecting-Ip": "203.0.113.77",
			"X-Forwarded-For":  "203.0.113.77",
			"Cf-Cache-Status":  cacheStatus,
			"Cf-Waf-Status":    "PASS",
			"Tracestate":       fmt.Sprintf("cf=%s", requestID),
		}

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

	log.Printf("mock-cf-edge starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
