package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"

	"github.com/jpmc/ingress-poc/pkg/httputil"
	"github.com/jpmc/ingress-poc/pkg/middleware"
	"github.com/jpmc/ingress-poc/pkg/otel"
)

type regionInfo struct {
	Region     string `json:"region"`
	Datacenter string `json:"datacenter"`
}

// fleetGateways maps hostname -> {envoyURL, kongURL} for fleet-specific routing
type fleetGateways struct {
	EnvoyURL string
	KongURL  string
}

var (
	port              string
	gatewayEnvoyURL   string
	gatewayKongURL    string
	managementAPIURL  string
	orchestrationMode string

	regions = []regionInfo{
		{Region: "us-east", Datacenter: "CDC1"},
		{Region: "eu-west", Datacenter: "Farn"},
		{Region: "ap-southeast", Datacenter: "SG-C01"},
	}
	regionIdx uint64

	// Fleet node routing cache: hostname -> fleet gateway URLs
	fleetRouteMu    sync.RWMutex
	fleetRouteCache = map[string]fleetGateways{}

	// Direct-route gateway cache: routes not tied to any fleet (i.e.
	// created directly via create_route — everything the repo-onboarding
	// agent registers) carry their own authoritative gateway_type. Without
	// this, resolveGateway's only fallback is a path-prefix guess
	// ("/api/*" -> Kong) that ignores what the route actually asked for —
	// wrong for any conventional REST API path registered for Envoy.
	routeGatewayMu    sync.RWMutex
	routeGatewayCache = map[string][]routeGateway{} // hostname -> entries, longest path prefix wins
)

type routeGateway struct {
	path        string
	gatewayType string
}

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8012"
	}
	gatewayEnvoyURL = os.Getenv("GATEWAY_ENVOY_URL")
	if gatewayEnvoyURL == "" {
		gatewayEnvoyURL = "http://gateway-envoy:8000"
	}
	gatewayKongURL = os.Getenv("GATEWAY_KONG_URL")
	if gatewayKongURL == "" {
		gatewayKongURL = "http://gateway-kong:8000"
	}
	managementAPIURL = os.Getenv("MANAGEMENT_API_URL")
	if managementAPIURL == "" {
		managementAPIURL = "http://management-api:8003"
	}
	orchestrationMode = os.Getenv("ORCHESTRATION_MODE")
}

// pollFleetNodes periodically fetches fleet -> node mapping from the management API
func pollFleetNodes() {
	client := &http.Client{Timeout: 5 * time.Second}
	time.Sleep(3 * time.Second) // wait for mgmt API

	for {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", managementAPIURL+"/fleets", nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			var fleets []struct {
				ID        string `json:"id"`
				K8sName   string `json:"k8s_name"`
				Subdomain string `json:"subdomain"`
				FleetType string `json:"fleet_type"`
				Nodes     []struct {
					ContainerName string `json:"container_name"`
					GatewayType   string `json:"gateway_type"`
					Port          int    `json:"port"`
					Status        string `json:"status"`
				} `json:"nodes"`
			}
			if err := json.Unmarshal(body, &fleets); err != nil {
				return
			}

			newCache := map[string]fleetGateways{}
			for _, f := range fleets {
				if f.FleetType == "control" || f.Subdomain == "" {
					continue
				}
				gw := fleetGateways{}

				if orchestrationMode == "kubernetes" {
					// In K8s mode, the ingress-operator creates a Service
					// named after the fleet's k8s_name in the ingress-dp namespace.
					// New fleets use a human-readable slug; existing fleets fall back to UUID.
					svcName := f.K8sName
					if svcName == "" {
						svcName = f.ID
					}
					svcURL := fmt.Sprintf("http://%s.ingress-dp:8000", svcName)
					gw.EnvoyURL = svcURL
					gw.KongURL = svcURL
				} else {
					// Docker mode: use container name as DNS hostname
					for _, n := range f.Nodes {
						if n.Status != "running" {
							continue
						}
						switch n.GatewayType {
						case "envoy":
							if gw.EnvoyURL == "" {
								gw.EnvoyURL = fmt.Sprintf("http://%s:8000", n.ContainerName)
							}
						case "kong":
							if gw.KongURL == "" {
								gw.KongURL = fmt.Sprintf("http://%s:8000", n.ContainerName)
							}
						}
					}
				}

				if gw.EnvoyURL != "" || gw.KongURL != "" {
					newCache[f.Subdomain] = gw
				}
			}

			fleetRouteMu.Lock()
			fleetRouteCache = newCache
			fleetRouteMu.Unlock()

			if len(newCache) > 0 {
				log.Printf("Fleet routing cache updated: %d hostnames mapped to fleet nodes", len(newCache))
			}
		}()
		time.Sleep(5 * time.Second)
	}
}

// pollDirectRoutes periodically fetches all active routes from the
// management API and indexes them by hostname for resolveGateway's
// route-specific fallback (see routeGatewayCache above).
func pollDirectRoutes() {
	client := &http.Client{Timeout: 5 * time.Second}
	time.Sleep(3 * time.Second) // wait for mgmt API

	for {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", managementAPIURL+"/routes?status=active", nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			var routes []struct {
				Hostname    string `json:"hostname"`
				Path        string `json:"path"`
				GatewayType string `json:"gateway_type"`
			}
			if err := json.Unmarshal(body, &routes); err != nil {
				return
			}

			newCache := map[string][]routeGateway{}
			for _, rt := range routes {
				if rt.Hostname == "" || rt.Hostname == "*" || rt.GatewayType == "" {
					continue
				}
				newCache[rt.Hostname] = append(newCache[rt.Hostname], routeGateway{
					path:        strings.TrimPrefix(rt.Path, "/"),
					gatewayType: rt.GatewayType,
				})
			}

			routeGatewayMu.Lock()
			routeGatewayCache = newCache
			routeGatewayMu.Unlock()
		}()
		time.Sleep(5 * time.Second)
	}
}

