import os
import uuid
import itertools

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import Response

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8010"))
HTTPS_PORT = int(os.getenv("HTTPS_PORT", "8443"))
FORWARD_TO = os.getenv("FORWARD_TO", "http://mock-akamai-edge:8011")
SSL_CERTFILE = os.getenv("SSL_CERTFILE", "/certs/jpm.com.crt")
SSL_KEYFILE = os.getenv("SSL_KEYFILE", "/certs/jpm.com.key")

app = FastAPI(title="Mock Akamai GTM", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("akamai.gtm", app)

DATACENTERS = ["us-east", "us-west", "eu-west", "ap-southeast"]
dc_cycle = itertools.cycle(DATACENTERS)


@app.get("/health")
async def health():
    return {"status": "ok", "service": "mock-akamai-gtm"}


@app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"])
async def gtm_forward(request: Request, path: str):
    with tracer.start_as_current_span("akamai.gtm.forward") as span:
        request_id = request.headers.get("x-akamai-request-id") or str(uuid.uuid4())
        datacenter = next(dc_cycle)

        span.set_attribute("akamai.service", "gtm")
        span.set_attribute("akamai.request_id", request_id)
        span.set_attribute("akamai.datacenter", datacenter)
        span.set_attribute("akamai.forward_to", "mock-akamai-edge")

        # Build headers
        incoming_headers = dict(request.headers)
        incoming_headers["x-akamai-request-id"] = request_id
        incoming_headers["x-akamai-gtm-datacenter"] = datacenter
        incoming_headers["x-akamai-gtm-reason"] = "load-balance"

        # Inject trace context
        trace_headers = get_trace_headers()
        incoming_headers.update(trace_headers)

        # Preserve original Host for subdomain-based routing downstream
        original_host = request.headers.get("host", "")
        if original_host:
            incoming_headers["x-forwarded-host"] = original_host

        # Remove hop-by-hop (but keep host info via x-forwarded-host)
        for h in ["host", "content-length", "transfer-encoding"]:
            incoming_headers.pop(h, None)

        try:
            body = await request.body()
            async with httpx.AsyncClient() as client:
                resp = await client.request(
                    method=request.method,
                    url=f"{FORWARD_TO}/{path}",
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
            return Response(content=f'{{"error": "Edge unreachable: {e}"}}',
                            status_code=502, media_type="application/json")


if __name__ == "__main__":
    import threading, ssl

    # Start HTTPS server in a thread
    def run_https():
        if os.path.exists(SSL_CERTFILE) and os.path.exists(SSL_KEYFILE):
            uvicorn.run(app, host="0.0.0.0", port=HTTPS_PORT,
                        ssl_certfile=SSL_CERTFILE, ssl_keyfile=SSL_KEYFILE,
                        log_level="info")

    https_thread = threading.Thread(target=run_https, daemon=True)
    https_thread.start()

    # HTTP server on main thread
    uvicorn.run(app, host="0.0.0.0", port=PORT)
