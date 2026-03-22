import os
import json
import time
import asyncio

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8102"))
MANAGEMENT_API_URL = os.getenv("MANAGEMENT_API_URL", "http://management-api:8003")
KONG_ADMIN_URL = os.getenv("KONG_ADMIN_URL", "http://gateway-kong:8001")
AUTH_SERVICE_URL = os.getenv("AUTH_SERVICE_URL", "http://auth-service:8001")
OPA_URL = os.getenv("OPA_URL", "http://opa:8181")

app = FastAPI(title="Kong Admin Proxy", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("kong-admin-proxy", app)

# Track synced routes
synced_routes: dict[str, dict] = {}  # path -> route info


def build_declarative_config(desired_routes: list[dict]) -> dict:
    """Build Kong declarative config with upstreams, services, routes, and health checks."""
    services = []
    routes_list = []
    upstreams = []
    seen_upstreams = set()

    for route in desired_routes:
        hostname = route.get("hostname", "*")
        host_slug = hostname.replace(".", "-").replace("*", "wildcard") if hostname != "*" else "wildcard"
        path_slug = route['path'].replace('/', '-').strip('-')
        svc_name = f"svc-{host_slug}-{path_slug}"
        upstream_name = f"upstream-{host_slug}-{path_slug}"
        backend = route["backend_url"].rstrip("/")

        # Parse backend for upstream target
        backend_no_scheme = backend.split("://")[-1]
        backend_host = backend_no_scheme.split(":")[0]
        backend_port = int(backend_no_scheme.split(":")[1]) if ":" in backend_no_scheme else 80
        scheme = backend.split("://")[0] if "://" in backend else "http"

        # Create upstream with health checks (deduplicate by target)
        if upstream_name not in seen_upstreams:
            seen_upstreams.add(upstream_name)
            upstreams.append({
                "name": upstream_name,
                "targets": [{"target": f"{backend_host}:{backend_port}", "weight": 100}],
                "healthchecks": {
                    "active": {
                        "type": "http",
                        "http_path": "/health",
                        "timeout": 2,
                        "healthy": {"interval": 10, "successes": 2},
                        "unhealthy": {"interval": 5, "http_failures": 3, "tcp_failures": 3, "timeouts": 3},
                    },
                    "passive": {
                        "type": "http",
                        "healthy": {"successes": 2},
                        "unhealthy": {"http_failures": 5},
                    },
                },
            })

        # Service points at upstream instead of raw backend
        services.append({
            "name": svc_name,
            "host": upstream_name,
            "port": backend_port,
            "protocol": scheme,
        })

        route_entry = {
            "name": f"route-{host_slug}-{path_slug}",
            "service": svc_name,
            "paths": [route["path"]],
            "methods": route.get("methods", ["GET", "POST", "PUT", "DELETE"]),
            "strip_path": False,
        }
        if hostname and hostname != "*":
            route_entry["hosts"] = [hostname]
        routes_list.append(route_entry)

    return {
        "_format_version": "3.0",
        "upstreams": upstreams,
        "services": services,
        "routes": routes_list,
        "plugins": [
            {
                "name": "cors",
                "config": {
                    "origins": ["*"],
                    "methods": ["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"],
                    "headers": ["Accept", "Authorization", "Content-Type", "DPoP",
                                "X-Akamai-Request-Id", "traceparent", "tracestate", "User-Agent"],
                    "credentials": True,
                },
            },
        ],
    }


async def post_declarative_config(config: dict) -> bool:
    """Post full declarative config to Kong /config endpoint (DB-less mode)."""
    async with httpx.AsyncClient() as client:
        r = await client.post(
            f"{KONG_ADMIN_URL}/config",
            json=config,
            headers={**get_trace_headers(), "Content-Type": "application/json"},
            timeout=10.0,
        )
        return r.status_code in (200, 201)


async def sync_routes():
    """Sync routes from management-api to Kong via declarative config."""
    global synced_routes
    while True:
        try:
            with tracer.start_as_current_span("kong.sync") as span:
                start = time.time()
                async with httpx.AsyncClient() as client:
                    r = await client.get(f"{MANAGEMENT_API_URL}/routes?gateway_type=kong&status=active",
                                         headers=get_trace_headers(), timeout=5.0)
                    desired_routes = r.json() if r.status_code == 200 else []

                # Build new desired state — key by hostname+path for uniqueness
                new_synced = {}
                for route in desired_routes:
                    key = f"{route.get('hostname', '*')}:{route['path']}"
                    new_synced[key] = {"backend_url": route["backend_url"], "hostname": route.get("hostname", "*")}

                # Only push config if something changed
                if new_synced != synced_routes:
                    config = build_declarative_config(desired_routes)
                    success = await post_declarative_config(config)
                    if success:
                        added = len(set(new_synced.keys()) - set(synced_routes.keys()))
                        removed = len(set(synced_routes.keys()) - set(new_synced.keys()))
                        for path in set(new_synced.keys()) - set(synced_routes.keys()):
                            with tracer.start_as_current_span("kong.route.create") as cs:
                                cs.set_attribute("route.path", path)
                        for path in set(synced_routes.keys()) - set(new_synced.keys()):
                            with tracer.start_as_current_span("kong.route.delete") as ds:
                                ds.set_attribute("route.path", path)
                        synced_routes = new_synced
                        span.set_attribute("routes.added", added)
                        span.set_attribute("routes.removed", removed)

                duration = (time.time() - start) * 1000
                span.set_attribute("routes.synced", len(desired_routes))
                span.set_attribute("sync.duration_ms", duration)
        except Exception:
            pass

        await asyncio.sleep(5)


# --- Drift detection endpoint ---
@app.get("/sync-status/routes")
async def sync_status_routes():
    """Return currently synced Kong routes for drift detection by management-api."""
    return [{"path": path, "backend_url": info.get("backend_url", ""), "status": "active",
             "gateway_type": "kong"}
            for path, info in synced_routes.items()]


async def report_health():
    """Poll Kong admin API for upstream health and report to management-api."""
    await asyncio.sleep(8)  # wait for Kong upstreams to be created and health checks to run
    while True:
        try:
            async with httpx.AsyncClient() as client:
                r = await client.get(f"{KONG_ADMIN_URL}/upstreams", timeout=5.0)
                if r.status_code == 200:
                    reports = []
                    for upstream in r.json().get("data", []):
                        name = upstream["name"]
                        hr = await client.get(f"{KONG_ADMIN_URL}/upstreams/{name}/health", timeout=5.0)
                        if hr.status_code == 200:
                            for target in hr.json().get("data", []):
                                target_addr = target.get("target", "0.0.0.0:0")
                                host = target_addr.split(":")[0]
                                port = int(target_addr.split(":")[1]) if ":" in target_addr else 0
                                health_str = target.get("health", "HEALTHCHECKS_OFF")
                                health = "healthy" if health_str == "HEALTHY" else "unhealthy" if health_str == "UNHEALTHY" else "unknown"
                                # Direct probe for latency
                                latency_ms = 0
                                try:
                                    start = time.time()
                                    probe = await client.get(f"http://{host}:{port}/health", timeout=3.0)
                                    latency_ms = round((time.time() - start) * 1000, 1)
                                    if probe.status_code != 200:
                                        health = "unhealthy"
                                except Exception:
                                    if health != "unhealthy":
                                        health = "unhealthy"
                                    latency_ms = 0
                                reports.append({
                                    "gateway_type": "kong",
                                    "cluster_name": name,
                                    "backend_host": host,
                                    "backend_port": port,
                                    "health_status": health,
                                    "latency_ms": latency_ms,
                                    "reporter": "kong-admin-proxy",
                                })
                    if reports:
                        await client.post(
                            f"{MANAGEMENT_API_URL}/health-reports",
                            json={"reports": reports},
                            timeout=5.0,
                        )
        except Exception:
            pass
        await asyncio.sleep(3)


@app.on_event("startup")
async def startup():
    # Wait for Kong to be ready
    await asyncio.sleep(5)
    asyncio.create_task(sync_routes())
    asyncio.create_task(report_health())


@app.get("/health")
async def health():
    return {"status": "ok", "service": "kong-admin-proxy", "synced_routes": len(synced_routes)}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
