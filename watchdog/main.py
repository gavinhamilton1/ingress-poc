"""
Watchdog Service — monitors the management-api health.

Solves the "who watches the watchmen" problem: if the management-api
goes down, this service detects it and exposes the failure via /status.

This service is intentionally stateless (no DB dependency) — it keeps
probe history in-memory so it can report even when infrastructure is
completely degraded.
"""
import os
import time
import asyncio

import uvicorn
import httpx
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8006"))
MANAGEMENT_API_URL = os.getenv("MANAGEMENT_API_URL", "http://management-api:8003")
PROBE_INTERVAL = int(os.getenv("PROBE_INTERVAL", "10"))

app = FastAPI(title="Watchdog", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("watchdog", app)

# In-memory state — intentionally no database
probe_history: list[dict] = []
consecutive_failures = 0
last_success = 0.0


def _compute_status() -> str:
    if consecutive_failures == 0:
        return "healthy"
    elif consecutive_failures <= 2:
        return "degraded"
    return "offline"


async def probe_management_api():
    global consecutive_failures, last_success
    while True:
        entry = {"ts": time.time(), "status": "unknown", "latency_ms": 0}
        try:
            start = time.time()
            async with httpx.AsyncClient() as client:
                r = await client.get(f"{MANAGEMENT_API_URL}/health", timeout=5.0)
                latency = round((time.time() - start) * 1000, 1)
                entry["latency_ms"] = latency
                if r.status_code == 200:
                    consecutive_failures = 0
                    last_success = time.time()
                    entry["status"] = "ok"
                else:
                    consecutive_failures += 1
                    entry["status"] = f"http_{r.status_code}"
        except Exception as e:
            consecutive_failures += 1
            entry["status"] = "error"
            entry["error"] = str(e)[:100]

        probe_history.append(entry)
        # Keep last 30 entries (5 minutes at 10s interval)
        while len(probe_history) > 30:
            probe_history.pop(0)

        await asyncio.sleep(PROBE_INTERVAL)


@app.on_event("startup")
async def startup():
    asyncio.create_task(probe_management_api())


@app.get("/status")
async def status():
    """Return management-api health status with probe history."""
    return {
        "management_api_status": _compute_status(),
        "management_api_url": MANAGEMENT_API_URL,
        "consecutive_failures": consecutive_failures,
        "last_successful_probe": last_success,
        "uptime_seconds": time.time() - probe_history[0]["ts"] if probe_history else 0,
        "probe_history": probe_history[-10:],  # last 10 entries
    }


@app.get("/health")
async def health():
    return {"status": "ok", "service": "watchdog",
            "management_api_status": _compute_status()}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
