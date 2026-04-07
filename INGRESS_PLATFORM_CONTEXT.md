# CIB Ingress Platform — Context Document for RFC Drafting

This document is the sole context reference for an AI agent drafting a full RFC for the CIB Ingress Platform. It assumes no prior knowledge of JPMorgan Chase, the CIB organisation, or this platform. It covers the problem this platform solves, what has been built, how it works, and where it is going. Read every section before producing the RFC.

---

## 1. PROBLEM STATEMENT AND MOTIVATION

JPMorgan Chase's Corporate and Investment Banking (CIB) division operates hundreds of internal and external-facing services across Markets, Payments, Global Banking, and cross-CIB (xCIB) infrastructure. Historically, each product team or line of business managed its own ingress layer: hand-edited Envoy YAML, bespoke Kong configurations, custom NGINX setups, or thin wrappers around cloud load balancers. This produced a set of compounding problems that became material compliance and operational risks.

The first problem is configuration proliferation. Without a central register, there is no authoritative answer to "what API paths are exposed to the internet today, and to whom?" A compliance audit or security review requires manually interrogating each team's gateway configuration, which is stored in disparate repos, sometimes undocumented, often outdated.

The second problem is inconsistent authentication enforcement. Some teams configure full JWT validation against JPMC's internal auth providers (Janus, Sentry, AuthE1.0). Others perform partial validation or none at all, relying instead on network-level controls that are not always present at the external edge. A misconfigured gateway on the payments platform could expose transfer endpoints without requiring a valid token.

The third problem is the absence of an audit trail. When a route like /api/v1/transfers appears in a gateway configuration, there is typically no record of who added it, when, or under what change-management approval. Financial regulators including the OCC and FRB require demonstrable controls over API surface changes. A gateway config file in a team's private repo with no commit history discipline does not satisfy this requirement.

The fourth problem is operational risk from manual configuration. Envoy and Kong have complex configuration schemas. A misconfigured cluster field, a typo in a matcher path, or an incorrect upstream endpoint can cause silent mis-routing. Teams have introduced outages by pushing hand-edited YAML that was syntactically valid but semantically broken.

The fifth problem is multi-region fragmentation. CIB operates in multiple regions: US East, US East-2, EU West, AP Southeast. Teams building regionally resilient services have had to design their own cross-region failover and config-replication logic, producing N different approaches to the same problem.

The sixth problem is the absence of self-service guardrails. Platform teams managing the underlying gateway infrastructure have no way to enforce policy at the control layer. If a team wants to bypass authentication for a sensitive path, there is currently nothing stopping them at the tooling level.

The CIB Ingress Platform was built to address all of these problems with a single control plane, a GitOps-backed audit trail, and platform-level policy enforcement.

---

## 2. PLATFORM VISION AND GOALS

The vision is a single control plane that manages all ingress for CIB — across all lines of business, gateway technologies, and regional clusters — with security policy enforced at the platform level so that individual teams cannot bypass it.

The specific goals are:

SINGLE CONTROL PLANE. One management API receives all fleet and route mutations. Teams interact with this API (via UI, CLI, or AI agent) rather than editing gateway configs directly. The API is the sole write path to the ingress infrastructure.

SECURITY BY DEFAULT. Authentication, TLS, and WAF configuration are attached to every route at creation time. The platform enforces that bearer-protected routes specify a valid issuer. Public routes (authn_mechanism=none) require explicit declaration. The policy validation endpoint allows pre-flight checks before routes are committed.

GITOPS-FIRST AUDIT TRAIL. Every fleet and route change is a git commit in a dedicated GitHub repository. This provides a tamper-evident, reviewer-accessible, rollback-capable history of every change to the ingress surface. Deleting a route means deleting a YAML file; the deletion is a recorded git operation.

SELF-SERVICE. Product teams can create fleets and routes without filing tickets with a central gateway team. The platform provides guardrails (policy validation, schema enforcement) without requiring manual approval for every change.

AI-NATIVE OPERATIONS. The platform ships with an MCP server (Model Context Protocol) that allows AI agents, including Claude CLI and Claude Desktop, to manage the platform via natural language. An engineer can say "deploy a /payments/transfer route to the access fleet with JWT auth required" and the AI agent will execute the full sequence of API calls correctly.

MULTI-GATEWAY. The platform supports Envoy (preferred for web traffic, streaming, gRPC) and Kong (preferred for API management, developer portals, plugin ecosystems) from the same control plane. A fleet declares its gateway type; routes inherit it. Future work includes per-route gateway overrides.

MULTI-REGION. Fleets declare a regions array. The orchestration layer ensures fleet manifests are written to the correct regional cluster contexts. In the target architecture, ArgoCD ApplicationSets replicate fleet namespaces across all declared regions automatically.

OBSERVABLE. The platform exposes drift detection (comparing intended config in the registry against actual gateway state), an append-only audit log, per-fleet health status, and a platform-wide status API. A background reconciler continuously syncs database state with actual Kubernetes pod state.

---

## 3. CONCEPTUAL MODEL

### 3a. Fleet

A fleet is the fundamental tenant boundary of the platform. Logically it represents a product or service team's ingress allocation: a dedicated hostname, a set of gateway nodes, an authentication policy, and a collection of routes.

In the current POC, a fleet maps to a Kubernetes Deployment in the ingress-dp namespace and a set of pod replicas running Envoy or Kong. In the target production architecture, a fleet maps to a Kubernetes namespace that is replicated across regional clusters via ArgoCD ApplicationSet.

A fleet owns the following configuration:
- id: stable identifier, either a human-readable slug (fleet-jpmm, fleet-execute) or a UUID for dynamically created fleets
- name: human-readable display name (JPMM, Execute, JPMA)
- subdomain: the hostname this fleet serves (jpmm.jpm.com, execute.jpm.com, access.jpm.com)
- lob: line of business (Markets, Payments, Global Banking, xCIB)
- gateway_type: envoy, kong, or mixed (for fleets with both node types)
- host_env: the hosting environment (psaas for on-prem PSaaS layer, aws for cloud-native)
- auth_provider: the default authentication provider (Janus for Markets, Sentry for Digital Banking/Payments, AuthE1.0 for Access/Access Mobile, Chase for SMB Merchant Services)
- regions: JSON array of target regions (e.g. ["us-east-1", "us-east-2"])
- authn_mechanism: default auth mode for routes (bearer, none, mtls, api-key)
- waf_profile: standard, strict, or off
- rate_limit_rps: fleet-level rate limit in requests per second
- resource_profile: small, medium, large (controls pod CPU/memory requests)
- autoscale_enabled/min/max/cpu_threshold: autoscaling parameters
- tls_required: required, edge, passthrough, or none
- fleet_type: data (serves external traffic) or control (management plane component)
- k8s_name: Kubernetes-safe slug for the fleet (e.g. "fleet-jpmm", "fleet-digital-banking")
- git_manifest_path: URL of the fleet's GitHub repository (e.g. https://github.com/org/fleet-jpmm)
- status: not_deployed, healthy, degraded, suspended

