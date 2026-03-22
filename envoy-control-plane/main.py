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


@app.on_event("startup")
async def startup():
    asyncio.create_task(poll_routes())


@app.get("/health")
async def health():
    return {"status": "ok", "service": "envoy-control-plane", "version": snapshot_version,
            "routes": len(current_routes)}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
