package main

import "encoding/json"

// Route represents the desired state of a route in the registry.
type Route struct {
	ID             string          `db:"id" json:"id"`
	Path           string          `db:"path" json:"path"`
	Hostname       string          `db:"hostname" json:"hostname"`
	BackendURL     string          `db:"backend_url" json:"backend_url"`
	Audience       string          `db:"audience" json:"audience"`
	AllowedRoles   json.RawMessage `db:"allowed_roles" json:"allowed_roles"`
	Methods        json.RawMessage `db:"methods" json:"methods"`
	Status         string          `db:"status" json:"status"`
	Team           string          `db:"team" json:"team"`
	CreatedBy      string          `db:"created_by" json:"created_by"`
	GatewayType    string          `db:"gateway_type" json:"gateway_type"`
	HealthPath     string          `db:"health_path" json:"health_path"`
	AuthnMechanism string          `db:"authn_mechanism" json:"authn_mechanism"`
	AuthIssuer     string          `db:"auth_issuer" json:"auth_issuer"`
	AuthzScopes    json.RawMessage `db:"authz_scopes" json:"authz_scopes"`
	TLSRequired    bool            `db:"tls_required" json:"tls_required"`
	Notes             string          `db:"notes" json:"notes"`
	TargetNodes       json.RawMessage `db:"target_nodes" json:"target_nodes"`
	FunctionCode      string          `db:"function_code" json:"function_code"`
	FunctionLanguage  string          `db:"function_language" json:"function_language"`
	LambdaContainerID string          `db:"lambda_container_id" json:"lambda_container_id"`
	LambdaPort        int             `db:"lambda_port" json:"lambda_port"`
	GitCommitSHA      string          `db:"git_commit_sha" json:"git_commit_sha,omitempty"`
	GitManifestPath   string          `db:"git_manifest_path" json:"git_manifest_path,omitempty"`
	SyncStatus        string          `db:"sync_status" json:"sync_status,omitempty"`
	CreatedAt         float64         `db:"created_at" json:"created_at"`
	UpdatedAt         float64         `db:"updated_at" json:"updated_at"`
}

// ActualRoute represents the actual state of a route as seen by the gateway.
type ActualRoute struct {
	ID            string  `db:"id" json:"id"`
	RouteID       string  `db:"route_id" json:"route_id"`
	GatewayType   string  `db:"gateway_type" json:"gateway_type"`
	Path          string  `db:"path" json:"path"`
	ActualStatus  string  `db:"actual_status" json:"actual_status"`
	ActualBackend string  `db:"actual_backend" json:"actual_backend"`
	Drift         bool    `db:"drift" json:"drift"`
	DriftDetail   string  `db:"drift_detail" json:"drift_detail"`
	LastChecked   float64 `db:"last_checked" json:"last_checked"`
}

// AuditLog records changes to routes.
type AuditLog struct {
	ID      string  `db:"id" json:"id"`
	RouteID string  `db:"route_id" json:"route_id"`
	Action  string  `db:"action" json:"action"`
	Actor   string  `db:"actor" json:"actor"`
	Detail  string  `db:"detail" json:"detail"`
	TS      float64 `db:"ts" json:"ts"`
}

// Fleet represents a logical group of gateway instances.
// gateway_type is informational only; nodes within a fleet can be any type.
// A fleet can contain both Envoy and Kong nodes simultaneously.
type Fleet struct {
	ID             string          `db:"id" json:"id"`
	Name           string          `db:"name" json:"name"`
	Subdomain      string          `db:"subdomain" json:"subdomain"`
	LOB            string          `db:"lob" json:"lob"`
	HostEnv        string          `db:"host_env" json:"host_env"`
	GatewayType    string          `db:"gateway_type" json:"gateway_type"` // informational only; nodes within a fleet can be any type
	Region         string          `db:"region" json:"region"`
	Regions        json.RawMessage `db:"regions" json:"regions"`
	AuthProvider   string          `db:"auth_provider" json:"auth_provider"`
	InstancesCount float64         `db:"instances_count" json:"instances_count"`
	Status               string          `db:"status" json:"status"`
	Description          string          `db:"description" json:"description"`
	TrafficType          string          `db:"traffic_type" json:"traffic_type"`
	TLSTermination       string          `db:"tls_termination" json:"tls_termination"`
	HTTP2Enabled         bool            `db:"http2_enabled" json:"http2_enabled"`
	ConnectionLimit      int             `db:"connection_limit" json:"connection_limit"`
	TimeoutConnectMs     int             `db:"timeout_connect_ms" json:"timeout_connect_ms"`
	TimeoutRequestMs     int             `db:"timeout_request_ms" json:"timeout_request_ms"`
	RateLimitRPS         int             `db:"rate_limit_rps" json:"rate_limit_rps"`
	KongPlugins          json.RawMessage `db:"kong_plugins" json:"kong_plugins"`
	HealthCheckPath      string          `db:"health_check_path" json:"health_check_path"`
	HealthCheckIntervalS int             `db:"health_check_interval_s" json:"health_check_interval_s"`
	AuthnMechanism       string          `db:"authn_mechanism" json:"authn_mechanism"`
	DefaultAuthzScopes   json.RawMessage `db:"default_authz_scopes" json:"default_authz_scopes"`
	TLSRequired          string          `db:"tls_required" json:"tls_required"`
	WAFProfile           string          `db:"waf_profile" json:"waf_profile"`
	ResourceProfile      string          `db:"resource_profile" json:"resource_profile"`
	AutoscaleEnabled     bool            `db:"autoscale_enabled" json:"autoscale_enabled"`
	AutoscaleMin         int             `db:"autoscale_min" json:"autoscale_min"`
	AutoscaleMax         int             `db:"autoscale_max" json:"autoscale_max"`
	AutoscaleCPUThresh   int             `db:"autoscale_cpu_threshold" json:"autoscale_cpu_threshold"`
	Notes                string          `db:"notes" json:"notes"`
	FleetType            string          `db:"fleet_type" json:"fleet_type"`
	K8sName              string          `db:"k8s_name" json:"k8s_name,omitempty"`
	GitCommitSHA         string          `db:"git_commit_sha" json:"git_commit_sha,omitempty"`
	GitManifestPath      string          `db:"git_manifest_path" json:"git_manifest_path,omitempty"`
	SyncStatus           string          `db:"sync_status" json:"sync_status,omitempty"`
	CreatedAt            float64         `db:"created_at" json:"created_at"`
	UpdatedAt            float64         `db:"updated_at" json:"updated_at"`
}

