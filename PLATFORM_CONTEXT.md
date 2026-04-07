CIB INGRESS PLATFORM — CONTEXT DOCUMENT
For use by AI agents drafting the Session Manager RFC
Generated: 2026-04-04

================================================================================
1. PLATFORM OVERVIEW
================================================================================

Name: CIB Ingress Platform (JPMC internal, sometimes abbreviated "CIB Ingress")
Repository: github.com/jpmc/ingress-poc
Current state: Proof-of-concept / early development. Not production-deployed.
Management API base URL (local dev): http://localhost:8003

Purpose:
The CIB Ingress Platform is a multi-tenant API and web ingress control plane for
JPMorgan Chase's Corporate & Investment Bank. It manages gateway fleets across
regions, handling TLS termination, authentication enforcement, route configuration,
and traffic management for multiple lines of business (Markets, Payments, Global
Banking, Security Services, xCIB).

Technology stack:
  - Go (management-api, mcp-server, auth-service, envoy-control-plane)
  - Kubernetes (kind for local dev; target: one cluster per region in production)
  - Envoy as the primary gateway for web/streaming traffic (xDS-driven config)
  - Kong as the gateway for API/developer-portal traffic (KongIngress resources)
  - PostgreSQL for persistent state (fleets, nodes, routes, audit logs, health)
  - GitHub GitOps: per-fleet GitHub repositories as the authoritative config store
  - ArgoCD (target production path): fans per-fleet repos out to regional clusters
  - OpenTelemetry + Jaeger for distributed tracing
  - React-based management console (SPA)
  - MCP server for AI agent integration


================================================================================
2. CORE CONCEPTS
================================================================================

FLEET
-----
A fleet is the primary tenant boundary on the platform. Each fleet:
  - Owns a dedicated subdomain (e.g. jpmm.jpm.com, access.jpm.com)
  - Belongs to a line of business (LOB) such as Markets or Payments
  - Specifies a gateway type: "envoy", "kong", or "mixed"
  - Has an auth provider (e.g. Janus, Sentry, AuthE1.0, Chase, N/A)
  - Defines TLS, WAF profile, rate limits, and autoscale settings
  - Is replicated across a set of regions (e.g. ["us-east-1", "us-east-2"])
  - In the target architecture, maps to a Kubernetes namespace replicated across
    regional clusters

Fleet types (fleet_type field):
  - "data": a live data-plane tenant fleet serving external traffic
  - "control": a control-plane service component (management-api, postgres, etc.)

NODE (FleetNode)
-----------------
A node is a single gateway pod within a fleet. In local dev (Docker mode), nodes
were Docker containers; in the current Kubernetes mode, nodes are Pods in the
ingress-dp namespace. Key properties:
  - Each fleet has one or more nodes for AZ resiliency
  - Nodes are spread across availability zones via topologySpreadConstraints
  - Each node runs a gateway container (Envoy or Kong) plus an auth sidecar
  - In local dev, nodes were named fleet-{name}-{gateway}-{n} (e.g.
    fleet-jpmm-envoy-1, fleet-jpmm-kong-1)
  - Node status reflects actual pod state: running, stopped, drifted

ROUTE
------
A route maps a hostname + URL path prefix to a backend service. Routes are the
unit of traffic management on the platform. Each route:
  - Targets a specific fleet (via fleet_instances table)
  - Has an authn_mechanism (bearer, none, mtls) enforced at the gateway
  - Can carry an audience claim requirement for JWT validation
  - Specifies allowed HTTP methods, TLS requirement, health check path
  - Has a sync_status reflecting alignment between DB, Git, and gateway

Route types:
  - Static route: proxies to an always-running backend_url
  - Lambda route: backend is a dynamically-deployed function pod created per-route

LAMBDA ROUTE
-------------
A lambda route is a special route where the backend is a JavaScript function
deployed as a dedicated Kubernetes Deployment + ClusterIP Service. The function
source code (JavaScript) is stored in the route record's function_code field. The
platform creates and manages the pod lifecycle. Lambda pods run in the ingress-cp
namespace alongside the management-api in the current POC architecture.

The MCP tool deploy_fleet (with function_code parameter) is the correct way to
create lambda routes. The create_route tool is for static backends only and does
NOT create K8s resources for lambda pods.


================================================================================
3. DATA MODELS
================================================================================

FLEET STRUCT
-------------
Field                   Type        Description
id                      string      Primary key. For seeded fleets uses slug format
                                    (e.g. "fleet-jpmm", "fleet-execute"). New fleets
                                    get UUIDs. Also the K8s Deployment name until
                                    k8s_name was introduced.
name                    string      Human-readable name (e.g. "JPMM", "Execute")
subdomain               string      Full hostname this fleet owns (e.g. "jpmm.jpm.com")
lob                     string      Line of business (e.g. "Markets", "Payments",
                                    "Global Banking", "Security Services", "xCIB")
