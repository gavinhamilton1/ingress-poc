package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func seedDefaults(db *sqlx.DB) {
	var count int
	db.Get(&count, "SELECT COUNT(*) FROM routes")
	if count > 0 {
		log.Printf("Database already seeded (%d routes)", count)
		return
	}

	svcWebURL := getEnvOr("SVC_WEB_URL", "http://svc-web:8004")
	svcAPIURL := getEnvOr("SVC_API_URL", "http://svc-api:8005")
	now := float64(time.Now().Unix())
	regions, _ := json.Marshal([]string{"us-east-1", "us-east-2"})

	// ──────────────────────────────────────────────
	// DATA PLANE FLEETS (12)
	// ──────────────────────────────────────────────
	type fleetSeed struct {
		ID, Name, Subdomain, LOB, HostEnv, GatewayType, Region, AuthProvider string
		InstancesCount                                                       float64
		Description, TrafficType, TLSTermination                             string
		HTTP2Enabled                                                         bool
		ConnectionLimit, TimeoutConnectMs                                    int
		TimeoutRequestMs, RateLimitRPS                                       int
		KongPlugins                                                          []string
		HealthCheckPath                                                      string
		HealthCheckIntervalS                                                 int
		AuthnMechanism                                                       string
		DefaultAuthzScopes                                                   []string
		TLSRequired, WAFProfile, ResourceProfile                             string
		AutoscaleEnabled                                                     bool
		AutoscaleMin, AutoscaleMax                                           int
		AutoscaleCPUThreshold                                                int
		FleetType                                                            string
		Notes                                                                string
	}

	dataFleets := []fleetSeed{
		{"fleet-jpmm", "JPMM", "jpmm.jpm.com", "Markets", "aws", "mixed", "us-east", "Janus", 3,
			"JP Morgan Markets — research, S&T, events", "web", "edge", true,
			1024, 5000, 30000, 500,
			[]string{"rate-limiting", "cors", "jwt"},
			"/health", 10, "bearer",
			[]string{"markets:read"},
			"required", "standard", "medium",
			true, 2, 16, 70, "data", ""},
		{"fleet-execute", "Execute", "execute.jpm.com", "Markets", "psaas", "envoy", "us-east", "Janus", 1,
			"High-throughput trading execution platform", "web", "edge", true,
			4096, 3000, 15000, 2000,
			[]string{"rate-limiting", "cors", "jwt"},
			"/health", 5, "bearer",
			[]string{"markets:read", "markets:write", "execute:trade"},
			"required", "standard", "large",
			true, 4, 32, 60, "data", ""},
		{"fleet-access", "JPMA", "access.jpm.com", "Payments", "psaas", "envoy", "us-east", "AuthE1.0", 1,
			"JP Morgan Access — payments portal", "web", "edge", true,
			2048, 5000, 30000, 1000,
			[]string{"rate-limiting", "cors"},
			"/health", 10, "bearer",
			[]string{"payments:read", "access:view"},
			"required", "strict", "medium",
			true, 2, 16, 70, "data", ""},
		{"fleet-mobile", "Access Mobile", "access-mobile.jpm.com", "Payments", "psaas", "envoy", "us-east", "AuthE1.0", 1,
			"Access mobile-optimized endpoints", "web", "edge", true,
			512, 5000, 20000, 500,
			[]string{"rate-limiting", "cors"},
			"/health", 10, "bearer",
			[]string{"payments:read", "access:mobile"},
			"required", "standard", "small",
			false, 2, 8, 75, "data", ""},
		{"fleet-digital", "JPMDB", "digital-banking.jpm.com", "Payments", "aws", "envoy", "us-east", "Sentry", 1,
			"Digital banking consumer platform", "web", "edge", true,
			2048, 5000, 30000, 1000,
			[]string{"rate-limiting", "cors"},
			"/health", 10, "bearer",
			[]string{"payments:read", "digital-banking:view"},
			"required", "strict", "medium",
			true, 2, 16, 70, "data", ""},
		{"fleet-smb", "Merchant Services", "smb.jpm.com", "Payments", "psaas", "envoy", "us-east", "Chase", 1,
			"Small-business merchant services", "web", "edge", true,
			1024, 5000, 30000, 500,
			[]string{"rate-limiting", "cors", "key-auth"},
			"/health", 10, "api-key",
			[]string{"payments:read", "merchant:view"},
			"required", "standard", "medium",
			false, 2, 8, 70, "data", ""},
		{"fleet-pdp", "PDP", "developer.jpm.com", "Payments", "aws", "kong", "us-east", "Sentry", 1,
			"Payments Developer Portal — API gateway", "api", "edge", true,
			1024, 5000, 30000, 1000,
			[]string{"rate-limiting", "cors", "key-auth", "request-transformer"},
			"/health", 10, "api-key",
			[]string{"payments:read", "developer:api"},
			"required", "standard", "medium",
			true, 2, 12, 70, "data", ""},
		{"fleet-iq", "IQ", "iq.jpm.com", "Global Banking", "psaas", "envoy", "us-east", "Janus", 1,
			"IQ global banking analytics", "web", "edge", true,
			1024, 5000, 30000, 500,
			[]string{"rate-limiting", "cors"},
			"/health", 10, "bearer",
			[]string{"global-banking:read", "iq:view"},
			"required", "standard", "medium",
			false, 2, 8, 70, "data", ""},
		{"fleet-secsvcs", "SecSvcs", "secsvcs.jpm.com", "Security Services", "psaas", "envoy", "us-east", "Janus", 1,
			"Security services administration", "web", "edge", true,
			1024, 5000, 30000, 200,
			[]string{"rate-limiting", "cors"},
			"/health", 10, "mtls",
			[]string{"security:read", "secsvcs:admin"},
			"required", "strict", "medium",
			false, 2, 8, 70, "data", ""},
		{"fleet-authn", "AuthN", "login.jpm.com", "xCIB", "psaas", "envoy", "us-east", "N/A", 1,
			"Authentication service — login flows", "web", "edge", true,
			2048, 3000, 10000, 5000,
			[]string{"rate-limiting", "cors"},
			"/health", 5, "none",
			[]string{},
			"required", "strict", "medium",
			true, 4, 24, 60, "data", ""},
		{"fleet-authz", "AuthZ", "authz.jpm.com", "xCIB", "psaas", "envoy", "us-east", "Sentry", 1,
			"Authorization policy engine", "web", "edge", true,
			2048, 3000, 10000, 3000,
			[]string{"rate-limiting", "cors"},
			"/health", 5, "bearer",
			[]string{"cib:admin", "authz:manage"},
			"required", "strict", "medium",
			true, 4, 16, 65, "data", ""},
		{"fleet-console", "Console", "console.jpm.com", "xCIB", "psaas", "envoy", "us-east", "N/A", 2,
			"Ingress management console", "web", "edge", true,
			512, 5000, 30000, 0,
			[]string{},
			"/", 15, "none",
			[]string{},
			"required", "standard", "small",
			false, 1, 4, 80, "data", ""},
	}

	// ──────────────────────────────────────────────
	// CONTROL PLANE FLEETS (16)
	// ──────────────────────────────────────────────
	type cpFleetSeed struct {
		ID, Name, LOB string
		Notes         string
	}
	cpFleets := []cpFleetSeed{
		{"cp-mgmt", "Management API", "xCIB", "Central registry storing all fleet, node, route, and health state with PostgreSQL persistence"},
		{"cp-auth", "Auth Service", "xCIB", "Handles PKCE authorization, DPoP-bound token exchange, session management, and Envoy ext-authz validation"},
		{"cp-envoy-xds", "Envoy xDS", "xCIB", "Polls the registry and serves per-fleet route, cluster, and listener config to Envoy nodes via REST xDS v3"},
		{"cp-kong-sync", "Kong Sync", "xCIB", "Polls the registry and pushes declarative YAML config to each Kong node's admin API on change detection"},
		{"cp-shared-gw", "Shared Gateway", "xCIB", "Fallback Envoy and Kong instances serving routes for fleets that don't yet have dedicated nodes deployed"},
		{"cp-gtm", "Mock GTM", "xCIB", "Simulates Akamai GTM geo-routing by cycling datacenters and forwarding to the nearest CDN edge with TLS termination"},
		{"cp-edge", "Mock CDN/WAF", "xCIB", "Simulates Akamai Edge with WAF rule enforcement, bot detection, cache hit simulation, and request tagging"},
		{"cp-psaas", "Mock PSaaS", "xCIB", "Simulates the on-prem perimeter layer routing /api/* to Kong and all other traffic to Envoy with TLS re-origination"},
		{"cp-opa", "OPA", "xCIB", "Evaluates fine-grained authorization policies against user roles, actions, and resource paths for Kong API routes"},
		{"cp-watchdog", "Watchdog", "xCIB", "Continuously probes management-api health and tracks consecutive failures to report degraded or offline status"},
		{"cp-jaeger", "Jaeger", "xCIB", "Collects OpenTelemetry spans from all services and provides a query API for distributed trace visualization"},
		{"cp-dns", "CoreDNS", "xCIB", "Resolves all *.jpm.com hostnames to localhost so the browser can reach fleet routes without real DNS infrastructure"},
		{"cp-postgres", "PostgreSQL", "xCIB", "Stores all persistent state including fleets, nodes, routes, health reports, audit logs, and route-node assignments"},
		{"cp-svc-web", "svc-web", "xCIB", "Catch-all web backend that returns JSON metadata or serves the sample Research portal HTML for browser requests"},
		{"cp-svc-api", "svc-api", "xCIB", "Catch-all API backend that validates requests through OPA fine-grained authorization before returning JSON responses"},
		{"cp-console-svc", "Console Nginx", "xCIB", "Serves the React console SPA static assets and reverse-proxies /_proxy/* paths to management-api and Jaeger"},
	}

	// Insert data plane fleets
	for _, f := range dataFleets {
		kongPlugins, _ := json.Marshal(orEmpty(f.KongPlugins))
		authzScopes, _ := json.Marshal(orEmpty(f.DefaultAuthzScopes))
		db.MustExec(`INSERT INTO fleets (id, name, subdomain, lob, host_env, gateway_type, region, regions,
			auth_provider, instances_count, status,
			description, traffic_type, tls_termination, http2_enabled, connection_limit,
			timeout_connect_ms, timeout_request_ms, rate_limit_rps, kong_plugins,
			health_check_path, health_check_interval_s, authn_mechanism, default_authz_scopes,
			tls_required, waf_profile, resource_profile,
			autoscale_enabled, autoscale_min, autoscale_max, autoscale_cpu_threshold,
			notes, fleet_type,
			created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35)`,
			f.ID, f.Name, f.Subdomain, f.LOB, f.HostEnv, f.GatewayType, f.Region, string(regions),
			f.AuthProvider, f.InstancesCount, "not_deployed",
			f.Description, f.TrafficType, f.TLSTermination, f.HTTP2Enabled, f.ConnectionLimit,
			f.TimeoutConnectMs, f.TimeoutRequestMs, f.RateLimitRPS, string(kongPlugins),
			f.HealthCheckPath, f.HealthCheckIntervalS, f.AuthnMechanism, string(authzScopes),
			f.TLSRequired, f.WAFProfile, f.ResourceProfile,
			f.AutoscaleEnabled, f.AutoscaleMin, f.AutoscaleMax, f.AutoscaleCPUThreshold,
			f.Notes, f.FleetType,
			now, now)
	}

	// Insert control plane fleets
	cpRegions, _ := json.Marshal([]string{"us-east-2"})
	emptyPlugins, _ := json.Marshal([]string{})
	emptyScopes, _ := json.Marshal([]string{})
	for _, cp := range cpFleets {
		db.MustExec(`INSERT INTO fleets (id, name, subdomain, lob, host_env, gateway_type, region, regions,
			auth_provider, instances_count, status,
			description, traffic_type, tls_termination, http2_enabled, connection_limit,
			timeout_connect_ms, timeout_request_ms, rate_limit_rps, kong_plugins,
			health_check_path, health_check_interval_s, authn_mechanism, default_authz_scopes,
			tls_required, waf_profile, resource_profile,
			autoscale_enabled, autoscale_min, autoscale_max, autoscale_cpu_threshold,
			notes, fleet_type,
			created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35)`,
			cp.ID, cp.Name, cp.ID+".internal", cp.LOB, "psaas", "service", "us-east-2", string(cpRegions),
			"", 1, "healthy",
			cp.Notes, "internal", "none", false, 0,
			0, 0, 0, string(emptyPlugins),
			"/health", 0, "none", string(emptyScopes),
			"", "", "",
			false, 0, 0, 0,
			cp.Notes, "control",
			now, now)
	}

	// ──────────────────────────────────────────────
	// ROUTES (19 data plane routes)
	// ──────────────────────────────────────────────
	type routeSeed struct {
		Path, BackendURL, Audience, GatewayType, Team, Hostname string
		AllowedRoles, AuthzScopes                                []string
		AuthnMechanism, AuthIssuer, HealthPath                   string
		Status                                                   string // "active" or "inactive"
	}

	allRoutes := []routeSeed{
		// === ACTIVE ROUTES (fleets with deployed nodes) ===
		// Platform wildcard routes (shared gateway)
		{"/health", svcAPIURL, "", "kong", "platform", "*", nil, nil, "none", "", "/health", "active"},
		{"/api/public", svcAPIURL, "", "kong", "platform", "*", nil, nil, "none", "", "/health", "active"},
		// xCIB / Console
		{"/", "http://console:80", "", "envoy", "cib", "console.jpm.com", nil, nil, "none", "N/A", "/", "active"},
		// Markets / JPMM
		{"/research", svcWebURL, "jpmm", "envoy", "markets", "jpmm.jpm.com", nil, []string{"markets:read", "research:view"}, "bearer", "Janus", "/health", "active"},
		{"/research/api", svcAPIURL, "jpmm", "kong", "markets", "jpmm.jpm.com", nil, []string{"markets:read", "research:api"}, "bearer", "Janus", "/health", "active"},
		{"/sandt", svcWebURL, "jpmm", "envoy", "markets", "jpmm.jpm.com", []string{"trader"}, []string{"markets:read", "trading:view"}, "bearer", "Janus", "/health", "active"},
		{"/events", svcWebURL, "jpmm", "envoy", "markets", "jpmm.jpm.com", nil, []string{"markets:read", "events:view"}, "bearer", "Janus", "/health", "active"},
		{"/events/api", svcAPIURL, "jpmm", "kong", "markets", "jpmm.jpm.com", nil, []string{"markets:read", "events:api"}, "bearer", "Janus", "/health", "active"},
		// Payments / Access
		{"/", svcWebURL, "access", "envoy", "payments", "access.jpm.com", nil, []string{"payments:read", "access:view"}, "bearer", "AuthE1.0", "/health", "active"},
		// xCIB / AuthN
		{"/", "http://auth-service:8001", "", "envoy", "cib", "login.jpm.com", nil, nil, "none", "N/A", "/health", "active"},
		// xCIB / AuthZ
		{"/", svcWebURL, "authz", "envoy", "cib", "authz.jpm.com", nil, []string{"cib:admin", "authz:manage"}, "mtls", "Sentry", "/health", "active"},

		// === INACTIVE ROUTES (fleets without nodes — pre-configured, awaiting deployment) ===
		// Markets / Execute
		{"/", svcWebURL, "execute", "envoy", "markets", "execute.jpm.com", []string{"trader"}, []string{"markets:read", "execute:trade"}, "bearer", "Janus", "/health", "inactive"},
		{"/api", svcAPIURL, "execute", "kong", "markets", "execute.jpm.com", []string{"trader"}, []string{"markets:read", "markets:write", "execute:api"}, "bearer", "Janus", "/health", "inactive"},
		// Payments / Access Mobile
		{"/", svcWebURL, "access", "envoy", "payments", "access-mobile.jpm.com", nil, []string{"payments:read", "access:mobile"}, "bearer", "AuthE1.0", "/health", "inactive"},
		// Payments / Digital Banking
		{"/", svcWebURL, "digital-banking", "envoy", "payments", "digital-banking.jpm.com", nil, []string{"payments:read", "digital-banking:view"}, "bearer", "Sentry", "/health", "inactive"},
		// Payments / Merchant Services
		{"/", svcWebURL, "smb", "envoy", "payments", "smb.jpm.com", nil, []string{"payments:read", "merchant:view"}, "bearer", "Chase", "/health", "inactive"},
		// Payments / PDP
		{"/api", svcAPIURL, "developer", "kong", "payments", "developer.jpm.com", nil, []string{"payments:read", "developer:api"}, "api-key", "Sentry", "/health", "inactive"},
		// Global Banking / IQ
		{"/", svcWebURL, "iq", "envoy", "global-banking", "iq.jpm.com", nil, []string{"global-banking:read", "iq:view"}, "bearer", "Janus", "/health", "inactive"},
		// Security Services / SecSvcs
		{"/", svcWebURL, "secsvcs", "envoy", "security-services", "secsvcs.jpm.com", nil, []string{"security:read", "secsvcs:admin"}, "mtls", "Janus", "/health", "inactive"},
	}

	for _, r := range allRoutes {
		roles, _ := json.Marshal(orEmpty(r.AllowedRoles))
		scopes, _ := json.Marshal(orEmpty(r.AuthzScopes))
		methods, _ := json.Marshal([]string{"GET", "POST", "PUT", "DELETE"})
		db.MustExec(`INSERT INTO routes (id, path, hostname, backend_url, audience, allowed_roles, methods,
			status, team, created_by, gateway_type, health_path, authn_mechanism, auth_issuer, authz_scopes,
			tls_required, notes, target_nodes, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			uuid.New().String(), r.Path, r.Hostname, r.BackendURL, r.Audience,
			string(roles), string(methods), r.Status, r.Team, "system", r.GatewayType,
			r.HealthPath, r.AuthnMechanism, r.AuthIssuer, string(scopes), true,
			"Seed route ("+r.Hostname+")", "[]", now, now)
	}

	// ──────────────────────────────────────────────
	// FLEET INSTANCES (route instances for data plane fleets)
	// ──────────────────────────────────────────────
	type instSeed struct {
		ID, FleetID, ContextPath, Backend, GatewayType string
		LatencyP99                                     float64
	}
	instSeeds := []instSeed{
		// === ACTIVE (fleets with nodes) ===
		// JPMM — 2 envoy + 1 kong nodes
		{"i-jpmm-1", "fleet-jpmm", "/research", svcWebURL, "envoy", 18},
		{"i-jpmm-2", "fleet-jpmm", "/research/api", svcAPIURL, "kong", 12},
		{"i-jpmm-3", "fleet-jpmm", "/sandt", svcWebURL, "envoy", 22},
		{"i-jpmm-4", "fleet-jpmm", "/events", svcWebURL, "envoy", 14},
		{"i-jpmm-5", "fleet-jpmm", "/events/api", svcAPIURL, "kong", 10},
		// Access — 1 envoy node
		{"i-acc-1", "fleet-access", "/", svcWebURL, "envoy", 20},
		// AuthN — 1 envoy node
		{"i-authn-1", "fleet-authn", "/", "http://auth-service:8001", "envoy", 15},
		// AuthZ — 1 envoy node
		{"i-authz-1", "fleet-authz", "/", svcWebURL, "envoy", 18},
		// Console — 2 envoy nodes
		{"i-console-1", "fleet-console", "/", "http://console:80", "envoy", 5},
		// === INACTIVE (fleets without nodes — pre-configured) ===
		// Execute
		{"i-exec-1", "fleet-execute", "/", svcWebURL, "envoy", 0},
		{"i-exec-2", "fleet-execute", "/api", svcAPIURL, "kong", 0},
		// Access Mobile
		{"i-accm-1", "fleet-mobile", "/", svcWebURL, "envoy", 0},
		// Digital Banking
		{"i-db-1", "fleet-digital", "/", svcWebURL, "envoy", 0},
		// Merchant Services
		{"i-smb-1", "fleet-smb", "/", svcWebURL, "envoy", 0},
		// PDP
		{"i-pdp-1", "fleet-pdp", "/api", svcAPIURL, "kong", 0},
		// IQ
		{"i-iq-1", "fleet-iq", "/", svcWebURL, "envoy", 0},
		// SecSvcs
		{"i-sec-1", "fleet-secsvcs", "/", svcWebURL, "envoy", 0},
	}

	deployedFleets := map[string]bool{
		"fleet-jpmm": true, "fleet-access": true, "fleet-authn": true,
		"fleet-authz": true, "fleet-console": true,
	}
	for _, i := range instSeeds {
		// All instances start as inactive; ensureFleetContainers will
		// activate them after actual pods are deployed.
		instStatus := "inactive"
		_ = deployedFleets // used later by ensureFleetContainers
		// Look up the matching route_id by fleet subdomain + path
		var routeID string
		var fleetForInst Fleet
		if err := db.Get(&fleetForInst, "SELECT * FROM fleets WHERE id=$1", i.FleetID); err == nil {
			db.Get(&routeID, "SELECT id FROM routes WHERE hostname=$1 AND path=$2 AND gateway_type=$3 LIMIT 1",
				fleetForInst.Subdomain, i.ContextPath, i.GatewayType)
		}
		db.MustExec(`INSERT INTO fleet_instances (id, fleet_id, context_path, backend, gateway_type, status, latency_p99, route_id, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			i.ID, i.FleetID, i.ContextPath, i.Backend, i.GatewayType, instStatus, i.LatencyP99, routeID, now)
	}

	// ──────────────────────────────────────────────
	// DATA PLANE NODES (desired state — running or stopped)
	// ──────────────────────────────────────────────
	type fleetNodeSeed struct {
		NodeName, FleetID, GatewayType, Datacenter, Status string
		Port                                               int
	}
	fleetNodeSeeds := []fleetNodeSeed{
		// Active fleets (will be deployed by ensureFleetContainers on startup)
		{"fleet-jpmm-envoy-1", "fleet-jpmm", "envoy", "us-east-1", "pending", 0},
		{"fleet-jpmm-envoy-2", "fleet-jpmm", "envoy", "us-east-1", "pending", 0},
		{"fleet-jpmm-kong-1", "fleet-jpmm", "kong", "us-east-1", "pending", 0},
		{"fleet-access-envoy-1", "fleet-access", "envoy", "us-east-1", "pending", 0},
		{"fleet-authn-envoy-1", "fleet-authn", "envoy", "us-east-2", "pending", 0},
		{"fleet-authz-envoy-1", "fleet-authz", "envoy", "us-east-2", "pending", 0},
		{"fleet-console-envoy-1", "fleet-console", "envoy", "us-east-2", "pending", 0},
		{"fleet-console-envoy-2", "fleet-console", "envoy", "us-east-2", "pending", 0},
		// Inactive fleets (no Docker containers — config only, awaiting deployment)
		{"fleet-execute-envoy-1", "fleet-execute", "envoy", "us-east-1", "stopped", 0},
		{"fleet-execute-kong-1", "fleet-execute", "kong", "us-east-1", "stopped", 0},
		{"fleet-mobile-envoy-1", "fleet-mobile", "envoy", "us-east-1", "stopped", 0},
		{"fleet-digital-envoy-1", "fleet-digital", "envoy", "us-east-1", "stopped", 0},
		{"fleet-smb-envoy-1", "fleet-smb", "envoy", "us-east-1", "stopped", 0},
		{"fleet-pdp-kong-1", "fleet-pdp", "kong", "us-east-1", "stopped", 0},
		{"fleet-iq-envoy-1", "fleet-iq", "envoy", "us-east-1", "stopped", 0},
		{"fleet-secsvcs-envoy-1", "fleet-secsvcs", "envoy", "us-east-1", "stopped", 0},
	}
	for _, n := range fleetNodeSeeds {
		db.MustExec(`INSERT INTO fleet_nodes (id, fleet_id, node_name, gateway_type, datacenter, status, port, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			uuid.New().String(), n.FleetID, n.NodeName, n.GatewayType, n.Datacenter, n.Status, n.Port, now)
	}

	// ──────────────────────────────────────────────
	// CONTROL PLANE NODES (17 virtual nodes — docker-compose managed)
	// ──────────────────────────────────────────────
	type cpNodeSeed struct {
		FleetID, ContainerName, GatewayType, Datacenter string
		Port                                            int
		DockerService                                   string
	}
	cpNodeSeeds := []cpNodeSeed{
		{"cp-mgmt", "cp-mgmt-1", "service", "us-east-2", 8003, "management-api"},
		{"cp-auth", "cp-auth-1", "service", "us-east-2", 8001, "auth-service"},
		{"cp-envoy-xds", "cp-envoy-xds-1", "service", "us-east-2", 8080, "envoy-control-plane"},
		{"cp-kong-sync", "cp-kong-sync-1", "service", "us-east-2", 8102, "kong-admin-proxy"},
		{"cp-shared-gw", "cp-shared-envoy-1", "envoy", "us-east-2", 8000, "gateway-envoy"},
		{"cp-shared-gw", "cp-shared-kong-1", "kong", "us-east-2", 8100, "gateway-kong"},
		{"cp-gtm", "cp-gtm-1", "service", "us-east-2", 8010, "mock-akamai-gtm"},
		{"cp-edge", "cp-edge-1", "service", "us-east-2", 8011, "mock-akamai-edge"},
		{"cp-psaas", "cp-psaas-1", "service", "us-east-2", 8012, "mock-psaas"},
		{"cp-opa", "cp-opa-1", "service", "us-east-2", 8181, "opa"},
		{"cp-watchdog", "cp-watchdog-1", "service", "us-east-2", 8006, "watchdog"},
		{"cp-jaeger", "cp-jaeger-1", "service", "us-east-2", 16686, "jaeger"},
		{"cp-dns", "cp-dns-1", "service", "us-east-2", 5553, "dns"},
		{"cp-postgres", "cp-postgres-1", "service", "us-east-2", 5432, "postgres"},
		{"cp-svc-web", "cp-svc-web-1", "service", "us-east-2", 8004, "svc-web"},
		{"cp-svc-api", "cp-svc-api-1", "service", "us-east-2", 8005, "svc-api"},
		{"cp-console-svc", "cp-console-svc-1", "service", "us-east-2", 80, "console"},
	}

	for _, n := range cpNodeSeeds {
		db.MustExec(`INSERT INTO cp_nodes (id, fleet_id, container_name, gateway_type, datacenter, status, port, docker_service, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			uuid.New().String(), n.FleetID, n.ContainerName, n.GatewayType, n.Datacenter, "active", n.Port, n.DockerService, now)
	}

	log.Printf("Seeded %d routes, %d data-plane fleets, %d control-plane fleets, %d fleet instances, %d CP nodes",
		len(allRoutes), len(dataFleets), len(cpFleets), len(instSeeds), len(cpNodeSeeds))

	// Route-node assignments are populated by ensureFleetContainers after Docker containers are created.
}

// fleetNodeSpec describes the desired node types for a fleet with mixed gateway types.
type fleetNodeSpec struct {
	GatewayType string
	Count       int
}

// getFleetNodeSpecs returns the desired node specifications for a fleet.
func getFleetNodeSpecs(f Fleet) []fleetNodeSpec {
	desired := int(f.InstancesCount)
	if desired <= 0 {
		desired = 1
	}

	if f.GatewayType == "mixed" || f.GatewayType == "" {
		// For mixed fleets: majority goes to envoy, at least 1 kong
		kongCount := 1
		envoyCount := desired - kongCount
		if envoyCount < 1 {
			envoyCount = 1
		}
		return []fleetNodeSpec{
			{"envoy", envoyCount},
			{"kong", kongCount},
		}
	}

	return []fleetNodeSpec{{f.GatewayType, desired}}
}

// ensureFleetContainers checks all fleets in the DB and re-creates any
// missing containers. Called on every startup so a full restart restores
// the fleet infrastructure. Supports mixed-type fleets (both envoy and kong).
func ensureFleetContainers(db *sqlx.DB) {
	// Wait for orchestrator to be ready
	time.Sleep(3 * time.Second)

	var fleets []Fleet
	if err := db.Select(&fleets, "SELECT * FROM fleets"); err != nil {
		log.Printf("ensureFleetContainers: failed to query fleets: %v", err)
		return
	}

	// Only deploy containers for fleets that should have them:
	// - On first run: core demo + xCIB fleets listed below
	// - On subsequent runs: any fleet with running nodes (meaning it was deployed before)
	//
	// To add a fleet to the auto-start list, add its ID here.
	// Fleet IDs: fleet-jpmm (JPMM), fleet-access (JPMA), fleet-digital (JPMDB),
	//            fleet-authn / fleet-authz / fleet-console (all xCIB)
	autoDeployFleets := map[string]bool{
		// Markets
		"fleet-jpmm":    true, // JPMM
		// Payments
		"fleet-access":   true, // JPMA
		"fleet-digital":  true, // JPMDB
		// xCIB
		"fleet-authn":   true,
		"fleet-authz":   true,
		"fleet-console": true,
	}

	for _, f := range fleets {
		// Control-plane fleets are docker-compose managed, not dynamically created
		if f.FleetType == "control" {
			continue
		}

		specs := getFleetNodeSpecs(f)
		totalDesired := 0
		for _, s := range specs {
			totalDesired += s.Count
		}

		// Skip fleets that shouldn't have containers
		shouldDeploy := autoDeployFleets[f.ID]
		if !shouldDeploy {
			existing, _ := orch.ListFleetNodes(f.ID)
			if len(existing) > 0 {
				shouldDeploy = true
			}
		}
		if !shouldDeploy {
			log.Printf("Fleet %s (%s): no containers needed (deploy via UI to spin up)", f.Name, f.GatewayType)
			continue
		}

		existing, err := orch.ListFleetNodes(f.ID)
		if err != nil {
			log.Printf("ensureFleetContainers: error listing containers for %s: %v", f.Name, err)
			continue
		}

		// In K8s mode, verify the Fleet CRD actually exists in the cluster.
		// Git manifests may be stale from a previous deployment cycle.
		if k8sOrch, ok := orch.(*K8sOrchestrator); ok && k8sOrch.dynClient != nil {
			_, getErr := k8sOrch.dynClient.Resource(fleetGVR).Namespace("ingress-dp").Get(
				context.Background(), f.ID, metav1.GetOptions{})
			if getErr != nil {
				// Fleet CRD not in cluster — treat as no running nodes
				existing = nil
			}
		}

		// Count running containers by type
		runningByType := map[string]int{}
		for _, n := range existing {
			if n.Status == "running" {
				runningByType[n.GatewayType]++
			}
		}

		totalRunning := 0
		for _, c := range runningByType {
			totalRunning += c
		}

		if totalRunning >= totalDesired {
			// Check per-type counts match
			allGood := true
			for _, s := range specs {
				if runningByType[s.GatewayType] < s.Count {
					allGood = false
					break
				}
			}
			if allGood {
				log.Printf("Fleet %s (%s): %d/%d containers running — OK", f.Name, f.GatewayType, totalRunning, totalDesired)
				continue
			}
		}

		// Remove any stopped containers first
		for _, n := range existing {
			if n.Status != "running" {
				removeSingleContainer(n.ContainerID)
			}
		}

		// Re-create all containers (clean slate)
		log.Printf("Fleet %s (%s): only %d/%d running — recreating", f.Name, f.GatewayType, totalRunning, totalDesired)
		orch.RemoveFleetNodes(f.ID)

		allNodes := []FleetNode{}
		for _, spec := range specs {
			nodes, err := orch.CreateFleetNodes(f.ID, spec.GatewayType, spec.Count)
			if err != nil {
				log.Printf("ensureFleetContainers: failed to create %s containers for %s: %v", spec.GatewayType, f.Name, err)
			} else {
				allNodes = append(allNodes, nodes...)
				log.Printf("Fleet %s: deployed %d %s containers", f.Name, len(nodes), spec.GatewayType)
			}
		}
		log.Printf("Fleet %s: total %d containers deployed", f.Name, len(allNodes))
	}

	// After all fleet containers are up, seed route-node assignments
	seedRouteNodeAssignments(db)
}

// removeSingleContainer stops and removes one container by ID (best effort).
func removeSingleContainer(containerID string) {
	stopResp, err := dockerRequest("POST", "/v1.43/containers/"+containerID+"/stop?t=2", nil)
	if err == nil {
		stopResp.Body.Close()
	}
	rmResp, err := dockerRequest("DELETE", "/v1.43/containers/"+containerID+"?force=true", nil)
	if err == nil {
		rmResp.Body.Close()
	}
}

// seedRouteNodeAssignments creates route_node_assignments after containers
// are up. For each route in a fleet, it finds running nodes of the matching
// gateway_type and creates assignments. This is called after ensureFleetContainers.
func seedRouteNodeAssignments(db *sqlx.DB) {
	// Clean up stale assignments whose container IDs no longer exist.
	// After a full Docker restart, containers get new IDs, so old assignments
	// become orphaned and prevent routes from showing on the new nodes.
	var allAssignments []struct {
		ID              string `db:"id"`
		NodeContainerID string `db:"node_container_id"`
		FleetID         string `db:"fleet_id"`
	}
	if err := db.Select(&allAssignments, "SELECT id, node_container_id, fleet_id FROM route_node_assignments"); err == nil && len(allAssignments) > 0 {
		// Build set of live container IDs across all fleets
		liveContainerIDs := map[string]bool{}
		fleetChecked := map[string]bool{}
		for _, a := range allAssignments {
			if fleetChecked[a.FleetID] {
				continue
			}
			fleetChecked[a.FleetID] = true
			nodes, _ := orch.ListFleetNodes(a.FleetID)
			for _, n := range nodes {
				liveContainerIDs[n.ContainerID] = true
			}
		}

		stale := 0
		for _, a := range allAssignments {
			if !liveContainerIDs[a.NodeContainerID] {
				db.Exec("DELETE FROM route_node_assignments WHERE id=$1", a.ID)
				stale++
			}
		}
		if stale > 0 {
			log.Printf("Cleaned up %d stale route-node assignments (containers no longer exist)", stale)
		}
	}

	var count int
	db.Get(&count, "SELECT COUNT(*) FROM route_node_assignments")
	if count > 0 {
		log.Printf("Route-node assignments already seeded (%d entries)", count)
		return
	}

	var routes []Route
	if err := db.Select(&routes, "SELECT * FROM routes"); err != nil {
		log.Printf("seedRouteNodeAssignments: failed to query routes: %v", err)
		return
	}

	var fleets []Fleet
	if err := db.Select(&fleets, "SELECT * FROM fleets WHERE fleet_type='data'"); err != nil {
		log.Printf("seedRouteNodeAssignments: failed to query fleets: %v", err)
		return
	}

	// Build hostname -> fleet lookup
	fleetBySubdomain := map[string]Fleet{}
	for _, f := range fleets {
		fleetBySubdomain[f.Subdomain] = f
	}

	now := float64(time.Now().Unix())
	assigned := 0

	for _, route := range routes {
		fleet, ok := fleetBySubdomain[route.Hostname]
		if !ok {
			continue // route not associated with a known fleet (e.g. wildcard routes)
		}

		// First try live Docker containers
		liveNodes, _ := orch.ListFleetNodes(fleet.ID)
		liveAssigned := false
		for _, node := range liveNodes {
			if node.Status == "running" && node.GatewayType == route.GatewayType {
				db.MustExec(`INSERT INTO route_node_assignments (id, route_id, node_container_id, fleet_id, status, created_at)
					VALUES ($1, $2, $3, $4, 'active', $5)`,
					uuid.New().String(), route.ID, node.ContainerID, fleet.ID, now)
				assigned++
				liveAssigned = true
			}
		}

		// If no live nodes, assign to DB node records (stopped nodes)
		if !liveAssigned {
			var dbNodes []FleetNodeRecord
			db.Select(&dbNodes, "SELECT * FROM fleet_nodes WHERE fleet_id=$1 AND gateway_type=$2", fleet.ID, route.GatewayType)
			for _, dn := range dbNodes {
				// Use node name as the identifier since there's no container ID
				db.MustExec(`INSERT INTO route_node_assignments (id, route_id, node_container_id, fleet_id, status, created_at)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					uuid.New().String(), route.ID, dn.NodeName, fleet.ID, "inactive", now)
				assigned++
			}
		}
	}

	log.Printf("Seeded %d route-node assignments", assigned)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
