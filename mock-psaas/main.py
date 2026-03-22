import os
import itertools

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import Response

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8012"))
GATEWAY_ENVOY_URL = os.getenv("GATEWAY_ENVOY_URL", "http://gateway-envoy:8000")
GATEWAY_KONG_URL = os.getenv("GATEWAY_KONG_URL", "http://gateway-kong:8000")

app = FastAPI(title="Mock PSaaS Perimeter", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("psaas.perimeter", app)

REGIONS = [
    {"region": "us-east", "datacenter": "CDC1"},
    {"region": "eu-west", "datacenter": "Farn"},
    {"region": "ap-southeast", "datacenter": "SG-C01"},
]
region_cycle = itertools.cycle(REGIONS)


@app.get("/health")
async def health():
    return {"status": "ok", "service": "mock-psaas"}


@app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"])
async def forward(request: Request, path: str):
    with tracer.start_as_current_span("psaas.perimeter.forward") as span:
        region_info = next(region_cycle)
        span.set_attribute("psaas.region", region_info["region"])
        span.set_attribute("psaas.datacenter", region_info["datacenter"])
        span.set_attribute("tls.reoriginated", True)
        span.set_attribute("tls.note", "In production TLS terminates here and re-originates to L4. DPoP is bound to the L4 connection.")

        # Propagate akamai request ID
        akamai_request_id = request.headers.get("x-akamai-request-id", "")
        if akamai_request_id:
            span.set_attribute("akamai.request_id", akamai_request_id)

        # Build forwarded headers
        incoming_headers = dict(request.headers)

        # Static routing: /api/* → Kong, everything else → Envoy
        if path.startswith("api/") or path == "api":
            target_url = GATEWAY_KONG_URL
        else:
            target_url = GATEWAY_ENVOY_URL
        incoming_headers["x-psaas-region"] = region_info["region"]
        incoming_headers["x-psaas-datacenter"] = region_info["datacenter"]
        incoming_headers["x-psaas-forward-ip"] = request.headers.get("x-true-client-ip", "203.0.113.42")

        # Append to x-forwarded-for
        xff = incoming_headers.get("x-forwarded-for", "")
        perimeter_ip = "10.100.1.1"
        incoming_headers["x-forwarded-for"] = f"{xff}, {perimeter_ip}" if xff else perimeter_ip

        # Inject trace context
        trace_headers = get_trace_headers()
        incoming_headers.update(trace_headers)

        # Preserve the original Host for gateway hostname-based routing
        forwarded_host = incoming_headers.get("x-forwarded-host", "")

        # Remove hop-by-hop headers
        for h in ["host", "content-length", "transfer-encoding"]:
            incoming_headers.pop(h, None)

        # Set Host header to the original fleet subdomain so Kong/Envoy can route by hostname
        if forwarded_host:
            incoming_headers["host"] = forwarded_host.split(":")[0]  # strip port

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
            return Response(content=f'{{"error": "Gateway unreachable: {e}"}}',
                            status_code=502, media_type="application/json")


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
