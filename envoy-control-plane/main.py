import os
import json
import time
import asyncio
import hashlib

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8080"))
MANAGEMENT_API_URL = os.getenv("MANAGEMENT_API_URL", "http://management-api:8003")
ENVOY_ADMIN_URL = os.getenv("ENVOY_ADMIN_URL", "http://gateway-envoy:9901")
AUTH_SERVICE_URL = os.getenv("AUTH_SERVICE_URL", "http://auth-service:8001")
OPA_URL = os.getenv("OPA_URL", "http://opa:8181")
JAEGER_GRPC = os.getenv("JAEGER_GRPC", "jaeger:4317")

app = FastAPI(title="Envoy Control Plane (REST xDS)", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("envoy-control-plane", app)

# Current snapshot state
current_routes: list[dict] = []
snapshot_version = "0"


def _group_routes_by_hostname(routes: list[dict]) -> dict[str, list]:
    """Group active envoy routes by hostname, returning {hostname: [envoy_route_entries]}."""
    groups = {}
    for route in routes:
        if route.get("gateway_type") != "envoy" or route.get("status") != "active":
            continue
        hostname = route.get("hostname", "*") or "*"
        entry = {
            "match": {"prefix": route["path"]},
            "route": {
                "cluster": f"cluster_{route['path'].replace('/', '_')}",
                "timeout": "30s",
            },
        }
        groups.setdefault(hostname, []).append(entry)
    return groups


def _build_virtual_hosts(routes: list[dict]) -> list[dict]:
    """Build per-hostname virtual hosts + a wildcard fallback."""
    groups = _group_routes_by_hostname(routes)
    vhosts = []

    # Named virtual hosts for specific hostnames (fleet subdomains)
    for hostname, entries in groups.items():
        if hostname == "*":
            continue
        vhosts.append({
            "name": f"vh_{hostname.replace('.', '_')}",
            "domains": [hostname, f"{hostname}:*"],  # match with or without port
            "routes": entries,
        })

    # Wildcard fallback for routes with hostname="*"
    wildcard_routes = groups.get("*", [])
    vhosts.append({
        "name": "wildcard",
        "domains": ["*"],
        "routes": wildcard_routes if wildcard_routes else [{
            "match": {"prefix": "/"},
            "direct_response": {"status": 503, "body": {"inline_string": "No routes configured for this host"}},
        }],
    })

    return vhosts


def build_route_config(routes: list[dict]) -> dict:
    """Build Envoy RDS response with per-hostname virtual hosts."""
    return {
        "version_info": snapshot_version,
        "resources": [{
            "@type": "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
            "name": "local_route",
            "virtual_hosts": _build_virtual_hosts(routes),
        }],
    }


def build_cluster_config(routes: list[dict]) -> dict:
    """Build Envoy CDS response from routes."""
    clusters = []
    for route in routes:
        if route.get("gateway_type") != "envoy" or route.get("status") != "active":
            continue
        backend = route.get("backend_url", "")
        host = backend.split("://")[-1].split(":")[0] if "://" in backend else backend.split(":")[0]
        port = int(backend.split(":")[-1]) if ":" in backend.split("://")[-1] else 80
        clusters.append({
            "name": f"cluster_{route['path'].replace('/', '_')}",
            "type": "STRICT_DNS",
            "load_assignment": {
                "cluster_name": f"cluster_{route['path'].replace('/', '_')}",
                "endpoints": [{"lb_endpoints": [{"endpoint": {
                    "address": {"socket_address": {"address": host, "port_value": port}}
                }}]}],
            },
            "health_checks": [{
                "timeout": "2s",
                "interval": "10s",
                "unhealthy_threshold": 3,
                "healthy_threshold": 2,
                "http_health_check": {"path": "/health"},
            }],
            "outlier_detection": {
                "consecutive_5xx": 5,
                "interval": "10s",
                "base_ejection_time": "30s",
                "max_ejection_percent": 50,
            },
        })

    # Add auth service cluster
    clusters.append({
        "name": "auth_service",
        "type": "STRICT_DNS",
        "load_assignment": {
            "cluster_name": "auth_service",
            "endpoints": [{"lb_endpoints": [{"endpoint": {
                "address": {"socket_address": {"address": AUTH_SERVICE_URL.split("://")[-1].split(":")[0],
                                                "port_value": int(AUTH_SERVICE_URL.split(":")[-1])}}
            }}]}],
        },
    })

    # Add OPA cluster
    clusters.append({
        "name": "opa_service",
        "type": "STRICT_DNS",
        "load_assignment": {
            "cluster_name": "opa_service",
            "endpoints": [{"lb_endpoints": [{"endpoint": {
                "address": {"socket_address": {"address": OPA_URL.split("://")[-1].split(":")[0],
                                                "port_value": int(OPA_URL.split(":")[-1])}}
            }}]}],
        },
    })

    # Add Jaeger cluster for OTEL tracing
    clusters.append({
        "name": "jaeger_cluster",
        "type": "STRICT_DNS",
        "typed_extension_protocol_options": {
            "envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
                "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
                "explicit_http_config": {"http2_protocol_options": {}},
            }
        },
        "load_assignment": {
            "cluster_name": "jaeger_cluster",
            "endpoints": [{"lb_endpoints": [{"endpoint": {
                "address": {"socket_address": {
                    "address": JAEGER_GRPC.split(":")[0],
                    "port_value": int(JAEGER_GRPC.split(":")[1]) if ":" in JAEGER_GRPC else 4317,
                }}
            }}]}],
        },
    })

    return {
        "version_info": snapshot_version,
        "resources": [{"@type": "type.googleapis.com/envoy.config.cluster.v3.Cluster", **c} for c in clusters],
    }