host_env                string      Hosting environment: "psaas", "aws", "on-prem"
gateway_type            string      "envoy", "kong", or "mixed"
region                  string      Primary region (e.g. "us-east")
regions                 []string    All target regions (e.g. ["us-east-1","us-east-2"])
auth_provider           string      Identity provider (e.g. "Janus", "Sentry",
                                    "AuthE1.0", "Chase", "N/A")
instances_count         float64     Desired node count
status                  string      "healthy", "not_deployed", "degraded", "suspended"
description             string      Human-readable description
traffic_type            string      "web", "api", "mixed", "internal"
tls_termination         string      "edge" (TLS terminated at gateway) or "none"
http2_enabled           bool        Whether HTTP/2 is enabled
connection_limit        int         Max concurrent connections
timeout_connect_ms      int         Backend connect timeout in milliseconds
timeout_request_ms      int         Request timeout in milliseconds
rate_limit_rps          int         Fleet-level rate limit (requests per second, 0=unlimited)
kong_plugins            []string    Kong plugin list (e.g. ["rate-limiting","cors","jwt"])
health_check_path       string      Path used for health probing (e.g. "/health")
health_check_interval_s int         Health probe interval in seconds
authn_mechanism         string      Default auth for fleet routes: "bearer","none","mtls","api-key"
default_authz_scopes    []string    Required OAuth scopes for fleet routes
tls_required            string      "required" or ""
waf_profile             string      "standard", "strict", or ""
resource_profile        string      "small", "medium", "large"
autoscale_enabled       bool        Whether HPA autoscaling is active
autoscale_min           int         Minimum node count for autoscaling
autoscale_max           int         Maximum node count for autoscaling
autoscale_cpu_threshold int         CPU % threshold for scale-out
notes                   string      Free-text notes
fleet_type              string      "data" or "control"
k8s_name                string      Kubernetes-safe name slug (introduced to decouple
                                    K8s resource names from DB UUIDs). E.g. "fleet-jpmm"
git_manifest_path       string      URL of the fleet's GitHub repo (e.g.
                                    "https://github.com/gavinhamilton1/fleet-test")
                                    or local path for single-repo mode
sync_status             string      GitOps sync state: "synced","pending","unknown","drifted"
created_at              int64       Unix timestamp
updated_at              int64       Unix timestamp

ROUTE STRUCT
-------------
Field                   Type        Description
id                      string      UUID primary key
path                    string      URL path prefix (e.g. "/research", "/api/v1/orders")
hostname                string      Target hostname (e.g. "jpmm.jpm.com") or "*" for wildcard
backend_url             string      Upstream service URL (e.g. "http://svc-web:8004")
audience                string      Required JWT audience claim (e.g. "jpmm")
allowed_roles           []string    Role-based access list (e.g. ["trader"])
methods                 []string    Allowed HTTP methods (e.g. ["GET","POST","PUT","DELETE"])
status                  string      "active" or "inactive"
team                    string      Owning team (e.g. "markets", "payments", "cib", "platform")
created_by              string      Actor who created the route (e.g. "system", user ID)
gateway_type            string      "envoy" or "kong"
health_path             string      Health check path for this route's backend
authn_mechanism         string      "bearer" (JWT), "none" (public), "mtls", "api-key"
auth_issuer             string      Identity of the JWT issuer (e.g. "Janus", "Sentry",
                                    "AuthE1.0", "N/A")
authz_scopes            []string    Required OAuth scopes (e.g. ["markets:read","research:view"])
tls_required            bool        Whether TLS is required for this route
notes                   string      Human-readable description
target_nodes            []string    Node container IDs this route is assigned to
function_code           string      JavaScript source for lambda routes (empty for static)
function_language       string      "javascript" (currently only supported language)
lambda_container_id     string      Container/pod ID of the deployed lambda
lambda_port             int         Port the lambda pod listens on
sync_status             string      "synced", "pending", "git_deleted", "unknown", "drifted"
created_at              int64       Unix timestamp
updated_at              int64       Unix timestamp

FLEETNODE STRUCT
-----------------
Field           Type    Description
id              string  UUID primary key
fleet_id        string  Foreign key to fleets.id
node_name       string  Node name (e.g. "fleet-jpmm-envoy-1")
container_id    string  K8s pod name or Docker container ID
gateway_type    string  "envoy" or "kong"
datacenter      string  AZ/datacenter label (e.g. "us-east-1")
region          string  Region label (e.g. "us-east-1")
status          string  "running", "stopped", "drifted"
port            int     Port the gateway listens on (0 in K8s mode; service-based)
index           int     Node index within fleet (0-based)
created_at      int64   Unix timestamp


================================================================================
4. ARCHITECTURE
================================================================================

4a. CURRENT (POC) ARCHITECTURE
--------------------------------
The POC runs in a single kind Kubernetes cluster on the developer's machine.

