-- Ingress PoC Schema — PostgreSQL
-- Auto-executed by management-api on startup

CREATE TABLE IF NOT EXISTS routes (
    id              TEXT PRIMARY KEY,
    path            TEXT NOT NULL,
    hostname        TEXT DEFAULT '*',
    backend_url     TEXT NOT NULL,
    audience        TEXT DEFAULT '',
    allowed_roles   JSONB DEFAULT '[]',
    methods         JSONB DEFAULT '["GET","POST","PUT","DELETE"]',
    status          TEXT DEFAULT 'active',
    team            TEXT DEFAULT 'platform',
    created_by      TEXT DEFAULT 'system',
    gateway_type    TEXT DEFAULT 'kong',
    health_path     TEXT DEFAULT '/health',
    authn_mechanism TEXT DEFAULT 'bearer',
    auth_issuer     TEXT DEFAULT '',
    authz_scopes    JSONB DEFAULT '[]',
    tls_required    BOOLEAN DEFAULT TRUE,
    notes           TEXT DEFAULT '',
    target_nodes    JSONB DEFAULT '[]',
    created_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

CREATE TABLE IF NOT EXISTS actual_routes (
    id              TEXT PRIMARY KEY,
    route_id        TEXT NOT NULL,
    gateway_type    TEXT,
    path            TEXT,
    actual_status   TEXT DEFAULT 'absent',
    actual_backend  TEXT DEFAULT '',
    drift           BOOLEAN DEFAULT FALSE,
    drift_detail    TEXT DEFAULT '',
    last_checked    DOUBLE PRECISION DEFAULT 0
);

CREATE TABLE IF NOT EXISTS audit_log (
    id       TEXT PRIMARY KEY,
    route_id TEXT,
    action   TEXT,
    actor    TEXT DEFAULT 'system',
    detail   TEXT DEFAULT '',
    ts       DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

CREATE TABLE IF NOT EXISTS fleets (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    subdomain       TEXT NOT NULL,
    lob             TEXT DEFAULT '',
    host_env        TEXT DEFAULT 'psaas',
    -- gateway_type is informational only; nodes within a fleet can be any type
    gateway_type    TEXT DEFAULT '',
    region          TEXT DEFAULT 'us-east',
    regions         JSONB DEFAULT '[]',
    auth_provider   TEXT DEFAULT '',
    instances_count DOUBLE PRECISION DEFAULT 4,
    status          TEXT DEFAULT 'healthy',
    description     TEXT DEFAULT '',
    traffic_type    TEXT DEFAULT 'web',
    tls_termination TEXT DEFAULT 'edge',
    http2_enabled   BOOLEAN DEFAULT TRUE,
    connection_limit INTEGER DEFAULT 1024,
    timeout_connect_ms INTEGER DEFAULT 5000,
    timeout_request_ms INTEGER DEFAULT 30000,
    rate_limit_rps  INTEGER DEFAULT 0,
    kong_plugins    JSONB DEFAULT '[]',
    health_check_path TEXT DEFAULT '/health',
    health_check_interval_s INTEGER DEFAULT 10,
    authn_mechanism TEXT DEFAULT 'bearer',
    default_authz_scopes JSONB DEFAULT '[]',
    tls_required    TEXT DEFAULT 'required',
    waf_profile     TEXT DEFAULT 'standard',
    resource_profile TEXT DEFAULT 'medium',
    autoscale_enabled BOOLEAN DEFAULT FALSE,
    autoscale_min   INTEGER DEFAULT 2,
    autoscale_max   INTEGER DEFAULT 16,
    autoscale_cpu_threshold INTEGER DEFAULT 70,
    notes           TEXT DEFAULT '',
    fleet_type      VARCHAR(20) DEFAULT 'data',
    created_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW()),
    updated_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

CREATE TABLE IF NOT EXISTS fleet_instances (
    id           TEXT PRIMARY KEY,
    fleet_id     TEXT NOT NULL,
    context_path TEXT NOT NULL,
    backend      TEXT NOT NULL,
    gateway_type TEXT DEFAULT 'envoy',
    status       TEXT DEFAULT 'active',
    latency_p99  DOUBLE PRECISION DEFAULT 0,
    route_id     TEXT DEFAULT '',
    created_at   DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

-- Add target_nodes column if it doesn't exist (for existing databases)
DO $$ BEGIN
    ALTER TABLE routes ADD COLUMN IF NOT EXISTS target_nodes JSONB DEFAULT '[]';
EXCEPTION WHEN others THEN NULL;
END $$;

-- Lambda / FaaS columns on routes
DO $$ BEGIN
    ALTER TABLE routes ADD COLUMN IF NOT EXISTS function_code TEXT DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE routes ADD COLUMN IF NOT EXISTS function_language VARCHAR(20) DEFAULT 'javascript';
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE routes ADD COLUMN IF NOT EXISTS lambda_container_id VARCHAR(255) DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE routes ADD COLUMN IF NOT EXISTS lambda_port INT DEFAULT 0;
EXCEPTION WHEN others THEN NULL;
END $$;

-- Route-to-node assignments: tracks which routes are deployed to which gateway nodes.
-- When a route is deployed, an assignment is created for each target node.
-- If all assignments for a route are removed, the route is "unattached".
CREATE TABLE IF NOT EXISTS route_node_assignments (
    id                TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    route_id          TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    node_container_id TEXT NOT NULL,
    fleet_id          TEXT NOT NULL,
    status            TEXT DEFAULT 'active',
    created_at        DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

-- Data-plane fleet nodes — tracks desired state of gateway nodes.
-- Nodes may be running (Docker container exists) or stopped (config only).
CREATE TABLE IF NOT EXISTS fleet_nodes (
    id              TEXT PRIMARY KEY,
    fleet_id        TEXT NOT NULL,
    node_name       TEXT NOT NULL,
    gateway_type    TEXT NOT NULL DEFAULT 'envoy',
    datacenter      TEXT DEFAULT 'us-east-1',
    status          TEXT DEFAULT 'stopped',
    port            INTEGER DEFAULT 0,
    container_id    TEXT DEFAULT '',
    created_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

-- Virtual nodes for control-plane fleets (not Docker-managed).
-- These appear in the nodes API alongside Docker-managed data-plane nodes.
CREATE TABLE IF NOT EXISTS cp_nodes (
    id              TEXT PRIMARY KEY,
    fleet_id        TEXT NOT NULL,
    container_name  TEXT NOT NULL,
    gateway_type    TEXT DEFAULT 'service',
    datacenter      TEXT DEFAULT 'us-east-2',
    status          TEXT DEFAULT 'active',
    port            INTEGER DEFAULT 0,
    docker_service  TEXT DEFAULT '',
    created_at      DOUBLE PRECISION DEFAULT EXTRACT(EPOCH FROM NOW())
);

-- Add notes and fleet_type columns to fleets if they don't exist (for existing databases)
DO $$ BEGIN
    ALTER TABLE fleets ADD COLUMN IF NOT EXISTS notes TEXT DEFAULT '';
EXCEPTION WHEN others THEN NULL;
END $$;
DO $$ BEGIN
    ALTER TABLE fleets ADD COLUMN IF NOT EXISTS fleet_type VARCHAR(20) DEFAULT 'data';
EXCEPTION WHEN others THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS health_reports (
    id                   TEXT PRIMARY KEY,
    gateway_type         TEXT NOT NULL,
    cluster_name         TEXT NOT NULL,
    backend_host         TEXT DEFAULT '',
    backend_port         DOUBLE PRECISION DEFAULT 0,
    health_status        TEXT DEFAULT 'unknown',
    latency_ms           DOUBLE PRECISION DEFAULT 0,
    consecutive_failures DOUBLE PRECISION DEFAULT 0,
    last_check_time      DOUBLE PRECISION DEFAULT 0,
    reporter             TEXT DEFAULT ''
);
