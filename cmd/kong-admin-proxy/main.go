package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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
	kongAdminURL     string
	authServiceURL   string
	opaURL           string
)

func init() {
	port = os.Getenv("PORT")
	if port == "" {
		port = "8102"
	}
	managementAPIURL = os.Getenv("MANAGEMENT_API_URL")
	if managementAPIURL == "" {
		managementAPIURL = "http://management-api:8003"
	}
	kongAdminURL = os.Getenv("KONG_ADMIN_URL")
	if kongAdminURL == "" {
		kongAdminURL = "http://gateway-kong:8001"
	}
	authServiceURL = os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8001"
	}
	opaURL = os.Getenv("OPA_URL")
	if opaURL == "" {
		opaURL = "http://opa:8181"
	}
}

// ---------------------------------------------------------------------------
// Per-fleet state
// ---------------------------------------------------------------------------

// fleetSyncState holds the synced routes and Kong node URLs for one fleet (or
// the "global" shared gateway).
type fleetSyncState struct {
	syncedRoutes map[string]map[string]string // key "hostname:path" -> {"backend_url": ..., "hostname": ...}
	kongAdminURL string                        // admin URL for this fleet's Kong container
}

const globalFleetKey = "global"

var (
	mu          sync.RWMutex
	fleetStates = map[string]*fleetSyncState{
		globalFleetKey: {
			syncedRoutes: map[string]map[string]string{},
		},
	}
	// triggerSync receives a signal to kick off an immediate sync cycle.
	triggerSyncCh = make(chan struct{}, 1)
)

// getFleetState returns or creates state for the given fleet key.
func getFleetState(fleetKey string) *fleetSyncState {
	if s, ok := fleetStates[fleetKey]; ok {
		return s
	}
	s := &fleetSyncState{
		syncedRoutes: map[string]map[string]string{},
	}
	fleetStates[fleetKey] = s
	return s
}