Fleet lifecycle: A fleet is created in not_deployed status. Calling the deploy endpoint creates the Kubernetes Deployment and Service in ingress-dp, writes the Fleet CRD YAML to git, and transitions the fleet toward healthy as pods become ready. A fleet can be suspended (scale to zero, preserve config), resumed, or deleted (removes K8s resources and git manifests).

Live fleet examples from the running system (as of this document):
- fleet-jpmm (JPMM, status: healthy, type: mixed, subdomain: jpmm.jpm.com, LOB: Markets, auth: Janus, connection_limit: 1024, rate_limit: 500 rps, WAF: standard)
- fleet-execute (Execute, status: suspended, type: envoy, subdomain: execute.jpm.com, LOB: Markets, rate_limit: 2000 rps, connection_limit: 4096, autoscale: 4-32 pods)
- fleet-access (JPMA, status: healthy, type: envoy, subdomain: access.jpm.com, LOB: Payments, auth: AuthE1.0, WAF: strict)
- fleet-digital (JPMDB, status: healthy, type: envoy, subdomain: digital-banking.jpm.com, LOB: Payments, auth: Sentry, WAF: strict)
- fleet-authn (AuthN, status: healthy, type: envoy, subdomain: login.jpm.com, LOB: xCIB, authn: none — public login flows)
- fleet-authz (AuthZ, status: healthy, type: envoy, subdomain: authz.jpm.com, LOB: xCIB, authn: mtls — admin policy engine)
- fleet-console (Console, status: healthy, type: envoy, subdomain: console.jpm.com, LOB: xCIB — management UI)
- fleet-pdp (PDP, status: not_deployed, type: kong, subdomain: developer.jpm.com, LOB: Payments — developer portal, API key auth)
- fleet-secsvcs (SecSvcs, status: not_deployed, type: envoy, subdomain: secsvcs.jpm.com, auth: mtls — security services)
- Test fleet (UUID: 8b5f4006-cbc3-492d-ba4a-1d22d2df990f, status: healthy, type: envoy, subdomain: test.jpm.com — sandbox fleet used by MCP agent)

Control plane fleets (fleet_type=control, status always healthy, managed separately): cp-mgmt (Management API), cp-auth (Auth Service), cp-envoy-xds (Envoy xDS), cp-kong-sync (Kong Sync), cp-postgres (PostgreSQL), cp-opa (OPA), cp-jaeger (Jaeger), cp-dns (CoreDNS), cp-shared-gw (Shared Gateway), cp-gtm (Mock GTM), cp-edge (Mock CDN/WAF), cp-psaas (Mock PSaaS), cp-watchdog (Watchdog), cp-console-svc (Console Nginx), cp-svc-web, cp-svc-api.

### 3b. Node

A node is a gateway pod within a fleet. Fleets have one or more nodes; each node runs a core gateway container (Envoy or Kong) and will eventually have an auth sidecar. Nodes are named explicitly in the fleet manifest: {fleet-id}-envoy-{index} or {fleet-id}-kong-{index}.

Example nodes from the live system:
- fleet-jpmm-envoy-1 (envoy, datacenter: us-east-1, status: running)
- fleet-jpmm-envoy-2 (envoy, datacenter: us-east-1, status: running)
- fleet-jpmm-kong-1 (kong, datacenter: us-east-1, status: running)
- fleet-access-envoy-1 (envoy, datacenter: us-east-1, status: running)
- fleet-authn-envoy-1 (envoy, datacenter: us-east-2, status: running)
- fleet-console-envoy-1 and fleet-console-envoy-2 (both envoy, datacenter: us-east-2, status: running)
- fleet-execute-envoy-1, fleet-execute-kong-1 (both stopped — fleet is suspended)

Node lifecycle: A node is in pending state when first seeded, transitions to running once the K8s reconciler confirms the pod is Ready, stops when the pod disappears, and can be flagged drifted if the database has a record for a node that no longer appears in the Git Fleet CRD.

Resiliency model in the POC: multiple nodes per fleet provide basic pod-level redundancy. The datacenter field records which AZ a node belongs to. In the target architecture, topologySpreadConstraints in the Fleet CRD ensure pods are spread across availability zones within a regional cluster. Regional resilience comes from deploying the same fleet namespace to multiple clusters.

### 3c. Route

A route maps a hostname plus path prefix to a backend service. Routes carry all the information needed to configure the gateway: authentication requirements, allowed methods, TLS settings, rate limits, and health check path.

