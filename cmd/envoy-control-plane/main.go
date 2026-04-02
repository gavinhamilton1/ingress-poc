package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/jpmc/ingress-poc/pkg/middleware"
	appotel "github.com/jpmc/ingress-poc/pkg/otel"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

var (
	port             string
	managementAPIURL string
	envoyAdminURL    string
	authServiceURL   string
	opaURL           string
	jaegerGRPC       string
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	managementAPIURL = os.Getenv("MANAGEMENT_API_URL")
	if managementAPIURL == "" {
		managementAPIURL = "http://management-api:8003"
	}
	envoyAdminURL = os.Getenv("ENVOY_ADMIN_URL")
	if envoyAdminURL == "" {
		envoyAdminURL = "http://gateway-envoy:9901"
	}
	authServiceURL = os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8001"
	}
	opaURL = os.Getenv("OPA_URL")
	if opaURL == "" {
		opaURL = "http://opa:8181"
	}
	jaegerGRPC = os.Getenv("JAEGER_GRPC")
	if jaegerGRPC == "" {
		jaegerGRPC = "jaeger:4317"
	}
}

// ---------------------------------------------------------------------------
// Per-fleet snapshot state
// ---------------------------------------------------------------------------

// fleetSnapshot holds the cached route list and version for a single fleet
// (or the "global" shared gateway).
type fleetSnapshot struct {
	routes  []map[string]interface{}
	version string
}

const globalFleetKey = "global"

var (
	mu        sync.RWMutex
	snapshots = map[string]*fleetSnapshot{
		globalFleetKey: {version: "0"},
	}
)

// getSnapshot returns the snapshot for the given fleet key, creating one if
// it does not exist yet.
func getSnapshot(fleetKey string) *fleetSnapshot {
	if s, ok := snapshots[fleetKey]; ok {
		return s
	}
	s := &fleetSnapshot{version: "0"}
	snapshots[fleetKey] = s
	return s
}

// ---------------------------------------------------------------------------
// xDS request body parsing
// ---------------------------------------------------------------------------

// xdsRequest is the subset of an xDS DiscoveryRequest we care about.
type xdsRequest struct {
	Node struct {
		ID      string `json:"id"`
		Cluster string `json:"cluster"`
	} `json:"node"`
}

// fleetKeyFromRequest reads the POST body, extracts node.cluster, and returns
// the fleet key. If the cluster starts with "fleet-", that value is returned
// as-is (e.g. "fleet-jpmm"). Otherwise globalFleetKey is returned so the
// shared gateway-envoy keeps getting all routes.
//
// The body bytes are returned so the caller can re-wrap them if needed.
func fleetKeyFromRequest(r *http.Request) (string, []byte) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return globalFleetKey, nil
	}
	r.Body.Close()

	var req xdsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return globalFleetKey, body
	}

	cluster := strings.TrimSpace(req.Node.Cluster)
	if strings.HasPrefix(cluster, "fleet-") {
		return cluster, body
	}
	return globalFleetKey, body
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// qualifyHostname ensures a bare hostname (no dots) gets a FQDN suffix so it
// resolves correctly from any namespace. Fleet gateway pods run in
// ingress-dp but backend services are in ingress-cp.
func qualifyHostname(host string) string {
	if host == "" || strings.Contains(host, ".") || host == "localhost" {
		return host // Already qualified, external, or localhost
	}
	// Bare hostname — qualify with the control-plane namespace
	ns := os.Getenv("BACKEND_NAMESPACE")
	if ns == "" {
		ns = "ingress-cp"
	}
	return host + "." + ns + ".svc.cluster.local"
}

// parseHostPort splits a URL like "http://host:port" into (host, port int).
// The returned host is qualified with a FQDN suffix for cross-namespace DNS.
func parseHostPort(raw string) (string, int) {
	noScheme := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		noScheme = raw[idx+3:]
	}
	host := noScheme
	portVal := 80
	if i := strings.LastIndex(noScheme, ":"); i >= 0 {
		host = noScheme[:i]
		fmt.Sscanf(noScheme[i+1:], "%d", &portVal)
	}
	host = qualifyHostname(host)
	return host, portVal
}