// CpNode represents a virtual node for a control-plane fleet (not Docker-managed).
type CpNode struct {
	ID            string  `db:"id" json:"id"`
	FleetID       string  `db:"fleet_id" json:"fleet_id"`
	ContainerName string  `db:"container_name" json:"container_name"`
	GatewayType   string  `db:"gateway_type" json:"gateway_type"`
	Datacenter    string  `db:"datacenter" json:"datacenter"`
	Status        string  `db:"status" json:"status"`
	Port          int     `db:"port" json:"port"`
	DockerService string  `db:"docker_service" json:"docker_service"`
	CreatedAt     float64 `db:"created_at" json:"created_at"`
}

// FleetNodeRecord represents a data-plane gateway node (desired state).
// May be running (Docker container exists) or stopped (config only).
type FleetNodeRecord struct {
	ID          string  `db:"id" json:"id"`
	FleetID     string  `db:"fleet_id" json:"fleet_id"`
	NodeName    string  `db:"node_name" json:"node_name"`
	GatewayType string  `db:"gateway_type" json:"gateway_type"`
	Datacenter  string  `db:"datacenter" json:"datacenter"`
	Status      string  `db:"status" json:"status"`
	Port        int     `db:"port" json:"port"`
	ContainerID string  `db:"container_id" json:"container_id"`
	CreatedAt   float64 `db:"created_at" json:"created_at"`
}

// FleetInstance represents a route instance deployed to a fleet.
type FleetInstance struct {
	ID          string  `db:"id" json:"id"`
	FleetID     string  `db:"fleet_id" json:"fleet_id"`
	ContextPath string  `db:"context_path" json:"context_path"`
	Backend     string  `db:"backend" json:"backend"`
	GatewayType string  `db:"gateway_type" json:"gateway_type"`
	Status      string  `db:"status" json:"status"`
	LatencyP99  float64 `db:"latency_p99" json:"latency_p99"`
	RouteID            string          `db:"route_id" json:"route_id"`
	CreatedAt          float64         `db:"created_at" json:"created_at"`
	FunctionCode       string          `db:"-" json:"function_code,omitempty"`
	FunctionLanguage   string          `db:"-" json:"function_language,omitempty"`
	LambdaContainerID  string          `db:"-" json:"lambda_container_id,omitempty"`
	Audience           string          `db:"-" json:"audience,omitempty"`
	Methods            json.RawMessage `db:"-" json:"methods,omitempty"`
}

// HealthReport records health probe results from gateway proxies.
type HealthReport struct {
	ID                  string  `db:"id" json:"id"`
	GatewayType         string  `db:"gateway_type" json:"gateway_type"`
	ClusterName         string  `db:"cluster_name" json:"cluster_name"`
	BackendHost         string  `db:"backend_host" json:"backend_host"`
	BackendPort         float64 `db:"backend_port" json:"backend_port"`
	HealthStatus        string  `db:"health_status" json:"health_status"`
	LatencyMS           float64 `db:"latency_ms" json:"latency_ms"`
	ConsecutiveFailures float64 `db:"consecutive_failures" json:"consecutive_failures"`
	LastCheckTime       float64 `db:"last_check_time" json:"last_check_time"`
	Reporter            string  `db:"reporter" json:"reporter"`
}

// FleetWithInstances is the API response shape for fleet endpoints.
type FleetWithInstances struct {
	Fleet
	Instances []FleetInstance `json:"instances"`
}

// RouteNodeAssignment tracks which routes are deployed to which gateway nodes.
// When all assignments for a route are removed, the route is considered "unattached".
type RouteNodeAssignment struct {
	ID              string  `db:"id" json:"id"`
	RouteID         string  `db:"route_id" json:"route_id"`
	NodeContainerID string  `db:"node_container_id" json:"node_container_id"`
	FleetID         string  `db:"fleet_id" json:"fleet_id"`
	Status          string  `db:"status" json:"status"`
	CreatedAt       float64 `db:"created_at" json:"created_at"`
}

// FleetWithNodes is the enriched API response shape that includes both instances and live nodes.
type FleetWithNodes struct {
	Fleet
	Instances []FleetInstance `json:"instances"`
	Nodes     []FleetNode     `json:"nodes,omitempty"`
}

// DriftReport is the API response shape for the /drift endpoint.
type DriftReport struct {
	RouteID        string  `json:"route_id"`
	Path           string  `json:"path"`
	DesiredStatus  string  `json:"desired_status"`
	GatewayType    string  `json:"gateway_type"`
	DesiredBackend string  `json:"desired_backend"`
	ActualStatus   string  `json:"actual_status"`
	ActualBackend  string  `json:"actual_backend"`
	Drift          bool    `json:"drift"`
	DriftDetail    string  `json:"drift_detail"`
	LastChecked    float64 `json:"last_checked"`
}
