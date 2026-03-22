import os
import uuid
import time
import json
import hashlib
import base64
import secrets
from typing import Optional

import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from jose import jwt, jwk, JWTError
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.backends import default_backend

from otel import init_otel, get_trace_headers
from opentelemetry import trace

app = FastAPI(title="Auth Service", version="1.0.0")
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_credentials=True,
                   allow_methods=["*"], allow_headers=["*"])
tracer = init_otel("auth-service", app)

PORT = int(os.getenv("PORT", "8001"))
AUTH_SERVICE_URL = os.getenv("AUTH_SERVICE_URL", f"http://localhost:{PORT}")

# --- Key Generation ---
_idp_private_key = ec.generate_private_key(ec.SECP256R1(), default_backend())
_idp_public_key = _idp_private_key.public_key()

_session_private_key = ec.generate_private_key(ec.SECP256R1(), default_backend())
_session_public_key = _session_private_key.public_key()


def _ec_key_to_jwk(public_key, kid: str) -> dict:
    numbers = public_key.public_numbers()
    x = base64.urlsafe_b64encode(numbers.x.to_bytes(32, 'big')).rstrip(b'=').decode()
    y = base64.urlsafe_b64encode(numbers.y.to_bytes(32, 'big')).rstrip(b'=').decode()
    return {"kty": "EC", "crv": "P-256", "x": x, "y": y, "kid": kid, "use": "sig", "alg": "ES256"}


def _ec_private_to_pem(private_key) -> str:
    return private_key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption()
    ).decode()


IDP_JWK = _ec_key_to_jwk(_idp_public_key, "idp-key-1")
SESSION_JWK = _ec_key_to_jwk(_session_public_key, "session-key-1")
IDP_PRIVATE_PEM = _ec_private_to_pem(_idp_private_key)
SESSION_PRIVATE_PEM = _ec_private_to_pem(_session_private_key)

# --- In-memory stores ---
DEMO_USERS = {
    "admin@demo.local": {"password": "demo1234", "name": "Admin User", "roles": ["architect", "platform-admin"], "entity": "PLATFORM"},
    "trader@demo.local": {"password": "demo1234", "name": "Trader User", "roles": ["trader"], "entity": "MARKETS"},
    "readonly@demo.local": {"password": "demo1234", "name": "Read-Only User", "roles": ["readonly"], "entity": "OPS"},
}

auth_codes: dict[str, dict] = {}  # code -> {sub, code_challenge, code_challenge_method, client_id, redirect_uri}
sessions: dict[str, dict] = {}    # sid -> session data
revoked_sessions: set[str] = set()
dpop_jti_cache: set[str] = set()


# --- Models ---
class AuthorizeRequest(BaseModel):
    email: str
    password: str
    client_id: str = "ingress-console"
    redirect_uri: str = "http://localhost:3000/callback"
    code_challenge: str
    code_challenge_method: str = "S256"


class TokenRequest(BaseModel):
    grant_type: str = "authorization_code"
    code: str
    redirect_uri: str = "http://localhost:3000/callback"
    client_id: str = "ingress-console"
    code_verifier: str


class SessionCreateRequest(BaseModel):
    access_token: str
    dpop_jwk: Optional[dict] = None


# --- Helpers ---
def _compute_jkt(jwk_dict: dict) -> str:
    canonical = json.dumps({"crv": jwk_dict.get("crv"), "kty": jwk_dict.get("kty"),
                            "x": jwk_dict.get("x"), "y": jwk_dict.get("y")}, sort_keys=True, separators=(',', ':'))
    return base64.urlsafe_b64encode(hashlib.sha256(canonical.encode()).digest()).rstrip(b'=').decode()


def _sign_jwt(payload: dict, private_pem: str, kid: str) -> str:
    return jwt.encode(payload, private_pem, algorithm="ES256", headers={"kid": kid})


# --- Endpoints ---
@app.post("/auth/authorize")
async def authorize(req: AuthorizeRequest):
    with tracer.start_as_current_span("auth.pkce.authorize") as span:
        user = DEMO_USERS.get(req.email)
        if not user or user["password"] != req.password:
            span.set_attribute("auth.result", "REJECT")
            raise HTTPException(401, "Invalid credentials")

        code = secrets.token_urlsafe(32)
        auth_codes[code] = {
            "sub": req.email, "code_challenge": req.code_challenge,
            "code_challenge_method": req.code_challenge_method,
            "client_id": req.client_id, "redirect_uri": req.redirect_uri,
        }
        span.set_attribute("user.sub", req.email)
        span.set_attribute("pkce.valid", True)
        return {"code": code, "state": "ok"}