A route owns:
- id: UUID
- hostname: the hostname this route is on (must match an existing fleet subdomain, or * for wildcard)
- path: URL path prefix (e.g. /research, /api/v1/orders, /)
- backend_url: the upstream service URL (e.g. http://svc-web:8004, http://orders-svc:8080)
- gateway_type: envoy or kong (which gateway technology handles this route)
- authn_mechanism: bearer (JWT required), none (public), mtls (mutual TLS), api-key
- auth_issuer: the JWT issuer to validate against (Janus, Sentry, AuthE1.0, Chase, Sentry, N/A)
- audience: the required JWT audience claim (e.g. "jpmm", "execute", "access")
- authz_scopes: required scopes in the JWT (e.g. ["markets:read", "research:view"])
- tls_required: true/false
- methods: JSON array of allowed HTTP methods
- health_path: health check endpoint on the backend
- team: owning team name
- notes: human-readable description
- function_code: source code for lambda routes (stored in route record and git)
- function_language: javascript (python planned)
- lambda_container_id, lambda_port: references to the lambda pod when function_code is set
- status: active or inactive
- sync_status: unknown, synced, pending, drifted, git_deleted

Two route types exist:

Static proxy routes: backend_url points to an already-running service. The route is purely configuration — the platform does not manage the backend lifecycle. Example: /research on jpmm.jpm.com proxies to http://svc-web:8004.

Lambda routes: function_code is provided. The platform creates a Kubernetes Deployment and ClusterIP Service for the function. The backend_url is set automatically to http://lambda-{routeID[:8]}-{funcName}:8080. Example: the /claude route on test.jpm.com has function_code and backend_url http://lambda-ff6669d3-claude:8080.

Live routes from the running system (selected examples):
- jpmm.jpm.com /research (active, bearer, Janus, envoy, scopes: markets:read research:view)
- jpmm.jpm.com /research/api (active, bearer, Janus, kong, scopes: markets:read research:api)
- jpmm.jpm.com /sandt (active, bearer, Janus, envoy, scopes: markets:read trading:view)
- jpmm.jpm.com /events (active, bearer, Janus, envoy)
- jpmm.jpm.com /events/api (active, bearer, Janus, kong)
- jpmm.jpm.com /gh (active, bearer, Janus, envoy — added via MCP/git)
- access.jpm.com / (active, bearer, AuthE1.0, envoy, scopes: payments:read access:view)
- authz.jpm.com / (active, mtls, Sentry, envoy, scopes: cib:admin authz:manage)
- login.jpm.com / (active, none, envoy — public login flow)
- console.jpm.com / (active, none, envoy — management console)
- developer.jpm.com /api (inactive, api-key, Sentry, kong — PDP developer portal, awaiting deployment)
- secsvcs.jpm.com / (inactive, mtls, Janus, envoy — awaiting deployment)
- test.jpm.com /claude (active, bearer, envoy — lambda route, function pod running)
- test.jpm.com /hello (active, bearer, envoy)
- * /health and * /api/public (active, none, kong — platform wildcard health/public endpoints)

---

## 4. ARCHITECTURE — CURRENT (POC)

The POC runs on a single kind Kubernetes cluster named ingress-cp. This deliberately collapses the control plane and data plane into one cluster to simplify local development, but the architecture is designed to separate them in production.

CLUSTER LAYOUT:
- Namespace ingress-cp: management-api, envoy-control-plane (xDS server), PostgreSQL
- Namespace ingress-dp: all fleet gateway pods, lambda pods

MANAGEMENT API (cmd/management-api):
- Language: Go
- Router: Chi (go-chi/chi v5)
- Database: PostgreSQL via sqlx
- Orchestration mode: selected at startup via ORCHESTRATION_MODE env var (docker for legacy Docker Compose mode, k8s for Kubernetes mode)
- Approximately 1800 lines of handler code in main.go plus separate files for reconciler, gitops_reconciler, orchestrator_k8s, github, and seed
- Instrumented with OpenTelemetry (traces exported to Jaeger)
- Optional API key auth via MANAGEMENT_API_KEY env var (enforced when set; bypassed for /health)
- Listens on port 8003 by default

DATABASE SCHEMA (key tables):
- fleets: all fleet configuration including k8s_name, git_manifest_path, regions
- fleet_nodes: per-node records with status, gateway_type, datacenter
- routes: all route configuration including function_code, lambda_container_id
- fleet_instances: links routes to fleets (the route-fleet assignment table)
- route_node_assignments: links routes to specific nodes
- audit_log: append-only record of every mutation
- health_reports: periodic health data from gateway pods

K8S ORCHESTRATOR (K8sOrchestrator struct):
- Implements the Orchestrator interface
- On fleet/route create or update: writes CRD YAML to the fleet's GitHub repo AND applies it directly to the cluster via the Kubernetes dynamic client (best-effort; git is authoritative)
- Custom Resource Definitions: ingress.jpmc.com/v1alpha1 Fleet and Route
- Fleet CRD namespace: ingress-dp
- Route CRD namespace: ingress-dp
- Dynamic client: configured from in-cluster config, or from DP_KUBECONFIG / DP_CLUSTER_CONTEXT env vars for multi-cluster setups
- Multi-cluster support: GITOPS_CLUSTER_NAMES env var (comma-separated cluster names); single-repo mode uses clusters/{cluster-name}/fleets and clusters/{cluster-name}/routes directory structure

GITHUB INTEGRATION (FleetRepoManager):
- Configured via GITOPS_GITHUB_TOKEN and GITOPS_GITHUB_USERNAME env vars
- When configured: each fleet gets its own GitHub repository named fleet-{fleet-name-slug}
- Repos are created via GitHub API and cloned locally to a configurable base path
- Local clone is used for read/write; pushes go back to GitHub via HTTPS with token
- When not configured: single local git repo is used (legacy/fallback mode)
- Repo naming: FleetRepoManager.repoName() lowercases and sanitises the fleet display name, then prepends "fleet-"
- Examples: fleet-jpmm, fleet-execute, fleet-digital-banking, fleet-test

XODS (REST xDS):
- A separate envoy-control-plane service polls the management API at GET /routes?fleet_id={id} every few seconds
- Returns route configuration in Envoy xDS RouteConfiguration JSON format
- The fleet_id query parameter accepts both UUID and k8s_name slug
- Envoy pods are configured to poll this endpoint for their route table

K8S RECONCILER (reconciler.go):
- Runs as a background goroutine every 10 seconds
- Lists all pods in ingress-dp namespace
- Builds a map of fleet → running pod count using owner references and k8s_name→UUID translation
- Updates fleet_nodes.status (running/stopped) and fleets.status (healthy/degraded/not_deployed) to match actual cluster state
- Respects user-set statuses: suspended and stopped fleets are not overridden
- Starts 15 seconds after management-api startup to allow seed data to settle

GITOPS RECONCILER (gitops_reconciler.go):
- Runs as a background goroutine every 10 seconds (with a 90-second startup delay)
- For each fleet that has a git_manifest_path starting with https://:
  1. Pulls latest from the fleet's GitHub repo
  2. Reads fleets/{fleet-id}.yaml (Fleet CRD) and reconciles fleet_nodes table
  3. Reads all routes/*.yaml (Route CRDs) and reconciles routes + fleet_instances tables
- DB rows that differ from git are corrected (git wins)
- DB routes present in the fleet but absent from git are set to inactive with sync_status=git_deleted
- An empty routes/ directory is treated as uninitialised and skipped (avoids false deletion on fresh clone)
- Results are stored in lastReconcileResult and available via GET /gitops/reconcile/status

LAMBDA PODS:
- Created in ingress-cp namespace (target: per-fleet namespace)
- Named: lambda-{first 8 chars of route UUID}-{function name}
- Example: lambda-ff6669d3-claude (the /claude route on test.jpm.com)
- Kubernetes Deployment + ClusterIP Service created by K8sOrchestrator.CreateLambdaContainer
- Service DNS within cluster: lambda-{id}-{name}.ingress-cp.svc.cluster.local
- Runtime: Node.js 20 (JavaScript)
- Not true serverless: pods are always-on (no scale-to-zero)
- Function code stored in route.function_code field in PostgreSQL AND in lambdas/ dir in fleet git repo

BACKGROUND TASKS on startup:
- go ensureFleetContainers(db): restores fleet pods from DB state on management-api restart
- go restoreLambdaContainers(): restores lambda pods
- go detectDrift(): periodic drift detection
- go computeFleetStatus(): periodic fleet status computation
- startReconciler(10s): K8s pod state sync (K8s mode only)
- startGitOpsReconciler(10s): Git→DB sync (K8s mode only)

LIVE GITOPS STATUS (from GET /gitops/status):
- mode: k8s
- clusters: [{cluster: "data-plane-1", sync_status: "progressing", fleet_count: 0}]

---

## 5. ARCHITECTURE — TARGET (PRODUCTION)

The production architecture separates concerns that are currently collapsed into the POC's single cluster.

CLUSTER TOPOLOGY:
- One Kubernetes cluster per region: us-east-1, us-east-2, eu-west-1, ap-southeast-1 (minimum)
- A dedicated management cluster hosts the management API, PostgreSQL, and the GitOps tooling
- Data plane clusters host fleet namespaces only

FLEET AS NAMESPACE:
- Each fleet is a Kubernetes namespace on each regional cluster it is deployed to
- Namespace name: the fleet's k8s_name slug (e.g. fleet-jpmm, fleet-digital-banking)
- ArgoCD ApplicationSet watches the fleet's GitHub repo and syncs the namespace to all declared regions
- Fleet CRD is applied to each regional cluster; the ingress-operator in that cluster creates the Deployment and Service
- Two or more pods per fleet namespace, with topologySpreadConstraints ensuring AZ spread

MANAGEMENT API AS INTENT LAYER:
- In production, the management API does not apply K8s resources directly
- It validates, authorises, writes YAML to the fleet's GitHub repo, and returns
- ArgoCD picks up the commit and syncs to the target clusters within seconds
- This eliminates the current dual-write (git + direct apply) and makes git the single write path

XDS PER REGION:
- Each regional data plane cluster runs its own xDS control plane instance
- The regional xDS server polls the management API (or reads directly from the fleet's git repo via a local clone)
- In the final state, gRPC ADS (Aggregated Discovery Service) replaces the current REST xDS polling for lower latency and more efficient updates

DNS AND GLOBAL LOAD BALANCING:
- Above the platform: DNS/GLB (Akamai GTM in production, mocked as cp-gtm in the POC) routes to the nearest healthy regional cluster
- If a region is unhealthy, GTM steers all traffic to the remaining regions
- The platform does not manage DNS; it provides the regional endpoints that DNS points to

AUTH SIDECARS:
- Each node pod in the target architecture includes an auth sidecar container
- The sidecar handles JWT validation (bearer), mTLS termination, and session token validation
- This moves auth enforcement off the gateway config layer and into a per-pod process, making it harder to misconfigure

---

## 6. GITOPS MODEL

Git is the authoritative source of truth for fleet and route configuration. Every write operation that goes through the management API results in a git commit. The GitOps reconciler continuously pulls and re-syncs the database from git, so the database is always a projection of git state.

REPOSITORY STRUCTURE (per-fleet GitHub repo):
  fleets/{fleet-id}.yaml       — Fleet CRD (replicas, nodes, gatewayType, subdomain)
  routes/{path-slug}.yaml      — Route CRD per active route
  lambdas/{short-id}.yaml      — Lambda CRD (function code, runtime, resource limits)
  README.md                    — Auto-generated description

Example Fleet CRD YAML (as generated by generateFleetCRD):

  apiVersion: ingress.jpmc.com/v1alpha1
  kind: Fleet
  metadata:
    name: fleet-jpmm
    namespace: ingress-dp
    labels:
      app.kubernetes.io/managed-by: management-api
      fleet.jpmc.com/id: "fleet-jpmm"
  spec:
    name: "JPMM"
    subdomain: "jpmm.jpm.com"
    gatewayType: mixed
    replicas: 3
    nodes:
      - name: "fleet-jpmm-envoy-1"
        index: 0
        gatewayType: "envoy"
        datacenter: "us-east-1"
        region: "us-east-1"
        status: "running"
      - name: "fleet-jpmm-envoy-2"
        index: 1
        gatewayType: "envoy"
        datacenter: "us-east-1"
        region: "us-east-1"
        status: "running"
      - name: "fleet-jpmm-kong-1"
        index: 2
        gatewayType: "kong"
        datacenter: "us-east-1"
        region: "us-east-1"
        status: "running"

Example Route CRD YAML (as generated by WriteRouteCRD):

  apiVersion: ingress.jpmc.com/v1alpha1
  kind: Route
  metadata:
    name: {route-uuid}
    namespace: ingress-dp
    labels:
      app.kubernetes.io/managed-by: management-api
  spec:
    path: "/research"
    hostname: "jpmm.jpm.com"
    backendUrl: "http://svc-web:8004"
    gatewayType: "envoy"
    audience: "jpmm"
    team: "markets"
    authnMechanism: "bearer"
    authIssuer: "Janus"
    tlsRequired: true
    healthPath: "/health"
    methods:
      - GET
      - POST
      - PUT
      - DELETE

Example Lambda CRD YAML:

  apiVersion: ingress.jpmc.com/v1alpha1
  kind: Lambda
  metadata:
    name: lambda-ff6669d3
    namespace: ingress-dp
    labels:
      app.kubernetes.io/managed-by: management-api
      ingress.jpmc.com/route: ff6669d3-7ea5-49de-b8b6-a29cfa88bfdb
  spec:
    functionName: claude
    runtime: nodejs20
    code: |
      [function source code]
  status:
    phase: Pending

ROUTE FILENAME CONVENTION: Route YAML files are named using a path-based slug derived from the route path and a short ID prefix. The MigrateRouteFilenames function renames legacy UUID-based filenames to this convention. Example: a route with path /research and id f6948683-... would be stored as research-f694.yaml.

AUDIT TRAIL VIA GIT: Every management API write to git uses a structured commit message. Examples from the running system's commit history: "Write route /claude (ff6669d3)", "Initialize fleet repo for Test". The commit actor is the management-api itself; in the target architecture the actor would be the authenticated user or AI agent session.

CONFLICT RESOLUTION: The K8sOrchestrator always uses CommitFiles (staging specific files only) rather than a full git add. This avoids accidental staging of unrelated changes. On pull conflicts, the local repo is reset to the remote HEAD before re-applying.

---

## 7. API SURFACE

The management API (listening on port 8003) exposes the following endpoint groups:

FLEET MANAGEMENT:
  GET    /fleets                               List all fleets with nodes and instances
  GET    /fleets/{fleet_id}                    Get fleet detail
  POST   /fleets                               Create fleet
  PUT    /fleets/{fleet_id}                    Update fleet config
  DELETE /fleets/{fleet_id}                    Delete fleet and all K8s resources
  POST   /fleets/{fleet_id}/deploy             Deploy nodes OR add a route (including lambdas)
  GET    /fleets/{fleet_id}/nodes              List fleet nodes
  POST   /fleets/{fleet_id}/scale              Scale fleet to N nodes
  DELETE /fleets/{fleet_id}/instances/{id}     Remove a specific fleet instance
  POST   /fleets/{fleet_id}/suspend            Suspend fleet (scale to zero, preserve config)
  POST   /fleets/{fleet_id}/resume             Resume suspended fleet
  POST   /fleets/{fleet_id}/nodes/{id}/stop    Stop a specific node
  POST   /fleets/{fleet_id}/nodes/{id}/start   Start a specific node
  DELETE /fleets/{fleet_id}/nodes/{id}         Delete a specific node
  POST   /fleets/{fleet_id}/nodes/deploy       Deploy a single new node

ROUTE MANAGEMENT:
  GET    /routes                               List routes (filterable by status, gateway_type, fleet_id, hostname, unassigned)
  GET    /routes/{id}                          Get route detail
  POST   /routes                               Create static proxy route
  PUT    /routes/{id}                          Update route
  PUT    /routes/{id}/status                   Update route status only
  DELETE /routes/{id}                          Delete route (also removes git YAML)
  GET    /routes/{id}/nodes                    Get nodes assigned to this route
  POST   /routes/{id}/reconcile                Force reconcile a specific route to its assigned nodes

GITOPS:
  GET    /gitops/status                        GitOps sync status and cluster states
  GET    /gitops/commits                       Recent git commits across fleet repos
  GET    /gitops/repos                         List of fleet GitHub repos
  POST   /gitops/sync                          Trigger full git push of current DB state
  POST   /fleets/{fleet_id}/gitops/sync        Rebuild and push fleet manifest from DB state
  GET    /gitops/diff/{fleet_id}               Show diff between DB state and git state
  POST   /gitops/reconcile                     Manual trigger of full Git→DB reconcile
  GET    /gitops/reconcile/status              Result of last reconcile pass
  POST   /gitops/migrate-route-names           Rename UUID-based route files to path-based names

OBSERVABILITY:
  GET    /audit-log                            Recent audit log entries
  GET    /actuals                              Actual gateway config (what gateways are serving)
  GET    /drift                                Drift report (registry vs actuals)
  GET    /health-reports                       Gateway health reports
  POST   /health-reports                       Gateway pods POST health reports here

LAMBDA:
  GET    /lambdas                              List all lambda deployments

POLICY:
  GET    /policy/validate                      Validate a proposed route config against CIB policy

HEALTH:
  GET    /health                               Service health check (always returns 200 ok)

xDS (consumed by Envoy gateways, not by operators):
  GET    /routes?fleet_id={id}                 Return xDS RouteConfiguration for a fleet

---

## 8. SECURITY AND AUTH MODEL

ROUTE-LEVEL AUTH CONFIGURATION:
Authentication is configured per-route, not per-fleet. This allows a fleet to serve both authenticated and public paths simultaneously. For example, login.jpm.com serves / with authn_mechanism=none (the login flow itself is public), while other paths in other fleets on the same platform require bearer tokens.

AUTH MECHANISMS:
- bearer: JWT validation. The gateway validates the JWT signature against the configured auth_issuer's JWKS endpoint, checks the audience claim matches the route's audience field, and verifies required scopes from authz_scopes.
- none: No authentication. The route is publicly accessible. Requires explicit declaration; the platform policy validator will flag if none is used on a sensitive path pattern.
- mtls: Mutual TLS. The client must present a valid certificate from the trusted CA. Used for machine-to-machine service routes (authz.jpm.com, secsvcs.jpm.com).
- api-key: API key validation via Kong key-auth plugin. Used for developer portal routes (developer.jpm.com).

AUTH ISSUERS (JPMC internal providers):
- Janus: Markets division auth provider (used by fleet-jpmm, fleet-execute, fleet-iq, fleet-secsvcs)
- Sentry: Cross-CIB auth provider (used by fleet-digital, fleet-authz, fleet-pdp)
- AuthE1.0: Payments auth provider (used by fleet-access, fleet-mobile)
- Chase: Consumer/SMB auth provider (used by fleet-smb)
- N/A: No issuer (used by authn_mechanism=none routes — login.jpm.com, console.jpm.com)

TLS MODEL:
- tls_required: true on all routes by default
- tls_termination: configured at fleet level (edge = TLS terminated at the ingress edge; passthrough = TLS forwarded to backend)
- In the POC, TLS termination is simulated by cp-edge (Mock CDN/WAF) and cp-psaas (Mock PSaaS)
- In production: Akamai CDN/WAF terminates TLS at the edge; internal hops use JPMC's internal PKI

WAF PROFILES:
- standard: baseline WAF rules (OWASP top 10, bot detection, common injection patterns)
- strict: additional rules for high-sensitivity paths (payments, auth services)
- off: no WAF (not available for external-facing fleets in production)

RATE LIMITING:
- Fleet-level rate_limit_rps field exists on all fleets
- Examples from live system: fleet-jpmm 500 rps, fleet-execute 2000 rps, fleet-access 1000 rps, fleet-authn 5000 rps (login flows require higher headroom)
- Per-route rate limiting is planned but enforcement at the gateway is not yet fully wired
- Kong plugin: rate-limiting plugin applied to most fleet configurations

POLICY VALIDATION (GET /policy/validate):
The platform exposes a pre-flight policy check that validates a proposed route configuration against CIB ingress policy. This is designed to be called before route creation to surface violations early (e.g. attempting to create a none-auth route on a payments subdomain). The MCP tool validate_route_policy wraps this endpoint.

NOT YET ENFORCED: Session management. The platform currently performs stateless request-level auth (JWT validation on each request). There is no session token store, no logout invalidation, no sticky routing for session affinity. This is the subject of the next RFC.

---

## 9. MULTI-REGION AND RESILIENCY MODEL

FLEET REGIONS ARRAY:
Every fleet has a regions field containing a JSON array of region strings. In the seed data all data-plane fleets declare ["us-east-1", "us-east-2"]. The orchestrator uses this to determine which cluster contexts receive the fleet manifest when writing to git.

The K8sOrchestrator.targetClusters() method reads the fleet's regions, then matches them against the configured cluster names (GITOPS_CLUSTER_NAMES env var, comma-separated). Matching is substring-based: a cluster named "data-plane-us-east-1" matches region "us-east-1". If no match is found, all clusters receive the manifest (safe broadcast fallback).

REGIONAL RESILIENCY:
- Full fleet replica per regional cluster: if us-east-1 becomes unavailable, the fleet's pods in us-east-2 continue serving traffic
- DNS/GLB (Akamai GTM, mocked in POC) routes around failed regions
- Route config is identical across all regions (pushed from the same git commit)
- No active-active or active-passive distinction at the platform level; the platform treats all regional copies as equal

AZ RESILIENCY WITHIN REGION:
- Multiple pods per fleet, distributed across AZs via topologySpreadConstraints (target architecture)
- In the POC, multiple pods exist (fleet-jpmm has 3 nodes, fleet-console has 2 nodes) but no hard AZ spread enforcement
- The datacenter field on each node records its intended AZ (us-east-1, us-east-2 in seed data)

CROSS-REGION STATE:
- Route configuration is stateless at the request level: each regional gateway reads routes from its local xDS server (which reads from the management API or from a local git clone)
- No cross-region session state exists currently (stateless JWT validation per request)
- Lambda pods are per-cluster; each regional cluster has its own lambda pod instances (code is identical, instances are independent)

CONSISTENCY MODEL:
- Config changes are pushed to git by the management API (single write path)
- ArgoCD (in the target architecture) syncs all regional clusters from the same git commit
- Until ArgoCD has synced, a brief inconsistency window exists where some regions may be running stale config
- The GitOps reconciler on each management API instance detects and corrects any drift between git state and DB state

---

## 10. GATEWAY TYPES AND THEIR ROLES

ENVOY:
- Preferred for web traffic, streaming, gRPC, and any workload requiring low latency or protocol-level flexibility
- Config delivered via REST xDS polling (GET /routes?fleet_id={id}) with plans to migrate to gRPC ADS
- The xDS response is a RouteConfiguration JSON object consumed directly by the Envoy dynamic routes filter
- Current xDS poll interval: a few seconds (exact interval configured in each Envoy pod's bootstrap config)
- Fleets using Envoy: fleet-jpmm (envoy nodes), fleet-execute, fleet-access, fleet-mobile, fleet-digital, fleet-authn, fleet-authz, fleet-console, fleet-iq, fleet-secsvcs, test fleet
- Also used by cp-shared-gw for routes not yet assigned to dedicated fleet nodes

KONG:
- Preferred for API management use cases: developer portals, partner API access, rich plugin ecosystem
- Config delivered via KongIngress CRD (Kubernetes-native approach) or via Kong Admin API (cp-kong-sync polls and pushes declarative YAML)
- Plugins in use: rate-limiting, cors, jwt (Envoy auth plugin), key-auth (API key validation), request-transformer, opa (policy evaluation)
- Fleets using Kong: fleet-jpmm (kong nodes for /research/api and /events/api), fleet-pdp (developer portal), fleet-execute (kong node for /api)
- Fleet-level kong_plugins array declares which Kong plugins are active for that fleet

MIXED FLEETS:
- fleet-jpmm is the primary example: status=healthy, gateway_type=mixed, has 2 envoy nodes and 1 kong node
- Routes in a mixed fleet specify their gateway_type individually (see the route list above: /research is envoy, /research/api is kong)
- The management API's listRoutes handler filters by gateway_type when a specific type is requested
- The xDS endpoint only returns envoy-type routes; the kong-sync service handles kong-type routes separately

PLANNED: Per-route gateway_type override within a single-type fleet (today all routes in an envoy fleet must be envoy; the override capability would allow a minority of routes to use kong without declaring the whole fleet mixed).

---

## 11. LAMBDA / SERVERLESS ROUTES

The platform supports platform-managed function deployment as a first-class feature. A route can carry executable function code that the platform deploys alongside the gateway configuration.

CREATION FLOW:
1. The caller invokes POST /fleets/{fleet_id}/deploy with context_path, function_code, and function_language
2. The management API creates a Kubernetes Deployment (one pod, Node.js 20 runtime) in ingress-cp (or the fleet namespace in production)
3. The management API creates a ClusterIP Service with the same name
4. The backend_url for the route is set to http://lambda-{routeID[:8]}-{funcName}:8080
5. The route record is written to the DB and to git (routes/ dir in fleet repo)
6. The Lambda CRD YAML is written to git (lambdas/ dir in fleet repo)
7. The route is assigned to available nodes

NAMING CONVENTION:
- Lambda pod/service name: lambda-{first 8 chars of route UUID}-{function name}
- Example: lambda-ff6669d3-claude (for route ff6669d3-7ea5-..., function name "claude")
- Service DNS: lambda-{id}-{name}.ingress-cp.svc.cluster.local (current)
- Target DNS: lambda-{id}-{name}.{fleet-namespace}.svc.cluster.local

FUNCTION STORAGE:
- function_code is stored in the route record in PostgreSQL (full source code)
- Also stored in git as a YAML literal block scalar in lambdas/{short-id}.yaml
- The GitOps reconciler compares function_code in git vs DB and corrects drift

CURRENT LIMITATIONS:
- Lambda pods are always-on (no scale-to-zero)
- No Knative or KEDA integration
- JavaScript only (Python runtime planned)
- Lambda pods currently created in ingress-cp namespace rather than in the fleet's namespace (isolation gap)

LIVE EXAMPLE: The test fleet (test.jpm.com) has two active lambda routes from the running system's audit log:
- /claude (route ff6669d3): lambda pod lambda-ff6669d3-claude running in ingress-cp, backend http://lambda-ff6669d3-claude:8080
- /hello (route 0865a4e3): standard envoy route, backend http://svc-web:8004

The audit log shows the /claude route was created multiple times as the operator iterated on the function code: CREATE at 1775249767, DELETE at 1775250074, re-CREATE at 1775250083, DELETE at 1775250296, final CREATE at 1775250871. This demonstrates the full create/delete/recreate cycle working correctly.

---

## 12. MCP / AI-NATIVE OPERATIONS

The platform ships a dedicated MCP (Model Context Protocol) server binary named cib-ingress-mcp.

BINARY: cmd/mcp-server/main.go (Go, compiled to cib-ingress-mcp or mcp-server)

TRANSPORT: stdio, JSON-RPC 2.0. The server reads from stdin and writes to stdout. stderr is used for logging. This makes it compatible with Claude CLI (claude --mcp-server), Claude Desktop, and any other MCP-compliant AI agent host.

CONFIGURATION:
- MANAGEMENT_API_URL env var: URL of the management API (default: http://management-api:8003)
- MANAGEMENT_API_KEY env var: optional API key forwarded as Authorization: Bearer {key}
- The binary is stateless; it proxies tool calls to the management API

TOOL INVENTORY (17 tools):
1. list_fleets — GET /fleets
2. get_fleet — GET /fleets/{id} (with name-based fallback lookup)
3. create_fleet — POST /fleets
4. update_fleet — PUT /fleets/{id}
5. delete_fleet — DELETE /fleets/{id}
6. deploy_fleet — POST /fleets/{id}/deploy (two modes: node deployment OR route deployment with optional lambda)
7. scale_fleet — POST /fleets/{id}/scale
8. suspend_fleet — POST /fleets/{id}/suspend
9. resume_fleet — POST /fleets/{id}/resume
10. list_routes — GET /routes (with hostname and status filters)
11. get_route — GET /routes/{id}
12. create_route — POST /routes (static proxy only — WARNING: does not create lambda pods)
13. update_route — PUT /routes/{id}
14. delete_route — DELETE /routes/{id}
15. get_platform_status — GET /health + fleet summary
16. get_drift — GET /drift
17. get_audit_log — GET /audit-log
18. get_gitops_status — GET /gitops/status
19. validate_route_policy — GET /policy/validate

KEY DESIGN DECISION — deploy_fleet vs create_route:
The deploy_fleet tool is the correct tool for both node deployment and lambda route creation. create_route only creates a database record and a git YAML; it does not spin up lambda pods or services. The tool descriptions contain explicit warnings: "WARNING: Do NOT use this for lambda/serverless/function routes." This prevents a common AI agent mistake of calling create_route with function_code and then wondering why the backend is unreachable.

NATURAL LANGUAGE EXAMPLES (capabilities demonstrated in live system):
- "Deploy a /claude route to the test fleet with a hello-world Node.js lambda" → deploy_fleet with fleet_id=test-fleet-uuid, context_path=/claude, function_code="module.exports = async (req, res) => { res.json({message: 'hello'}) }"
- "What fleets are currently healthy?" → list_fleets, filter status=healthy in response
- "Suspend the Execute fleet" → suspend_fleet with fleet_id=fleet-execute
- "Show me recent route changes" → get_audit_log with limit=20

AUDIT TRAIL FOR MCP OPERATIONS: The audit log actor field records who or what made each change. The system actor is used for automated operations; fleet-deploy is used for deploy_fleet operations. In the live audit log: "Deployed /claude to fleet Test (test.jpm.com), assigned to 0 nodes" with actor=fleet-deploy.

---

## 13. OPERATIONAL MODEL

FLEET STATUS LIFECYCLE:
- not_deployed: fleet record exists, no pods running
- pending: pod creation in progress (reconciler sees pods not yet Ready)
- healthy: all pods running and Ready, no explicitly stopped nodes
- degraded: mix of running and explicitly stopped nodes
- suspended: operator-initiated scale-to-zero (reconciler will not override this)
- stopped: all pods stopped, not user-initiated (treated as not_deployed by reconciler)

NODE STATUS LIFECYCLE:
- pending: initial seeded state, pod creation not yet triggered
- running: pod exists and all containers are Ready
- stopped: pod gone or not Ready (set by reconciler unless fleet is suspended)
- drifted: node exists in DB but not in the Git Fleet CRD (flagged by GitOps reconciler)

DRIFT DETECTION:
The drift detection background task (go detectDrift()) compares the route configuration in the registry (DB) against what is actually being served by the gateways. This catches cases where a gateway pod has stale xDS data, a direct edit was made to a gateway config bypassing the management API, or a pod restarted with an older config snapshot.
Drift is surfaced via GET /drift and via the get_drift MCP tool.

RECONCILIATION LOOP (two reconcilers running in parallel):
1. K8s reconciler (10s interval): Kubernetes pod state → database. Corrects fleet and node status records to match what pods are actually running.
2. GitOps reconciler (10s interval, 90s startup delay): Git repository state → database. Corrects routes and nodes to match what is declared in the fleet's GitHub repo. This is the authoritative reconciler: git state wins over DB state.

AUDIT LOG:
The audit_log table is append-only. Every create, update, delete, and status change on a fleet or route writes a record with:
- route_id: the affected resource ID
- action: CREATE, UPDATE, DELETE, STATUS_CHANGE, GIT_DELETED
- actor: the initiating identity (system, fleet-deploy, gitops-reconciler, or a user/session ID)
- detail: human-readable description of the change
- ts: Unix timestamp

Sample recent audit log entries from the live system:
- CREATE actor=fleet-deploy "Deployed /claude to fleet Test (test.jpm.com), assigned to 0 nodes" ts=1775250871
- DELETE actor=system "Deleted route /claude → http://lambda-c5b39892-claude:8080" ts=1775250296
- CREATE actor=fleet-deploy "Deployed /claude to fleet Test (test.jpm.com), assigned to 0 nodes" ts=1775250083
- DELETE actor=system "Deleted route /claude → http://lambda-claude:8080" ts=1775250074
- CREATE actor=system "Created route /claude → http://lambda-claude:8080" ts=1775249767

HEALTH REPORTS:
Gateway pods can POST to /health-reports with their observed status. This supplements the K8s reconciler's pod-level view with application-level health data (e.g. a pod that is Running but whose gateway process is unhealthy).

---

## 14. KNOWN GAPS AND ROADMAP ITEMS

The following items are explicitly NOT implemented in the current POC. This list is intended to be honest and specific so that the RFC can clearly distinguish between what exists today and what is proposed.

SESSION MANAGEMENT (NEXT RFC):
There is no session store. The platform performs stateless JWT validation on every request. There are no session tokens, no session IDs, no logout/invalidation endpoints, no sticky routing for session affinity, and no cross-request session state. An authenticated user who logs out cannot have their token revoked before it naturally expires. This is the highest-priority gap and is the subject of a dedicated RFC that this context document is intended to support.

PER-FLEET NAMESPACE ISOLATION:
All fleet pods currently run in ingress-dp regardless of which fleet they belong to. The fleet-jpmm pods, fleet-access pods, and test fleet pods all share the same namespace. There is no K8s NetworkPolicy isolating them. In the target architecture each fleet is a namespace, providing hard K8s boundary.

TRUE MULTI-CLUSTER:
The POC runs entirely in a single kind cluster (ingress-cp). The multi-cluster code paths exist in the orchestrator (GITOPS_CLUSTER_NAMES, DP_KUBECONFIG, DP_CLUSTER_CONTEXT) but have not been exercised with real separate clusters.

SCALE-TO-ZERO LAMBDAS:
Lambda pods are long-running Kubernetes Deployments. There is no scale-to-zero or event-driven scaling. Knative or KEDA integration is not implemented.

KONG FULL INTEGRATION:
The fleet-pdp fleet (developer.jpm.com) declares gateway_type=kong and several Kong plugins (rate-limiting, cors, key-auth, request-transformer), but the full KongIngress CRD flow delivering route config to Kong pods is not completely wired. The cp-kong-sync control plane component exists but the end-to-end push is incomplete.

RBAC:
There is no user or role model in the management API. Any caller with the API key (or any caller if MANAGEMENT_API_KEY is not set) can perform any operation. Role-based access control (e.g. fleet owners can only modify their own fleet's routes) is planned but not implemented.

RATE LIMIT ENFORCEMENT:
The rate_limit_rps field exists on both fleets and routes, and is stored in the database and git manifests. However, the end-to-end enforcement path — pushing the rate limit value into Envoy's rate limit filter config or Kong's rate-limiting plugin config — is not fully wired.

MTLS CERTIFICATE MANAGEMENT:
authn_mechanism=mtls is supported in the route config model (authz.jpm.com and secsvcs.jpm.com use it), but the certificate provisioning, rotation, and distribution pipeline is not implemented. MTLS routes in the current POC rely on manually provisioned certs.

CANARY / TRAFFIC SPLITTING:
There is no weighted routing, canary deployment, or blue/green switch mechanism. All traffic to a fleet is routed to the full pod set without weighting.

METRICS AND DISTRIBUTED TRACING:
The management API is instrumented with OpenTelemetry (traces sent to Jaeger at cp-jaeger). However, there are no Prometheus metrics exposed from the management API itself, and the gateway pods do not have configured Prometheus exporters. Operational dashboards and alerting rules are not implemented.

GRPC XDS:
The current xDS implementation uses REST polling (GET /routes). Production Envoy deployments benefit from gRPC ADS (Aggregated Discovery Service) for lower latency config propagation and more efficient connection handling. gRPC xDS is not yet implemented.

---

## 15. TECHNOLOGY CHOICES AND RATIONALE

GO:
Chosen for the management API and MCP server. Go provides single binary deployment (no runtime dependency management), efficient concurrency for multiple background reconciler goroutines, strong standard library for HTTP and JSON handling, and compile-time type safety that is valuable in a critical-path control plane. The management API binary is approximately 1800 lines of handler code plus supporting files.

CHI ROUTER:
go-chi/chi v5 was chosen over alternatives (Gin, Echo, standard net/http mux) for its idiomatic middleware composition, clean URL parameter handling, and lightweight footprint. The middleware-first design makes it easy to add cross-cutting concerns like API key auth, CORS, and request tracing at the router level.

POSTGRESQL:
Relational model fits the fleet/route/node data structure (normalised tables, foreign key relationships, ACID transactions for audit log). PostgreSQL's jsonb type handles variable-length fields like regions, methods, authz_scopes, and kong_plugins without requiring schema changes. The inline migration approach (ALTER TABLE IF NOT EXISTS ADD COLUMN) allows schema evolution without a dedicated migration tool.

KUBERNETES CRDS:
Fleet and Route as first-class Kubernetes custom resources (apiVersion: ingress.jpmc.com/v1alpha1) enables operator pattern: the ingress-operator watches for Fleet and Route objects and reconciles the actual Envoy/Kong configuration. This is the production-target model. In the POC, the CRDs are applied directly via the dynamic client; in production they are applied by ArgoCD after the management API writes them to git.

KIND (KUBERNETES IN DOCKER):
kind (Kubernetes IN Docker) provides a full multi-node Kubernetes cluster running inside Docker containers on a developer laptop. This enables the same Kubernetes tooling, CRDs, and client libraries to be used in local development as in production. A single kind cluster replaces what would be multiple EKS or on-prem clusters in production, making the POC accessible to any engineer with Docker installed.

REST XIDS (CURRENT) vs GRPC ADS (TARGET):
REST xDS was chosen for the POC because it is simpler to implement and debug (plain HTTP GET, JSON response). The Envoy gateway polls GET /routes?fleet_id={id} every few seconds. This is sufficient for development but introduces a polling lag and is not suitable for production at scale. The target architecture migrates to gRPC Aggregated Discovery Service (ADS), which is push-based and handles all xDS resource types (listeners, clusters, routes, endpoints) over a single long-lived connection.

GITHUB FOR GITOPS:
GitHub provides a familiar pull request workflow that engineers can use for route change review. Every management API write creates a commit in the fleet's GitHub repo, giving a full audit trail accessible to anyone with repo access. The PR workflow can be used to enforce additional approval steps (e.g. a payments team lead must approve route changes on fleet-access). The platform uses HTTPS token authentication for git operations (GITOPS_GITHUB_TOKEN) to avoid SSH key management complexity.

ENVOY:
Industry-standard proxy with native xDS support, gRPC proxying, HTTP/2 and HTTP/3, and a pluggable filter chain. Chosen over NGINX (less xDS support, older architecture) and Traefik (less control over auth filter chain). The xDS integration is the key technical reason: Envoy can be hot-reloaded with new route config without pod restarts, which is critical for a platform that needs to propagate route changes in near-real-time.

OPENTELEMETRY + JAEGER:
The management API emits traces using OpenTelemetry. Route create, update, and delete operations are traced with span attributes (route.id, route.path). Traces are sent to the Jaeger instance (cp-jaeger) for visualisation. This provides end-to-end request observability across the control plane.

---

## 16. DEPLOYMENT AND LOCAL DEVELOPMENT

LOCAL SETUP:
The POC runs via a combination of Docker Compose (for the control plane services: management-api, envoy-control-plane, PostgreSQL, Jaeger, console-nginx) and a kind Kubernetes cluster (ingress-cp) for the data plane fleet pods. The kind cluster is created with a specific name and a kubeconfig that the management API uses for its K8s clients.

ENVIRONMENT VARIABLES (management-api):
- PORT: HTTP listen port (default: 8003)
- ENVOY_CONTROL_PLANE_URL: xDS server URL (default: http://envoy-control-plane:8080)
- KONG_ADMIN_PROXY_URL: Kong admin proxy URL (default: http://kong-admin-proxy:8102)
- KONG_ADMIN_URL: Kong admin direct URL (default: http://gateway-kong:8001)
- ORCHESTRATION_MODE: docker or k8s (determines which Orchestrator implementation is used)
- MANAGEMENT_API_KEY: if set, enables API key auth on all endpoints except /health
- GITOPS_GITHUB_TOKEN: GitHub PAT with repo scope (enables per-fleet GitHub repos)
- GITOPS_GITHUB_USERNAME: GitHub username or org
- GITOPS_CLUSTER_NAMES: comma-separated cluster names for multi-cluster mode
- DP_KUBECONFIG: path to kubeconfig for data plane cluster (cross-cluster access)
- DP_CLUSTER_CONTEXT: kubeconfig context name for data plane cluster

ENVIRONMENT VARIABLES (mcp-server):
- MANAGEMENT_API_URL: management API base URL
- MANAGEMENT_API_KEY: forwarded as Bearer token to management API

DATABASE SEEDING:
The management API seeds the database on first startup (when the routes table is empty). The seed creates 12 data-plane fleets, 16 control-plane fleets, 19 routes, 17 fleet instances, and 16 fleet nodes. Seeded fleet IDs use human-readable slugs (fleet-jpmm, fleet-execute) for backwards compatibility; dynamically created fleets use UUIDs.

CLUSTER NAME: The kind cluster is named ingress-cp. The ORCHESTRATION_MODE=k8s activates the K8sOrchestrator. Fleet pods are created in the ingress-dp namespace. The management API itself runs outside the cluster (in Docker Compose) and uses the local kubeconfig to reach the kind cluster API server.

---

## 17. LIVE SYSTEM STATE SUMMARY (as of document generation, April 2026)

FLEETS CURRENTLY HEALTHY (running pods):
- fleet-jpmm (JPMM, Markets, mixed, jpmm.jpm.com) — 3 nodes (2 envoy + 1 kong)
- fleet-access (JPMA, Payments, envoy, access.jpm.com) — 1 envoy node
- fleet-authn (AuthN, xCIB, envoy, login.jpm.com) — 1 envoy node
- fleet-authz (AuthZ, xCIB, envoy, authz.jpm.com) — 1 envoy node
- fleet-console (Console, xCIB, envoy, console.jpm.com) — 2 envoy nodes
- fleet-digital (JPMDB, Payments, envoy, digital-banking.jpm.com) — healthy
- Test fleet (UUID 8b5f4006-..., envoy, test.jpm.com) — healthy
- All 16 control plane fleets: healthy

FLEETS NOT DEPLOYED / SUSPENDED:
- fleet-execute (suspended by operator)
- fleet-iq, fleet-mobile, fleet-smb, fleet-pdp, fleet-secsvcs (not_deployed — config exists, no pods running)

ACTIVE ROUTES: 13 routes with status=active across 7 hostnames
INACTIVE ROUTES: 14 routes with status=inactive (config present, fleet not yet deployed)

GITOPS STATUS: mode=k8s, cluster data-plane-1, sync_status=progressing

RECENT NOTABLE OPERATIONS (from audit log):
- Multiple iterations of /claude lambda route on test.jpm.com — deployed, deleted, and redeployed by MCP agent
- Fleet-level status changes (suspend/resume on fleet-execute visible from node status=stopped)