// kongNode represents a running Kong container returned by the management-api.
type kongNode struct {
	ID       string `json:"id"`
	FleetID  string `json:"fleet_id"`
	AdminURL string `json:"admin_url"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
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

func postJSON(ctx context.Context, url string, payload interface{}, extraHeaders http.Header) (int, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vals := range extraHeaders {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// ---------------------------------------------------------------------------
// Kong declarative config builder
// ---------------------------------------------------------------------------

func buildDeclarativeConfig(desiredRoutes []map[string]interface{}) map[string]interface{} {
	var services []map[string]interface{}
	var routesList []map[string]interface{}
	var upstreams []map[string]interface{}
	seenUpstreams := map[string]bool{}

	for _, route := range desiredRoutes {
		hostname := toString(route["hostname"])
		if hostname == "" {
			hostname = "*"
		}
		hostSlug := hostname
		if hostname == "*" {
			hostSlug = "wildcard"
		} else {
			hostSlug = strings.ReplaceAll(strings.ReplaceAll(hostname, ".", "-"), "*", "wildcard")
		}
		path := toString(route["path"])
		pathSlug := strings.Trim(strings.ReplaceAll(path, "/", "-"), "-")
		svcName := "svc-" + hostSlug + "-" + pathSlug
		upstreamName := "upstream-" + hostSlug + "-" + pathSlug
		backend := strings.TrimRight(toString(route["backend_url"]), "/")

		// Parse backend for upstream target.
		noScheme := backend
		if idx := strings.Index(backend, "://"); idx >= 0 {
			noScheme = backend[idx+3:]
		}
		backendHost := noScheme
		backendPort := 80
		if i := strings.LastIndex(noScheme, ":"); i >= 0 {
			backendHost = noScheme[:i]
			fmt.Sscanf(noScheme[i+1:], "%d", &backendPort)
		}
		scheme := "http"
		if idx := strings.Index(backend, "://"); idx >= 0 {
			scheme = backend[:idx]
		}

		// Create upstream with health checks (deduplicate).
		if !seenUpstreams[upstreamName] {
			seenUpstreams[upstreamName] = true
			upstreams = append(upstreams, map[string]interface{}{
				"name": upstreamName,
				"targets": []map[string]interface{}{
					{"target": fmt.Sprintf("%s:%d", backendHost, backendPort), "weight": 100},
				},
				"healthchecks": map[string]interface{}{
					"active": map[string]interface{}{
						"type":      "http",
						"http_path": "/health",
						"timeout":   2,
						"healthy":   map[string]interface{}{"interval": 10, "successes": 2},
						"unhealthy": map[string]interface{}{
							"interval":     5,
							"http_failures": 3,
							"tcp_failures":  3,
							"timeouts":      3,
						},
					},
					"passive": map[string]interface{}{
						"type":      "http",
						"healthy":   map[string]interface{}{"successes": 2},
						"unhealthy": map[string]interface{}{"http_failures": 5},
					},
				},
			})
		}

		// Service points at upstream.
		services = append(services, map[string]interface{}{
			"name":     svcName,
			"host":     upstreamName,
			"port":     backendPort,
			"protocol": scheme,
		})

		// Route entry.
		methods, _ := route["methods"].([]interface{})
		if len(methods) == 0 {
			methods = []interface{}{"GET", "POST", "PUT", "DELETE"}
		}
		methodStrings := make([]string, len(methods))
		for i, m := range methods {
			methodStrings[i] = toString(m)
		}

		routeEntry := map[string]interface{}{
			"name":       "route-" + hostSlug + "-" + pathSlug,
			"service":    svcName,
			"paths":      []string{path},
			"methods":    methodStrings,
			"strip_path": false,
		}
		if hostname != "" && hostname != "*" {
			routeEntry["hosts"] = []string{hostname}
		}
		routesList = append(routesList, routeEntry)
	}

	return map[string]interface{}{
		"_format_version": "3.0",
		"upstreams":       upstreams,
		"services":        services,
		"routes":          routesList,
		"plugins": []map[string]interface{}{
			{
				"name": "cors",
				"config": map[string]interface{}{
					"origins": []string{"*"},
					"methods": []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
					"headers": []string{
						"Accept", "Authorization", "Content-Type", "DPoP",
						"X-Akamai-Request-Id", "traceparent", "tracestate", "User-Agent",
					},
					"credentials": true,
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Fleet discovery
// ---------------------------------------------------------------------------

// discoverFleets queries the management-api for all fleets and their Kong
// nodes. Returns a map of fleetKey -> []kongNode.
func discoverFleets(ctx context.Context, headers http.Header) map[string][]kongNode {
	result := map[string][]kongNode{}

	// Get fleet list from management-api.
	status, body, err := getJSON(ctx, managementAPIURL+"/fleets", headers)
	if err != nil || status != 200 {
		return result
	}

	var fleets []map[string]interface{}
	if err := json.Unmarshal(body, &fleets); err != nil {
		return result
	}

	for _, fleet := range fleets {
		fleetID := toString(fleet["id"])
		if fleetID == "" {
			continue
		}
		fleetKey := fleetID

		// Get nodes for this fleet.
		nStatus, nBody, nErr := getJSON(ctx, managementAPIURL+"/fleets/"+fleetID+"/nodes", headers)
		if nErr != nil || nStatus != 200 {
			continue
		}

		// Response is {"fleet_id":..., "nodes":[...], "count":...}
		var resp struct {
			Nodes []struct {
				ContainerID   string `json:"container_id"`
				ContainerName string `json:"container_name"`
				FleetID       string `json:"fleet_id"`
				GatewayType   string `json:"gateway_type"`
				AdminURL      string `json:"admin_url"`
				Host          string `json:"host"`
				Port          int    `json:"port"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(nBody, &resp); err != nil {
			continue
		}

		// Keep only Kong nodes.
		var nodes []kongNode
		for _, n := range resp.Nodes {
			if n.GatewayType == "kong" {
				nodes = append(nodes, kongNode{
					ID:       n.ContainerID,
					FleetID:  n.FleetID,
					AdminURL: n.AdminURL,
					Host:     n.Host,
					Port:     n.Port,
				})
			}
		}

		if len(nodes) > 0 {
			result[fleetKey] = nodes
		}
	}

	return result
}