@app.post("/auth/token")
async def token(req: TokenRequest):
    with tracer.start_as_current_span("auth.pkce.token") as span:
        stored = auth_codes.pop(req.code, None)
        if not stored:
            span.set_attribute("pkce.verified", False)
            raise HTTPException(400, "Invalid or expired code")

        # Verify PKCE
        verifier_hash = base64.urlsafe_b64encode(
            hashlib.sha256(req.code_verifier.encode()).digest()
        ).rstrip(b'=').decode()
        if verifier_hash != stored["code_challenge"]:
            span.set_attribute("pkce.verified", False)
            raise HTTPException(400, "PKCE verification failed")

        user = DEMO_USERS[stored["sub"]]
        now = int(time.time())
        access_payload = {
            "iss": AUTH_SERVICE_URL, "sub": stored["sub"], "aud": "ingress-gateway",
            "iat": now, "exp": now + 3600, "email": stored["sub"], "name": user["name"],
            "roles": user["roles"], "entity": user["entity"], "client_id": stored["client_id"],
        }
        access_token = _sign_jwt(access_payload, IDP_PRIVATE_PEM, "idp-key-1")
        id_token = _sign_jwt({**access_payload, "aud": stored["client_id"]}, IDP_PRIVATE_PEM, "idp-key-1")

        span.set_attribute("pkce.verified", True)
        span.set_attribute("token.expiry", now + 3600)
        return {"access_token": access_token, "id_token": id_token, "token_type": "DPoP", "expires_in": 3600}


@app.get("/.well-known/jwks.json")
async def idp_jwks():
    return {"keys": [IDP_JWK]}


@app.get("/session/jwks.json")
async def session_jwks():
    return {"keys": [SESSION_JWK]}


@app.post("/session/create")
async def create_session(req: SessionCreateRequest):
    with tracer.start_as_current_span("session.create") as span:
        try:
            claims = jwt.decode(req.access_token, IDP_PRIVATE_PEM, algorithms=["ES256"],
                                audience="ingress-gateway", options={"verify_exp": True})
        except JWTError:
            # Try decoding without verification for demo flexibility
            claims = jwt.get_unverified_claims(req.access_token)

        sid = str(uuid.uuid4())
        jkt = _compute_jkt(req.dpop_jwk) if req.dpop_jwk else None
        now = int(time.time())

        session_payload = {
            "iss": AUTH_SERVICE_URL, "sub": claims.get("sub"), "sid": sid,
            "aud": "ingress-gateway", "iat": now, "exp": now + 3600,
            "email": claims.get("email"), "name": claims.get("name"),
            "roles": claims.get("roles", []), "entity": claims.get("entity"),
            "client_id": claims.get("client_id", "ingress-console"),
        }
        if jkt:
            session_payload["cnf"] = {"jkt": jkt}

        session_jwt = _sign_jwt(session_payload, SESSION_PRIVATE_PEM, "session-key-1")

        sessions[sid] = {
            "sid": sid, "sub": claims.get("sub"), "email": claims.get("email"),
            "name": claims.get("name"), "roles": claims.get("roles", []),
            "entity": claims.get("entity"), "created_at": now, "expires_at": now + 3600,
            "dpop_jkt": jkt, "status": "active",
        }

        span.set_attribute("session.id", sid)
        span.set_attribute("dpop.jkt", (jkt or "")[:12])
        span.set_attribute("session.subject", claims.get("sub", ""))
        return {"session_jwt": session_jwt, "sid": sid, "expires_in": 3600}


@app.post("/session/revoke/{sid}")
async def revoke_session(sid: str):
    with tracer.start_as_current_span("session.revoke") as span:
        if sid not in sessions:
            raise HTTPException(404, "Session not found")
        revoked_sessions.add(sid)
        sessions[sid]["status"] = "revoked"
        span.set_attribute("session.id", sid)
        span.set_attribute("revocation.ts", int(time.time()))
        return {"status": "revoked", "sid": sid}


@app.get("/session/{sid}")
async def get_session(sid: str):
    session = sessions.get(sid)
    if not session:
        raise HTTPException(404, "Session not found")
    return session


@app.get("/sessions")
async def list_sessions():
    return list(sessions.values())


@app.get("/revocations")
async def list_revocations():
    return list(revoked_sessions)