// jsonSorted returns a deterministic JSON string for comparison / hashing.
func jsonSorted(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// xDS config builders
// ---------------------------------------------------------------------------

func groupRoutesByHostname(routes []map[string]interface{}) map[string][]map[string]interface{} {
	groups := map[string][]map[string]interface{}{}
	for _, route := range routes {
		if toString(route["gateway_type"]) != "envoy" || toString(route["status"]) != "active" {
			continue
		}
		hostname := toString(route["hostname"])
		if hostname == "" {
			hostname = "*"
		}
		path := toString(route["path"])
		hostSlug := strings.ReplaceAll(strings.ReplaceAll(hostname, ".", "_"), "*", "wildcard")
		clusterName := "cluster_" + hostSlug + strings.ReplaceAll(path, "/", "_")
		entry := map[string]interface{}{
			"match": map[string]interface{}{"prefix": path},
			"route": map[string]interface{}{
				"cluster": clusterName,
				"timeout": "30s",
			},
		}
		groups[hostname] = append(groups[hostname], entry)
	}
	return groups
}

func buildVirtualHosts(routes []map[string]interface{}) []map[string]interface{} {
	groups := groupRoutesByHostname(routes)

	// Collect hostnames in sorted order for deterministic output.
	hostnames := make([]string, 0, len(groups))
	for h := range groups {
		if h != "*" {
			hostnames = append(hostnames, h)
		}
	}
	sort.Strings(hostnames)

	var vhosts []map[string]interface{}

	// Named virtual hosts for specific hostnames.
	for _, hostname := range hostnames {
		entries := groups[hostname]
		vhosts = append(vhosts, map[string]interface{}{
			"name":    "vh_" + strings.ReplaceAll(hostname, ".", "_"),
			"domains": []string{hostname, hostname + ":*"},
			"routes":  entries,
		})
	}

	// Wildcard fallback.
	wildcardRoutes := groups["*"]
	if len(wildcardRoutes) == 0 {
		wildcardRoutes = []map[string]interface{}{
			{
				"match": map[string]interface{}{"prefix": "/"},
				"direct_response": map[string]interface{}{
					"status": 503,
					"body":   map[string]interface{}{"inline_string": "No routes configured for this host"},
				},
			},
		}
	}
	vhosts = append(vhosts, map[string]interface{}{
		"name":    "wildcard",
		"domains": []string{"*"},
		"routes":  wildcardRoutes,
	})

	return vhosts
}

func buildRouteConfig(routes []map[string]interface{}, version string) map[string]interface{} {
	return map[string]interface{}{
		"version_info": version,
		"resources": []map[string]interface{}{
			{
				"@type":         "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
				"name":          "local_route",
				"virtual_hosts": buildVirtualHosts(routes),
			},
		},
	}
}

func buildClusterConfig(routes []map[string]interface{}, version string) map[string]interface{} {
	var clusters []map[string]interface{}

	for _, route := range routes {
		if toString(route["gateway_type"]) != "envoy" || toString(route["status"]) != "active" {
			continue
		}
		backend := toString(route["backend_url"])
		host, portVal := parseHostPort(backend)
		path := toString(route["path"])
		hostname := toString(route["hostname"])
		if hostname == "" {
			hostname = "*"
		}
		hostSlug := strings.ReplaceAll(strings.ReplaceAll(hostname, ".", "_"), "*", "wildcard")
		clusterName := "cluster_" + hostSlug + strings.ReplaceAll(path, "/", "_")

		clusters = append(clusters, map[string]interface{}{
			"name": clusterName,
			"type": "STRICT_DNS",
			"load_assignment": map[string]interface{}{
				"cluster_name": clusterName,
				"endpoints": []interface{}{
					map[string]interface{}{
						"lb_endpoints": []interface{}{
							map[string]interface{}{
								"endpoint": map[string]interface{}{
									"address": map[string]interface{}{
										"socket_address": map[string]interface{}{
											"address":    host,
											"port_value": portVal,
										},
									},
								},
							},
						},
					},
				},
			},
			"health_checks": []map[string]interface{}{
				{
					"timeout":              "2s",
					"interval":             "10s",
					"unhealthy_threshold":  3,
					"healthy_threshold":    2,
					"http_health_check":    map[string]interface{}{"path": "/health"},
				},
			},
			"outlier_detection": map[string]interface{}{
				"consecutive_5xx":      5,
				"interval":            "10s",
				"base_ejection_time":  "30s",
				"max_ejection_percent": 50,
			},
		})
	}

	// Auth service cluster
	authHost, authPort := parseHostPort(authServiceURL)
	clusters = append(clusters, map[string]interface{}{
		"name": "auth_service",
		"type": "STRICT_DNS",
		"load_assignment": map[string]interface{}{
			"cluster_name": "auth_service",
			"endpoints": []interface{}{
				map[string]interface{}{
					"lb_endpoints": []interface{}{
						map[string]interface{}{
							"endpoint": map[string]interface{}{
								"address": map[string]interface{}{
									"socket_address": map[string]interface{}{
										"address":    authHost,
										"port_value": authPort,
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// OPA cluster
	opaHost, opaPort := parseHostPort(opaURL)
	clusters = append(clusters, map[string]interface{}{
		"name": "opa_service",
		"type": "STRICT_DNS",
		"load_assignment": map[string]interface{}{
			"cluster_name": "opa_service",
			"endpoints": []interface{}{
				map[string]interface{}{
					"lb_endpoints": []interface{}{
						map[string]interface{}{
							"endpoint": map[string]interface{}{
								"address": map[string]interface{}{
									"socket_address": map[string]interface{}{
										"address":    opaHost,
										"port_value": opaPort,
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Jaeger cluster (with http2 protocol options)
	jaegerHost := jaegerGRPC
	jaegerPort := 4317
	if i := strings.LastIndex(jaegerGRPC, ":"); i >= 0 {
		jaegerHost = jaegerGRPC[:i]
		fmt.Sscanf(jaegerGRPC[i+1:], "%d", &jaegerPort)
	}
	clusters = append(clusters, map[string]interface{}{
		"name": "jaeger_cluster",
		"type": "STRICT_DNS",
		"typed_extension_protocol_options": map[string]interface{}{
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": map[string]interface{}{
				"@type":                "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
				"explicit_http_config": map[string]interface{}{"http2_protocol_options": map[string]interface{}{}},
			},
		},
		"load_assignment": map[string]interface{}{
			"cluster_name": "jaeger_cluster",
			"endpoints": []interface{}{
				map[string]interface{}{
					"lb_endpoints": []interface{}{
						map[string]interface{}{
							"endpoint": map[string]interface{}{
								"address": map[string]interface{}{
									"socket_address": map[string]interface{}{
										"address":    jaegerHost,
										"port_value": jaegerPort,
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Build resources with @type merged into each cluster.
	resources := make([]map[string]interface{}, 0, len(clusters))
	for _, c := range clusters {
		c["@type"] = "type.googleapis.com/envoy.config.cluster.v3.Cluster"
		resources = append(resources, c)
	}

	return map[string]interface{}{
		"version_info": version,
		"resources":    resources,
	}
}

func buildListenerConfig(routes []map[string]interface{}, version string) map[string]interface{} {
	return map[string]interface{}{
		"version_info": version,
		"resources": []map[string]interface{}{
			{
				"@type": "type.googleapis.com/envoy.config.listener.v3.Listener",
				"name":  "listener_0",
				"address": map[string]interface{}{
					"socket_address": map[string]interface{}{
						"address":    "0.0.0.0",
						"port_value": 8000,
					},
				},
				"filter_chains": []map[string]interface{}{
					{
						"filters": []map[string]interface{}{
							{
								"name": "envoy.filters.network.http_connection_manager",
								"typed_config": map[string]interface{}{
									"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
									"stat_prefix":          "ingress_http",
									"generate_request_id":  true,
									"tracing": map[string]interface{}{
										"provider": map[string]interface{}{
											"name": "envoy.tracers.opentelemetry",
											"typed_config": map[string]interface{}{
												"@type": "type.googleapis.com/envoy.config.trace.v3.OpenTelemetryConfig",
												"grpc_service": map[string]interface{}{
													"envoy_grpc": map[string]interface{}{"cluster_name": "jaeger_cluster"},
													"timeout":    "3s",
												},
												"service_name": "envoy-gateway",
											},
										},
									},
									"route_config": map[string]interface{}{
										"name":          "local_route",
										"virtual_hosts": buildVirtualHosts(routes),
									},
									"http_filters": []map[string]interface{}{
										// ext_authz validates Bearer tokens against the auth-service
										// using Envoy's native HTTP ext-authz protocol.
										// Unauthenticated requests (no Bearer / cookie) are passed
										// through so HTML login pages can still be served.
										// Expired or revoked tokens receive 401, which the browser
										// app handles by redirecting to its login view.
										{
											"name": "envoy.filters.http.ext_authz",
											"typed_config": map[string]interface{}{
												"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz",
												"http_service": map[string]interface{}{
													"server_uri": map[string]interface{}{
														"uri":     authServiceURL + "/gateway/ext-authz-http",
														"cluster": "auth_service",
														"timeout": "5s",
													},
													"path_prefix": "/gateway/ext-authz-http",
													"authorization_response": map[string]interface{}{
														// Forward x-auth-* headers set by the auth service
														// into the upstream (backend) request.
														"allowed_upstream_headers": map[string]interface{}{
															"patterns": []map[string]interface{}{
																{"prefix": "x-auth-"},
															},
														},
													},
												},
												// Allow requests through if auth-service is temporarily
												// unreachable — prevents auth outage from taking down
												// all web traffic in the demo environment.
												"failure_mode_allow": true,
											},
										},
										{
											"name": "envoy.filters.http.router",
											"typed_config": map[string]interface{}{
												"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", s)
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

func getJSON(ctx context.Context, url string, headers http.Header) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func postJSON(ctx context.Context, url string, payload interface{}) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------------------------------------------------------------------------
// Background goroutines
// ---------------------------------------------------------------------------

// pollRoutes fetches routes from the management-api and updates per-fleet
// snapshots. It polls for the global (all-routes) set and also for each
// known fleet that has previously connected via xDS.
func pollRoutes(tracer trace.Tracer) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for ; ; <-ticker.C {
		func() {
			ctx, span := tracer.Start(context.Background(), "xds.routes.poll")
			defer span.End()

			headers := appotel.InjectTraceHeaders(ctx)

			// Collect the set of fleet keys we need to poll for.
			mu.RLock()
			fleetKeys := make([]string, 0, len(snapshots))
			for k := range snapshots {
				fleetKeys = append(fleetKeys, k)
			}
			mu.RUnlock()

			// Always include the global key.
			hasGlobal := false
			for _, k := range fleetKeys {
				if k == globalFleetKey {
					hasGlobal = true
					break
				}
			}
			if !hasGlobal {
				fleetKeys = append(fleetKeys, globalFleetKey)
			}

			totalChanged := 0

			for _, fleetKey := range fleetKeys {
				// Build the URL. Global gets only routes with no node assignments;
				// fleet keys get filtered by fleet_id.
				url := managementAPIURL + "/routes?gateway_type=envoy&status=active"
				if fleetKey != globalFleetKey {
					// fleetKey is the fleet ID directly (e.g. "fleet-jpmm")
					url += "&fleet_id=" + fleetKey
				} else {
					// Global shared gateway only gets routes with no node assignments.
					// Routes assigned to a fleet with dedicated nodes are excluded.
					url += "&unassigned=true"
				}

				status, body, err := getJSON(ctx, url, headers)
				if err != nil {
					continue
				}
				var newRoutes []map[string]interface{}
				if status == 200 {
					_ = json.Unmarshal(body, &newRoutes)
				}

				mu.RLock()
				snap := getSnapshot(fleetKey)
				oldJSON := jsonSorted(snap.routes)
				mu.RUnlock()
				newJSON := jsonSorted(newRoutes)
				changed := oldJSON != newJSON

				if changed {
					hash := md5.Sum([]byte(newJSON))
					version := fmt.Sprintf("%x", hash[:4])

					mu.Lock()
					s := getSnapshot(fleetKey)
					s.routes = newRoutes
					s.version = version
					mu.Unlock()

					totalChanged++

					_, updateSpan := tracer.Start(ctx, "xds.snapshot.update")
					updateSpan.SetAttributes(
						attribute.String("fleet", fleetKey),
						attribute.Int("routes.count", len(newRoutes)),
						attribute.String("version", version),
					)
					updateSpan.End()
				}
			}

			span.SetAttributes(
				attribute.Int("fleets.polled", len(fleetKeys)),
				attribute.Int("fleets.changed", totalChanged),
			)
		}()
	}
}

func reportHealth() {
	// Wait for Envoy to start and run first health checks.
	time.Sleep(8 * time.Second)

	probeClient := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for ; ; <-ticker.C {
		func() {
			ctx := context.Background()
			status, body, err := getJSON(ctx, envoyAdminURL+"/clusters?format=json", nil)
			if err != nil || status != 200 {
				return
			}

			var clusterData map[string]interface{}
			if err := json.Unmarshal(body, &clusterData); err != nil {
				return
			}

			statuses, ok := clusterData["cluster_statuses"].([]interface{})
			if !ok {
				return
			}

			var reports []map[string]interface{}
			for _, cs := range statuses {
				clusterStatus, ok := cs.(map[string]interface{})
				if !ok {
					continue
				}
				name := toString(clusterStatus["name"])
				if !strings.HasPrefix(name, "cluster_") {
					continue
				}

				hostStatuses, _ := clusterStatus["host_statuses"].([]interface{})
				for _, hs := range hostStatuses {
					hostStatus, ok := hs.(map[string]interface{})
					if !ok {
						continue
					}

					addrMap, _ := hostStatus["address"].(map[string]interface{})
					socketAddr, _ := addrMap["socket_address"].(map[string]interface{})
					host := toString(socketAddr["address"])
					portVal := toInt(socketAddr["port_value"])

					// Determine health.
					healthStatusMap, _ := hostStatus["health_status"].(map[string]interface{})
					eds := toString(healthStatusMap["eds_health_status"])
					if eds == "" {
						eds = "HEALTHY"
					}
					failed, _ := healthStatusMap["failed_active_health_check"].(bool)
					health := "healthy"
					if eds == "UNHEALTHY" || failed {
						health = "unhealthy"
					}

					// Direct probe for latency.
					var latencyMs float64
					probeURL := fmt.Sprintf("http://%s:%d/health", host, portVal)
					start := time.Now()
					resp, probeErr := probeClient.Get(probeURL)
					if probeErr == nil {
						latencyMs = float64(time.Since(start).Milliseconds())
						if resp.StatusCode != 200 {
							health = "unhealthy"
						}
						resp.Body.Close()
					} else {
						health = "unhealthy"
						latencyMs = 0
					}

					reports = append(reports, map[string]interface{}{
						"gateway_type":  "envoy",
						"cluster_name":  name,
						"backend_host":  host,
						"backend_port":  portVal,
						"health_status": health,
						"latency_ms":    latencyMs,
						"reporter":      "envoy-control-plane",
					})
				}
			}

			if len(reports) > 0 {
				_ = postJSON(ctx, managementAPIURL+"/health-reports", map[string]interface{}{
					"reports": reports,
				})
			}
		}()
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// resolveFleetSnapshot reads the xDS request body, determines the fleet key,
// and returns the matching snapshot's routes and version.
func resolveFleetSnapshot(r *http.Request) ([]map[string]interface{}, string) {
	fleetKey, _ := fleetKeyFromRequest(r)

	mu.RLock()
	defer mu.RUnlock()

	// Ensure the fleet key exists in snapshots so the next poll cycle will
	// fetch its routes.
	snap := getSnapshot(fleetKey)

	// If this is a new fleet that hasn't been polled yet, its routes will be
	// empty. That's fine -- next poll cycle will populate it.
	return snap.routes, snap.version
}

func handleDiscoveryRoutes(w http.ResponseWriter, r *http.Request) {
	routes, version := resolveFleetSnapshot(r)
	cfg := buildRouteConfig(routes, version)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func handleDiscoveryClusters(w http.ResponseWriter, r *http.Request) {
	routes, version := resolveFleetSnapshot(r)
	cfg := buildClusterConfig(routes, version)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func handleDiscoveryListeners(w http.ResponseWriter, r *http.Request) {
	routes, version := resolveFleetSnapshot(r)
	cfg := buildListenerConfig(routes, version)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func handleSnapshotRoutes(w http.ResponseWriter, r *http.Request) {
	// Optional ?fleet param for per-fleet queries. Without it, return all routes
	// across all fleet snapshots (used by the drift detector in management-api).
	fleetKey := r.URL.Query().Get("fleet")

	mu.RLock()
	defer mu.RUnlock()

	var result []map[string]interface{}
	addRoutesFromSnap := func(snap *fleetSnapshot) {
		for _, route := range snap.routes {
			if toString(route["gateway_type"]) == "envoy" && toString(route["status"]) == "active" {
				result = append(result, map[string]interface{}{
					"path":         toString(route["path"]),
					"hostname":     toString(route["hostname"]),
					"backend_url":  toString(route["backend_url"]),
					"status":       toString(route["status"]),
					"gateway_type": toString(route["gateway_type"]),
				})
			}
		}
	}

	if fleetKey != "" {
		addRoutesFromSnap(getSnapshot(fleetKey))
	} else {
		// Return routes from all fleet snapshots (global + per-fleet).
		for _, snap := range snapshots {
			addRoutesFromSnap(snap)
		}
	}

	if result == nil {
		result = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	globalSnap := getSnapshot(globalFleetKey)
	fleetCount := len(snapshots)
	resp := map[string]interface{}{
		"status":  "ok",
		"service": "envoy-control-plane",
		"version": globalSnap.version,
		"routes":  len(globalSnap.routes),
		"fleets":  fleetCount,
	}
	mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	tp, tracer := appotel.InitOTEL("envoy-control-plane")
	defer tp.Shutdown(context.Background())

	go pollRoutes(tracer)
	go reportHealth()

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(appotel.Middleware("envoy-control-plane"))

	// xDS REST endpoints (8 total: v2 + v3 for routes, clusters, listeners, plus two extra)
	r.Post("/v3/discovery:routes", handleDiscoveryRoutes)
	r.Post("/v2/discovery:routes", handleDiscoveryRoutes)
	r.Post("/v3/discovery:clusters", handleDiscoveryClusters)
	r.Post("/v2/discovery:clusters", handleDiscoveryClusters)
	r.Post("/v3/discovery:listeners", handleDiscoveryListeners)
	r.Post("/v2/discovery:listeners", handleDiscoveryListeners)

	// Drift detection
	r.Get("/snapshot/routes", handleSnapshotRoutes)

	// Health
	r.Get("/health", handleHealth)

	log.Printf("envoy-control-plane listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