// pushConfigToKong POSTs declarative config to a single Kong admin URL.
func pushConfigToKong(ctx context.Context, adminURL string, config map[string]interface{}, headers http.Header) error {
	longClient := &http.Client{Timeout: 10 * time.Second}
	configBody, err := json.Marshal(config)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+"/config", bytes.NewReader(configBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := longClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("kong returned status %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Background goroutines
// ---------------------------------------------------------------------------

func syncRoutes(tracer trace.Tracer) {
	// Wait for Kong to be ready.
	time.Sleep(5 * time.Second)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-triggerSyncCh:
		}
		func() {
			ctx, span := tracer.Start(context.Background(), "kong.sync")
			defer span.End()

			start := time.Now()
			headers := appotel.InjectTraceHeaders(ctx)

			// ----------------------------------------------------------
			// 1. Sync the global (shared) gateway-kong instance.
			// ----------------------------------------------------------
			syncFleet(ctx, tracer, globalFleetKey, kongAdminURL, headers)

			// ----------------------------------------------------------
			// 2. Discover per-fleet Kong containers and sync each.
			// ----------------------------------------------------------
			fleetNodes := discoverFleets(ctx, headers)
			for fleetKey, nodes := range fleetNodes {
				// Deduplicate admin URLs for this fleet so we don't push the same
				// config twice when multiple nodes share the same Kong instance.
				seen := map[string]bool{}
				for _, node := range nodes {
					adminURL := node.AdminURL
					if adminURL == "" && node.Host != "" && node.Port > 0 {
						adminURL = fmt.Sprintf("http://%s:%d", node.Host, node.Port)
					}
					// In single-Kong deployments (e.g., local K8s dev) there may be no
					// per-node admin URL — fall back to the shared Kong admin endpoint.
					if adminURL == "" {
						adminURL = kongAdminURL
					}
					if seen[adminURL] {
						continue
					}
					seen[adminURL] = true
					syncFleet(ctx, tracer, fleetKey, adminURL, headers)
				}
			}

			// Clean up fleet states for fleets that no longer have nodes.
			mu.Lock()
			for key := range fleetStates {
				if key == globalFleetKey {
					continue
				}
				if _, exists := fleetNodes[key]; !exists {
					delete(fleetStates, key)
				}
			}
			mu.Unlock()

			durationMs := float64(time.Since(start).Milliseconds())
			span.SetAttributes(
				attribute.Int("fleets.synced", len(fleetNodes)+1),
				attribute.Float64("sync.duration_ms", durationMs),
			)
		}()
	}
}

// syncFleet synchronises routes for a single fleet (or global) to a single
// Kong admin endpoint.
func syncFleet(ctx context.Context, tracer trace.Tracer, fleetKey, adminURL string, headers http.Header) {
	_, span := tracer.Start(ctx, "kong.sync.fleet")
	span.SetAttributes(attribute.String("fleet", fleetKey), attribute.String("kong.admin_url", adminURL))
	defer span.End()

	// Build the route query URL. Global gets only unassigned routes.
	url := managementAPIURL + "/routes?gateway_type=kong&status=active"
	if fleetKey != globalFleetKey {
		url += "&fleet_id=" + fleetKey
	} else {
		// Global shared Kong only gets routes with no node assignments.
		// Routes assigned to a fleet with dedicated nodes are excluded;
		// they must be served by the fleet's own Kong instance.
		url += "&unassigned=true"
	}

	status, body, err := getJSON(ctx, url, headers)
	if err != nil {
		return
	}
	var desiredRoutes []map[string]interface{}
	if status == 200 {
		_ = json.Unmarshal(body, &desiredRoutes)
	}

	// Build new desired state keyed by hostname:path.
	newSynced := map[string]map[string]string{}
	for _, route := range desiredRoutes {
		hostname := toString(route["hostname"])
		if hostname == "" {
			hostname = "*"
		}
		key := hostname + ":" + toString(route["path"])
		newSynced[key] = map[string]string{
			"backend_url": toString(route["backend_url"]),
			"hostname":    hostname,
		}
	}

	// Only push config if something changed.
	mu.RLock()
	state := getFleetState(fleetKey)
	changed := !mapsEqual(newSynced, state.syncedRoutes)
	oldKeys := copyKeys(state.syncedRoutes)
	mu.RUnlock()

	if changed {
		config := buildDeclarativeConfig(desiredRoutes)

		traceHeaders := appotel.InjectTraceHeaders(ctx)
		if err := pushConfigToKong(ctx, adminURL, config, traceHeaders); err != nil {
			log.Printf("kong sync failed for %s at %s: %v", fleetKey, adminURL, err)
			return
		}

		newKeys := copyKeys(newSynced)
		added := diffKeys(newKeys, oldKeys)
		removed := diffKeys(oldKeys, newKeys)

		for _, path := range added {
			_, cs := tracer.Start(ctx, "kong.route.create")
			cs.SetAttributes(
				attribute.String("fleet", fleetKey),
				attribute.String("route.path", path),
			)
			cs.End()
		}
		for _, path := range removed {
			_, ds := tracer.Start(ctx, "kong.route.delete")
			ds.SetAttributes(
				attribute.String("fleet", fleetKey),
				attribute.String("route.path", path),
			)
			ds.End()
		}

		mu.Lock()
		s := getFleetState(fleetKey)
		s.syncedRoutes = newSynced
		s.kongAdminURL = adminURL
		mu.Unlock()

		span.SetAttributes(
			attribute.Int("routes.added", len(added)),
			attribute.Int("routes.removed", len(removed)),
		)
	}

	span.SetAttributes(attribute.Int("routes.synced", len(desiredRoutes)))
}

func reportHealth() {
	// Wait for Kong upstreams to be created and health checks to run.
	time.Sleep(8 * time.Second)

	probeClient := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for ; ; <-ticker.C {
		func() {
			ctx := context.Background()

			// Collect all Kong admin URLs we know about.
			mu.RLock()
			type fleetAdmin struct {
				key      string
				adminURL string
			}
			var targets []fleetAdmin
			for key, state := range fleetStates {
				adminURL := state.kongAdminURL
				if key == globalFleetKey && adminURL == "" {
					adminURL = kongAdminURL
				}
				if adminURL == "" {
					continue
				}
				targets = append(targets, fleetAdmin{key: key, adminURL: adminURL})
			}
			mu.RUnlock()

			var reports []map[string]interface{}
			for _, target := range targets {
				fleetReports := probeKongUpstreams(ctx, target.adminURL, probeClient)
				reports = append(reports, fleetReports...)
			}

			if len(reports) > 0 {
				_, _ = postJSON(ctx, managementAPIURL+"/health-reports", map[string]interface{}{
					"reports": reports,
				}, nil)
			}
		}()
	}
}

// probeKongUpstreams queries a single Kong admin endpoint for upstream health
// and returns health reports.
func probeKongUpstreams(ctx context.Context, adminURL string, probeClient *http.Client) []map[string]interface{} {
	status, body, err := getJSON(ctx, adminURL+"/upstreams", nil)
	if err != nil || status != 200 {
		return nil
	}

	var upstreamData map[string]interface{}
	if err := json.Unmarshal(body, &upstreamData); err != nil {
		return nil
	}

	dataSlice, _ := upstreamData["data"].([]interface{})
	var reports []map[string]interface{}

	for _, u := range dataSlice {
		upstream, ok := u.(map[string]interface{})
		if !ok {
			continue
		}
		name := toString(upstream["name"])

		// Get health per upstream.
		hStatus, hBody, hErr := getJSON(ctx, adminURL+"/upstreams/"+name+"/health", nil)
		if hErr != nil || hStatus != 200 {
			continue
		}

		var healthData map[string]interface{}
		if err := json.Unmarshal(hBody, &healthData); err != nil {
			continue
		}

		targets, _ := healthData["data"].([]interface{})
		for _, t := range targets {
			target, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			targetAddr := toString(target["target"])
			if targetAddr == "" {
				targetAddr = "0.0.0.0:0"
			}

			host := targetAddr
			portVal := 0
			if i := strings.LastIndex(targetAddr, ":"); i >= 0 {
				host = targetAddr[:i]
				fmt.Sscanf(targetAddr[i+1:], "%d", &portVal)
			}

			healthStr := toString(target["health"])
			health := "unknown"
			switch healthStr {
			case "HEALTHY":
				health = "healthy"
			case "UNHEALTHY":
				health = "unhealthy"
			}

			// Direct probe for latency.
			var latencyMs float64
			probeURL := fmt.Sprintf("http://%s:%d/health", host, portVal)
			probeStart := time.Now()
			resp, probeErr := probeClient.Get(probeURL)
			if probeErr == nil {
				latencyMs = float64(time.Since(probeStart).Milliseconds())
				if resp.StatusCode != 200 {
					health = "unhealthy"
				}
				resp.Body.Close()
			} else {
				if health != "unhealthy" {
					health = "unhealthy"
				}
				latencyMs = 0
			}

			reports = append(reports, map[string]interface{}{
				"gateway_type":  "kong",
				"cluster_name":  name,
				"backend_host":  host,
				"backend_port":  portVal,
				"health_status": health,
				"latency_ms":    latencyMs,
				"reporter":      "kong-admin-proxy",
			})
		}
	}

	return reports
}

// ---------------------------------------------------------------------------
// Map helpers
// ---------------------------------------------------------------------------

func mapsEqual(a, b map[string]map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if len(av) != len(bv) {
			return false
		}
		for kk, vv := range av {
			if bv[kk] != vv {
				return false
			}
		}
	}
	return true
}