// resolveGateway looks up the target gateway for hostname+path, in order:
//  1. A fleet-specific node (existing fleet-based routing).
//  2. The gateway_type actually configured on a matching route (longest
//     path-prefix match) — authoritative for routes not tied to any fleet,
//     e.g. everything created directly via create_route.
//  3. A path-prefix guess ("/api/*" -> Kong) as the last resort, for
//     traffic that matches neither of the above.
func resolveGateway(hostname, path string, isAPI bool) string {
	fleetRouteMu.RLock()
	gw, found := fleetRouteCache[hostname]
	fleetRouteMu.RUnlock()

	if found {
		if isAPI && gw.KongURL != "" {
			return gw.KongURL
		}
		if !isAPI && gw.EnvoyURL != "" {
			return gw.EnvoyURL
		}
	}

	if gwType, ok := lookupRouteGatewayType(hostname, path); ok {
		if gwType == "kong" {
			return gatewayKongURL
		}
		return gatewayEnvoyURL
	}

	// Fallback to shared gateway, guessed from the path
	if isAPI {
		return gatewayKongURL
	}
	return gatewayEnvoyURL
}

// lookupRouteGatewayType finds the gateway_type of the best-matching
// (longest path prefix) route cached for hostname.
func lookupRouteGatewayType(hostname, path string) (string, bool) {
	routeGatewayMu.RLock()
	defer routeGatewayMu.RUnlock()

	entries, ok := routeGatewayCache[hostname]
	if !ok {
		return "", false
	}
	bestLen := -1
	best := ""
	for _, e := range entries {
		if !strings.HasPrefix(path, e.path) {
			continue
		}
		if len(e.path) > bestLen {
			bestLen = len(e.path)
			best = e.gatewayType
		}
	}
	return best, bestLen >= 0
}

func main() {
	tp, tracer := otel.InitOTEL("psaas.perimeter")
	defer tp.Shutdown(nil)

	// Start fleet node discovery
	go pollFleetNodes()
	go pollDirectRoutes()

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(otel.Middleware("psaas.perimeter"))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "mock-psaas",
		})
	})

	r.HandleFunc("/*", func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx, span := tracer.Start(ctx, "psaas.perimeter.forward")
		defer span.End()

		// Cycle through regions
		idx := atomic.AddUint64(&regionIdx, 1) - 1
		region := regions[idx%uint64(len(regions))]

		span.SetAttributes(
			attribute.String("psaas.region", region.Region),
			attribute.String("psaas.datacenter", region.Datacenter),
			attribute.Bool("tls.reoriginated", true),
			attribute.String("tls.note", "In production TLS terminates here and re-originates to L4. DPoP is bound to the L4 connection."),
			attribute.String("auth.subject", req.Header.Get("X-Auth-Subject")),
		)

		// Propagate akamai request ID
		akamaiRequestID := req.Header.Get("X-Akamai-Request-Id")
		if akamaiRequestID != "" {
			span.SetAttributes(attribute.String("akamai.request_id", akamaiRequestID))
		}

		path := strings.TrimPrefix(req.URL.Path, "/")

		// Resolve hostname from X-Forwarded-Host for fleet node lookup
		hostname := req.Header.Get("X-Forwarded-Host")
		if hostname == "" {
			hostname = req.Host
		}
		// Strip port
		if idx := strings.Index(hostname, ":"); idx != -1 {
			hostname = hostname[:idx]
		}

		// Route: /api/* -> Kong node, everything else -> Envoy node
		// Uses fleet-specific nodes when available, falls back to shared gateway
		isAPI := path == "api" || strings.HasPrefix(path, "api/")
		targetURL := resolveGateway(hostname, path, isAPI)

		span.SetAttributes(
			attribute.String("psaas.target_gateway", targetURL),
			attribute.String("psaas.hostname", hostname),
			attribute.Bool("psaas.is_api", isAPI),
		)

		// Build extra headers
		extra := map[string]string{
			"X-Psaas-Region":     region.Region,
			"X-Psaas-Datacenter": region.Datacenter,
		}

		trueClientIP := req.Header.Get("X-True-Client-Ip")
		if trueClientIP == "" {
			trueClientIP = "203.0.113.42"
		}
		extra["X-Psaas-Forward-Ip"] = trueClientIP

		// Append to x-forwarded-for
		perimeterIP := "10.100.1.1"
		xff := req.Header.Get("X-Forwarded-For")
		if xff != "" {
			extra["X-Forwarded-For"] = xff + ", " + perimeterIP
		} else {
			extra["X-Forwarded-For"] = perimeterIP
		}

		// Preserve the original Host for gateway hostname-based routing
		forwardedHost := req.Header.Get("X-Forwarded-Host")
		if forwardedHost != "" {
			// Strip port from forwarded host and set as Host
			host := forwardedHost
			if idx := strings.Index(host, ":"); idx != -1 {
				host = host[:idx]
			}
			extra["Host"] = host
		}

		fullURL := fmt.Sprintf("%s/%s", targetURL, path)

		resp, err := httputil.ForwardRequest(ctx, httputil.DefaultClient, req, fullURL, extra)
		if err != nil {
			span.SetAttributes(attribute.Int("http.status_code", 502), attribute.String("error", err.Error()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(502)
			fmt.Fprintf(w, `{"error": "Gateway unreachable: %s"}`, err.Error())
			return
		}
		defer resp.Body.Close()

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		httputil.CopyResponseHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	log.Printf("mock-psaas starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