Namespaces:
  - ingress-cp: control plane — management-api, postgres, envoy-control-plane,
    kong-sync, auth-service, console, jaeger, coredns, and lambda pods
  - ingress-dp: data plane — Envoy and Kong gateway Deployments (one per fleet)

Key components:
  - management-api (port 8003): central registry and orchestration service.
    Stores all fleet, node, route, and health state in PostgreSQL. Exposes
    REST API for console, MCP server, and gateways. Writes GitOps manifests.
    Runs two background reconcilers (K8s pod reconciler and GitOps reconciler).

  - envoy-control-plane (port 8080): REST xDS v3 control plane. Envoy gateway
    pods poll this service for route, cluster, and listener configuration.
    The management-api pushes updates on route changes.

  - kong-sync: Polls management-api for route changes and pushes declarative
    YAML configuration to each Kong node's Admin API.

  - auth-service (port 8001): Handles PKCE authorization, DPoP-bound token
    exchange, session management, and Envoy ext-authz validation. Sits behind
    the login.jpm.com fleet.

  - PostgreSQL: Stores all persistent state.

  - GitOps: management-api writes Fleet and Route CRD YAML to per-fleet GitHub
    repos (e.g. github.com/gavinhamilton1/fleet-test) or a local git repo.
    The gitops_reconciler.go runs every 10 seconds syncing Git back to DB.

  - K8s Reconciler (reconciler.go): Runs every 10 seconds. Lists pods in
    ingress-dp, maps pod names back to fleet UUIDs via k8s_name, and updates
    fleet/node status in the DB. Respects user-set "suspended" status.

  - Orchestrator: The management-api uses an Orchestrator interface to abstract
    infrastructure operations. In K8s mode (K8sOrchestrator), it writes CRD
    manifests to Git AND applies them directly to the cluster. In docker mode
    (legacy), it called the Docker daemon. The ORCHESTRATION_MODE env var
    selects the mode (default: "docker").

