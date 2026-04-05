// CIB Ingress MCP Server
//
// Implements the Model Context Protocol (MCP) stdio transport so AI agents
// can manage fleets, routes, and inspect platform status on behalf of a user.
//
// Usage:
//
//	MANAGEMENT_API_URL=http://management-api:8003 \
//	MANAGEMENT_API_KEY=<optional> \
//	./mcp-server
//
// The server reads JSON-RPC 2.0 messages from stdin (one per line) and
// writes responses to stdout. stderr is used for logging.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ── JSON-RPC / MCP wire types ─────────────────────────────────────────────────

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP capability types ──────────────────────────────────────────────────────

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ServerInfo      ServerInfo      `json:"serverInfo"`
	Capabilities    map[string]any  `json:"capabilities"`
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema Schema `json:"inputSchema"`
}

type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]SchemaProp `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type SchemaProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── Tool registry ─────────────────────────────────────────────────────────────

var tools = []ToolDef{
	{
		Name:        "list_fleets",
		Description: "List all ingress fleets with their current status, gateway type, and node counts. Use this to get an overview of what is deployed.",
		InputSchema: Schema{Type: "object", Properties: map[string]SchemaProp{}},
	},
	{
		Name:        "get_fleet",
		Description: "Get detailed information about a specific fleet including configuration, nodes, and health status.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id": {Type: "string", Description: "Fleet ID (UUID) or fleet name. If a name is given the API will match by name."},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name: "create_fleet",
		Description: `Create a new ingress fleet. A fleet is a logical grouping of gateway nodes serving a specific hostname/subdomain.
After creation the fleet starts in 'not_deployed' status — call deploy_fleet to spin up nodes.
Gateway type: use 'envoy' for web/streaming traffic, 'kong' for API/developer-portal traffic.`,
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"name":         {Type: "string", Description: "Human-readable fleet name, e.g. 'JPMM Markets'"},
				"subdomain":    {Type: "string", Description: "Subdomain this fleet serves, e.g. 'jpmm-markets'. Used for hostname routing."},
				"lob":          {Type: "string", Description: "Line of business, e.g. 'markets', 'banking', 'research'"},
				"gateway_type": {Type: "string", Description: "Gateway implementation", Enum: []string{"envoy", "kong"}},
				"traffic_type": {Type: "string", Description: "Traffic profile", Enum: []string{"web", "api", "mixed"}},
				"host_env":     {Type: "string", Description: "Hosting environment (default: psaas)", Enum: []string{"psaas", "aws", "on-prem"}},
				"description":  {Type: "string", Description: "Optional description of the fleet's purpose"},
				"instances_count": {Type: "string", Description: "Initial desired node count (default: 2)"},
			},
			Required: []string{"name", "subdomain", "lob"},
		},
	},
	{
		Name: "update_fleet",
		Description: "Update fleet configuration such as description, instance count, rate limits, or timeouts.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id":        {Type: "string", Description: "Fleet ID (UUID)"},
				"name":            {Type: "string", Description: "New display name"},
				"description":     {Type: "string", Description: "Updated description"},
				"instances_count": {Type: "string", Description: "Desired node count"},
				"rate_limit_rps":  {Type: "string", Description: "Rate limit in requests per second (0 = unlimited)"},
				"timeout_request_ms": {Type: "string", Description: "Request timeout in milliseconds"},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name:        "delete_fleet",
		Description: "Permanently delete a fleet and all its nodes. This removes K8s resources and the GitOps manifests. Cannot be undone.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id": {Type: "string", Description: "Fleet ID (UUID) to delete"},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name: "deploy_fleet",
		Description: `Deploy gateway nodes for a fleet OR add a route (including lambda/function routes) to a fleet.

TWO MODES:
1. Node deployment (no context_path): spins up gateway pods. Pass fleet_id and optionally count.
2. Route deployment (with context_path): adds a route to the fleet AND creates all required K8s resources.
   - For lambda/serverless routes: provide function_code and function_language. The API will build and deploy the lambda pod/service automatically.
   - For static proxy routes: provide backend_url pointing to an existing service.

