# Ingress POC — Build Specification
> Hand this document to Claude Code. It contains everything needed to build the project from scratch. Do not deviate from the architecture described here without flagging it first.

> **xDS simplification note:** This POC uses REST xDS (polling) for the Envoy control plane rather than gRPC ADS. This is a deliberate POC simplification — the production architecture specifies gRPC ADS. The dynamic route loading behaviour is identical. Do not implement gRPC ADS here.

> **GitOps simplification note:** The production architecture uses Bitbucket (on-prem) as the source of truth with ArgoCD applying manifests to the cluster. This POC replaces that path with a direct Management API write to the gateway control plane. The Console must display a visible banner on route submit: "In production this change would be committed to Bitbucket and applied by ArgoCD. In this POC the Management API writes directly to the gateway control plane." This is intentional — do not add Bitbucket or ArgoCD.

> **L3 simplification note:** The production architecture includes a PSaaS+ / CTC Edge regional perimeter between the CDN/WAF layer and the gateways. This POC includes a `mock-psaas` service that simulates this layer: it forwards the request, injects PSaaS-style headers, emits an OTEL span, and re-originates TLS (simulated via a header annotation). This completes the L3 layer for tracing purposes without requiring real perimeter infrastructure.

> **Observability:** Every service emits OpenTelemetry spans. W3C Trace Context (`traceparent` / `tracestate`) is propagated across all hops. Mock infrastructure services emit spans that look like real infrastructure telemetry. All traces are collected by Jaeger all-in-one, which provides the trace waterfall UI, span attribute inspector, and service dependency graph.

---

## 1. Overview

A runnable demonstration of a unified internet ingress architecture covering both the data plane and the control plane. Real Kong and Envoy containers fronting mock backend services, a working auth pipeline (PKCE + DPoP + OPA), an Ingress Registry with drift detection, full distributed tracing via OpenTelemetry, and automated deployment to Render via GitHub Actions.

**Full request path (data plane):**
```
Client
  → mock-akamai-gtm   (L1 — GTM)
  → mock-akamai-edge  (L2 — CDN/WAF)
  → mock-psaas        (L3 — Regional perimeter)
  → gateway-envoy or gateway-kong  (L4 — auth enforcement)
  → svc-web or svc-api             (L5 — backend service)
```

**Control plane path:**
```
Console
  → Management API (policy validation + Registry write)
  → Envoy control plane (REST xDS) | Kong admin proxy (Admin API)
  → gateways pick up routes within 5 seconds

Management API also polls actual gateway state every 10 seconds:
  → Ingress Registry (desired vs actual comparison)
  → Drift detected → surfaced in Console drift dashboard
```

Every data plane hop is a traced span. The complete journey is visible in Jaeger from simulated CDN edge to L5 service response.

**Primary demo flows:**
1. Route deployment — engineer submits route in Console → gateway picks it up live within 5 seconds
2. Authenticated request succeeding — full auth pipeline visible in Console + full trace in Jaeger
3. Unauthenticated request rejected — 401 at pre-filter, trace shows exactly where
4. Session revocation — request succeeds, operator revokes, next request blocked
5. OPA policy deny — wrong role hits restricted route, 403 with deny reason visible in trace
6. Drift detection — deactivate a route in Console, drift dashboard shows desired ≠ actual
7. Trace explorer — click any request in Console to open its Jaeger waterfall trace

---

## 2. Repository layout

```
ingress-poc/
├── render.yaml
├── docker-compose.yml
├── .github/
│   └── workflows/
│       └── deploy.yml
├── shared/
│   └── otel.py                  # Shared OTEL setup — imported by all Python services
├── auth-service/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── envoy-control-plane/
│   ├── Dockerfile
│   ├── main.py                  # REST xDS server (POC simplification — prod uses gRPC ADS)
│   └── requirements.txt
├── gateway-envoy/
│   ├── Dockerfile
│   └── envoy-bootstrap.yaml
├── gateway-kong/
│   ├── Dockerfile
│   └── kong.yaml
├── kong-admin-proxy/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── mock-akamai-gtm/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── mock-akamai-edge/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── mock-psaas/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── opa/
│   ├── Dockerfile
│   ├── opa-config.yaml
│   └── policies/
│       ├── coarse.rego
│       └── fine.rego
├── management-api/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
├── console/
│   ├── Dockerfile
│   ├── package.json
│   ├── vite.config.js
│   └── src/
│       ├── main.jsx
│       ├── App.jsx
│       └── pages/
│           ├── Dashboard.jsx
│           ├── Routes.jsx
│           ├── RequestLog.jsx
│           ├── Sessions.jsx
│           ├── Traces.jsx
│           └── Login.jsx
├── svc-web/
│   ├── Dockerfile
│   ├── main.py
│   └── requirements.txt
└── svc-api/
    ├── Dockerfile
    ├── main.py
    └── requirements.txt
```

---

## 3. Observability architecture

### 3.1 OpenTelemetry — shared setup

All Python services use the same `shared/otel.py`. This file is copied into each service's Docker build context.