def build_listener_config(routes: list[dict]) -> dict:
    """Build Envoy LDS response with per-hostname virtual hosts."""
    return {
        "version_info": snapshot_version,
        "resources": [{
            "@type": "type.googleapis.com/envoy.config.listener.v3.Listener",
            "name": "listener_0",
            "address": {"socket_address": {"address": "0.0.0.0", "port_value": 8000}},
            "filter_chains": [{
                "filters": [{
                    "name": "envoy.filters.network.http_connection_manager",
                    "typed_config": {
                        "@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
                        "stat_prefix": "ingress_http",
                        "tracing": {
                            "provider": {
                                "name": "envoy.tracers.opentelemetry",
                                "typed_config": {
                                    "@type": "type.googleapis.com/envoy.config.trace.v3.OpenTelemetryConfig",
                                    "grpc_service": {
                                        "envoy_grpc": {"cluster_name": "jaeger_cluster"},
                                        "timeout": "3s",
                                    },
                                    "service_name": "envoy-gateway",
                                },
                            },
                        },
                        "route_config": {
                            "name": "local_route",
                            "virtual_hosts": _build_virtual_hosts(routes),
                        },
                        "http_filters": [
                            {"name": "envoy.filters.http.router", "typed_config": {
                                "@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"
                            }},
                        ],
                    },
                }],
            }],
        }],
    }


# --- xDS REST endpoints ---
@app.post("/v3/discovery:routes")
@app.post("/v2/discovery:routes")
async def discovery_routes(request: Request):
    return JSONResponse(build_route_config(current_routes))


@app.post("/v3/discovery:clusters")
@app.post("/v2/discovery:clusters")
async def discovery_clusters(request: Request):
    return JSONResponse(build_cluster_config(current_routes))


@app.post("/v3/discovery:listeners")
@app.post("/v2/discovery:listeners")
async def discovery_listeners(request: Request):
    return JSONResponse(build_listener_config(current_routes))


# --- Drift detection endpoint ---
@app.get("/snapshot/routes")
async def snapshot_routes():
    """Return current route list for drift detection by management-api."""
    return [{"path": r["path"], "backend_url": r.get("backend_url", ""), "status": r.get("status", ""),
             "gateway_type": r.get("gateway_type", "")}
            for r in current_routes if r.get("gateway_type") == "envoy" and r.get("status") == "active"]


# --- Polling background task ---
async def poll_routes():
    global current_routes, snapshot_version
    while True:
        try:
            with tracer.start_as_current_span("xds.routes.poll") as span:
                async with httpx.AsyncClient() as client:
                    r = await client.get(f"{MANAGEMENT_API_URL}/routes?gateway_type=envoy&status=active",
                                         headers=get_trace_headers(), timeout=5.0)
                    new_routes = r.json() if r.status_code == 200 else []

                changed = json.dumps(new_routes, sort_keys=True) != json.dumps(current_routes, sort_keys=True)
                span.set_attribute("routes.active", len(new_routes))
                span.set_attribute("routes.changed", changed)

                if changed:
                    current_routes = new_routes
                    snapshot_version = hashlib.md5(json.dumps(new_routes, sort_keys=True).encode()).hexdigest()[:8]
                    with tracer.start_as_current_span("xds.snapshot.update") as update_span:
                        update_span.set_attribute("routes.count", len(new_routes))
                        update_span.set_attribute("version", snapshot_version)
        except Exception:
            pass

        await asyncio.sleep(5)


async def report_health():
    """Poll Envoy admin API for cluster health and report to management-api."""
    await asyncio.sleep(8)  # wait for Envoy to start and run first health checks
    while True:
        try:
            async with httpx.AsyncClient() as client:
                r = await client.get(f"{ENVOY_ADMIN_URL}/clusters?format=json", timeout=5.0)
                if r.status_code == 200:
                    cluster_data = r.json()
                    reports = []
                    for cluster_status in cluster_data.get("cluster_statuses", []):
                        name = cluster_status.get("name", "")
                        # Only report on dynamic route clusters, skip infra
                        if not name.startswith("cluster_"):
                            continue
                        for host_status in cluster_status.get("host_statuses", []):
                            addr = host_status.get("address", {}).get("socket_address", {})
                            host = addr.get("address", "")
                            port = addr.get("port_value", 0)
                            # Determine health from eds_health_status or failed_active_health_check
                            eds = host_status.get("health_status", {}).get("eds_health_status", "HEALTHY")
                            failed = host_status.get("health_status", {}).get("failed_active_health_check", False)
                            health = "unhealthy" if (eds == "UNHEALTHY" or failed) else "healthy"
                            # Try to get latency from a direct probe
                            latency_ms = 0
                            try:
                                start = time.time()
                                probe = await client.get(f"http://{host}:{port}/health", timeout=3.0)
                                latency_ms = round((time.time() - start) * 1000, 1)
                                if probe.status_code != 200:
                                    health = "unhealthy"
                            except Exception:
                                health = "unhealthy"
                                latency_ms = 0
                            reports.append({
                                "gateway_type": "envoy",
                                "cluster_name": name,
                                "backend_host": host,
                                "backend_port": port,
                                "health_status": health,
                                "latency_ms": latency_ms,
                                "reporter": "envoy-control-plane",
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
    asyncio.create_task(poll_routes())
    asyncio.create_task(report_health())


@app.get("/health")
async def health():
    return {"status": "ok", "service": "envoy-control-plane", "version": snapshot_version,
            "routes": len(current_routes)}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
