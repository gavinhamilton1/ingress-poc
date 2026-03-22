import os
import uuid
import random

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import Response, JSONResponse

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8011"))
GATEWAY_ENVOY_URL = os.getenv("GATEWAY_ENVOY_URL", "http://mock-psaas:8012")
GATEWAY_KONG_URL = os.getenv("GATEWAY_KONG_URL", "http://mock-psaas:8012")

app = FastAPI(title="Mock Akamai Edge (CDN/WAF)", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("akamai.edge", app)


def waf_check(path: str, query: str, headers: dict) -> tuple[bool, str]:
    """WAF simulation. Returns (blocked, rule_name)."""
    if "<script>" in path or "<script>" in query:
        return True, "xss"
    if "../" in path:
        return True, "path-traversal"
    if not headers.get("user-agent"):
        return True, "bot-check"
    return False, ""


@app.get("/health")
async def health():
    return {"status": "ok", "service": "mock-akamai-edge"}


@app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"])
async def edge_forward(request: Request, path: str):
    with tracer.start_as_current_span("akamai.edge.request") as span:
        request_id = request.headers.get("x-akamai-request-id", str(uuid.uuid4()))
        span.set_attribute("akamai.service", "edge")
        span.set_attribute("akamai.request_id", request_id)
        span.set_attribute("akamai.edge_ip", "23.40.11.5")
        span.set_attribute("akamai.country", "GB")

        # WAF check
        query_string = str(request.query_params) if request.query_params else ""
        blocked, waf_rule = waf_check(f"/{path}", query_string, dict(request.headers))
        span.set_attribute("akamai.waf.checked", True)
        span.set_attribute("akamai.waf.blocked", blocked)
        if blocked:
            span.set_attribute("akamai.waf.block_reason", waf_rule)
            return JSONResponse(
                status_code=403,
                content={"error": "WAF blocked", "rule": waf_rule, "request_id": request_id},
            )

        # Cache simulation (20% of GETs)
        cache_hit = request.method == "GET" and random.random() < 0.2
        span.set_attribute("akamai.cache.hit", cache_hit)

        # Static routing: /api/* → Kong (via PSaaS), everything else → Envoy (via PSaaS)
        if path.startswith("api/") or path == "api":
            target_url = GATEWAY_KONG_URL
            span.set_attribute("akamai.forward_to", "gateway-kong")
        else:
            target_url = GATEWAY_ENVOY_URL
            span.set_attribute("akamai.forward_to", "gateway-envoy")

        # Build headers
        incoming_headers = dict(request.headers)
        incoming_headers["x-akamai-request-id"] = request_id
        incoming_headers["x-akamai-edgescape"] = "georegion=263,country_code=GB,city=LONDON,lat=51.50,long=-0.12"
        incoming_headers["x-true-client-ip"] = "203.0.113.42"
        incoming_headers["x-forwarded-for"] = "203.0.113.42, 23.40.11.5"
        incoming_headers["x-akamai-cache-status"] = "HIT" if cache_hit else "MISS"
        incoming_headers["x-akamai-waf-status"] = "PASS"

        # Inject trace context
        trace_headers = get_trace_headers()
        incoming_headers.update(trace_headers)
        incoming_headers["tracestate"] = f"akamai={request_id}"

        # Preserve original Host for subdomain routing
        forwarded_host = incoming_headers.get("x-forwarded-host", "")
        if not forwarded_host:
            forwarded_host = request.headers.get("host", "")
            if forwarded_host:
                incoming_headers["x-forwarded-host"] = forwarded_host

        # Remove hop-by-hop
        for h in ["host", "content-length", "transfer-encoding"]:
            incoming_headers.pop(h, None)

        try:
            body = await request.body()
            async with httpx.AsyncClient() as client:
                resp = await client.request(
                    method=request.method,
                    url=f"{target_url}/{path}",
                    headers=incoming_headers,
                    content=body,
                    timeout=30.0,
                )
            span.set_attribute("http.status_code", resp.status_code)
            return Response(
                content=resp.content,
                status_code=resp.status_code,
                headers=dict(resp.headers),
            )
        except Exception as e:
            span.set_attribute("http.status_code", 502)
            span.set_attribute("error", str(e))
            return Response(content=f'{{"error": "Upstream unreachable: {e}"}}',
                            status_code=502, media_type="application/json")


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