**`shared/otel.py`:**
```python
import os
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.propagate import set_global_textmap
from opentelemetry.propagators.composite import CompositePropagator
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator
from opentelemetry.baggage.propagation import W3CBaggagePropagator

def init_otel(service_name: str, app=None):
    otlp_endpoint = os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4318")

    resource = Resource.create({
        "service.name": service_name,
        "service.version": "1.0.0",
        "deployment.environment": os.getenv("ENVIRONMENT", "demo"),
    })

    exporter = OTLPSpanExporter(endpoint=f"{otlp_endpoint}/v1/traces")
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)

    # W3C Trace Context + W3C Baggage — primary propagation format
    set_global_textmap(CompositePropagator([
        TraceContextTextMapPropagator(),
        W3CBaggagePropagator(),
    ]))

    if app is not None:
        FastAPIInstrumentor.instrument_app(app)

    return trace.get_tracer(service_name)


def get_trace_headers() -> dict:
    """Inject current trace context into a dict for outgoing httpx requests."""
    from opentelemetry.propagate import inject
    headers = {}
    inject(headers)
    return headers
```

**Call on startup in every service:**
```python
from otel import init_otel
app = FastAPI(...)
tracer = init_otel("service-name", app)
```

**OTEL dependencies — add to every service's requirements.txt:**
```
opentelemetry-sdk==1.24.0
opentelemetry-instrumentation-fastapi==0.45b0
opentelemetry-exporter-otlp-proto-http==1.24.0
```

**Outgoing HTTP calls — always inject trace context:**
```python
from otel import get_trace_headers

async with httpx.AsyncClient() as client:
    r = await client.post(
        url,
        headers={**other_headers, **get_trace_headers()},
    )
```

---

### 3.2 Jaeger — trace collector and UI

**Image:** `jaegertracing/all-in-one:latest`

**Ports:**
- `4318` — OTLP HTTP ingest (used by all Python services)
- `4317` — OTLP gRPC ingest (used by Envoy, OPA)
- `16686` — Jaeger UI

**Env:** `COLLECTOR_OTLP_ENABLED=true`

No other configuration. In-memory storage is fine for POC.

**What the Jaeger UI provides:**
- **Trace search** — filter by service name, operation, tags (`auth.result=REJECT`, `dpop.valid=false`), duration range, time range
- **Trace waterfall** — nested span view showing the complete request path with duration bars. For a successful authenticated request this looks like:
  ```
  akamai.gtm           ████░░░░░░░░░░░░░░░░░░░  8ms
    akamai.edge          ████████░░░░░░░░░░░░░  32ms
      psaas.perimeter      ██████░░░░░░░░░░░░░  28ms
        kong-gateway         ████████████░░░░░  45ms
          ext_authz             ████████░░░░░░  18ms
            dpop.verify              ████░░░░░  4ms
            revoke_cache.check       ██░░░░░░░  1ms
          auth.opa_coarse           ██████░░░░  6ms
          context_propagator        ░░░░░░░░░░  0ms
          svc-api                ████████████░  14ms
            opa.fine                     ████░  3ms
  ```
- **Span detail** — click any span to inspect all attributes (`auth.step`, `dpop.valid`, `opa.allow`, `opa.deny_reason`, `akamai.datacenter`, `session.roles` etc)
- **Service dependency graph** — auto-generated from trace data showing which services call which with request counts and error rates

**Console integration:** `{VITE_JAEGER_URL}/trace/{trace_id}` deep-links to any individual trace. Every request log entry in the Console has a `View Trace →` button using this URL.

---

### 3.3 W3C Trace Context propagation

**`traceparent` format:** `00-{32-hex-trace-id}-{16-hex-parent-span-id}-01`

The OTEL SDK extracts and propagates this automatically when using `FastAPIInstrumentor` and the `get_trace_headers()` helper on all outgoing calls.

**Header fallback in the gateway** — when a request arrives without `traceparent`, check in order:

```
1. traceparent                   — W3C (preferred)
2. x-akamai-request-id           — Akamai GTM/Ion request ID
3. x-edge-request-id             — Akamai edge alternate
4. x-b3-traceid + x-b3-spanid   — B3 legacy
5. none — generate new trace ID
```

