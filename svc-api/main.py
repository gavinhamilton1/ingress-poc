import os
import json
import time

import uvicorn
import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

from otel import init_otel, get_trace_headers
from opentelemetry import trace

SERVICE_NAME = os.getenv("SERVICE_NAME", "svc-api")
PORT = int(os.getenv("PORT", "8005"))
OPA_URL = os.getenv("OPA_URL", "http://opa:8181")

app = FastAPI(title=SERVICE_NAME, version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel(SERVICE_NAME, app)


async def check_fine_opa(session_info: dict, action: str, path: str) -> dict:
    """L5 fine-grained OPA check."""
    with tracer.start_as_current_span("opa.fine") as span:
        opa_input = {
            "input": {
                "session": session_info,
                "action": action,
                "path": path,
            }
        }
        try:
            async with httpx.AsyncClient() as client:
                r = await client.post(
                    f"{OPA_URL}/v1/data/ingress/policy/fine",
                    json=opa_input,
                    headers=get_trace_headers(),
                    timeout=5.0,
                )
                result = r.json().get("result", {})
                allowed = result.get("allow", False)
                deny_reason = result.get("deny_reason", "")
                span.set_attribute("opa.allow", allowed)
                if deny_reason:
                    span.set_attribute("opa.deny_reason", deny_reason)
                return {"allow": allowed, "deny_reason": deny_reason}
        except Exception as e:
            span.set_attribute("opa.allow", True)
            span.set_attribute("opa.error", str(e))
            return {"allow": True, "deny_reason": ""}


@app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH"])
async def catch_all(request: Request, path: str):
    with tracer.start_as_current_span("service.request") as span:
        auth_headers = {}
        for key, value in request.headers.items():
            if key.startswith("x-auth-") or key == "x-request-id":
                auth_headers[key] = value
                span.set_attribute(key, value)

        span.set_attribute("service.name", SERVICE_NAME)
        span.set_attribute("request.path", f"/{path}")

        # Build session info for fine-grained OPA
        session_info = {
            "sub": auth_headers.get("x-auth-subject", ""),
            "roles": json.loads(auth_headers.get("x-auth-roles", "[]")),
            "entity": auth_headers.get("x-auth-entity", ""),
        }

        action = "read" if request.method == "GET" else "write"
        if "admin" in path:
            action = "admin"

        opa_result = await check_fine_opa(session_info, action, f"/{path}")

        if not opa_result["allow"]:
            return {"error": "Forbidden", "reason": opa_result["deny_reason"],
                    "status_code": 403, "service": SERVICE_NAME}

        return {
            "service": SERVICE_NAME,
            "path": f"/{path}",
            "method": request.method,
            "timestamp": time.time(),
            "auth_context": auth_headers,
            "opa_fine": opa_result,
            "message": f"Response from {SERVICE_NAME}",
        }


@app.get("/health")
async def health():
    return {"status": "ok", "service": SERVICE_NAME}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