func copyKeys(m map[string]map[string]string) map[string]bool {
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

func diffKeys(a, b map[string]bool) []string {
	var result []string
	for k := range a {
		if !b[k] {
			result = append(result, k)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func handleSyncStatusRoutes(w http.ResponseWriter, r *http.Request) {
	// Optional ?fleet param for per-fleet queries. Without it, return all routes
	// across all fleets (used by the drift detector in management-api).
	fleetKey := r.URL.Query().Get("fleet")

	mu.RLock()
	defer mu.RUnlock()

	var result []map[string]interface{}
	if fleetKey != "" {
		// Return routes for the specified fleet only.
		state := getFleetState(fleetKey)
		for path, info := range state.syncedRoutes {
			result = append(result, map[string]interface{}{
				"path":         path,
				"backend_url":  info["backend_url"],
				"status":       "active",
				"gateway_type": "kong",
			})
		}
	} else {
		// Return all routes from all fleet states (global + per-fleet).
		for _, state := range fleetStates {
			for path, info := range state.syncedRoutes {
				result = append(result, map[string]interface{}{
					"path":         path,
					"backend_url":  info["backend_url"],
					"status":       "active",
					"gateway_type": "kong",
				})
			}
		}
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	// Non-blocking send — if a sync is already queued, this is a no-op.
	select {
	case triggerSyncCh <- struct{}{}:
	default:
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"triggered":true}`))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	globalState := getFleetState(globalFleetKey)
	totalRoutes := 0
	for _, state := range fleetStates {
		totalRoutes += len(state.syncedRoutes)
	}
	resp := map[string]interface{}{
		"status":              "ok",
		"service":             "kong-admin-proxy",
		"synced_routes":       len(globalState.syncedRoutes),
		"total_synced_routes": totalRoutes,
		"fleets":              len(fleetStates),
	}
	mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	tp, tracer := appotel.InitOTEL("kong-admin-proxy")
	defer tp.Shutdown(context.Background())

	// Set the global fleet's Kong admin URL.
	mu.Lock()
	fleetStates[globalFleetKey].kongAdminURL = kongAdminURL
	mu.Unlock()

	go syncRoutes(tracer)
	go reportHealth()

	r := chi.NewRouter()
	r.Use(middleware.CORS())
	r.Use(appotel.Middleware("kong-admin-proxy"))

	// Drift detection
	r.Get("/sync-status/routes", handleSyncStatusRoutes)

	// Manual sync trigger (called by management-api reconcile)
	r.Post("/sync/trigger", handleSyncTrigger)

	// Health
	r.Get("/health", handleHealth)

	log.Printf("kong-admin-proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