**Fallback implementation (in both gateway-kong's kong-admin-proxy and any Envoy ext_authz handler):**
```python
import hashlib, secrets
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

def extract_or_synthesise_trace(headers: dict) -> dict:
    """
    Returns dict of span attributes describing trace origin.
    Side effect: ensures a valid traceparent is in the current context.
    """
    propagator = TraceContextTextMapPropagator()
    ctx = propagator.extract(headers)
    span = trace.get_current_span(ctx)
    if span and span.get_span_context().is_valid:
        return {"trace.origin": "w3c"}

    akamai_id = headers.get("x-akamai-request-id") or headers.get("x-edge-request-id")
    if akamai_id:
        # Deterministic trace ID from Akamai request ID
        trace_id = hashlib.sha256(akamai_id.encode()).hexdigest()[:32]
        return {
            "trace.origin": "akamai",
            "trace.synthetic": True,
            "akamai.request_id": akamai_id,
            "trace.synthesised_id": trace_id,
        }

    b3_trace = headers.get("x-b3-traceid")
    if b3_trace:
        return {"trace.origin": "b3", "b3.trace_id": b3_trace}

    return {"trace.origin": "generated"}
```

---

### 3.4 Standard span attributes

**Apply to every relevant span:**

| Attribute | Applied by | Value |
|---|---|---|
| `service.name` | All | Set via OTEL resource |
| `http.method` | All | Auto via FastAPI instrumentation |
| `http.status_code` | All | Auto |
| `http.route` | All | Auto |
| `trace.origin` | Gateway | w3c \| akamai \| b3 \| generated |
| `akamai.request_id` | Gateway, edge mock | From x-akamai-request-id header |
| `auth.step` | Gateway | pre-filter \| session-validator \| opa-coarse \| context-propagator |
| `auth.result` | Gateway | PASS \| REJECT |
| `auth.reject_reason` | Gateway | reason string on REJECT |
| `session.id` | Gateway, auth-service | sid claim |
| `session.subject` | Gateway, auth-service | sub claim |
| `session.roles` | Gateway | JSON array |
| `session.entity` | Gateway | entity claim |
| `dpop.valid` | auth-service ext-authz | true \| false |
| `dpop.jkt` | auth-service ext-authz | first 12 chars of thumbprint |
| `dpop.error` | auth-service ext-authz | error string on failure |
| `revoke_cache.hit` | auth-service ext-authz | true \| false |
| `opa.allow` | Gateway, svc-api | true \| false |
| `opa.deny_reason` | Gateway, svc-api | string on deny |
| `opa.obligations` | Gateway | JSON array |
| `route.path` | Gateway, management-api | /api/markets etc |
| `route.gateway_type` | Gateway | envoy \| kong |
| `akamai.service` | Mock Akamai | gtm \| edge |
| `akamai.datacenter` | Mock GTM | simulated DC name |
| `akamai.waf.blocked` | Mock edge | true \| false |
| `akamai.cache.hit` | Mock edge | true \| false |

---

## 4. Services

### 4.1 auth-service (FastAPI, Python)

**Responsibilities:**
- Mock IdP — PKCE auth code flow, DPoP-bound token issuance
- Session Manager — session JWT creation, JWKS endpoint, session revocation

**Key endpoints:**
```
POST /auth/authorize          — validate credentials, issue auth code
POST /auth/token              — PKCE exchange, return access_token + id_token
GET  /.well-known/jwks.json   — IdP public keys (for access tokens)
GET  /session/jwks.json       — Session Manager public keys (for session JWTs)
POST /session/create          — create session JWT bound to DPoP JWK thumbprint
POST /session/revoke/{sid}    — mark session revoked (CAEP simulation)
GET  /session/{sid}           — get session info
GET  /sessions                — list all sessions
GET  /revocations             — list revoked SIDs (polled by gateways every 2s)
POST /gateway/ext-authz       — DPoP + revoke validation called by gateways
GET  /demo/users              — list demo users
GET  /health
```

**OTEL spans:**
- `auth.pkce.authorize` — include `user.sub`, `pkce.valid`
- `auth.pkce.token` — include `pkce.verified`, `token.expiry`
- `session.create` — include `session.id`, `dpop.jkt`, `session.subject`
- `session.revoke` — include `session.id`, `revocation.ts`
- `ext_authz.validate` — include all auth attributes
  - child: `dpop.verify` — `dpop.valid`, `dpop.htm`, `dpop.htu`, `dpop.jti`, `dpop.error`
  - child: `revoke_cache.check` — `revoke_cache.hit`, `session.id`

**Session JWT payload:**
```json
{
  "iss": "<AUTH_SERVICE_URL>",
  "sub": "<user_id>",
  "sid": "<session_id>",
  "aud": "ingress-gateway",
  "iat": 1234567890,
  "exp": 1234567890,
  "email": "user@demo.local",
  "name": "User Name",
  "roles": ["trader"],
  "entity": "MARKETS",
  "client_id": "ingress-console",
  "cnf": { "jkt": "<dpop_key_thumbprint>" }
}
```

**Demo users:**
```
admin@demo.local    / demo1234   roles: [architect, platform-admin]  entity: PLATFORM
trader@demo.local   / demo1234   roles: [trader]                     entity: MARKETS
readonly@demo.local / demo1234   roles: [readonly]                   entity: OPS
```

---

### 4.2 mock-akamai-gtm (FastAPI, Python)

Simulates Akamai GTM. Front of the request path. Forwards to mock-akamai-edge. Emits spans as `service.name: akamai.gtm`.

**Behaviour:**
- Accepts any request on port 8010
- Generates `x-akamai-request-id` (UUID) if not present
- Adds `x-akamai-gtm-datacenter` header — round-robin: `us-east`, `us-west`, `eu-west`, `ap-southeast`
- Injects W3C `traceparent` header (starts new trace if none)
- Forwards to `FORWARD_TO` env var (mock-akamai-edge)

**Headers injected:**
```
x-akamai-request-id:      <uuid>
x-akamai-gtm-datacenter:  <datacenter>
x-akamai-gtm-reason:      load-balance
traceparent:               <w3c>
```

**OTEL span `akamai.gtm.forward`:**
```
akamai.service:       gtm
akamai.request_id:    <uuid>
akamai.datacenter:    <simulated>
akamai.forward_to:    mock-akamai-edge
http.status_code:     <upstream response>
```

---

### 4.3 mock-akamai-edge (FastAPI, Python)

Simulates Akamai Ion CDN + Kona WAF. Receives from GTM, WAF-checks, injects full Akamai edge headers, routes to appropriate gateway. Emits spans as `service.name: akamai.edge`.

**WAF simulation (block with 403):**
- Request path or query contains `<script>` → block, `waf.rule: xss`
- Request path contains `../` → block, `waf.rule: path-traversal`
- Missing `User-Agent` header → block, `waf.rule: bot-check`
- All other requests pass

**Cache simulation:** 20% of GET requests tagged as `akamai.cache.hit: true` (random, no actual caching).

**Routing:**
- Path starts with `/api/` → `GATEWAY_KONG_URL`
- Path starts with `/web/` → `GATEWAY_ENVOY_URL`
- Everything else → `GATEWAY_KONG_URL`

**Headers injected:**
```
x-akamai-request-id:      <from GTM or generated>
x-akamai-edgescape:       georegion=263,country_code=GB,city=LONDON,lat=51.50,long=-0.12
x-true-client-ip:         <simulated: 203.0.113.42>
x-forwarded-for:          203.0.113.42, 23.40.11.5
x-akamai-cache-status:    HIT | MISS
x-akamai-waf-status:      PASS | BLOCK
traceparent:               <propagated>
tracestate:                akamai=<request_id>
```

**OTEL span `akamai.edge.request`:**
```
akamai.service:           edge
akamai.request_id:        <from GTM header>
akamai.edge_ip:           23.40.11.5
akamai.country:           GB
akamai.waf.checked:       true
akamai.waf.blocked:       true | false
akamai.waf.block_reason:  <rule name if blocked>
akamai.cache.hit:         true | false
akamai.forward_to:        gateway-envoy | gateway-kong
http.status_code:         <response>
```

---

### 4.4 mock-psaas (FastAPI, Python)

Simulates the L3 regional perimeter (PSaaS+ / CTC Edge). Receives from mock-akamai-edge, injects perimeter headers, simulates TLS re-origination, and forwards to the appropriate gateway. Emits spans as `service.name: psaas.perimeter`.

**Behaviour:**
- Accepts any request on port 8012
- Injects perimeter headers before forwarding
- Simulates TLS re-origination by annotating the span (`tls.reoriginated: true`) — in production this is where the outer TLS session ends and a new inner TLS session begins, which is significant for DPoP channel binding
- Forwards to `GATEWAY_ENVOY_URL` for `/web/*` paths, `GATEWAY_KONG_URL` for `/api/*` paths (same routing logic as mock-akamai-edge)
- Propagates `traceparent` / `tracestate` unchanged

**Headers injected:**
```
x-psaas-region:       us-east | eu-west | ap-southeast (round-robin)
x-psaas-datacenter:   CDC1 | Farn | SG-C01 (matches region)
x-psaas-forward-ip:   <edge IP from x-forwarded-for>
x-forwarded-for:      <appended: perimeter IP>
```

**OTEL span `psaas.perimeter.forward`:**
```
psaas.region:         us-east | eu-west | ap-southeast
psaas.datacenter:     CDC1 | Farn | SG-C01
tls.reoriginated:     true
tls.note:             In production TLS terminates here and re-originates to L4. DPoP is bound to the L4 connection.
akamai.request_id:    propagated from upstream header
http.status_code:     <response>
```

**Key endpoints:**
```
ANY /{path:path}   — catch-all, forward to gateway
GET /health
```

---

### 4.5 envoy-control-plane (FastAPI, Python)

REST xDS — see §3.2 of original spec. **OTEL spans:**
- `xds.routes.poll` — `routes.active`, `routes.changed`
- `xds.snapshot.update` — `routes.count`, `version`

---

### 4.6 gateway-envoy (Envoy Docker container)

Envoy native OTEL tracing via `envoy.tracers.opentelemetry` in the listener config (served via xDS).

**Tracing config:**
```yaml
tracing:
  provider:
    name: envoy.tracers.opentelemetry
    typed_config:
      "@type": type.googleapis.com/envoy.config.trace.v3.OpenTelemetryConfig
      grpc_service:
        envoy_grpc:
          cluster_name: jaeger_cluster
        timeout: 3s
      service_name: envoy-gateway
```

Include `jaeger_cluster` in the CDS response (points to Jaeger port 4317). 100% sampling.

---

### 4.7 gateway-kong (Kong Docker container)

Kong native OTEL via the `opentelemetry` plugin. Install globally via kong-admin-proxy.

**Plugin config:**
```yaml
- name: opentelemetry
  config:
    endpoint: http://jaeger:4318/v1/traces
    resource_attributes:
      service.name: kong-gateway
    propagation:
      default_format: w3c
    sampling_rate: 1.0
```

---

### 4.8 kong-admin-proxy (FastAPI, Python)

**OTEL spans:**
- `kong.sync` — `routes.synced`, `routes.added`, `routes.removed`, `sync.duration_ms`
- `kong.route.create` / `kong.route.delete` — `route.path`, `route.team`

---

### 4.9 opa (OPA Docker container)

**`opa-config.yaml`:**
```yaml
distributed_tracing:
  type: grpc
  address: jaeger:4317
  service_name: opa-policy
  sample_percentage: 100
  encryption: "off"

decision_logs:
  console: true
```

**Run command:**
```
opa run --server --addr 0.0.0.0:8181 --config-file /opa-config.yaml /policies
```

---

### 4.10 management-api (FastAPI + SQLAlchemy, Python)

**Responsibilities:**
- Accept route intent from Console, validate against policy, store in Postgres (desired state)
- Expose routes to Envoy control plane and Kong admin proxy
- Maintain audit log
- Poll actual gateway state every 10 seconds and compare against desired state (drift detection)

**Database models:**

```python
Route:           # desired state
  id: UUID PK
  path: str UNIQUE
  hostname: str
  backend_url: str
  auth_policy: enum       # public | authenticated | roles
  allowed_roles: JSON
  methods: JSON
  status: enum            # pending | active | inactive
  team: str
  created_by: str
  gateway_type: enum      # envoy | kong | auto
  tls_required: bool
  notes: str
  created_at: float
  updated_at: float

ActualRoute:     # actual state — polled from gateways
  id: UUID PK
  route_id: UUID FK → Route
  gateway_type: str
  path: str
  actual_status: str      # active | absent
  actual_backend: str
  drift: bool
  drift_detail: str
  last_checked: float

AuditLog:
  id: UUID PK
  route_id: UUID FK
  action: str             # CREATE | UPDATE | DELETE | STATUS_CHANGE
  actor: str
  detail: str
  ts: float
```

**Drift detection (background task, every 10 seconds):**
```python
async def detect_drift():
    while True:
        # Poll Envoy control plane for current xDS snapshot
        envoy_routes = GET {ENVOY_CONTROL_PLANE_URL}/snapshot/routes

        # Poll Kong admin proxy for currently configured routes
        kong_routes = GET {KONG_ADMIN_PROXY_URL}/sync-status/routes

        for route in get_all_routes_from_db():
            actual = lookup(route.path, envoy_routes if route.gateway_type == "envoy" else kong_routes)
            drift = (actual is None and route.status == "active") \
                 or (actual is not None and route.status == "inactive") \
                 or (actual is not None and actual.backend_url != route.backend_url)
            upsert_actual_route(route.id, actual, drift=drift)

        await asyncio.sleep(10)
```

**Endpoints:**
```
GET    /routes                    — list routes (filter: status, gateway_type)
GET    /routes/{id}
POST   /routes                    — create (validates policy first)
PUT    /routes/{id}
PUT    /routes/{id}/status        — activate | inactive
DELETE /routes/{id}
GET    /audit-log
GET    /policy/validate
GET    /actuals                   — actual gateway state for all routes
GET    /drift                     — routes where desired != actual (shortcut for Console)
GET    /health
```

**`/drift` response:**
```json
[
  {
    "route_id": "uuid",
    "path": "/api/admin",
    "desired_status": "inactive",
    "actual_status": "active",
    "gateway_type": "kong",
    "drift": true,
    "drift_detail": "Route is inactive in Registry but still present in Kong — gateway has not yet reconciled",
    "last_checked": 1234567890
  }
]
```

**Policy validation rules:** path starts with `/`, valid backend URL, roles policy needs roles, admin paths cannot be public, TLS required for non-localhost, team required.

**Default routes (seeded on startup):**
```
/api/public    → svc-api    auth: public                                    gateway: kong
/api/readonly  → svc-api    auth: authenticated                             gateway: kong
/api/markets   → svc-api    auth: roles [trader, architect, platform-admin] gateway: kong
/api/admin     → svc-api    auth: roles [architect, platform-admin]         gateway: kong
/web/portal    → svc-web    auth: authenticated                             gateway: envoy
/web/admin     → svc-web    auth: roles [architect, platform-admin]         gateway: envoy
```

**OTEL spans:**
- `route.create` / `route.update` / `route.delete` / `route.status_change`
- `policy.validate` — `violations.count`
- `registry.drift_check` — `routes.checked`, `routes.drifted`, `check.duration_ms`

---

### 4.11 console (React + Vite)

**Styling:** Dark theme, monospace font. Colours as per original spec.

**GitOps simplification banner:** Show a persistent amber banner in the header on all pages:
> "POC mode — route changes write directly to the gateway control plane. In production this would commit to Bitbucket and be applied by ArgoCD."

**Pages:**

**Dashboard** (`/`)
- Summary cards: active routes, drifted routes (red if >0), active sessions, requests/5min, rejections/5min
- Live request feed (last 10, refresh every 2s)
- Gateway health status: Envoy, Kong, OPA, Auth Service, Envoy Control Plane, Kong Admin Proxy
- Trace Explorer card: last 5 trace IDs with deep-link to Jaeger

**Routes** (`/routes`)
- Table: path, hostname, auth policy, gateway type, status, team
- Drift indicator per row: green dot (in sync) | red dot (drifted) | grey dot (unknown) — pulled from `/actuals`
- Add route form with inline policy validation
- GitOps banner on submit confirmation: "In production this change would be committed to Bitbucket and applied by ArgoCD."
- Audit log panel below table

**Drift Dashboard** (`/drift`)
- Table of all routes with desired vs actual state comparison
- Columns: path, gateway, desired status, actual status, desired backend, actual backend, drift status, last checked
- Drifted rows highlighted in amber
- Refresh button — forces `/actuals` poll
- Explanation panel: "Drift is detected when the Ingress Registry (desired state) does not match what the gateway is actually serving (actual state). The gateway control plane reconciles every 5 seconds — transient drift is expected during route changes."
- Demo flow: deactivate a route → watch it go amber → wait 5s → watch it resolve

**Request Log** (`/request-log`)
- Live log: timestamp, method, path, result, status, latency, subject, roles, trace_id (truncated, click-to-copy)
- Expand row: full auth pipeline trace with per-step PASS/REJECT + reason
- `View Trace →` button → `{JAEGER_URL}/trace/{trace_id}` in new tab

**Sessions** (`/sessions`)
- Table: SID, email, name, roles, entity, created at, status
- Revoke button per row — calls auth-service POST /session/revoke/{sid}
- After revoke: row turns red, tooltip "Next request from this session will be blocked"

**Traces** (`/traces`)
- Iframe embedding Jaeger UI at `{VITE_JAEGER_URL}` with a pre-populated service filter
- Fallback if iframe blocked: list of recent trace IDs with deep-links and inline span summary

**Login** (`/login`)
- Demo user dropdown + password field
- PKCE + DPoP flow (keypair generated via WebCrypto, session JWT stored in sessionStorage)

**Request tester** (panel on Request Log page)
- Path dropdown, method selector, user selector
- Generates fresh DPoP proof per request
- Sends via `{VITE_GATEWAY_URL}` (mock-akamai-gtm — full simulated path)
- Shows raw response + pipeline trace + Jaeger trace link inline

---

### 4.12 svc-web and svc-api (FastAPI, Python)

**OTEL spans:**
- `service.request` — all `x-auth-*` header values as attributes, `service.name`, `request.path`
- `opa.fine` (child) — for fine-grained L5 OPA check

**Response format unchanged from original spec.**

---

## 5. Auth pipeline

### DPoP flow (browser)

```javascript
// Generate keypair at login
const keyPair = await crypto.subtle.generateKey(
  { name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"]
);
const publicJwk = await crypto.subtle.exportKey("jwk", keyPair.publicKey);
const privateJwk = await crypto.subtle.exportKey("jwk", keyPair.privateKey);

// Generate DPoP proof per request
async function generateDpopProof(method, url, privateJwk, publicJwk) {
  const header = { typ: "dpop+jwt", alg: "ES256",
    jwk: { kty: publicJwk.kty, crv: publicJwk.crv, x: publicJwk.x, y: publicJwk.y } };
  const payload = {
    jti: crypto.randomUUID(), htm: method.toUpperCase(),
    htu: url.split("?")[0], iat: Math.floor(Date.now() / 1000),
  };
  // Sign with crypto.subtle.sign ECDSA/SHA-256, return compact JWT
}

// Request
fetch(gatewayUrl + path, {
  headers: {
    "Authorization": `Bearer ${sessionJwt}`,
    "DPoP": await generateDpopProof(method, gatewayUrl + path, privateJwk, publicJwk),
  }
});
```

### Gateway pipeline

```
Pre-filter:         JWT validation (local JWKS cache)     → span: auth.pre_filter
Component 1:        Session Validator (ext_authz)          → span: auth.session_validator
  child:            DPoP verify (htm, htu, iat, jti)       → span: dpop.verify
  child:            Revoke Cache check                      → span: revoke_cache.check
Component 2:        OPA coarse (HTTP POST)                  → span: auth.opa_coarse
Component 3:        Context Propagator (allowlist)          → span: auth.context_propagator
  → Strip ALL client identity headers
  → Set: x-auth-subject, x-auth-session-id, x-auth-roles,
         x-auth-entity, x-auth-client-id, x-auth-dpop-jkt,
         x-auth-email, x-auth-name, x-auth-obligations, x-request-id
```

All pipeline spans are children of the root gateway span, which is a continuation of the trace from mock-akamai-edge. The full Jaeger trace shows: `akamai.gtm → akamai.edge → kong/envoy-gateway → auth.session_validator → auth.opa_coarse → svc-api`.

---

## 6. OPA policies

`policies/coarse.rego`:
```rego
package ingress.policy.coarse

default allow = false

allow {
    not_revoked
    has_roles
    route_permitted
}

not_revoked { not input.session.revoked }
has_roles { count(input.session.roles) > 0 }

route_permitted {
    role := input.session.roles[_]
    route_acl[input.route][_] == role
}

route_permitted { route_acl[input.route][_] == "*" }

# Relationship-based mock (production uses SpiceDB)
route_permitted {
    input.route == "/api/markets"
    input.session.entity == "MARKETS"
    input.session.roles[_] == "trader"
}

route_acl := {
    "/api/public":   ["*"],
    "/api/readonly": ["readonly", "trader", "architect", "platform-admin"],
    "/api/markets":  ["trader", "architect", "platform-admin"],
    "/api/admin":    ["architect", "platform-admin"],
    "/web/portal":   ["*"],
    "/web/admin":    ["architect", "platform-admin"],
}

deny_reason = "session revoked"          { input.session.revoked }
deny_reason = "no valid roles"           { not has_roles }
deny_reason = "role not permitted"       { not route_permitted; has_roles; not_revoked }

obligations[ob] { allow; ob := {"type": "audit-log", "required": true} }
obligations[ob] { allow; input.session.entity == "MARKETS"
                  ob := {"type": "data-classification", "level": "confidential"} }
```

---

## 7. Render deployment

```yaml
services:
  - name: ingress-jaeger
    type: web
    env: docker
    image:
      url: jaegertracing/all-in-one:latest
    envVars:
      - key: COLLECTOR_OTLP_ENABLED
        value: "true"

  - name: ingress-auth-service
    type: web
    env: docker
    dockerfilePath: ./auth-service/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: PORT
        value: 8001
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-opa
    type: web
    env: docker
    dockerfilePath: ./opa/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-management-api
    type: web
    env: docker
    dockerfilePath: ./management-api/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: DATABASE_URL
        fromDatabase:
          name: ingress-registry-db
          property: connectionString
      - key: SVC_WEB_URL
        fromService:
          name: ingress-svc-web
          type: web
          property: host
      - key: SVC_API_URL
        fromService:
          name: ingress-svc-api
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-envoy-control-plane
    type: web
    env: docker
    dockerfilePath: ./envoy-control-plane/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: MANAGEMENT_API_URL
        fromService:
          name: ingress-management-api
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-gateway-envoy
    type: web
    env: docker
    dockerfilePath: ./gateway-envoy/Dockerfile

  - name: ingress-gateway-kong
    type: web
    env: docker
    dockerfilePath: ./gateway-kong/Dockerfile

  - name: ingress-kong-admin-proxy
    type: web
    env: docker
    dockerfilePath: ./kong-admin-proxy/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: MANAGEMENT_API_URL
        fromService:
          name: ingress-management-api
          type: web
          property: host
      - key: KONG_ADMIN_URL
        fromService:
          name: ingress-gateway-kong
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-mock-akamai-gtm
    type: web
    env: docker
    dockerfilePath: ./mock-akamai-gtm/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: FORWARD_TO
        fromService:
          name: ingress-mock-akamai-edge
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-mock-akamai-edge
    type: web
    env: docker
    dockerfilePath: ./mock-akamai-edge/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: GATEWAY_ENVOY_URL
        fromService:
          name: ingress-mock-psaas
          type: web
          property: host
      - key: GATEWAY_KONG_URL
        fromService:
          name: ingress-mock-psaas
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-mock-psaas
    type: web
    env: docker
    dockerfilePath: ./mock-psaas/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: GATEWAY_ENVOY_URL
        fromService:
          name: ingress-gateway-envoy
          type: web
          property: host
      - key: GATEWAY_KONG_URL
        fromService:
          name: ingress-gateway-kong
          type: web
          property: host
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-svc-web
    type: web
    env: docker
    dockerfilePath: ./svc-web/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: SERVICE_NAME
        value: svc-web
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-svc-api
    type: web
    env: docker
    dockerfilePath: ./svc-api/Dockerfile
    healthCheckPath: /health
    envVars:
      - key: SERVICE_NAME
        value: svc-api
      - key: OTEL_EXPORTER_OTLP_ENDPOINT
        fromService:
          name: ingress-jaeger
          type: web
          property: host

  - name: ingress-console
    type: web
    env: docker
    dockerfilePath: ./console/Dockerfile
    envVars:
      - key: VITE_AUTH_SERVICE_URL
        fromService:
          name: ingress-auth-service
          type: web
          property: host
      - key: VITE_MANAGEMENT_API_URL
        fromService:
          name: ingress-management-api
          type: web
          property: host
      - key: VITE_GATEWAY_URL
        fromService:
          name: ingress-mock-akamai-gtm
          type: web
          property: host
      - key: VITE_JAEGER_URL
        fromService:
          name: ingress-jaeger
          type: web
          property: host

databases:
  - name: ingress-registry-db
    databaseName: ingress_registry
    plan: free
```

**Note:** `VITE_GATEWAY_URL` now points to `ingress-mock-akamai-gtm`. All demo requests go through the full simulated Akamai path.

---

## 8. GitHub Actions pipeline

```yaml
name: Deploy to Render
on:
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Trigger Render deploy
        run: curl -X POST "${{ secrets.RENDER_DEPLOY_HOOK_URL }}"
```

**GitHub secret required:** `RENDER_DEPLOY_HOOK_URL`

---

## 9. docker-compose.yml (local dev)

```yaml
version: "3.9"
services:
  jaeger:
    image: jaegertracing/all-in-one:latest
    ports: ["16686:16686", "4317:4317", "4318:4318"]
    environment:
      COLLECTOR_OTLP_ENABLED: "true"

  auth-service:
    build: ./auth-service
    ports: ["8001:8001"]
    environment:
      PORT: "8001"
      AUTH_SERVICE_URL: "http://localhost:8001"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger]

  opa:
    build: ./opa
    ports: ["8181:8181"]
    environment:
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger]

  management-api:
    build: ./management-api
    ports: ["8003:8003"]
    environment:
      DATABASE_URL: "sqlite:///./registry.db"
      SVC_WEB_URL: "http://svc-web:8004"
      SVC_API_URL: "http://svc-api:8005"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger, auth-service]

  envoy-control-plane:
    build: ./envoy-control-plane
    ports: ["8080:8080"]
    environment:
      MANAGEMENT_API_URL: "http://management-api:8003"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger, management-api]

  gateway-envoy:
    build: ./gateway-envoy
    ports: ["8000:8000"]
    environment:
      XDS_HOST: "envoy-control-plane"
      XDS_PORT: "8080"
    depends_on: [envoy-control-plane, auth-service, opa, jaeger]

  gateway-kong:
    build: ./gateway-kong
    ports: ["8100:8000", "8101:8001"]
    depends_on: [auth-service, opa, jaeger]

  kong-admin-proxy:
    build: ./kong-admin-proxy
    ports: ["8102:8102"]
    environment:
      MANAGEMENT_API_URL: "http://management-api:8003"
      KONG_ADMIN_URL: "http://gateway-kong:8001"
      AUTH_SERVICE_URL: "http://auth-service:8001"
      OPA_URL: "http://opa:8181"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [gateway-kong, management-api, jaeger]

  mock-akamai-gtm:
    build: ./mock-akamai-gtm
    ports: ["8010:8010"]
    environment:
      FORWARD_TO: "http://mock-akamai-edge:8011"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [mock-akamai-edge, jaeger]

  mock-akamai-edge:
    build: ./mock-akamai-edge
    ports: ["8011:8011"]
    environment:
      GATEWAY_ENVOY_URL: "http://mock-psaas:8012"
      GATEWAY_KONG_URL: "http://mock-psaas:8012"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [mock-psaas, jaeger]

  mock-psaas:
    build: ./mock-psaas
    ports: ["8012:8012"]
    environment:
      GATEWAY_ENVOY_URL: "http://gateway-envoy:8000"
      GATEWAY_KONG_URL: "http://gateway-kong:8000"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [gateway-envoy, gateway-kong, jaeger]

  svc-web:
    build: ./svc-web
    ports: ["8004:8004"]
    environment:
      SERVICE_NAME: "svc-web"
      PORT: "8004"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger]

  svc-api:
    build: ./svc-api
    ports: ["8005:8005"]
    environment:
      SERVICE_NAME: "svc-api"
      PORT: "8005"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://jaeger:4318"
    depends_on: [jaeger]

  console:
    build: ./console
    ports: ["3000:3000"]
    environment:
      VITE_AUTH_SERVICE_URL: "http://localhost:8001"
      VITE_MANAGEMENT_API_URL: "http://localhost:8003"
      VITE_GATEWAY_URL: "http://localhost:8010"
      VITE_JAEGER_URL: "http://localhost:16686"
    depends_on: [management-api, auth-service, mock-akamai-gtm]
```

---

## 10. Dockerfile patterns

All Python services:
```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY ../shared/otel.py ./otel.py
COPY . .
CMD ["python", "main.py"]
```

Console:
```dockerfile
FROM node:20-alpine AS build
WORKDIR /app
COPY package*.json .
RUN npm ci
COPY . .
ARG VITE_AUTH_SERVICE_URL
ARG VITE_MANAGEMENT_API_URL
ARG VITE_GATEWAY_URL
ARG VITE_JAEGER_URL
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
```

OPA:
```dockerfile
FROM openpolicyagent/opa:latest
COPY policies/ /policies/
COPY opa-config.yaml /opa-config.yaml
EXPOSE 8181
CMD ["run", "--server", "--addr", "0.0.0.0:8181", "--config-file", "/opa-config.yaml", "/policies"]
```

Envoy, Kong: unchanged from original spec.

---

## 11. Constraints and notes for Claude Code

**Observability:**
- Every Python service must call `init_otel(service_name, app)` on startup. `otel.py` lives in `shared/` and is copied into each service's Docker build context via the Dockerfile.
- `OTEL_EXPORTER_OTLP_ENDPOINT` is the single env var controlling where traces go. Default `http://jaeger:4318`.
- All outgoing `httpx` calls must inject trace context using `get_trace_headers()`.
- Mock Akamai and PSaaS services must set `service.name` to `akamai.gtm`, `akamai.edge`, `psaas.perimeter` in their OTEL resource.
- All gateway auth pipeline steps must be child spans of the root request span.
- The request log from the gateway must include `trace_id` in every entry.
- 100% trace sampling throughout.

**Trace context propagation:**
- W3C `traceparent` / `tracestate` are the primary headers. OTEL SDK handles extraction and injection automatically.
- Akamai header fallback logic (`x-akamai-request-id` → synthesised `traceparent`) lives in the gateway only.
- Mock infrastructure services always produce W3C headers.

**Request path:**
- `mock-akamai-edge` forwards to `mock-psaas`, not directly to the gateways.
- `mock-psaas` forwards to the appropriate gateway based on path prefix.
- `VITE_GATEWAY_URL` in the Console points to `mock-akamai-gtm` — all demo requests go through the full simulated L1→L2→L3→L4 path.

**Control plane — simplifications that must be documented in the UI:**
- The GitOps banner must appear on every page. Exact text: "POC mode — route changes write directly to the gateway control plane. In production this would commit to Bitbucket and be applied by ArgoCD."
- Drift detection polls every 10 seconds. The Drift Dashboard must show a "Last checked" timestamp and a manual Refresh button.
- `envoy-control-plane` must expose `GET /snapshot/routes` returning the current route list it has served via xDS.
- `kong-admin-proxy` must expose `GET /sync-status/routes` returning the route list currently configured in Kong.
- These endpoints are used by management-api's drift detection — they are not part of the xDS or Admin API protocols.

**Architecture:**
- The envoy-control-plane uses REST xDS, not gRPC ADS. No grpcio dependencies.
- No organisation names, internal system names, or proprietary terms anywhere.
- CORS permissive on all backend services.

---

## 12. Build order for Claude Code

1. `shared/otel.py` — must exist before any Python service is built
2. `auth-service`
3. `opa`
4. `svc-web` and `svc-api`
5. `management-api` — includes drift detection background task, `/actuals`, `/drift` endpoints
6. `envoy-control-plane` — must expose `/snapshot/routes` endpoint for drift detection polling
7. `gateway-envoy`
8. `kong-admin-proxy` — must expose `/sync-status/routes` endpoint for drift detection polling
9. `gateway-kong`
10. `mock-psaas` — depends on gateway URLs
11. `mock-akamai-edge` — now forwards to mock-psaas (not directly to gateways)
12. `mock-akamai-gtm` — depends on mock-akamai-edge
13. `console` — all pages including Drift Dashboard, GitOps banner throughout
14. `render.yaml` and `docker-compose.yml`
15. `.github/workflows/deploy.yml`

---

*Ingress POC Build Spec · v1.3 · March 2026*
