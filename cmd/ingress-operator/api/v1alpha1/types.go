// Package v1alpha1 contains API Schema definitions for the ingress v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=ingress.jpmc.com
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --------------------------------------------------------------------------
// Fleet CRD
// --------------------------------------------------------------------------

// AutoscaleSpec defines the autoscaling configuration for a fleet.
type AutoscaleSpec struct {
	Enabled      bool `json:"enabled,omitempty"`
	Min          int  `json:"min,omitempty"`
	Max          int  `json:"max,omitempty"`
	CPUThreshold int  `json:"cpuThreshold,omitempty"`
}

// FleetSpec defines the desired state of a Fleet.
type FleetSpec struct {
	Name                 string       `json:"name"`
	Subdomain            string       `json:"subdomain,omitempty"`
	LOB                  string       `json:"lob,omitempty"`
	HostEnv              string       `json:"hostEnv,omitempty"`
	GatewayType          string       `json:"gatewayType"` // envoy, kong, mixed
	Region               string       `json:"region,omitempty"`
	Regions              []string     `json:"regions,omitempty"`
	AuthProvider         string       `json:"authProvider,omitempty"`
	Description          string       `json:"description,omitempty"`
	TrafficType          string       `json:"trafficType,omitempty"`   // web, api
	TLSTermination       string       `json:"tlsTermination,omitempty"`
	HTTP2Enabled         bool         `json:"http2Enabled,omitempty"`
	ConnectionLimit      int          `json:"connectionLimit,omitempty"`
	TimeoutConnectMs     int          `json:"timeoutConnectMs,omitempty"`
	TimeoutRequestMs     int          `json:"timeoutRequestMs,omitempty"`
	RateLimitRps         int          `json:"rateLimitRps,omitempty"`
	KongPlugins          []string     `json:"kongPlugins,omitempty"`
	HealthCheckPath      string       `json:"healthCheckPath,omitempty"`
	HealthCheckIntervalS int          `json:"healthCheckIntervalS,omitempty"`
	AuthnMechanism       string       `json:"authnMechanism,omitempty"`
	DefaultAuthzScopes   []string     `json:"defaultAuthzScopes,omitempty"`
	TLSRequired          string       `json:"tlsRequired,omitempty"`
	WAFProfile           string       `json:"wafProfile,omitempty"`
	ResourceProfile      string       `json:"resourceProfile,omitempty"` // small, medium, large
	Autoscale            AutoscaleSpec `json:"autoscale,omitempty"`
	Replicas             int32        `json:"replicas,omitempty"`
	FleetType            string       `json:"fleetType,omitempty"` // data, control
	Notes                string       `json:"notes,omitempty"`
}

// NodeStatus describes the status of a single gateway node.
type NodeStatus struct {
	Name        string `json:"name"`
	Ready       bool   `json:"ready"`
	GatewayType string `json:"gatewayType"`
	Address     string `json:"address,omitempty"`
}

// FleetStatus defines the observed state of a Fleet.
type FleetStatus struct {
	Phase           string             `json:"phase,omitempty"` // Pending, Provisioning, Ready, Degraded, Failed
	ReadyReplicas   int32              `json:"readyReplicas,omitempty"`
	DesiredReplicas int32              `json:"desiredReplicas,omitempty"`
	AvailableNodes  []NodeStatus       `json:"availableNodes,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Gateway",type=string,JSONPath=`.spec.gatewayType`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// Fleet is the Schema for the fleets API.
type Fleet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FleetSpec   `json:"spec,omitempty"`
	Status FleetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FleetList contains a list of Fleet.
type FleetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Fleet `json:"items"`
}

// --------------------------------------------------------------------------
// Route CRD
// --------------------------------------------------------------------------

// RouteSpec defines the desired state of a Route.
type RouteSpec struct {
	Path             string   `json:"path"`
	Hostname         string   `json:"hostname,omitempty"`
	BackendURL       string   `json:"backendURL"`
	Audience         string   `json:"audience,omitempty"`
	AllowedRoles     []string `json:"allowedRoles,omitempty"`
	Methods          []string `json:"methods,omitempty"`
	Team             string   `json:"team,omitempty"`
	CreatedBy        string   `json:"createdBy,omitempty"`
	GatewayType      string   `json:"gatewayType,omitempty"` // envoy, kong
	HealthPath       string   `json:"healthPath,omitempty"`
	AuthnMechanism   string   `json:"authnMechanism,omitempty"`
	AuthIssuer       string   `json:"authIssuer,omitempty"`
	AuthzScopes      []string `json:"authzScopes,omitempty"`
	TLSRequired      bool     `json:"tlsRequired,omitempty"`
	Notes            string   `json:"notes,omitempty"`
	TargetFleet      string   `json:"targetFleet"`
	TargetNodes      []string `json:"targetNodes,omitempty"`
	FunctionCode     string   `json:"functionCode,omitempty"`
	FunctionLanguage string   `json:"functionLanguage,omitempty"`
}

// RouteStatus defines the observed state of a Route.
type RouteStatus struct {
	Phase           string             `json:"phase,omitempty"` // Pending, Synced, Degraded, Failed
	DeployedToNodes []string           `json:"deployedToNodes,omitempty"`
	Drift           bool               `json:"drift,omitempty"`
	DriftDetail     string             `json:"driftDetail,omitempty"`
	LastChecked     *metav1.Time       `json:"lastChecked,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Fleet",type=string,JSONPath=`.spec.targetFleet`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=boolean,JSONPath=`.status.drift`

// Route is the Schema for the routes API.
type Route struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RouteSpec   `json:"spec,omitempty"`
	Status RouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RouteList contains a list of Route.
type RouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Route `json:"items"`
}
