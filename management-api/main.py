import os
import uuid
import time
import json
import asyncio
from typing import Optional
from urllib.parse import urlparse

import uvicorn
import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from sqlalchemy import create_engine, Column, String, Float, Boolean, JSON, Text, Enum as SAEnum
from sqlalchemy.orm import declarative_base, sessionmaker

from otel import init_otel, get_trace_headers
from opentelemetry import trace

PORT = int(os.getenv("PORT", "8003"))
DATABASE_URL = os.getenv("DATABASE_URL", "sqlite:///./registry.db")
SVC_WEB_URL = os.getenv("SVC_WEB_URL", "http://svc-web:8004")
SVC_API_URL = os.getenv("SVC_API_URL", "http://svc-api:8005")
ENVOY_CONTROL_PLANE_URL = os.getenv("ENVOY_CONTROL_PLANE_URL", "http://envoy-control-plane:8080")
KONG_ADMIN_PROXY_URL = os.getenv("KONG_ADMIN_PROXY_URL", "http://kong-admin-proxy:8102")

app = FastAPI(title="Management API", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("management-api", app)

# --- Database ---
# Handle postgres:// vs postgresql:// for SQLAlchemy 2.x
if DATABASE_URL.startswith("postgres://"):
    DATABASE_URL = DATABASE_URL.replace("postgres://", "postgresql://", 1)

engine = create_engine(DATABASE_URL, echo=False,
                       connect_args={"check_same_thread": False} if "sqlite" in DATABASE_URL else {})
SessionLocal = sessionmaker(bind=engine)
Base = declarative_base()


class RouteModel(Base):
    __tablename__ = "routes"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    path = Column(String, nullable=False)
    hostname = Column(String, default="*")
    backend_url = Column(String, nullable=False)
    auth_policy = Column(String, default="authenticated")  # public | authenticated | roles
    allowed_roles = Column(JSON, default=list)
    methods = Column(JSON, default=lambda: ["GET", "POST", "PUT", "DELETE"])
    status = Column(String, default="active")  # pending | active | inactive
    team = Column(String, default="platform")
    created_by = Column(String, default="system")
    gateway_type = Column(String, default="kong")  # envoy | kong | auto
    tls_required = Column(Boolean, default=True)
    notes = Column(Text, default="")
    created_at = Column(Float, default=time.time)
    updated_at = Column(Float, default=time.time)


class ActualRouteModel(Base):
    __tablename__ = "actual_routes"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    route_id = Column(String, nullable=False)
    gateway_type = Column(String)
    path = Column(String)
    actual_status = Column(String, default="absent")  # active | absent
    actual_backend = Column(String, default="")
    drift = Column(Boolean, default=False)
    drift_detail = Column(String, default="")
    last_checked = Column(Float, default=time.time)


class AuditLogModel(Base):
    __tablename__ = "audit_log"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    route_id = Column(String)
    action = Column(String)  # CREATE | UPDATE | DELETE | STATUS_CHANGE
    actor = Column(String, default="system")
    detail = Column(Text, default="")
    ts = Column(Float, default=time.time)


class FleetModel(Base):
    __tablename__ = "fleets"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    name = Column(String, nullable=False)
    subdomain = Column(String, nullable=False, unique=True)
    lob = Column(String, default="")  # Markets | Payments | Global Banking | Security Services | CIB
    gateway_type = Column(String, default="kong")  # kong | envoy
    region = Column(String, default="us-east")
    regions = Column(JSON, default=list)  # ["us-east-1", "us-east-2"]
    auth_provider = Column(String, default="")  # Janus | AuthE1.0 | Sentry | Chase | N/A
    instances_count = Column(Float, default=4)  # target instance count
    status = Column(String, default="healthy")  # healthy | degraded | offline
    created_at = Column(Float, default=time.time)
    updated_at = Column(Float, default=time.time)


class FleetInstanceModel(Base):
    __tablename__ = "fleet_instances"
    id = Column(String, primary_key=True, default=lambda: str(uuid.uuid4()))
    fleet_id = Column(String, nullable=False)
    context_path = Column(String, nullable=False)
    backend = Column(String, nullable=False)
    gateway_type = Column(String, default="envoy")  # envoy | kong
    status = Column(String, default="active")  # active | warning | offline
    latency_p99 = Column(Float, default=0)
    route_id = Column(String, default="")  # FK to route if deployed from a route
    created_at = Column(Float, default=time.time)


Base.metadata.create_all(engine)


# --- Helpers ---
def _route_to_dict(r: RouteModel) -> dict:
    return {
        "id": r.id, "path": r.path, "hostname": r.hostname, "backend_url": r.backend_url,
        "auth_policy": r.auth_policy, "allowed_roles": r.allowed_roles, "methods": r.methods,
        "status": r.status, "team": r.team, "created_by": r.created_by,
        "gateway_type": r.gateway_type, "tls_required": r.tls_required, "notes": r.notes,
        "created_at": r.created_at, "updated_at": r.updated_at,
    }


def _actual_to_dict(a: ActualRouteModel) -> dict:
    return {
        "id": a.id, "route_id": a.route_id, "gateway_type": a.gateway_type,
        "path": a.path, "actual_status": a.actual_status, "actual_backend": a.actual_backend,
        "drift": a.drift, "drift_detail": a.drift_detail, "last_checked": a.last_checked,
    }


def _audit_to_dict(a: AuditLogModel) -> dict:
    return {
        "id": a.id, "route_id": a.route_id, "action": a.action,
        "actor": a.actor, "detail": a.detail, "ts": a.ts,
    }


def _add_audit(db, route_id: str, action: str, actor: str = "system", detail: str = ""):
    entry = AuditLogModel(id=str(uuid.uuid4()), route_id=route_id, action=action,
                          actor=actor, detail=detail, ts=time.time())
    db.add(entry)
    db.commit()


def validate_route(data: dict) -> list[str]:
    violations = []
    path = data.get("path", "")
    if not path.startswith("/"):
        violations.append("Path must start with /")
    backend = data.get("backend_url", "")
    if not backend:
        violations.append("backend_url is required")
    else:
        parsed = urlparse(backend)
        if not parsed.scheme or not parsed.netloc:
            violations.append("backend_url must be a valid URL")
    if data.get("auth_policy") == "roles" and not data.get("allowed_roles"):
        violations.append("roles policy requires allowed_roles")
    if "/admin" in path and data.get("auth_policy") == "public":
        violations.append("Admin paths cannot be public")
    if data.get("tls_required") and backend and "localhost" not in backend and not backend.startswith("https"):
        pass  # Relaxed for POC
    if not data.get("team"):
        violations.append("team is required")
    return violations


# --- Seed default routes ---
def seed_defaults():
    db = SessionLocal()
    if db.query(RouteModel).count() > 0:
        db.close()
        return
    # [portal].jpm.com/[path] — /api/* → Kong, everything else → Envoy
    defaults = [
        {"path": "/health",     "backend_url": SVC_API_URL, "auth_policy": "public", "allowed_roles": [], "gateway_type": "kong",  "team": "platform", "hostname": "*"},
        {"path": "/api/public", "backend_url": SVC_API_URL, "auth_policy": "public", "allowed_roles": [], "gateway_type": "kong",  "team": "platform", "hostname": "*"},
    ]
    fleet_routes = [
        # ── Markets / JPMM — jpmm.jpm.com ───────────────────────
        {"path": "/research",      "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "markets", "hostname": "jpmm.jpm.com"},
        {"path": "/research/api",  "backend_url": SVC_API_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "kong",  "team": "markets", "hostname": "jpmm.jpm.com"},
        {"path": "/sandt",         "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "markets", "hostname": "jpmm.jpm.com"},
        {"path": "/events",        "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "markets", "hostname": "jpmm.jpm.com"},
        {"path": "/events/api",    "backend_url": SVC_API_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "kong",  "team": "markets", "hostname": "jpmm.jpm.com"},

        # ── Markets / Execute — execute.jpm.com ─────────────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "markets", "hostname": "execute.jpm.com"},
        {"path": "/api",           "backend_url": SVC_API_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "kong",  "team": "markets", "hostname": "execute.jpm.com"},

        # ── Payments / JPMA / Access — access.jpm.com ────────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "payments", "hostname": "access.jpm.com"},

        # ── Payments / JPMA / Access Mobile — access-mobile.jpm.com
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "payments", "hostname": "access-mobile.jpm.com"},

        # ── Payments / JPMDB / Digital Banking — digital-banking.jpm.com
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "payments", "hostname": "digital-banking.jpm.com"},

        # ── Payments / Merchant Services / SMB — smb.jpm.com ─────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "payments", "hostname": "smb.jpm.com"},

        # ── Payments / PDP — developer.jpm.com ───────────────────
        {"path": "/api",           "backend_url": SVC_API_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "kong",  "team": "payments", "hostname": "developer.jpm.com"},

        # ── Global Banking / IQ — iq.jpm.com ────────────────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "global-banking", "hostname": "iq.jpm.com"},

        # ── Security Services / SecSvcs — secsvcs.jpm.com ────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "security-services", "hostname": "secsvcs.jpm.com"},

        # ── CIB / AuthN — login.jpm.com ──────────────────────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "public", "allowed_roles": [], "gateway_type": "envoy", "team": "cib", "hostname": "login.jpm.com"},

        # ── CIB / AuthZ — authz.jpm.com ──────────────────────────
        {"path": "/",              "backend_url": SVC_WEB_URL, "auth_policy": "authenticated", "allowed_roles": [], "gateway_type": "envoy", "team": "cib", "hostname": "authz.jpm.com"},
    ]
    all_routes = defaults + fleet_routes
    for d in all_routes:
        hostname = d.pop("hostname", "*")
        route = RouteModel(id=str(uuid.uuid4()), status="active", created_by="system",
                           hostname=hostname, methods=["GET", "POST", "PUT", "DELETE"],
                           tls_required=False, notes=f"Seed route ({hostname})",
                           created_at=time.time(), updated_at=time.time(), **d)
        db.add(route)
        _add_audit(db, route.id, "CREATE", "system", f"Seeded route {d['path']} for {hostname}")
    db.commit()

    # Seed fleets — always re-seed if empty
    if db.query(FleetModel).count() == 0:
        # Fleets match the exact table: LOB / Fleet / Route / URL / Regions / Instances / Type / Auth
        R = ["us-east-1", "us-east-2"]
        fleet_seeds = [
            # Markets
            {"id": "fleet-jpmm",       "name": "JPMM",              "subdomain": "jpmm.jpm.com",            "lob": "Markets",            "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Janus",    "instances_count": 4, "status": "healthy"},
            {"id": "fleet-execute",    "name": "Execute",           "subdomain": "execute.jpm.com",          "lob": "Markets",            "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Janus",    "instances_count": 8, "status": "healthy"},
            # Payments
            {"id": "fleet-access",     "name": "JPMA",              "subdomain": "access.jpm.com",           "lob": "Payments",           "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "AuthE1.0", "instances_count": 8, "status": "healthy"},
            {"id": "fleet-access-mob", "name": "Access Mobile",     "subdomain": "access-mobile.jpm.com",    "lob": "Payments",           "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "AuthE1.0", "instances_count": 2, "status": "healthy"},
            {"id": "fleet-digbank",    "name": "JPMDB",             "subdomain": "digital-banking.jpm.com",  "lob": "Payments",           "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Sentry",   "instances_count": 4, "status": "healthy"},
            {"id": "fleet-smb",        "name": "Merchant Services", "subdomain": "smb.jpm.com",              "lob": "Payments",           "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Chase",    "instances_count": 4, "status": "healthy"},
            {"id": "fleet-pdp",        "name": "PDP",               "subdomain": "developer.jpm.com",        "lob": "Payments",           "gateway_type": "kong",  "region": "us-east", "regions": R, "auth_provider": "Sentry",   "instances_count": 4, "status": "healthy"},
            # Global Banking
            {"id": "fleet-iq",         "name": "IQ",                "subdomain": "iq.jpm.com",               "lob": "Global Banking",     "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Janus",    "instances_count": 4, "status": "healthy"},
            # Security Services
            {"id": "fleet-secsvcs",    "name": "SecSvcs",           "subdomain": "secsvcs.jpm.com",          "lob": "Security Services",  "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Janus",    "instances_count": 4, "status": "healthy"},
            # CIB
            {"id": "fleet-authn",      "name": "AuthN",             "subdomain": "login.jpm.com",            "lob": "CIB",                "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "N/A",      "instances_count": 4, "status": "healthy"},
            {"id": "fleet-authz",      "name": "AuthZ",             "subdomain": "authz.jpm.com",            "lob": "CIB",                "gateway_type": "envoy", "region": "us-east", "regions": R, "auth_provider": "Sentry",   "instances_count": 4, "status": "healthy"},
        ]
        instance_seeds = [
            # JPMM — jpmm.jpm.com
            {"id": "i-jpmm-1",  "fleet_id": "fleet-jpmm",    "context_path": "/research",      "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 18},
            {"id": "i-jpmm-2",  "fleet_id": "fleet-jpmm",    "context_path": "/research/api",   "backend": SVC_API_URL, "gateway_type": "kong",  "status": "active", "latency_p99": 12},
            {"id": "i-jpmm-3",  "fleet_id": "fleet-jpmm",    "context_path": "/sandt",          "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 22},
            {"id": "i-jpmm-4",  "fleet_id": "fleet-jpmm",    "context_path": "/events",         "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 14},
            {"id": "i-jpmm-5",  "fleet_id": "fleet-jpmm",    "context_path": "/events/api",     "backend": SVC_API_URL, "gateway_type": "kong",  "status": "active", "latency_p99": 10},
            # Execute — execute.jpm.com
            {"id": "i-exec-1",  "fleet_id": "fleet-execute",  "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 8},
            {"id": "i-exec-2",  "fleet_id": "fleet-execute",  "context_path": "/api",           "backend": SVC_API_URL, "gateway_type": "kong",  "status": "active", "latency_p99": 6},
            # Access — access.jpm.com
            {"id": "i-acc-1",   "fleet_id": "fleet-access",   "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 20},
            # Access Mobile — access-mobile.jpm.com
            {"id": "i-accm-1",  "fleet_id": "fleet-access-mob","context_path": "/",             "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 25},
            # Digital Banking — digital-banking.jpm.com
            {"id": "i-db-1",    "fleet_id": "fleet-digbank",  "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 19},
            # SMB — smb.jpm.com
            {"id": "i-smb-1",   "fleet_id": "fleet-smb",      "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 16},
            # PDP — developer.jpm.com
            {"id": "i-pdp-1",   "fleet_id": "fleet-pdp",      "context_path": "/api",           "backend": SVC_API_URL, "gateway_type": "kong",  "status": "active", "latency_p99": 11},
            # IQ — iq.jpm.com
            {"id": "i-iq-1",    "fleet_id": "fleet-iq",       "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 19},
            # SecSvcs — secsvcs.jpm.com
            {"id": "i-sec-1",   "fleet_id": "fleet-secsvcs",  "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 22},
            # AuthN — login.jpm.com
            {"id": "i-authn-1", "fleet_id": "fleet-authn",    "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 15},
            # AuthZ — authz.jpm.com
            {"id": "i-authz-1", "fleet_id": "fleet-authz",    "context_path": "/",              "backend": SVC_WEB_URL, "gateway_type": "envoy", "status": "active", "latency_p99": 18},
        ]
        for f in fleet_seeds:
            db.add(FleetModel(created_at=time.time(), updated_at=time.time(), **f))
        for i in instance_seeds:
            db.add(FleetInstanceModel(created_at=time.time(), **i))
        db.commit()

    db.close()


# --- Endpoints ---
@app.get("/routes")
async def list_routes(status: Optional[str] = None, gateway_type: Optional[str] = None):
    db = SessionLocal()
    q = db.query(RouteModel)
    if status:
        q = q.filter(RouteModel.status == status)
    if gateway_type:
        q = q.filter(RouteModel.gateway_type == gateway_type)
    routes = [_route_to_dict(r) for r in q.all()]
    db.close()
    return routes


@app.get("/routes/{route_id}")
async def get_route(route_id: str):
    db = SessionLocal()
    route = db.query(RouteModel).filter(RouteModel.id == route_id).first()
    db.close()
    if not route:
        raise HTTPException(404, "Route not found")
    return _route_to_dict(route)


@app.post("/routes")
async def create_route(request: Request):
    with tracer.start_as_current_span("route.create") as span:
        data = await request.json()
        violations = validate_route(data)
        if violations:
            span.set_attribute("policy.violations", json.dumps(violations))
            raise HTTPException(400, {"violations": violations})

        db = SessionLocal()
        # Check path+hostname uniqueness
        hostname = data.get("hostname", "*")
        existing = db.query(RouteModel).filter(
            RouteModel.path == data["path"],
            RouteModel.hostname == hostname
        ).first()
        if existing:
            db.close()
            raise HTTPException(409, f"Route with path {data['path']} on {hostname} already exists")

        route = RouteModel(
            id=str(uuid.uuid4()), path=data["path"], hostname=data.get("hostname", "*"),
            backend_url=data["backend_url"], auth_policy=data.get("auth_policy", "authenticated"),
            allowed_roles=data.get("allowed_roles", []),
            methods=data.get("methods", ["GET", "POST", "PUT", "DELETE"]),
            status=data.get("status", "active"), team=data.get("team", "platform"),
            created_by=data.get("created_by", "console"), gateway_type=data.get("gateway_type", "kong"),
            tls_required=data.get("tls_required", False), notes=data.get("notes", ""),
            created_at=time.time(), updated_at=time.time(),
        )
        db.add(route)
        _add_audit(db, route.id, "CREATE", data.get("created_by", "console"), f"Created route {data['path']}")
        db.commit()
        result = _route_to_dict(route)
        db.close()

        span.set_attribute("route.path", data["path"])
        span.set_attribute("route.gateway_type", data.get("gateway_type", "kong"))
        return result


@app.put("/routes/{route_id}")
async def update_route(route_id: str, request: Request):
    with tracer.start_as_current_span("route.update") as span:
        data = await request.json()
        db = SessionLocal()
        route = db.query(RouteModel).filter(RouteModel.id == route_id).first()
        if not route:
            db.close()
            raise HTTPException(404, "Route not found")

        for key in ["path", "hostname", "backend_url", "auth_policy", "allowed_roles",
                     "methods", "status", "team", "gateway_type", "tls_required", "notes"]:
            if key in data:
                setattr(route, key, data[key])
        route.updated_at = time.time()

        _add_audit(db, route_id, "UPDATE", data.get("actor", "console"), f"Updated route {route.path}")
        db.commit()
        result = _route_to_dict(route)
        db.close()

        span.set_attribute("route.path", result["path"])
        return result


@app.put("/routes/{route_id}/status")
async def update_route_status(route_id: str, request: Request):
    with tracer.start_as_current_span("route.status_change") as span:
        data = await request.json()
        new_status = data.get("status")
        if new_status not in ["active", "inactive", "pending"]:
            raise HTTPException(400, "Invalid status")

        db = SessionLocal()
        route = db.query(RouteModel).filter(RouteModel.id == route_id).first()
        if not route:
            db.close()
            raise HTTPException(404, "Route not found")

        old_status = route.status
        route.status = new_status
        route.updated_at = time.time()
        _add_audit(db, route_id, "STATUS_CHANGE", data.get("actor", "console"),
                   f"Status changed from {old_status} to {new_status}")
        db.commit()
        result = _route_to_dict(route)
        db.close()

        span.set_attribute("route.path", result["path"])
        span.set_attribute("route.old_status", old_status)
        span.set_attribute("route.new_status", new_status)
        return result


@app.delete("/routes/{route_id}")
async def delete_route(route_id: str):
    with tracer.start_as_current_span("route.delete") as span:
        db = SessionLocal()
        route = db.query(RouteModel).filter(RouteModel.id == route_id).first()
        if not route:
            db.close()
            raise HTTPException(404, "Route not found")

        path = route.path
        _add_audit(db, route_id, "DELETE", "console", f"Deleted route {path}")
        db.delete(route)
        db.commit()
        db.close()

        span.set_attribute("route.path", path)
        return {"deleted": True, "id": route_id}


@app.get("/audit-log")
async def get_audit_log():
    db = SessionLocal()
    entries = db.query(AuditLogModel).order_by(AuditLogModel.ts.desc()).limit(100).all()
    result = [_audit_to_dict(a) for a in entries]
    db.close()
    return result


@app.get("/policy/validate")
async def validate_policy(path: str = "", backend_url: str = "", auth_policy: str = "authenticated",
                           allowed_roles: str = "", team: str = ""):
    with tracer.start_as_current_span("policy.validate") as span:
        data = {
            "path": path, "backend_url": backend_url, "auth_policy": auth_policy,
            "allowed_roles": allowed_roles.split(",") if allowed_roles else [], "team": team,
        }
        violations = validate_route(data)
        span.set_attribute("violations.count", len(violations))
        return {"valid": len(violations) == 0, "violations": violations}


@app.get("/actuals")
async def get_actuals():
    db = SessionLocal()
    actuals = db.query(ActualRouteModel).all()
    result = [_actual_to_dict(a) for a in actuals]
    db.close()
    return result


@app.get("/drift")
async def get_drift():
    db = SessionLocal()
    # Join routes with actuals to build drift response
    routes = db.query(RouteModel).all()
    actuals = {a.route_id: a for a in db.query(ActualRouteModel).all()}
    result = []
    for route in routes:
        actual = actuals.get(route.id)
        drift = actual.drift if actual else (route.status == "active")
        result.append({
            "route_id": route.id,
            "path": route.path,
            "desired_status": route.status,
            "actual_status": actual.actual_status if actual else "unknown",
            "desired_backend": route.backend_url,
            "actual_backend": actual.actual_backend if actual else "",
            "gateway_type": route.gateway_type,
            "drift": drift,
            "drift_detail": actual.drift_detail if actual else "Not yet checked",
            "last_checked": actual.last_checked if actual else 0,
        })
    db.close()
    return result


# --- Fleet Endpoints ---
@app.get("/fleets")
async def list_fleets():
    db = SessionLocal()
    fleets = db.query(FleetModel).all()
    result = []
    for f in fleets:
        instances = db.query(FleetInstanceModel).filter(FleetInstanceModel.fleet_id == f.id).all()
        result.append({
            "id": f.id, "name": f.name, "subdomain": f.subdomain, "lob": f.lob,
            "gateway_type": f.gateway_type, "region": f.region, "regions": f.regions or [],
            "auth_provider": f.auth_provider, "instances_count": f.instances_count,
            "status": f.status, "created_at": f.created_at, "updated_at": f.updated_at,
            "instances": [{"id": i.id, "fleet_id": i.fleet_id, "context_path": i.context_path,
                          "backend": i.backend, "gateway_type": i.gateway_type or "envoy",
                          "status": i.status, "latency_p99": i.latency_p99,
                          "route_id": i.route_id, "created_at": i.created_at} for i in instances],
        })
    db.close()
    return result


@app.get("/fleets/{fleet_id}")
async def get_fleet(fleet_id: str):
    db = SessionLocal()
    f = db.query(FleetModel).filter(FleetModel.id == fleet_id).first()
    if not f:
        db.close()
        raise HTTPException(404, "Fleet not found")
    instances = db.query(FleetInstanceModel).filter(FleetInstanceModel.fleet_id == f.id).all()
    result = {
        "id": f.id, "name": f.name, "subdomain": f.subdomain, "lob": f.lob,
        "gateway_type": f.gateway_type, "region": f.region, "regions": f.regions or [],
        "auth_provider": f.auth_provider, "instances_count": f.instances_count,
        "status": f.status, "created_at": f.created_at, "updated_at": f.updated_at,
        "instances": [{"id": i.id, "fleet_id": i.fleet_id, "context_path": i.context_path,
                      "backend": i.backend, "gateway_type": i.gateway_type or "envoy",
                      "status": i.status, "latency_p99": i.latency_p99,
                      "route_id": i.route_id, "created_at": i.created_at} for i in instances],
    }
    db.close()
    return result


@app.post("/fleets")
async def create_fleet(request: Request):
    data = await request.json()
    subdomain = data["subdomain"]
    # Auto-append .jpm.com if not already a full domain
    if not subdomain.endswith(".jpm.com"):
        subdomain = f"{subdomain}.jpm.com"
    db = SessionLocal()
    # Check uniqueness
    existing = db.query(FleetModel).filter(FleetModel.subdomain == subdomain).first()
    if existing:
        db.close()
        raise HTTPException(409, f"Fleet with subdomain {subdomain} already exists")
    fleet = FleetModel(
        id=str(uuid.uuid4()), name=data["name"], subdomain=subdomain,
        gateway_type=data.get("gateway_type", "kong"), region=data.get("region", "us-east"),
        status="healthy", created_at=time.time(), updated_at=time.time(),
    )
    db.add(fleet)
    db.commit()
    result = {"id": fleet.id, "name": fleet.name, "subdomain": fleet.subdomain,
              "gateway_type": fleet.gateway_type, "region": fleet.region, "status": fleet.status,
              "instances": []}
    db.close()
    return result


@app.post("/fleets/{fleet_id}/deploy")
async def deploy_to_fleet(fleet_id: str, request: Request):
    """Deploy a route to a fleet as a new instance."""
    data = await request.json()
    db = SessionLocal()
    fleet = db.query(FleetModel).filter(FleetModel.id == fleet_id).first()
    if not fleet:
        db.close()
        raise HTTPException(404, "Fleet not found")

    instance = FleetInstanceModel(
        id=str(uuid.uuid4()), fleet_id=fleet_id,
        context_path=data["context_path"], backend=data["backend"],
        status="active", latency_p99=0, route_id=data.get("route_id", ""),
        created_at=time.time(),
    )
    db.add(instance)

    # Also create the route in the registry if it doesn't exist
    existing_route = db.query(RouteModel).filter(
        RouteModel.path == data["context_path"],
        RouteModel.hostname == fleet.subdomain
    ).first()
    if not existing_route:
        route = RouteModel(
            id=str(uuid.uuid4()), path=data["context_path"], hostname=fleet.subdomain,
            backend_url=data["backend"], auth_policy=data.get("auth_policy", "authenticated"),
            allowed_roles=data.get("allowed_roles", []),
            methods=data.get("methods", ["GET", "POST", "PUT", "DELETE"]),
            status="active", team=data.get("team", "platform"),
            created_by="fleet-deploy",
            gateway_type="envoy" if data["context_path"].startswith("/web") else "kong",
            tls_required=False, notes=f"Deployed to fleet {fleet.name}",
            created_at=time.time(), updated_at=time.time(),
        )
        db.add(route)
        instance.route_id = route.id
        _add_audit(db, route.id, "CREATE", "fleet-deploy", f"Deployed to fleet {fleet.name} at {data['context_path']}")

    db.commit()
    result = {"id": instance.id, "fleet_id": fleet_id, "context_path": instance.context_path,
              "backend": instance.backend, "status": instance.status}
    db.close()
    return result


@app.delete("/fleets/{fleet_id}/instances/{instance_id}")
async def remove_fleet_instance(fleet_id: str, instance_id: str):
    db = SessionLocal()
    instance = db.query(FleetInstanceModel).filter(
        FleetInstanceModel.id == instance_id,
        FleetInstanceModel.fleet_id == fleet_id
    ).first()
    if not instance:
        db.close()
        raise HTTPException(404, "Instance not found")
    db.delete(instance)
    db.commit()
    db.close()
    return {"deleted": True, "id": instance_id}


# --- Drift Detection Background Task ---
async def detect_drift():
    while True:
        try:
            with tracer.start_as_current_span("registry.drift_check") as span:
                db = SessionLocal()
                routes = db.query(RouteModel).all()
                envoy_routes = {}
                kong_routes = {}

                try:
                    async with httpx.AsyncClient() as client:
                        r = await client.get(f"{ENVOY_CONTROL_PLANE_URL}/snapshot/routes",
                                             headers=get_trace_headers(), timeout=5.0)
                        if r.status_code == 200:
                            for er in r.json():
                                envoy_routes[er.get("path", "")] = er
                except Exception:
                    pass

                try:
                    async with httpx.AsyncClient() as client:
                        r = await client.get(f"{KONG_ADMIN_PROXY_URL}/sync-status/routes",
                                             headers=get_trace_headers(), timeout=5.0)
                        if r.status_code == 200:
                            for kr in r.json():
                                kong_routes[kr.get("path", "")] = kr
                except Exception:
                    pass

                drifted_count = 0
                for route in routes:
                    actual_routes = envoy_routes if route.gateway_type == "envoy" else kong_routes
                    actual = actual_routes.get(route.path)

                    drift = False
                    drift_detail = ""

                    if actual is None and route.status == "active":
                        drift = True
                        drift_detail = f"Route is active in Registry but absent from {'Envoy' if route.gateway_type == 'envoy' else 'Kong'} — gateway has not yet reconciled"
                    elif actual is not None and route.status == "inactive":
                        drift = True
                        drift_detail = f"Route is inactive in Registry but still present in {'Envoy' if route.gateway_type == 'envoy' else 'Kong'} — gateway has not yet reconciled"
                    elif actual is not None and actual.get("backend_url", actual.get("backend", "")) != route.backend_url:
                        drift = True
                        drift_detail = "Backend URL mismatch between Registry and gateway"
                    else:
                        drift_detail = "In sync"

                    if drift:
                        drifted_count += 1

                    # Upsert actual route
                    existing = db.query(ActualRouteModel).filter(ActualRouteModel.route_id == route.id).first()
                    if existing:
                        existing.actual_status = "active" if actual else "absent"
                        existing.actual_backend = actual.get("backend_url", actual.get("backend", "")) if actual else ""
                        existing.drift = drift
                        existing.drift_detail = drift_detail
                        existing.last_checked = time.time()
                    else:
                        new_actual = ActualRouteModel(
                            id=str(uuid.uuid4()), route_id=route.id,
                            gateway_type=route.gateway_type, path=route.path,
                            actual_status="active" if actual else "absent",
                            actual_backend=actual.get("backend_url", actual.get("backend", "")) if actual else "",
                            drift=drift, drift_detail=drift_detail, last_checked=time.time(),
                        )
                        db.add(new_actual)

                db.commit()
                db.close()

                span.set_attribute("routes.checked", len(routes))
                span.set_attribute("routes.drifted", drifted_count)
        except Exception as e:
            pass

        await asyncio.sleep(10)


@app.on_event("startup")
async def startup():
    seed_defaults()
    asyncio.create_task(detect_drift())


@app.get("/health")
async def health():
    return {"status": "ok", "service": "management-api"}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