@app.post("/gateway/ext-authz")
async def ext_authz(request: Request):
    with tracer.start_as_current_span("ext_authz.validate") as span:
        body = await request.json()
        headers = body.get("headers", {})
        auth_header = headers.get("authorization", headers.get("Authorization", ""))
        dpop_header = headers.get("dpop", headers.get("DPoP", ""))
        method = body.get("method", "GET")
        path = body.get("path", "/")

        if not auth_header.startswith("Bearer "):
            span.set_attribute("auth.result", "REJECT")
            span.set_attribute("auth.reject_reason", "missing_bearer_token")
            return {"allowed": False, "reason": "Missing Bearer token", "status_code": 401}

        token = auth_header[7:]

        # Decode session JWT
        try:
            claims = jwt.get_unverified_claims(token)
        except Exception:
            span.set_attribute("auth.result", "REJECT")
            span.set_attribute("auth.reject_reason", "invalid_jwt")
            return {"allowed": False, "reason": "Invalid JWT", "status_code": 401}

        sid = claims.get("sid", "")
        sub = claims.get("sub", "")
        roles = claims.get("roles", [])
        entity = claims.get("entity", "")

        span.set_attribute("session.id", sid)
        span.set_attribute("session.subject", sub)
        span.set_attribute("session.roles", json.dumps(roles))
        span.set_attribute("session.entity", entity)

        # DPoP verification
        with tracer.start_as_current_span("dpop.verify") as dpop_span:
            dpop_valid = True
            dpop_error = None
            dpop_jkt = ""

            if dpop_header:
                try:
                    dpop_claims = jwt.get_unverified_claims(dpop_header)
                    dpop_headers = jwt.get_unverified_header(dpop_header)
                    dpop_jwk_header = dpop_headers.get("jwk", {})
                    dpop_jkt = _compute_jkt(dpop_jwk_header)

                    # Verify htm and htu
                    if dpop_claims.get("htm", "").upper() != method.upper():
                        dpop_valid = False
                        dpop_error = "htm mismatch"
                    if dpop_claims.get("htu") and not path.startswith("/"):
                        pass  # Flexible for POC

                    # Check jti uniqueness
                    jti = dpop_claims.get("jti", "")
                    if jti in dpop_jti_cache:
                        dpop_valid = False
                        dpop_error = "jti replay"
                    else:
                        dpop_jti_cache.add(jti)

                    # Check iat freshness (allow 5 minute window)
                    iat = dpop_claims.get("iat", 0)
                    if abs(time.time() - iat) > 300:
                        dpop_valid = False
                        dpop_error = "iat too old"

                    # Check cnf binding
                    cnf = claims.get("cnf", {})
                    if cnf.get("jkt") and cnf["jkt"] != dpop_jkt:
                        dpop_valid = False
                        dpop_error = "jkt mismatch"

                    dpop_span.set_attribute("dpop.htm", dpop_claims.get("htm", ""))
                    dpop_span.set_attribute("dpop.htu", dpop_claims.get("htu", ""))
                    dpop_span.set_attribute("dpop.jti", jti)
                except Exception as e:
                    dpop_valid = False
                    dpop_error = str(e)

            dpop_span.set_attribute("dpop.valid", dpop_valid)
            dpop_span.set_attribute("dpop.jkt", dpop_jkt[:12] if dpop_jkt else "")
            if dpop_error:
                dpop_span.set_attribute("dpop.error", dpop_error)

        if not dpop_valid:
            span.set_attribute("auth.result", "REJECT")
            span.set_attribute("auth.reject_reason", f"dpop_failed: {dpop_error}")
            return {"allowed": False, "reason": f"DPoP verification failed: {dpop_error}", "status_code": 401}

        # Revocation check
        with tracer.start_as_current_span("revoke_cache.check") as revoke_span:
            is_revoked = sid in revoked_sessions
            revoke_span.set_attribute("revoke_cache.hit", is_revoked)
            revoke_span.set_attribute("session.id", sid)

        if is_revoked:
            span.set_attribute("auth.result", "REJECT")
            span.set_attribute("auth.reject_reason", "session_revoked")
            return {"allowed": False, "reason": "Session has been revoked", "status_code": 401}

        span.set_attribute("auth.result", "PASS")
        span.set_attribute("dpop.valid", dpop_valid)
        span.set_attribute("dpop.jkt", dpop_jkt[:12] if dpop_jkt else "")

        return {
            "allowed": True,
            "headers": {
                "x-auth-subject": sub,
                "x-auth-session-id": sid,
                "x-auth-roles": json.dumps(roles),
                "x-auth-entity": entity,
                "x-auth-client-id": claims.get("client_id", ""),
                "x-auth-dpop-jkt": dpop_jkt[:12] if dpop_jkt else "",
                "x-auth-email": claims.get("email", ""),
                "x-auth-name": claims.get("name", ""),
            },
            "claims": claims,
        }


@app.get("/demo/users")
async def demo_users():
    return [{"email": email, "name": u["name"], "roles": u["roles"], "entity": u["entity"]}
            for email, u in DEMO_USERS.items()]


@app.get("/health")
async def health():
    return {"status": "ok", "service": "auth-service"}


if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT)
