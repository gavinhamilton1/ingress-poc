# CIB Unified Ingress — MCP Server Guide

The ingress platform exposes a Model Context Protocol (MCP) server so AI agents
(Claude Desktop, Claude Code, custom agents) can manage fleets, configure routes,
and inspect platform status on behalf of an operator.

---

## How It Works

The MCP server (`cmd/mcp-server`) is a Go binary that:
- Communicates over **stdio** using the MCP protocol (JSON-RPC 2.0, newline-delimited)
- Calls the **Management API** (`http://management-api:8003`) internally
- Requires no extra infrastructure — the cluster's existing management-api is the backend

```
AI Agent (Claude)
      │  MCP stdio
      ▼
cib-ingress-mcp  ──HTTP──▶  management-api:8003  ──▶  Postgres + K8s
```

---

## Setup

### 1. Port-forward the Management API

The management-api runs inside the Kind cluster and is not publicly exposed.
Open a terminal and keep this running whenever you want MCP access:

```bash
kubectl port-forward -n ingress-cp svc/management-api 8003:8003
```

### 2. Build the MCP server binary

From the project root:

```bash
go build -o ~/bin/cib-ingress-mcp ./cmd/mcp-server/
```

### 3. Register with Claude Desktop

The `claude_desktop_config.json` is already configured. If you need to re-add it,
open `~/Library/Application Support/Claude/claude_desktop_config.json` and ensure
the `mcpServers` block is present:

```json
{
  "mcpServers": {
    "cib-ingress": {
      "command": "/Users/gavin/bin/cib-ingress-mcp",
      "env": {
        "MANAGEMENT_API_URL": "http://localhost:8003",
        "MANAGEMENT_API_KEY": ""
      }
    }
  }
}
```

Restart Claude Desktop after editing this file.

### 4. (Optional) Enable API Key Auth

If you want to protect the Management API from unauthenticated access, set
`MANAGEMENT_API_KEY` in the management-api deployment:

```bash
kubectl set env deployment/management-api -n ingress-cp MANAGEMENT_API_KEY=your-secret-key
```

Then pass the same key in the MCP config:

```json
"MANAGEMENT_API_KEY": "your-secret-key"
```

All endpoints except `/health` will then require `Authorization: Bearer <key>`.

---

## Available Tools

### Fleet Management

| Tool | Description |
|---|---|
| `list_fleets` | List all fleets with status, gateway type, and subdomain |
| `get_fleet` | Get full details for a fleet — accepts UUID or name |
| `create_fleet` | Create a new fleet (starts as `not_deployed`) |
| `update_fleet` | Update fleet config: name, description, instance count, rate limits |
| `delete_fleet` | Permanently delete a fleet and all its K8s resources |
| `deploy_fleet` | Spin up gateway nodes for a fleet |
| `scale_fleet` | Scale a fleet to a target node count |
| `suspend_fleet` | Stop all nodes while preserving configuration |
| `resume_fleet` | Restart a previously suspended fleet |

### Route Management

| Tool | Description |
|---|---|
| `list_routes` | List all routes, optionally filtered by hostname or status |
| `get_route` | Get full route config including auth and rate limit settings |
| `create_route` | Create a route mapping a hostname + path to a backend URL |
| `update_route` | Update backend URL, auth, status, or rate limits |
| `delete_route` | Remove a route from the gateway and GitOps manifests |
| `validate_route_policy` | Check a proposed route against CIB ingress policy before creating |

### Platform Status & Observability

| Tool | Description |
|---|---|
| `get_platform_status` | Summary of all fleet statuses, node counts, and drift indicators |
| `get_drift` | Full drift report: desired vs actual gateway configuration |
| `get_audit_log` | Recent create/update/delete operations with actor and timestamp |
| `get_gitops_status` | GitOps repo state and recent commits |

---

## Example Prompts

Once the MCP server is connected to Claude Desktop you can ask naturally:

**Status & Exploration**
> "What fleets are currently deployed and what's their health status?"

> "Show me all routes on jpmm.jpm.com"

> "Is there any configuration drift on the platform right now?"

> "What changes have been made to routes in the last 24 hours?"

**Fleet Operations**
> "Create a new fleet called 'JPMM Markets' on subdomain jpmm-markets.jpm.com using Envoy"

> "Deploy the JPMM Markets fleet with 4 nodes"

> "Scale the JPMM fleet down to 2 nodes"

> "Suspend the Execute fleet"

**Route Operations**
> "Add a route for /api/v1/prices on jpmm.jpm.com pointing to http://prices-svc:8080, require bearer auth"

> "Change the backend URL for the /research route to http://new-research-svc:8080"

> "Disable the /all-nodes-test route"

> "Delete all inactive routes on jpmm.jpm.com"

**Policy & Compliance**
> "Validate that a new route on jpmm.jpm.com with no auth would pass policy"

> "Which routes are configured without authentication?"

---

## Running as a K8s Pod (In-Cluster)

For production use the MCP server can run as a pod inside the cluster,
removing the need for a local port-forward.

Build and load the image:

```bash
docker build -f mcp-server/Dockerfile -t mcp-server:latest .
kind load docker-image mcp-server:latest --name ingress-cp
```

Deploy as a Job or persistent Deployment in the `ingress-cp` namespace.
Set `MANAGEMENT_API_URL=http://management-api:8003` — no port-forward needed.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `MANAGEMENT_API_URL` | `http://management-api:8003` | Base URL of the Management API |
| `MANAGEMENT_API_KEY` | _(empty)_ | Bearer token for API auth (optional) |

---

## Architecture Notes

- The MCP server is **stateless** — it holds no local state and can be restarted freely
- All operations are **synchronous** — tool calls return when the API call completes
- Fleet and route changes trigger **GitOps commits** in the background — the response
  reflects the desired-state update, not the final reconciled K8s state
- The `get_drift` tool shows whether desired state has been reconciled to actuals
- The `get_platform_status` tool is the best starting point for any diagnostic session
