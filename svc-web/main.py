import os
import json
import time

import uvicorn
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

from otel import init_otel, get_trace_headers
from opentelemetry import trace

SERVICE_NAME = os.getenv("SERVICE_NAME", "svc-web")
PORT = int(os.getenv("PORT", "8004"))

app = FastAPI(title=SERVICE_NAME, version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel(SERVICE_NAME, app)


@app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def catch_all(request: Request, path: str):
    with tracer.start_as_current_span("service.request") as span:
        # Extract auth headers
        auth_headers = {}
        for key, value in request.headers.items():
            if key.startswith("x-auth-") or key == "x-request-id":
                auth_headers[key] = value
                span.set_attribute(key, value)

        span.set_attribute("service.name", SERVICE_NAME)
        span.set_attribute("request.path", f"/{path}")

        return {
            "service": SERVICE_NAME,
            "path": f"/{path}",
            "method": request.method,
            "timestamp": time.time(),
            "auth_context": auth_headers,
            "message": f"Response from {SERVICE_NAME}",
        }


@app.get("/health")
async def health():
    return {"status": "ok", "service": SERVICE_NAME}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