Fleet Kubernetes resources (managed by the ingress-operator CRD):
  - Fleet CRD (ingress.jpmc.com/v1alpha1, Kind: Fleet) in ingress-dp namespace
  - Route CRD (ingress.jpmc.com/v1alpha1, Kind: Route) in ingress-dp namespace
  - Deployment per fleet in ingress-dp (named by fleet's k8s_name slug)
  - ClusterIP Service per fleet in ingress-dp
  - Lambda Deployments and ClusterIP Services in ingress-cp

Configuration via environment variables:
  - PORT: management-api listen port (default: 8003)
  - ENVOY_CONTROL_PLANE_URL: URL for envoy xDS (default: http://envoy-control-plane:8080)
  - KONG_ADMIN_URL: Kong Admin API URL
  - ORCHESTRATION_MODE: "k8s" or "docker"
  - DP_KUBECONFIG / DP_CLUSTER_CONTEXT: cross-cluster K8s access
  - MANAGEMENT_API_KEY: optional bearer token for API auth
  - GITHUB_TOKEN / GITHUB_USERNAME: for per-fleet repo creation

4b. TARGET (PRODUCTION) ARCHITECTURE
--------------------------------------
This section describes the intended production design, not yet implemented.

  - One Kubernetes cluster per region: us-east-1, us-east-2, eu-west-1, etc.
  - Fleet = Kubernetes namespace, replicated across the fleet's regions[] list
  - Node = Pod (minimum 2 per cluster, spread across AZs via
    topologySpreadConstraints)
  - Each Pod runs: gateway container (Envoy or Kong) + auth sidecar
  - ArgoCD ApplicationSet fans per-fleet GitHub repos out to regional clusters
  - Management API writes exclusively to Git; ArgoCD applies to K8s clusters
  - xDS control plane deployed per region
  - Cross-region traffic routing handled at DNS/load balancer layer (above the
    platform — GTM/Akamai routes to the nearest live region)
  - Control plane components (management-api, postgres) run in their own
    dedicated cluster or namespace, not co-located with data plane fleets


================================================================================
5. API SURFACE
================================================================================

The management-api HTTP router (chi v5) exposes the following endpoints at
http://localhost:8003 (local dev):

ROUTE ENDPOINTS
  GET    /routes                         List routes (filters: status, gateway_type,
                                         node_id, fleet_id, unassigned, hostname)
  GET    /routes/{id}                    Get single route
  POST   /routes                         Create route (DB only, no K8s resources)
  PUT    /routes/{id}                    Update route
  PUT    /routes/{id}/status             Update route status field only
  DELETE /routes/{id}                    Delete route (removes Git YAML, marks inactive)
  GET    /routes/{id}/nodes              Get node assignments for a route
  POST   /routes/{id}/reconcile          Trigger reconcile for a specific route

FLEET ENDPOINTS
  GET    /fleets                         List all fleets
  GET    /fleets/{fleet_id}              Get fleet with nodes and instances
  POST   /fleets                         Create fleet
  PUT    /fleets/{fleet_id}              Update fleet
  DELETE /fleets/{fleet_id}              Delete fleet (removes K8s resources + Git)
  POST   /fleets/{fleet_id}/deploy       Deploy nodes OR add a route/lambda to fleet
  GET    /fleets/{fleet_id}/nodes        Get nodes for a fleet
  POST   /fleets/{fleet_id}/scale        Scale fleet to N nodes
  DELETE /fleets/{fleet_id}/instances/{instance_id}  Remove a fleet instance
  POST   /fleets/{fleet_id}/suspend      Suspend fleet (scale to 0, preserve config)
  POST   /fleets/{fleet_id}/resume       Resume suspended fleet
  POST   /fleets/{fleet_id}/nodes/{container_id}/stop    Stop individual node
  POST   /fleets/{fleet_id}/nodes/{container_id}/start   Start individual node
  DELETE /fleets/{fleet_id}/nodes/{container_id}         Delete a node
  POST   /fleets/{fleet_id}/nodes/deploy                 Deploy a single new node
  GET    /fleets/{fleet_id}/nodes/{container_id}/routes  Get routes for a specific node

GITOPS ENDPOINTS
  GET    /gitops/status                  GitOps sync state overview
  GET    /gitops/commits                 Recent Git commits
  GET    /gitops/repos                   List per-fleet Git repos
  POST   /gitops/sync                    Push DB state to Git
  POST   /fleets/{fleet_id}/gitops/sync  Rebuild fleet manifest from DB to Git
  GET    /gitops/diff/{fleet_id}         Show diff between DB and Git state
  POST   /gitops/reconcile              Trigger manual Git-to-DB reconcile
  GET    /gitops/reconcile/status        Last reconcile result
  POST   /gitops/migrate-route-names     Rename UUID route files to path-based names

AUDIT, DRIFT, POLICY
  GET    /audit-log                      Full audit log of all create/update/delete ops
  GET    /actuals                        Current actual state from gateways
  GET    /drift                          Drift report: desired vs actual config
  GET    /policy/validate                Validate a proposed route against CIB policy

HEALTH AND DIAGNOSTICS
  GET    /health                         Service health check (no auth required)
  GET    /health-reports                 List recent health reports from gateways
  POST   /health-reports                 Receive a health report from a gateway node

LAMBDA
  GET    /lambdas                        List all deployed lambda functions


================================================================================
6. GITOPS MODEL
================================================================================

The platform uses a Git-first GitOps model. Git is the authoritative source of
truth for route configuration and fleet topology.

PER-FLEET GITHUB REPOSITORIES
  Each data-plane fleet gets its own GitHub repository named after the fleet.
  Example: github.com/gavinhamilton1/fleet-test
  The fleet's git_manifest_path field stores the full GitHub URL.
  Repos are auto-created by the management-api via the GitHub API when a fleet
  is deployed (requires GITHUB_TOKEN and GITHUB_USERNAME env vars).

REPOSITORY STRUCTURE
  fleets/
    {fleet-id}.yaml    -- Fleet CRD manifest describing topology and nodes
  routes/
    {path-based-name}.yaml  -- One Route CRD YAML per route
                              (e.g. "api-users-profile.yaml" for /api/users/profile)

FLEET CRD YAML FORMAT (example)
  apiVersion: ingress.jpmc.com/v1alpha1
  kind: Fleet
  metadata:
    name: fleet-test
    namespace: ingress-dp
  spec:
    subdomain: test.jpm.com
    gatewayType: envoy
    replicas: 2
    nodes:
      - name: fleet-test-envoy-1
        gatewayType: envoy
        datacenter: us-east-1
        status: running
      - name: fleet-test-envoy-2
        gatewayType: envoy
        datacenter: us-east-2
        status: running

ROUTE CRD YAML FORMAT (example)
  apiVersion: ingress.jpmc.com/v1alpha1
  kind: Route
  metadata:
    name: {route-uuid}
    namespace: ingress-dp
  spec:
    path: /api/v1/orders
    hostname: orders.jpm.com
    backendUrl: http://orders-svc:8080
    gatewayType: envoy
    audience: orders
    team: payments
    authnMechanism: bearer
    authIssuer: Janus
    tlsRequired: true
    healthPath: /health
    notes: "Orders API v1"
    methods:
      - GET
      - POST
    # For lambda routes, functionCode is a YAML literal block scalar:
    functionCode: |
      module.exports = async (req, res) => {
        res.json({ message: 'hello from lambda' })
      }
    functionLanguage: javascript

ROUTE FILENAME CONVENTION
  Route YAML files use path-based names derived from the route path.
  Example: /api/v1/orders → "api-v1-orders.yaml"
  Legacy files used UUID-based names. The /gitops/migrate-route-names endpoint
  performs a one-time migration to path-based names.

GITOPS RECONCILER (gitops_reconciler.go)
  - Runs every 10 seconds as a background goroutine (startGitOpsReconciler)
  - Startup delay of 90 seconds to allow fleet repos to be cloned first
  - For each fleet with a GitHub-backed repo (git_manifest_path LIKE 'https://%'):
    1. Pull latest from remote (best-effort)
    2. Parse Fleet CRD YAML → reconcile fleet_nodes table (add missing nodes,
       update drifted fields, flag DB-only nodes as "drifted")
    3. Parse each Route CRD YAML in routes/ → reconcile routes and
       fleet_instances tables (add missing routes, correct drifted field values)
    4. Routes present in DB (active) but absent from Git → set to inactive with
       sync_status="git_deleted" and log an audit event

MANAGEMENT API WRITE OPERATIONS
  On route creation: writes Route CRD YAML to git repo and commits
  On route deletion: deletes YAML from git repo and commits
  On fleet deploy: writes Fleet CRD YAML to git repo and commits; also applies
    CRD directly to cluster via dynamic K8s client for immediate effect
  Git is committed before or alongside DB writes; if Git commit fails, the
  operation may partially fail

SYNC STATUS VALUES
  "synced"     -- Git and DB are in agreement
  "pending"    -- Change in DB not yet pushed to Git
  "git_deleted"-- YAML was removed from Git; DB route set inactive
  "drifted"    -- DB and Git disagree; git state will be applied
  "unknown"    -- Sync status not yet determined (new records)


================================================================================
7. AUTH MODEL (CURRENT)
================================================================================

FLEET-LEVEL AUTH
  Each fleet has an auth_provider field naming the identity provider used for
  that tenant. Common values: "Janus" (Markets/IQ), "Sentry" (PDP/Digital/AuthZ),
  "AuthE1.0" (Access), "Chase" (Merchant Services), "N/A" (AuthN, Console).

  The auth_provider is informational metadata on the fleet record. The actual
  enforcement happens at the gateway via the auth sidecar.

ROUTE-LEVEL AUTH (PER-REQUEST)
  Each route has:
    authn_mechanism: one of "bearer", "none", "mtls", "api-key"
      - bearer: validates a JWT on every request. The gateway (or auth sidecar)
        checks signature, expiry, audience, and issuer.
      - none: no authentication check. Route is public.
      - mtls: mutual TLS client certificate required.
      - api-key: API key header required (Kong routes).
    auth_issuer: the expected JWT issuer (e.g. "Janus", "Sentry", "AuthE1.0")
    audience: the expected "aud" claim in the JWT
    authz_scopes: required OAuth scopes (e.g. ["markets:read", "research:view"])
    tls_required: whether the connection must be TLS

AUTH SERVICE (auth-service)
  A separate Go service serving the login.jpm.com fleet (fleet-authn).
  Implements PKCE authorization code flow, DPoP-bound token exchange, and
  session management. Also exposes an Envoy-native HTTP ext-authz endpoint
  for per-request JWT validation from Envoy sidecars.

CURRENT GAPS IN AUTH (relevant to Session Manager RFC)
  - Auth is entirely per-request. Each request must carry a valid JWT.
  - No platform-level session concept: no session tokens, no session store,
    no session binding to a fleet or node.
  - No sticky routing: there is no mechanism to route a user's subsequent
    requests to the same node for session affinity.
  - No platform-level logout or token invalidation. Invalidation must happen
    at the identity provider level; the gateway has no revocation list.
  - No session timeout enforcement at the gateway. JWT expiry is the only
    time-bound control; there is no idle-timeout or maximum session duration
    enforced by the platform independently.
  - Lambda pods are stateless. A lambda function has no shared storage to
    persist session state across invocations or across multiple lambda replicas.
  - The auth-service has some session state internally (per the commit history:
    "single active sessions per user" and cookie-based session for browser
    navigation), but this is scoped to the auth-service itself and is not a
    platform-wide session manager that other fleets can use.
  - No session persistence across fleet nodes: if a node is stopped or replaced,
    any in-memory session state is lost.


================================================================================
8. GATEWAY TYPES
================================================================================

ENVOY
  Used for: web traffic, streaming, browser-facing applications, authentication
  flows, high-throughput trading platforms.
  Configuration: REST xDS v3. Envoy gateway pods poll the envoy-control-plane
  service for RouteConfiguration, ClusterLoadAssignment, and Listener objects.
  The management-api notifies the control plane on route changes.
  xDS polling endpoint: GET /routes?fleet_id={id} returns route configs in xDS
  format for a specific fleet.
  Auth: JWT validation is performed by an auth sidecar (Envoy ext_authz) which
  calls the auth-service's ext-authz endpoint per request.
  Most data-plane fleets use Envoy (fleet-jpmm, fleet-access, fleet-execute,
  fleet-digital, fleet-mobile, fleet-smb, fleet-iq, fleet-secsvcs, fleet-authn,
  fleet-authz, fleet-console).

KONG
  Used for: API gateway traffic, developer portals, API key management.
  Configuration: Declarative YAML pushed by kong-sync service to the Kong Admin
  API on change detection.
  Plugins used include: rate-limiting, cors, jwt, key-auth, request-transformer.
  Currently used by: fleet-pdp (developer.jpm.com) and some mixed fleets.
  Routes have gateway_type="kong" to target Kong nodes specifically.

MIXED FLEETS
  Fleet fleet-jpmm (JPMM) has gateway_type="mixed", meaning it uses both Envoy
  and Kong nodes. Envoy handles /research, /sandt, /events (web paths); Kong
  handles /research/api, /events/api (API paths).
  Execute fleet also has both envoy and kong nodes.

ROUTE GATEWAY TARGETING
  Each route has a gateway_type field. The platform ensures routes with
  gateway_type="envoy" are served by Envoy nodes and routes with
  gateway_type="kong" are served by Kong nodes. In mixed fleets, routes are
  distributed to the appropriate node type.


================================================================================
9. MULTI-REGION MODEL
================================================================================

FLEET REGIONS
  Every fleet has:
    region: primary region label (e.g. "us-east")
    regions: list of target regions (e.g. ["us-east-1", "us-east-2"])
  All seeded data-plane fleets target ["us-east-1", "us-east-2"].
  Control-plane fleets target ["us-east-2"].

NODE REGION LABELS
  Fleet nodes have datacenter and region fields (e.g. "us-east-1", "us-east-2").
  In the current POC with a single kind cluster, these are symbolic; the cluster
  is not actually split across AZs.
  In the target architecture, datacenter maps to a real AZ and region maps to a
  real cluster name.

CLUSTER CONFIGURATION
  The K8sOrchestrator reads cluster names from GITOPS_CLUSTER_NAMES env var
  (comma-separated). In single-cluster local dev, only one cluster is configured.
  In multi-region mode, each cluster name corresponds to a region (e.g.
  "us-east-1", "us-east-2").

CROSS-REGION ROUTING
  In the target production architecture:
    - DNS (GTM/Akamai) routes users to the nearest regional cluster entry point
    - Each regional cluster has its own ingress gateways for the fleet
    - ArgoCD syncs the same fleet GitOps repo to all target regional clusters
    - The management-api is a global service; regional clusters are read-only
      from the management-api's perspective (it writes to git; ArgoCD applies)
  In the current POC, cross-region routing is simulated by the Mock GTM
  (cp-gtm) control plane component.


================================================================================
10. MCP / AI INTEGRATION
================================================================================

MCP SERVER BINARY: /Users/gavin/bin/cib-ingress-mcp
Source: /Users/gavin/Library/CloudStorage/OneDrive-Personal/dev/jpmc/ingress-poc/cmd/mcp-server/main.go

The MCP server implements the Model Context Protocol (stdio transport) so AI
agents can manage the platform. It translates MCP tool calls to management-api
REST calls and formats responses as readable text.

Environment variables:
  MANAGEMENT_API_URL: base URL (default: http://localhost:8003)
  MANAGEMENT_API_KEY: optional API key for auth header

AVAILABLE MCP TOOLS

Tool name               Description / Key behaviour
----------------------  -------------------------------------------------------
list_fleets             Returns all fleets with status, gateway type, node counts
get_fleet               Returns detailed fleet info including nodes and instances.
                        Accepts fleet_id as UUID or fleet name (does fuzzy match).
create_fleet            Creates fleet record in DB. Fleet starts in "not_deployed"
                        status. Does NOT deploy nodes. Call deploy_fleet after.
update_fleet            Updates fleet config (name, description, instances_count,
                        rate_limit_rps, timeout_request_ms)
delete_fleet            Permanently deletes fleet, removes K8s resources and Git
                        manifests. Irreversible.
deploy_fleet            Two modes:
                        Mode 1 (no context_path): spins up gateway pods for fleet.
                        Mode 2 (with context_path): adds a route AND creates all K8s
                        resources. Use this for lambda routes (with function_code).
                        Do NOT use create_route for lambda deployments.
scale_fleet             Scales fleet Deployment to a given replica count.
suspend_fleet           Scales to 0 pods, sets status="suspended". Config preserved.
resume_fleet            Restores suspended fleet to previous replica count.
list_routes             Lists all routes. Filters: hostname, status.
get_route               Returns full route details including auth config and nodes.
create_route            Creates a static route (DB + Git only). Does NOT create K8s
                        resources. Not suitable for lambda routes.
update_route            Updates route fields (backend_url, authn_mechanism, status,
                        rate_limit_rps, description).
delete_route            Removes route from DB, Git manifest, and gateway config.
get_platform_status     Summary of platform health: fleet counts, statuses, drift.
get_drift               Drift report showing divergence between desired (DB/Git)
                        and actual (gateway xDS) state.
get_audit_log           Recent audit log entries (default 20, max 100).
get_gitops_status       GitOps repo state, recent commits, ArgoCD sync status.
validate_route_policy   Pre-flight check of a proposed route against CIB policy.
                        Returns pass/fail with specific violations.

IMPORTANT DISTINCTION
  deploy_fleet (with function_code): creates lambda K8s resources (Deployment +
    ClusterIP Service in ingress-cp) AND writes route to DB and Git.
  create_route: writes route metadata to DB and Git only. No K8s resources.
    Suitable for static backends (e.g. "http://orders-svc:8080") that already
    exist in the cluster independently of this platform.


================================================================================
11. LIVE PLATFORM DATA (as of 2026-04-04)
================================================================================

FLEET INVENTORY (from GET /fleets)

Data-plane fleets (fleet_type="data"):
  fleet-jpmm       JPMM                jpmm.jpm.com            Markets     mixed   not_deployed
  fleet-execute    Execute             execute.jpm.com         Markets     envoy   suspended
  fleet-access     JPMA                access.jpm.com          Payments    envoy   not_deployed
  fleet-mobile     Access Mobile       access-mobile.jpm.com   Payments    envoy   not_deployed
  fleet-digital    JPMDB               digital-banking.jpm.com Payments    envoy   not_deployed
  fleet-smb        Merchant Services   smb.jpm.com             Payments    envoy   not_deployed
  fleet-pdp        PDP                 developer.jpm.com       Payments    kong    not_deployed
  fleet-iq         IQ                  iq.jpm.com              Global Bkg  envoy   not_deployed
  fleet-secsvcs    SecSvcs             secsvcs.jpm.com         Sec Svcs    envoy   not_deployed
  fleet-authn      AuthN               login.jpm.com           xCIB        envoy   not_deployed
  fleet-authz      AuthZ               authz.jpm.com           xCIB        envoy   not_deployed
  fleet-console    Console             console.jpm.com         xCIB        envoy   not_deployed

Control-plane components (fleet_type="control"):
  cp-mgmt, cp-auth, cp-envoy-xds, cp-kong-sync, cp-shared-gw, cp-gtm,
  cp-edge, cp-psaas, cp-opa, cp-watchdog, cp-jaeger, cp-dns, cp-postgres,
  cp-svc-web, cp-svc-api, cp-console-svc

AUTH PROVIDERS BY FLEET
  Janus:    fleet-jpmm, fleet-execute, fleet-iq, fleet-secsvcs
  Sentry:   fleet-digital, fleet-pdp, fleet-authz
  AuthE1.0: fleet-access, fleet-mobile
  Chase:    fleet-smb
  N/A:      fleet-authn, fleet-console

SAMPLE ACTIVE ROUTES (from GET /routes)
  ID: b2bde801  path:/   hostname:access.jpm.com     backend:http://svc-web:8004
                authn:bearer  issuer:AuthE1.0  scopes:[payments:read,access:view]
                status:active  gateway:envoy

  ID: 90bc79c3  path:/   hostname:authz.jpm.com      backend:http://svc-web:8004
                authn:mtls    issuer:Sentry            scopes:[cib:admin,authz:manage]
                status:active  gateway:envoy

  ID: 2d9fb652  path:/   hostname:console.jpm.com    backend:http://console:80
                authn:none    issuer:N/A               status:active  gateway:envoy

  ID: 251d690a  path:/api/public  hostname:*  backend:http://svc-api:8005
                authn:none    status:active  gateway:kong (platform wildcard)

  ID: b2776db9  path:/health      hostname:*  backend:http://fake-backend:9999
                authn:none    status:active  gateway:kong (platform wildcard)


================================================================================
12. OPERATIONAL CONCEPTS
================================================================================

FLEET STATUS VALUES
  not_deployed  No pods running. Fleet configured but not yet started.
  healthy       All expected pods running and ready.
  degraded      Mix of running and explicitly-stopped nodes in the fleet.
  suspended     User explicitly suspended the fleet (scaled to 0). Config intact.
  stopped       (Legacy) Fleet stopped; not used in current K8s mode.

ROUTE STATUS VALUES
  active    Route is live and serving traffic (or ready to once fleet is deployed).
  inactive  Route is deactivated. May have been git_deleted or manually disabled.

SYNC STATUS VALUES (both fleets and routes)
  synced        DB and Git are in agreement.
  pending       Pending Git push.
  git_deleted   Route was removed from Git; DB record deactivated automatically.
  drifted       DB and Git disagree; reconciler will correct DB to match Git.
  unknown       Not yet determined (newly created records before first reconcile).

DRIFT DETECTION
  The management-api runs a background detectDrift() goroutine. It compares the
  desired configuration in the DB/Git against the actual state returned by the
  Envoy xDS control plane and Kong Admin API. Non-empty drift indicates routes
  that exist in the desired config but not in the actual gateway state, or vice
  versa. Accessible via GET /drift and the MCP get_drift tool.

AUDIT LOG
  Every create, update, and delete operation on routes and fleets is recorded
  in the audit_log table with: entity ID, action type, actor (user ID or
  "system"/"gitops-reconciler"), timestamp, and a human-readable detail string.
  Git-triggered events (route deactivated because YAML removed from git) are
  also audited with actor="gitops-reconciler".

K8S RECONCILER (reconciler.go)
  Runs every 10 seconds (after 15-second startup delay).
  Lists all pods in the ingress-dp namespace.
  Maps pod names to fleet UUIDs via the k8s_name → fleet ID lookup table.
  Updates fleet_nodes status to "running" or "stopped" based on pod phase and
  container readiness.
  Updates fleet status to "healthy", "degraded", or "not_deployed" based on
  pod counts and explicitly-stopped nodes.
  Deliberately does NOT override "suspended" or "stopped" status set by user.

GITOPS RECONCILER (gitops_reconciler.go)
  Runs every 10 seconds (after 90-second startup delay for repo cloning).
  Only runs in K8s orchestration mode (K8sOrchestrator).
  For each fleet with a GitHub-backed git_manifest_path:
    - Pulls latest from remote (non-fatal on failure)
    - Reconciles fleet_nodes from Fleet CRD YAML
    - Reconciles routes from Route CRD YAMLs in routes/ directory
    - Deactivates routes whose YAML has been removed from git
  Result stored in lastReconcileResult, accessible via GET /gitops/reconcile/status.

HEALTH REPORTS
  Gateway nodes can POST health reports to /health-reports. The platform stores
  these and exposes them via GET /health-reports. Used by the Watchdog component
  (cp-watchdog) to track consecutive failures and report degraded status.


================================================================================
13. CURRENT GAPS RELEVANT TO SESSION MANAGER RFC
================================================================================

The following capabilities do not currently exist on the CIB Ingress Platform.
They represent the scope of the Session Manager RFC:

1. NO SESSION CONCEPT
   Auth is entirely per-request. Each HTTP request must carry a valid JWT.
   There is no platform-level session token, session ID, or session record.
   The gateway does not know whether two requests belong to the same user session.

2. NO SESSION STORE
   There is no shared session store (Redis, database table, etc.) that gateway
   nodes or lambda pods can read from to validate a session token. Lambda pods
   are stateless. Each node operates independently.

3. NO STICKY ROUTING (SESSION AFFINITY)
   Requests are load-balanced across fleet nodes without any session affinity.
   If a user's requests must reach the same node (e.g. for in-memory session
   state), the platform currently has no mechanism to enforce this. Envoy's
   consistent hash load balancing is not configured.

4. NO PLATFORM-LEVEL LOGOUT / INVALIDATION
   When a user logs out or an admin wants to revoke a session, there is no
   platform-level mechanism to invalidate a token or session. The JWT remains
   valid until its exp claim. The platform has no revocation list or blocklist.

5. NO SESSION TIMEOUT ENFORCEMENT
   JWT expiry (exp claim) is the only time-based control. The platform does not
   enforce idle session timeouts (inactivity-based expiry) or maximum session
   durations independently of the token's exp claim.

6. NO SESSION PERSISTENCE ACROSS NODE RESTARTS
   When a fleet node is stopped, replaced, or scaled down, any in-node session
   state is lost. The platform has no mechanism to migrate or persist session
   state during scaling events.

7. AUTH IS REQUEST-SCOPED, NOT SESSION-SCOPED
   The auth-service handles the auth code flow and issues tokens, but session
   management in the auth-service is scoped to that service's own user sessions
   (browser cookie for the auth flow). It does not expose a session management
   API that data-plane fleets can use to validate that a user's session is still
   active across multiple fleet interactions.

8. NO CROSS-FLEET SESSION CORRELATION
   A user navigating from jpmm.jpm.com to access.jpm.com is treated as two
   entirely independent authentication events. There is no concept of a
   platform-wide session that spans multiple fleet tenants for the same user.

IMPLICATIONS FOR THE RFC
  The Session Manager RFC should address how the platform introduces a session
  management layer that:
    (a) Defines session tokens as a first-class concept distinct from JWTs
    (b) Provides a shared session store accessible to all fleet nodes
    (c) Enables session affinity (sticky routing) where required
    (d) Implements session invalidation (logout, admin revocation)
    (e) Enforces idle and absolute session timeouts at the gateway level
    (f) Handles session state across scaling events and node failures
    (g) Optionally unifies sessions across fleet tenants for SSO-like behavior

  The RFC should also address whether session management is implemented as:
    - A new control-plane component (session-manager service)
    - An extension to the existing auth-service
    - A gateway-level filter/plugin (Envoy filter or Kong plugin)
    - A combination of the above

  Given the existing auth-service at login.jpm.com handles PKCE and DPoP token
  exchange, the Session Manager would most naturally extend or complement that
  service, with a shared session store (likely Redis) accessible to all fleet
  gateways via the auth sidecar.