IMPORTANT: Always use this tool (not create_route) when deploying lambda/function routes. create_route only writes metadata — it does NOT create the lambda pod or service.`,
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id":          {Type: "string", Description: "Fleet ID (UUID or k8s name slug, e.g. 'fleet-test') to deploy to"},
				"count":             {Type: "string", Description: "Number of nodes to deploy (mode 1 only, default: fleet's instances_count)"},
				"context_path":      {Type: "string", Description: "URL path for the route, e.g. '/api/v1/orders' (required for mode 2)"},
				"backend_url":       {Type: "string", Description: "Backend service URL for static proxy routes, e.g. 'http://orders-svc:8080' (mode 2, omit if using function_code)"},
				"gateway_type":      {Type: "string", Description: "Gateway type for the route", Enum: []string{"envoy", "kong"}},
				"function_code":     {Type: "string", Description: "Lambda function source code (JavaScript). If provided, a lambda pod+service will be created automatically. Example: module.exports = async (req, res) => { res.json({ message: 'hello' }) }"},
				"function_language": {Type: "string", Description: "Lambda function language (default: javascript)", Enum: []string{"javascript"}},
				"methods":           {Type: "string", Description: "JSON array of HTTP methods, e.g. '[\"GET\",\"POST\"]' (default: all)"},
				"audience":          {Type: "string", Description: "Required audience claim for JWT auth (optional)"},
				"notes":             {Type: "string", Description: "Human-readable notes about this route (optional)"},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name:        "scale_fleet",
		Description: "Scale a fleet to a different number of gateway nodes.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id": {Type: "string", Description: "Fleet ID (UUID)"},
				"count":    {Type: "string", Description: "Target node count"},
			},
			Required: []string{"fleet_id", "count"},
		},
	},
	{
		Name:        "suspend_fleet",
		Description: "Suspend a fleet — stops all nodes but preserves configuration. Use resume_fleet to bring it back.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id": {Type: "string", Description: "Fleet ID (UUID) to suspend"},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name:        "resume_fleet",
		Description: "Resume a previously suspended fleet — restarts all nodes.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"fleet_id": {Type: "string", Description: "Fleet ID (UUID) to resume"},
			},
			Required: []string{"fleet_id"},
		},
	},
	{
		Name:        "list_routes",
		Description: "List all configured ingress routes. Each route maps a hostname + path to a backend service. Optionally filter by hostname (fleet subdomain).",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"hostname": {Type: "string", Description: "Filter routes by hostname/subdomain (optional)"},
				"status":   {Type: "string", Description: "Filter by status: active, inactive (optional)"},
			},
		},
	},
	{
		Name:        "get_route",
		Description: "Get full details for a specific route including auth config, rate limits, and node assignments.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"route_id": {Type: "string", Description: "Route ID (UUID)"},
			},
			Required: []string{"route_id"},
		},
	},
	{
		Name: "create_route",
		Description: `Create a static ingress route that proxies to an already-running backend service.
Auth: use 'bearer' for JWT-protected endpoints, 'none' for public paths.
Methods: HTTP methods the route accepts, e.g. ["GET","POST"].

