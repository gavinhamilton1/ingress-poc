// mock-cf-lb simulates Cloudflare Load Balancer: origin-pool steering in
// front of an edge layer, mirroring mock-akamai-gtm's role in the Akamai
// path so both CDN paths converge on the same downstream (mock-psaas ->
// gateway-envoy/kong -> real backends) once past their respective edges.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/httputil"
	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

var (
	port      string
	forwardTo string

	// Cloudflare Load Balancer steers across "pools" of origins (its term
	// for what Akamai's GTM calls datacenters) using a steering policy —
	// "dynamic_latency" is CF's default, geo/weighted are the alternatives.
	pools          = []string{"pool-us-east", "pool-us-west", "pool-eu-west"}
	steeringPolicy = "dynamic_latency"
	poolIdx        uint64
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8030"
	}
	forwardTo = os.Getenv("FORWARD_TO")
	if forwardTo == "" {
		forwardTo = "http://mock-cf-edge:8031"
	}
}

func main() {
	tp, tracer := otel.InitOTEL("cf.lb")
	defer tp.Shutdown(nil)

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("cf.lb"))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "mock-cf-lb",
		})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "cf.lb.forward")
		defer span.End()

		requestID := req.Header.Get("Cf-Ray")
		if requestID == "" {
			requestID = strings.ReplaceAll(uuid.New().String(), "-", "") + "-lb"
		}

		idx := atomic.AddUint64(&poolIdx, 1) - 1
		pool := pools[idx%uint64(len(pools))]

		span.SetAttributes(
			attribute.String("cf.service", "load-balancer"),
			attribute.String("cf.ray", requestID),
			attribute.String("cf.lb.pool", pool),
			attribute.String("cf.lb.steering_policy", steeringPolicy),
			attribute.String("cf.forward_to", "mock-cf-edge"),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		path := strings.TrimPrefix(req.URL.Path, "/")

		extra := map[string]string{
			"Cf-Ray":         requestID,
			"Cf-Lb-Pool":     pool,
			"Cf-Lb-Steering": steeringPolicy,
		}

		originalHost := req.Header.Get("X-Forwarded-Host")
		if originalHost == "" {
			originalHost = req.Host
		}
		if originalHost != "" {
			extra["X-Forwarded-Host"] = originalHost
		}

		fullURL := fmt.Sprintf("%s/%s", forwardTo, path)

		resp, err := httputil.ForwardRequest(ctx, httputil.DefaultClient, req, fullURL, extra)
		if err != nil {
			span.SetAttributes(attribute.Int("http.status_code", 502), attribute.String("error", err.Error()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(502)
			fmt.Fprintf(w, `{"error": "Edge unreachable: %s"}`, err.Error())
			return
		}
		defer resp.Body.Close()

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		httputil.CopyResponseHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	log.Printf("mock-cf-lb HTTP starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