WARNING: Do NOT use this for lambda/serverless/function routes. If you need to deploy function code, use deploy_fleet with function_code instead — create_route does not create lambda pods or services.`,
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"hostname":    {Type: "string", Description: "Hostname/subdomain this route belongs to, must match an existing fleet subdomain"},
				"path":        {Type: "string", Description: "URL path prefix, e.g. '/api/v1/orders'"},
				"backend_url": {Type: "string", Description: "Backend service URL, e.g. 'http://orders-svc:8080'"},
				"description": {Type: "string", Description: "Human-readable description of what this route serves"},
				"authn_mechanism": {Type: "string", Description: "Auth requirement", Enum: []string{"bearer", "none", "mtls"}},
				"methods":     {Type: "string", Description: "JSON array of HTTP methods, e.g. '[\"GET\",\"POST\"]' (default: all)"},
				"team":        {Type: "string", Description: "Owning team name"},
				"gateway_type": {Type: "string", Description: "Which gateway handles this route", Enum: []string{"envoy", "kong"}},
				"rate_limit_rps": {Type: "string", Description: "Per-route rate limit in req/s (0 = unlimited)"},
			},
			Required: []string{"hostname", "path", "backend_url"},
		},
	},
	{
		Name:        "update_route",
		Description: "Update an existing route's configuration. Only provide the fields you want to change.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"route_id":    {Type: "string", Description: "Route ID (UUID) to update"},
				"backend_url": {Type: "string", Description: "New backend URL"},
				"description": {Type: "string", Description: "Updated description"},
				"authn_mechanism": {Type: "string", Description: "Updated auth requirement"},
				"status":      {Type: "string", Description: "Route status", Enum: []string{"active", "inactive"}},
				"rate_limit_rps": {Type: "string", Description: "Updated rate limit"},
			},
			Required: []string{"route_id"},
		},
	},
	{
		Name:        "delete_route",
		Description: "Delete a route. Removes the route from the gateway configuration and GitOps manifests immediately.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"route_id": {Type: "string", Description: "Route ID (UUID) to delete"},
			},
			Required: []string{"route_id"},
		},
	},
	{
		Name:        "get_platform_status",
		Description: "Get a summary of platform health: fleet statuses, node counts, drift indicators, and recent activity. Good starting point for diagnosing issues.",
		InputSchema: Schema{Type: "object", Properties: map[string]SchemaProp{}},
	},
	{
		Name:        "get_drift",
		Description: "Get the drift report — shows any divergence between desired configuration (registry) and actual gateway state. Non-empty results indicate config drift that needs attention.",
		InputSchema: Schema{Type: "object", Properties: map[string]SchemaProp{}},
	},
	{
		Name:        "get_audit_log",
		Description: "Get recent audit log entries — every route/fleet create, update, delete operation with who made it and when.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"limit": {Type: "string", Description: "Number of entries to return (default: 20, max: 100)"},
			},
		},
	},
	{
		Name:        "get_gitops_status",
		Description: "Get GitOps sync status — shows the state of the Git repository, recent commits, and whether ArgoCD is in sync.",
		InputSchema: Schema{Type: "object", Properties: map[string]SchemaProp{}},
	},
	{
		Name:        "validate_route_policy",
		Description: "Validate a proposed route configuration against CIB ingress policy before creating it. Returns pass/fail with specific policy violations if any.",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]SchemaProp{
				"hostname":        {Type: "string", Description: "Hostname for the proposed route"},
				"path":            {Type: "string", Description: "URL path for the proposed route"},
				"authn_mechanism": {Type: "string", Description: "Auth mechanism to validate"},
				"gateway_type":    {Type: "string", Description: "Gateway type to validate against"},
			},
			Required: []string{"hostname", "path"},
		},
	},
}

// ── API client ────────────────────────────────────────────────────────────────

var (
	apiURL    string
	apiKey    string
	apiClient = &http.Client{Timeout: 15 * time.Second}
)

func apiCall(method, path string, body any) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, apiURL+path, reqBody)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("management API unreachable: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func apiGet(path string) ([]byte, int, error)               { return apiCall("GET", path, nil) }
func apiPost(path string, body any) ([]byte, int, error)    { return apiCall("POST", path, body) }
func apiPut(path string, body any) ([]byte, int, error)     { return apiCall("PUT", path, body) }
func apiDelete(path string) ([]byte, int, error)            { return apiCall("DELETE", path, nil) }

// prettyJSON re-indents API response bytes for readable tool output.
func prettyJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(b)
}

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func toolError(msg string) ToolResult {
	return ToolResult{Content: []ContentBlock{{Type: "text", Text: "Error: " + msg}}, IsError: true}
}

func toolOK(data []byte, statusCode int) ToolResult {
	if statusCode >= 400 {
		return ToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("API returned %d:\n%s", statusCode, prettyJSON(data))}},
			IsError: true,
		}
	}
	return ToolResult{Content: []ContentBlock{{Type: "text", Text: prettyJSON(data)}}}
}

// ── Tool handlers ─────────────────────────────────────────────────────────────

func handleTool(name string, args map[string]any) ToolResult {
	switch name {

	case "list_fleets":
		data, code, err := apiGet("/fleets")
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "get_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		// Try direct lookup first, then scan by name
		data, code, err := apiGet("/fleets/" + id)
		if err != nil {
			return toolError(err.Error())
		}
		if code == 404 {
			// Try matching by name from the full list
			all, _, err2 := apiGet("/fleets")
			if err2 != nil {
				return toolOK(data, code)
			}
			var fleets []map[string]any
			if json.Unmarshal(all, &fleets) == nil {
				for _, f := range fleets {
					if strings.EqualFold(fmt.Sprintf("%v", f["name"]), id) {
						fID := fmt.Sprintf("%v", f["id"])
						data2, code2, _ := apiGet("/fleets/" + fID)
						return toolOK(data2, code2)
					}
				}
			}
		}
		return toolOK(data, code)

	case "create_fleet":
		body := map[string]any{
			"name":        strArg(args, "name"),
			"subdomain":   strArg(args, "subdomain"),
			"lob":         strArg(args, "lob"),
			"host_env":    strArg(args, "host_env"),
			"description": strArg(args, "description"),
		}
		if gt := strArg(args, "gateway_type"); gt != "" {
			body["gateway_type"] = gt
		}
		if tt := strArg(args, "traffic_type"); tt != "" {
			body["traffic_type"] = tt
		}
		if ic := strArg(args, "instances_count"); ic != "" {
			body["instances_count"] = ic
		}
		data, code, err := apiPost("/fleets", body)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "update_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		body := map[string]any{}
		for _, k := range []string{"name", "description", "instances_count", "rate_limit_rps", "timeout_request_ms"} {
			if v := strArg(args, k); v != "" {
				body[k] = v
			}
		}
		data, code, err := apiPut("/fleets/"+id, body)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "delete_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		data, code, err := apiDelete("/fleets/" + id)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "deploy_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		body := map[string]any{}
		for _, k := range []string{"count", "context_path", "backend_url", "gateway_type",
			"function_code", "function_language", "audience", "notes"} {
			if v := strArg(args, k); v != "" {
				body[k] = v
			}
		}
		if m := strArg(args, "methods"); m != "" {
			var methods []string
			if err := json.Unmarshal([]byte(m), &methods); err == nil {
				body["methods"] = methods
			}
		}
		data, code, err := apiPost("/fleets/"+id+"/deploy", body)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "scale_fleet":
		id := strArg(args, "fleet_id")
		count := strArg(args, "count")
		if id == "" || count == "" {
			return toolError("fleet_id and count are required")
		}
		data, code, err := apiPost("/fleets/"+id+"/scale", map[string]any{"count": count})
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "suspend_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		data, code, err := apiPost("/fleets/"+id+"/suspend", nil)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "resume_fleet":
		id := strArg(args, "fleet_id")
		if id == "" {
			return toolError("fleet_id is required")
		}
		data, code, err := apiPost("/fleets/"+id+"/resume", nil)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "list_routes":
		path := "/routes"
		parts := []string{}
		if h := strArg(args, "hostname"); h != "" {
			parts = append(parts, "hostname="+h)
		}
		if s := strArg(args, "status"); s != "" {
			parts = append(parts, "status="+s)
		}
		if len(parts) > 0 {
			path += "?" + strings.Join(parts, "&")
		}
		data, code, err := apiGet(path)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "get_route":
		id := strArg(args, "route_id")
		if id == "" {
			return toolError("route_id is required")
		}
		data, code, err := apiGet("/routes/" + id)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "create_route":
		hostname := strArg(args, "hostname")
		path := strArg(args, "path")
		backendURL := strArg(args, "backend_url")
		if hostname == "" || path == "" || backendURL == "" {
			return toolError("hostname, path, and backend_url are required")
		}
		body := map[string]any{
			"hostname":    hostname,
			"path":        path,
			"backend_url": backendURL,
		}
		for _, k := range []string{"description", "authn_mechanism", "team", "gateway_type", "rate_limit_rps"} {
			if v := strArg(args, k); v != "" {
				body[k] = v
			}
		}
		if m := strArg(args, "methods"); m != "" {
			var methods []string
			if err := json.Unmarshal([]byte(m), &methods); err == nil {
				body["methods"] = methods
			}
		}
		data, code, err := apiPost("/routes", body)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "update_route":
		id := strArg(args, "route_id")
		if id == "" {
			return toolError("route_id is required")
		}
		body := map[string]any{}
		for _, k := range []string{"backend_url", "description", "authn_mechanism", "status", "rate_limit_rps"} {
			if v := strArg(args, k); v != "" {
				body[k] = v
			}
		}
		data, code, err := apiPut("/routes/"+id, body)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "delete_route":
		id := strArg(args, "route_id")
		if id == "" {
			return toolError("route_id is required")
		}
		data, code, err := apiDelete("/routes/" + id)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "get_platform_status":
		// Fetch fleets + drift in parallel and compose a summary
		fleetData, _, err1 := apiGet("/fleets")
		driftData, _, err2 := apiGet("/drift")
		if err1 != nil {
			return toolError(err1.Error())
		}

		var fleets []map[string]any
		json.Unmarshal(fleetData, &fleets)

		summary := map[string]any{"fleets": map[string]any{}}
		statuses := map[string]int{}
		totalNodes := 0
		for _, f := range fleets {
			status := fmt.Sprintf("%v", f["status"])
			statuses[status]++
			if nodes, ok := f["nodes"].([]any); ok {
				totalNodes += len(nodes)
			}
			fleetSummary := map[string]any{
				"id":           f["id"],
				"name":         f["name"],
				"status":       status,
				"gateway_type": f["gateway_type"],
				"subdomain":    f["subdomain"],
			}
			summary["fleets"].(map[string]any)[fmt.Sprintf("%v", f["id"])] = fleetSummary
		}
		summary["fleet_count"] = len(fleets)
		summary["status_breakdown"] = statuses
		summary["total_nodes"] = totalNodes

		if err2 == nil {
			var drift []any
			if json.Unmarshal(driftData, &drift) == nil {
				summary["drift_count"] = len(drift)
				if len(drift) > 0 {
					summary["drift_items"] = drift
				}
			}
		}

		b, _ := json.MarshalIndent(summary, "", "  ")
		return ToolResult{Content: []ContentBlock{{Type: "text", Text: string(b)}}}

	case "get_drift":
		data, code, err := apiGet("/drift")
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "get_audit_log":
		limit := strArg(args, "limit")
		if limit == "" {
			limit = "20"
		}
		data, code, err := apiGet("/audit-log?limit=" + limit)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	case "get_gitops_status":
		statusData, _, err1 := apiGet("/gitops/status")
		commitsData, _, err2 := apiGet("/gitops/commits")
		if err1 != nil {
			return toolError(err1.Error())
		}
		result := map[string]any{}
		json.Unmarshal(statusData, &result)
		if err2 == nil {
			var commits any
			if json.Unmarshal(commitsData, &commits) == nil {
				result["recent_commits"] = commits
			}
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		return ToolResult{Content: []ContentBlock{{Type: "text", Text: string(b)}}}

	case "validate_route_policy":
		params := []string{}
		for _, k := range []string{"hostname", "path", "authn_mechanism", "gateway_type"} {
			if v := strArg(args, k); v != "" {
				params = append(params, k+"="+v)
			}
		}
		path := "/policy/validate"
		if len(params) > 0 {
			path += "?" + strings.Join(params, "&")
		}
		data, code, err := apiGet(path)
		if err != nil {
			return toolError(err.Error())
		}
		return toolOK(data, code)

	default:
		return toolError("unknown tool: " + name)
	}
}

// ── MCP message dispatch ──────────────────────────────────────────────────────

func dispatch(req Request) *Response {
	resp := &Response{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {

	case "initialize":
		resp.Result = InitializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo:      ServerInfo{Name: "cib-ingress-mcp", Version: "1.0.0"},
			Capabilities: map[string]any{
				"tools": map[string]any{},
			},
		}

	case "notifications/initialized":
		return nil // no response for notifications

	case "ping":
		resp.Result = map[string]any{}

	case "tools/list":
		resp.Result = map[string]any{"tools": tools}

	case "tools/call":
		var p ToolCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &RPCError{Code: -32602, Message: "invalid params: " + err.Error()}
			return resp
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		result := handleTool(p.Name, p.Arguments)
		resp.Result = result

	default:
		resp.Error = &RPCError{Code: -32601, Message: "method not found: " + req.Method}
	}

	return resp
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[cib-ingress-mcp] ")
	log.SetFlags(log.LstdFlags)

	apiURL = os.Getenv("MANAGEMENT_API_URL")
	if apiURL == "" {
		apiURL = "http://management-api:8003"
	}
	apiKey = os.Getenv("MANAGEMENT_API_KEY")

	log.Printf("starting — management API: %s  auth: %v", apiURL, apiKey != "")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4MB max message
	writer := bufio.NewWriter(os.Stdout)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("parse error: %v", err)
			errResp := Response{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "parse error"},
			}
			if b, err := json.Marshal(errResp); err == nil {
				writer.Write(b)
				writer.WriteByte('\n')
				writer.Flush()
			}
			continue
		}

		resp := dispatch(req)
		if resp == nil {
			continue // notification — no response
		}

		b, err := json.Marshal(resp)
		if err != nil {
			log.Printf("marshal error: %v", err)
			continue
		}
		writer.Write(b)
		writer.WriteByte('\n')
		writer.Flush()
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("stdin error: %v", err)
	}
}
