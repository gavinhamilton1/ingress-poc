package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	dockerSocketPath   = "/var/run/docker.sock"
	envoyBasePort      = 9000
	kongBasePort       = 9100
	lambdaBasePort     = 9200
	defaultNetworkName = "ingress-poc_default"
	lambdaImage        = "node:20-alpine"
	lambdaInternalPort = 8080
)

// FleetNode represents a running gateway container instance.
type FleetNode struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	FleetID       string `json:"fleet_id"`
	GatewayType   string `json:"gateway_type"`
	Port          int    `json:"port"`
	Status        string `json:"status"`
	Index         int    `json:"index"`
	Datacenter    string `json:"datacenter"`
	Region        string `json:"region"`
}

// Mock datacenter/region assignment based on node index.
var datacenters = []struct{ DC, Region, AZ string }{
	{"us-east-1", "us-east-1", "US East (N. Virginia)"},
	{"us-east-2", "us-east-2", "US East (Ohio)"},
	{"eu-west-1", "eu-west-1", "EU West (Ireland)"},
	{"ap-southeast-1", "ap-southeast-1", "AP Southeast (Singapore)"},
}

// overrideDatacenter is set when the user explicitly picks a DC.
var overrideDatacenter string

// overrideContainerName is set when the user provides a custom node name.
var overrideContainerName string

func assignDatacenter(index int) string {
	if overrideDatacenter != "" {
		return overrideDatacenter
	}
	return datacenters[index%len(datacenters)].DC
}

func assignRegion(index int) string {
	if overrideDatacenter != "" {
		// Look up region from override DC
		for _, dc := range datacenters {
			if dc.DC == overrideDatacenter {
				return dc.Region
			}
		}
		// Extract region from DC name (e.g., "us-east-1a" -> "us-east-1")
		if len(overrideDatacenter) > 1 {
			return overrideDatacenter[:len(overrideDatacenter)-1]
		}
	}
	return datacenters[index%len(datacenters)].Region
}

// portMu protects allocated port tracking across concurrent fleet creation.
var portMu sync.Mutex

// dockerHTTPClient returns an *http.Client that dials the Docker Unix socket.
func dockerHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", dockerSocketPath)
			},
		},
	}
}

// dockerRequest sends an HTTP request to the Docker Engine API over the Unix socket.
func dockerRequest(method, path string, body interface{}) (*http.Response, error) {
	client := dockerHTTPClient()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := "http://localhost" + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return client.Do(req)
}

// dockerResponseBody reads and closes a Docker API response body.
func dockerResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// getDockerNetworkName returns the Docker network name to attach containers to.
// It first tries to find the network from the DOCKER_NETWORK env var,
// then looks for the compose default network, and falls back to the constant.
func getDockerNetworkName() string {
	if env := os.Getenv("DOCKER_NETWORK"); env != "" {
		return env
	}
	// Try to auto-detect: list networks and find one matching *ingress*poc*default
	resp, err := dockerRequest("GET", "/v1.46/networks", nil)
	if err != nil {
		return defaultNetworkName
	}
	data, err := dockerResponseBody(resp)
	if err != nil {
		return defaultNetworkName
	}
	var networks []struct {
		Name string `json:"Name"`
	}
	json.Unmarshal(data, &networks)
	for _, n := range networks {
		if strings.Contains(n.Name, "ingress") && strings.Contains(n.Name, "default") {
			return n.Name
		}
	}
	return defaultNetworkName
}

// findNextAvailablePort scans existing fleet containers to find the next unused port
// in the given base range.
func findNextAvailablePort(basePort int, count int) ([]int, error) {
	portMu.Lock()
	defer portMu.Unlock()

	usedPorts := map[int]bool{}

	// List all managed containers to find used ports
	resp, err := dockerRequest("GET", `/v1.46/containers/json?all=true&filters={"label":["ingress.managed=true"]}`, nil)
	if err == nil {
		data, _ := dockerResponseBody(resp)
		var containers []struct {
			Ports []struct {
				PublicPort int `json:"PublicPort"`
			} `json:"Ports"`
		}
		json.Unmarshal(data, &containers)
		for _, c := range containers {
			for _, p := range c.Ports {
				if p.PublicPort > 0 {
					usedPorts[p.PublicPort] = true
				}
			}
		}
	}

	ports := make([]int, 0, count)
	candidate := basePort + 1 // start from basePort+1 (e.g., 9001)
	for len(ports) < count {
		if !usedPorts[candidate] {
			ports = append(ports, candidate)
		}
		candidate++
		if candidate > basePort+500 {
			return nil, fmt.Errorf("exhausted port range starting at %d", basePort)
		}
	}
	return ports, nil
}

// generateEnvoyBootstrapYAML returns the bootstrap YAML string for an Envoy
// instance. Uses REST xDS (matching our control plane) with node.cluster set
// to the fleet ID so the control plane can filter routes per-fleet.
func generateEnvoyBootstrapYAML(fleetID string, index int) string {
	nodeID := fmt.Sprintf("fleet-%s-%d", fleetID, index)
	return fmt.Sprintf(`admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 9901

node:
  id: "%s"
  cluster: "%s"

dynamic_resources:
  lds_config:
    resource_api_version: V3
    api_config_source:
      api_type: REST
      transport_api_version: V3
      cluster_names: ["xds_cluster"]
      refresh_delay: 5s
  cds_config:
    resource_api_version: V3
    api_config_source:
      api_type: REST
      transport_api_version: V3
      cluster_names: ["xds_cluster"]
      refresh_delay: 5s

static_resources:
  clusters:
    - name: xds_cluster
      connect_timeout: 5s
      type: STRICT_DNS
      lb_policy: ROUND_ROBIN
      load_assignment:
        cluster_name: xds_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: envoy-control-plane
                      port_value: 8080
`, nodeID, fleetID)
}

// pullImageIfNeeded checks if an image exists locally and pulls it if not.
func pullImageIfNeeded(image string) error {
	// Check if image exists
	encodedImage := strings.ReplaceAll(image, "/", "%2F")
	encodedImage = strings.ReplaceAll(encodedImage, ":", "%3A")
	resp, err := dockerRequest("GET", "/v1.46/images/"+encodedImage+"/json", nil)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return nil // image exists
		}
	}

	// Pull the image
	log.Printf("Pulling image %s ...", image)
	resp, err = dockerRequest("POST", "/v1.46/images/create?fromImage="+strings.Split(image, ":")[0]+"&tag="+strings.Split(image, ":")[1], nil)
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer resp.Body.Close()

	// Drain the response body (Docker streams pull progress)
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != 200 {
		return fmt.Errorf("pull image %s: status %d", image, resp.StatusCode)
	}
	log.Printf("Pulled image %s", image)
	return nil
}

// createFleetContainers creates N gateway containers for a fleet.
// For envoy fleets: creates envoyproxy/envoy:v1.30-latest containers
// For kong fleets: creates kong:3.6 containers
// Each container is named fleet-{fleetID}-{gateway_type}-{index}
func createFleetContainers(fleetID, gatewayType string, count int, networkName string) ([]FleetNode, error) {
	if networkName == "" {
		networkName = getDockerNetworkName()
	}

	var image string
	var basePort int
	switch gatewayType {
	case "envoy":
		image = "envoyproxy/envoy:v1.30-latest"
		basePort = envoyBasePort
	case "kong":
		image = "kong:3.6"
		basePort = kongBasePort
	default:
		return nil, fmt.Errorf("unsupported gateway type: %s", gatewayType)
	}

	// Pull image first
	if err := pullImageIfNeeded(image); err != nil {
		log.Printf("Warning: could not pull image %s: %v (will try to use local)", image, err)
	}

	// Find available ports
	ports, err := findNextAvailablePort(basePort, count)
	if err != nil {
		return nil, fmt.Errorf("allocate ports: %w", err)
	}

	nodes := make([]FleetNode, 0, count)
	for i := 0; i < count; i++ {
		index := i + 1
		containerName := fmt.Sprintf("%s-%s-%d", fleetID, gatewayType, index)
		hostPort := ports[i]

		var containerConfig map[string]interface{}
		var hostConfig map[string]interface{}

		if gatewayType == "envoy" {
			// Pass bootstrap YAML via env var and write it inline at container start
			bootstrapYAML := generateEnvoyBootstrapYAML(fleetID, index)

			containerConfig = map[string]interface{}{
				"Image": image,
				"ExposedPorts": map[string]interface{}{
					"8000/tcp": map[string]interface{}{},
					"9901/tcp": map[string]interface{}{},
				},
				"Env": []string{
					"ENVOY_BOOTSTRAP=" + bootstrapYAML,
				},
				"Cmd": []string{
					"sh", "-c",
					`printf '%s' "$ENVOY_BOOTSTRAP" > /tmp/bootstrap.yaml && envoy -c /tmp/bootstrap.yaml --service-node ` +
						fmt.Sprintf("%s-%d", fleetID, index) + ` --service-cluster ` + fleetID,
				},
				"Labels": map[string]string{
					"ingress.fleet":      fleetID,
					"ingress.gateway":    "envoy",
					"ingress.managed":    "true",
					"ingress.index":      fmt.Sprintf("%d", index),
					"ingress.datacenter": assignDatacenter(index),
					"ingress.region":     assignRegion(index),
				},
			}
			hostConfig = map[string]interface{}{
				"PortBindings": map[string]interface{}{
					"8000/tcp": []map[string]string{
						{"HostPort": fmt.Sprintf("%d", hostPort)},
					},
				},
			}
		} else {
			// Kong DB-less mode — pass initial config via env var
			kongConfig := `_format_version: "3.0"\nservices: []\nroutes: []`

			containerConfig = map[string]interface{}{
				"Image": image,
				"ExposedPorts": map[string]interface{}{
					"8000/tcp": map[string]interface{}{},
					"8001/tcp": map[string]interface{}{},
				},
				"Env": []string{
					"KONG_DATABASE=off",
					"KONG_PROXY_LISTEN=0.0.0.0:8000",
					"KONG_ADMIN_LISTEN=0.0.0.0:8001",
					"KONG_LOG_LEVEL=info",
					"KONG_INIT_CONFIG=" + kongConfig,
				},
				"Cmd": []string{
					"sh", "-c",
					`printf '%s' "$KONG_INIT_CONFIG" > /etc/kong/kong.yaml && /docker-entrypoint.sh kong docker-start`,
				},
				"Labels": map[string]string{
					"ingress.fleet":      fleetID,
					"ingress.gateway":    "kong",
					"ingress.managed":    "true",
					"ingress.index":      fmt.Sprintf("%d", index),
					"ingress.datacenter": assignDatacenter(index),
					"ingress.region":     assignRegion(index),
				},
			}
			hostConfig = map[string]interface{}{
				"PortBindings": map[string]interface{}{
					"8000/tcp": []map[string]string{
						{"HostPort": fmt.Sprintf("%d", hostPort)},
					},
				},
				"Memory": 268435456, // 256MB limit for Kong
			}
		}

		// Create the container — add memory limit for all fleet containers
		if _, hasMemory := hostConfig["Memory"]; !hasMemory {
			hostConfig["Memory"] = 134217728 // 128MB default for Envoy
		}
		createBody := map[string]interface{}{
			"Image":        containerConfig["Image"],
			"ExposedPorts": containerConfig["ExposedPorts"],
			"Labels":       containerConfig["Labels"],
			"HostConfig":   hostConfig,
			"NetworkingConfig": map[string]interface{}{
				"EndpointsConfig": map[string]interface{}{
					networkName: map[string]interface{}{},
				},
			},
		}
		if cmd, ok := containerConfig["Cmd"]; ok {
			createBody["Cmd"] = cmd
		}
		if env, ok := containerConfig["Env"]; ok {
			createBody["Env"] = env
		}

		resp, err := dockerRequest("POST", "/v1.46/containers/create?name="+containerName, createBody)
		if err != nil {
			return nil, fmt.Errorf("create container %s: %w", containerName, err)
		}
		data, _ := dockerResponseBody(resp)

		if resp.StatusCode != 201 {
			return nil, fmt.Errorf("create container %s: status %d: %s", containerName, resp.StatusCode, string(data))
		}

		var createResp struct {
			ID string `json:"Id"`
		}
		json.Unmarshal(data, &createResp)

		// Start the container
		startResp, err := dockerRequest("POST", "/v1.46/containers/"+createResp.ID+"/start", nil)
		if err != nil {
			return nil, fmt.Errorf("start container %s: %w", containerName, err)
		}
		startResp.Body.Close()

		if startResp.StatusCode != 204 && startResp.StatusCode != 304 {
			return nil, fmt.Errorf("start container %s: status %d", containerName, startResp.StatusCode)
		}

		log.Printf("Started container %s (ID: %.12s) on port %d", containerName, createResp.ID, hostPort)

		nodes = append(nodes, FleetNode{
			ContainerID:   createResp.ID,
			ContainerName: containerName,
			FleetID:       fleetID,
			GatewayType:   gatewayType,
			Port:          hostPort,
			Status:        "running",
			Index:         index,
		})
	}

	return nodes, nil
}

// removeFleetContainers stops and removes all containers for a fleet.
func removeFleetContainers(fleetID string) error {
	containers, err := listFleetContainers(fleetID)
	if err != nil {
		return fmt.Errorf("list fleet containers: %w", err)
	}

	for _, node := range containers {
		// Stop the container (with 10 second timeout)
		stopResp, err := dockerRequest("POST", "/v1.46/containers/"+node.ContainerID+"/stop?t=10", nil)
		if err != nil {
			log.Printf("Warning: could not stop container %s: %v", node.ContainerName, err)
			continue
		}
		stopResp.Body.Close()

		// Remove the container
		rmResp, err := dockerRequest("DELETE", "/v1.46/containers/"+node.ContainerID+"?force=true&v=true", nil)
		if err != nil {
			log.Printf("Warning: could not remove container %s: %v", node.ContainerName, err)
			continue
		}
		rmResp.Body.Close()

		log.Printf("Removed container %s (%.12s)", node.ContainerName, node.ContainerID)
	}

	// Clean up bootstrap/config temp files
	envoyDir := filepath.Join(os.TempDir(), "ingress-poc-envoy-bootstrap")
	kongDir := filepath.Join(os.TempDir(), "ingress-poc-kong-config")
	files, _ := filepath.Glob(filepath.Join(envoyDir, fmt.Sprintf("fleet-%s-*", fleetID)))
	for _, f := range files {
		os.Remove(f)
	}
	files, _ = filepath.Glob(filepath.Join(kongDir, fmt.Sprintf("fleet-%s-*", fleetID)))
	for _, f := range files {
		os.Remove(f)
	}

	return nil
}

// stopFleetContainers stops (but doesn't remove) all containers for a fleet.
func stopFleetContainers(fleetID string) error {
	containers, err := listFleetContainers(fleetID)
	if err != nil {
		return fmt.Errorf("list fleet containers: %w", err)
	}
	for _, node := range containers {
		if node.Status != "running" {
			continue
		}
		stopResp, err := dockerRequest("POST", "/v1.46/containers/"+node.ContainerID+"/stop?t=5", nil)
		if err != nil {
			log.Printf("Warning: could not stop container %s: %v", node.ContainerName, err)
			continue
		}
		stopResp.Body.Close()
		log.Printf("Stopped container %s (%.12s)", node.ContainerName, node.ContainerID)
	}
	return nil
}

// startFleetContainers starts all stopped containers for a fleet.
func startFleetContainers(fleetID string) error {
	containers, err := listFleetContainers(fleetID)
	if err != nil {
		return fmt.Errorf("list fleet containers: %w", err)
	}
	for _, node := range containers {
		if node.Status == "running" {
			continue
		}
		startResp, err := dockerRequest("POST", "/v1.46/containers/"+node.ContainerID+"/start", nil)
		if err != nil {
			log.Printf("Warning: could not start container %s: %v", node.ContainerName, err)
			continue
		}
		startResp.Body.Close()
		log.Printf("Started container %s (%.12s)", node.ContainerName, node.ContainerID)
	}
	return nil
}

// stopLambdaContainersForFleet stops lambda containers for routes in a fleet.
// Uses the global `db` variable from main.go (same package).
func stopLambdaContainersForFleet(database interface{ Select(dest interface{}, query string, args ...interface{}) error; Get(dest interface{}, query string, args ...interface{}) error }, fleetID string) {
	var routes []Route
	database.Select(&routes, "SELECT * FROM routes WHERE status='active'")
	var fleet Fleet
	if err := database.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		return
	}
	for _, r := range routes {
		if r.Hostname == fleet.Subdomain && r.LambdaContainerID != "" {
			stopResp, _ := dockerRequest("POST", "/v1.46/containers/"+r.LambdaContainerID+"/stop?t=5", nil)
			if stopResp != nil {
				stopResp.Body.Close()
			}
			log.Printf("Stopped lambda container %.12s for route %s", r.LambdaContainerID, r.Path)
		}
	}
}

// startLambdaContainersForFleet starts lambda containers for routes in a fleet.
func startLambdaContainersForFleet(database interface{ Select(dest interface{}, query string, args ...interface{}) error; Get(dest interface{}, query string, args ...interface{}) error }, fleetID string) {
	var routes []Route
	database.Select(&routes, "SELECT * FROM routes")
	var fleet Fleet
	if err := database.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		return
	}
	for _, r := range routes {
		if r.Hostname == fleet.Subdomain && r.LambdaContainerID != "" {
			startResp, _ := dockerRequest("POST", "/v1.46/containers/"+r.LambdaContainerID+"/start", nil)
			if startResp != nil {
				startResp.Body.Close()
			}
			log.Printf("Started lambda container %.12s for route %s", r.LambdaContainerID, r.Path)
		}
	}
}

// listFleetContainers returns running containers for a fleet.
func listFleetContainers(fleetID string) ([]FleetNode, error) {
	filter := fmt.Sprintf(`{"label":["ingress.fleet=%s","ingress.managed=true"]}`, fleetID)
	resp, err := dockerRequest("GET", "/v1.46/containers/json?all=true&filters="+filter, nil)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	data, err := dockerResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var containers []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
		Ports  []struct {
			PublicPort int `json:"PublicPort"`
		} `json:"Ports"`
	}
	if err := json.Unmarshal(data, &containers); err != nil {
		// Docker may return {"message":"..."} instead of [] when no containers match
		// or when the API returns an error. Treat as empty list.
		if len(data) > 0 && data[0] == '{' {
			return []FleetNode{}, nil
		}
		return nil, fmt.Errorf("parse containers: %w", err)
	}

	nodes := make([]FleetNode, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		port := 0
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				port = p.PublicPort
				break
			}
		}

		index := 0
		if idxStr, ok := c.Labels["ingress.index"]; ok {
			fmt.Sscanf(idxStr, "%d", &index)
		}

		gwType := c.Labels["ingress.gateway"]

		nodes = append(nodes, FleetNode{
			ContainerID:   c.ID,
			ContainerName: name,
			FleetID:       fleetID,
			GatewayType:   gwType,
			Port:          port,
			Status:        c.State,
			Index:         index,
			Datacenter:    c.Labels["ingress.datacenter"],
			Region:        c.Labels["ingress.region"],
		})
	}

	return nodes, nil
}

// scaleFleetContainers scales the fleet to the desired count by adding or removing containers.
// When a fleet is mixed-type, this only scales containers of the specified gatewayType.
func scaleFleetContainers(fleetID, gatewayType string, desiredCount int, networkName string) ([]FleetNode, error) {
	allExisting, err := listFleetContainers(fleetID)
	if err != nil {
		return nil, fmt.Errorf("list existing containers: %w", err)
	}

	// Filter to only containers of the requested gateway type
	existing := make([]FleetNode, 0)
	for _, n := range allExisting {
		if n.GatewayType == gatewayType {
			existing = append(existing, n)
		}
	}

	currentCount := len(existing)

	if desiredCount == currentCount {
		return existing, nil
	}

	if desiredCount > currentCount {
		// Scale up: create additional containers
		// Find the highest index among ALL containers in this fleet (not just this type)
		// to avoid name collisions
		maxIndex := 0
		for _, n := range allExisting {
			if n.GatewayType == gatewayType && n.Index > maxIndex {
				maxIndex = n.Index
			}
		}
		additional := desiredCount - currentCount
		newNodes, err := createFleetContainersStartingAt(fleetID, gatewayType, additional, maxIndex+1, networkName)
		if err != nil {
			return nil, fmt.Errorf("scale up: %w", err)
		}
		return append(existing, newNodes...), nil
	}

	// Scale down: remove excess containers (remove highest-indexed first)
	toRemove := currentCount - desiredCount
	for i := 0; i < toRemove && i < len(existing); i++ {
		// Find the highest-indexed container of the requested type
		maxIdx := -1
		maxPos := -1
		for j, node := range existing {
			if node.Index > maxIdx {
				maxIdx = node.Index
				maxPos = j
			}
		}
		if maxPos >= 0 {
			node := existing[maxPos]
			// Stop
			stopResp, _ := dockerRequest("POST", "/v1.46/containers/"+node.ContainerID+"/stop?t=10", nil)
			if stopResp != nil {
				stopResp.Body.Close()
			}
			// Remove
			rmResp, _ := dockerRequest("DELETE", "/v1.46/containers/"+node.ContainerID+"?force=true&v=true", nil)
			if rmResp != nil {
				rmResp.Body.Close()
			}
			log.Printf("Scale down: removed container %s", node.ContainerName)
			// Remove from slice
			existing = append(existing[:maxPos], existing[maxPos+1:]...)
		}
	}

	return existing, nil
}

// createFleetContainersStartingAt is like createFleetContainers but starts indexing at startIndex.
func createFleetContainersStartingAt(fleetID, gatewayType string, count, startIndex int, networkName string) ([]FleetNode, error) {
	if networkName == "" {
		networkName = getDockerNetworkName()
	}

	var image string
	var basePort int
	switch gatewayType {
	case "envoy":
		image = "envoyproxy/envoy:v1.30-latest"
		basePort = envoyBasePort
	case "kong":
		image = "kong:3.6"
		basePort = kongBasePort
	default:
		return nil, fmt.Errorf("unsupported gateway type: %s", gatewayType)
	}

	if err := pullImageIfNeeded(image); err != nil {
		log.Printf("Warning: could not pull image %s: %v", image, err)
	}

	ports, err := findNextAvailablePort(basePort, count)
	if err != nil {
		return nil, fmt.Errorf("allocate ports: %w", err)
	}

	nodes := make([]FleetNode, 0, count)
	for i := 0; i < count; i++ {
		index := startIndex + i
		containerName := fmt.Sprintf("%s-%s-%d", fleetID, gatewayType, index)
		if overrideContainerName != "" && i == 0 {
			containerName = overrideContainerName
		}
		hostPort := ports[i]

		var containerConfig map[string]interface{}
		var hostConfig map[string]interface{}

		if gatewayType == "envoy" {
			bootstrapYAML := generateEnvoyBootstrapYAML(fleetID, index)

			containerConfig = map[string]interface{}{
				"Image": image,
				"ExposedPorts": map[string]interface{}{
					"8000/tcp": map[string]interface{}{},
					"9901/tcp": map[string]interface{}{},
				},
				"Env": []string{
					"ENVOY_BOOTSTRAP=" + bootstrapYAML,
				},
				"Cmd": []string{
					"sh", "-c",
					`printf '%s' "$ENVOY_BOOTSTRAP" > /tmp/bootstrap.yaml && envoy -c /tmp/bootstrap.yaml --service-node ` +
						fmt.Sprintf("%s-%d", fleetID, index) + ` --service-cluster ` + fleetID,
				},
				"Labels": map[string]string{
					"ingress.fleet":      fleetID,
					"ingress.gateway":    "envoy",
					"ingress.managed":    "true",
					"ingress.index":      fmt.Sprintf("%d", index),
					"ingress.datacenter": assignDatacenter(index),
					"ingress.region":     assignRegion(index),
				},
			}
			hostConfig = map[string]interface{}{
				"PortBindings": map[string]interface{}{
					"8000/tcp": []map[string]string{
						{"HostPort": fmt.Sprintf("%d", hostPort)},
					},
				},
			}
		} else {
			kongConfig := `_format_version: "3.0"\nservices: []\nroutes: []`

			containerConfig = map[string]interface{}{
				"Image": image,
				"ExposedPorts": map[string]interface{}{
					"8000/tcp": map[string]interface{}{},
					"8001/tcp": map[string]interface{}{},
				},
				"Env": []string{
					"KONG_DATABASE=off",
					"KONG_PROXY_LISTEN=0.0.0.0:8000",
					"KONG_ADMIN_LISTEN=0.0.0.0:8001",
					"KONG_LOG_LEVEL=info",
					"KONG_INIT_CONFIG=" + kongConfig,
				},
				"Cmd": []string{
					"sh", "-c",
					`printf '%s' "$KONG_INIT_CONFIG" > /etc/kong/kong.yaml && /docker-entrypoint.sh kong docker-start`,
				},
				"Labels": map[string]string{
					"ingress.fleet":      fleetID,
					"ingress.gateway":    "kong",
					"ingress.managed":    "true",
					"ingress.index":      fmt.Sprintf("%d", index),
					"ingress.datacenter": assignDatacenter(index),
					"ingress.region":     assignRegion(index),
				},
			}
			hostConfig = map[string]interface{}{
				"PortBindings": map[string]interface{}{
					"8000/tcp": []map[string]string{
						{"HostPort": fmt.Sprintf("%d", hostPort)},
					},
				},
			}
		}

		createBody := map[string]interface{}{
			"Image":        containerConfig["Image"],
			"ExposedPorts": containerConfig["ExposedPorts"],
			"Labels":       containerConfig["Labels"],
			"HostConfig":   hostConfig,
			"NetworkingConfig": map[string]interface{}{
				"EndpointsConfig": map[string]interface{}{
					networkName: map[string]interface{}{},
				},
			},
		}
		if cmd, ok := containerConfig["Cmd"]; ok {
			createBody["Cmd"] = cmd
		}
		if env, ok := containerConfig["Env"]; ok {
			createBody["Env"] = env
		}

		resp, err := dockerRequest("POST", "/v1.46/containers/create?name="+containerName, createBody)
		if err != nil {
			return nil, fmt.Errorf("create container %s: %w", containerName, err)
		}
		data, _ := dockerResponseBody(resp)

		if resp.StatusCode != 201 {
			return nil, fmt.Errorf("create container %s: status %d: %s", containerName, resp.StatusCode, string(data))
		}

		var createResp struct {
			ID string `json:"Id"`
		}
		json.Unmarshal(data, &createResp)

		startResp, err := dockerRequest("POST", "/v1.46/containers/"+createResp.ID+"/start", nil)
		if err != nil {
			return nil, fmt.Errorf("start container %s: %w", containerName, err)
		}
		startResp.Body.Close()

		if startResp.StatusCode != 204 && startResp.StatusCode != 304 {
			return nil, fmt.Errorf("start container %s: status %d", containerName, startResp.StatusCode)
		}

		log.Printf("Started container %s (ID: %.12s) on port %d", containerName, createResp.ID, hostPort)

		nodes = append(nodes, FleetNode{
			ContainerID:   createResp.ID,
			ContainerName: containerName,
			FleetID:       fleetID,
			GatewayType:   gatewayType,
			Port:          hostPort,
			Status:        "running",
			Index:         index,
		})
	}

	return nodes, nil
}

// ---------- Lambda / FaaS Container Management ----------

// generateLambdaServerJS returns the inline Node.js server code that is written
// to a temp directory and bind-mounted into the lambda container.
// lambdaServerJS is embedded at build time from the lambda-runtime directory.
//
//go:embed lambda-server.js
var lambdaServerJS string

func generateLambdaServerJS() string {
	return lambdaServerJS
}

// createLambdaContainer creates a Node.js lambda container with user code.
// The function code is passed via FUNCTION_CODE env var.
// Container name: lambda-{routeID[:8]}-{functionName}
// Returns container ID and mapped host port.
func createLambdaContainer(routeID, functionName, functionCode, networkName string) (containerID string, port int, err error) {
	if networkName == "" {
		networkName = getDockerNetworkName()
	}

	// Pull node image
	if pullErr := pullImageIfNeeded(lambdaImage); pullErr != nil {
		log.Printf("Warning: could not pull %s: %v (will try local)", lambdaImage, pullErr)
	}

	// Allocate a host port
	ports, err := findNextAvailablePort(lambdaBasePort, 1)
	if err != nil {
		return "", 0, fmt.Errorf("allocate lambda port: %w", err)
	}
	hostPort := ports[0]

	// Build container name
	containerName := lambdaContainerName(routeID, functionName)

	// Instead of bind-mounting server.js (which fails from inside Docker),
	// pass the server code via LAMBDA_SERVER_CODE env var and use an inline
	// node command that writes it to disk then runs it.
	serverCode := generateLambdaServerJS()

	createBody := map[string]interface{}{
		"Image": lambdaImage,
		"ExposedPorts": map[string]interface{}{
			fmt.Sprintf("%d/tcp", lambdaInternalPort): map[string]interface{}{},
		},
		"Cmd": []string{"sh", "-c", "echo \"$LAMBDA_SERVER_CODE\" > /tmp/server.js && node /tmp/server.js"},
		"Env": []string{
			"FUNCTION_CODE=" + functionCode,
			"FUNCTION_NAME=" + functionName,
			"LAMBDA_SERVER_CODE=" + serverCode,
			fmt.Sprintf("PORT=%d", lambdaInternalPort),
			"OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318",
		},
		"Labels": map[string]string{
			"ingress.lambda":  "true",
			"ingress.route":   routeID,
			"ingress.managed": "true",
		},
		"HostConfig": map[string]interface{}{
			"PortBindings": map[string]interface{}{
				fmt.Sprintf("%d/tcp", lambdaInternalPort): []map[string]string{
					{"HostPort": fmt.Sprintf("%d", hostPort)},
				},
			},
		},
		"NetworkingConfig": map[string]interface{}{
			"EndpointsConfig": map[string]interface{}{
				networkName: map[string]interface{}{},
			},
		},
	}

	resp, err := dockerRequest("POST", "/v1.46/containers/create?name="+containerName, createBody)
	if err != nil {
		return "", 0, fmt.Errorf("create lambda container: %w", err)
	}
	data, _ := dockerResponseBody(resp)
	if resp.StatusCode != 201 {
		return "", 0, fmt.Errorf("create lambda container: status %d: %s", resp.StatusCode, string(data))
	}

	var createResp struct {
		ID string `json:"Id"`
	}
	json.Unmarshal(data, &createResp)

	// Start the container
	startResp, err := dockerRequest("POST", "/v1.46/containers/"+createResp.ID+"/start", nil)
	if err != nil {
		return "", 0, fmt.Errorf("start lambda container: %w", err)
	}
	startResp.Body.Close()
	if startResp.StatusCode != 204 && startResp.StatusCode != 304 {
		return "", 0, fmt.Errorf("start lambda container: status %d", startResp.StatusCode)
	}

	log.Printf("Started lambda container %s (ID: %.12s) on host port %d for route %s",
		containerName, createResp.ID, hostPort, routeID)

	return createResp.ID, hostPort, nil
}

// removeLambdaContainer stops and removes a lambda container by ID.
func removeLambdaContainer(containerID string) error {
	// Stop
	stopResp, err := dockerRequest("POST", "/v1.46/containers/"+containerID+"/stop?t=5", nil)
	if err != nil {
		return fmt.Errorf("stop lambda container %s: %w", containerID, err)
	}
	stopResp.Body.Close()

	// Remove
	rmResp, err := dockerRequest("DELETE", "/v1.46/containers/"+containerID+"?force=true&v=true", nil)
	if err != nil {
		return fmt.Errorf("remove lambda container %s: %w", containerID, err)
	}
	rmResp.Body.Close()

	log.Printf("Removed lambda container %.12s", containerID)
	return nil
}

// listLambdaContainers returns all running lambda containers.
func listLambdaContainers() ([]map[string]interface{}, error) {
	filter := `{"label":["ingress.lambda=true","ingress.managed=true"]}`
	resp, err := dockerRequest("GET", "/v1.46/containers/json?all=true&filters="+filter, nil)
	if err != nil {
		return nil, fmt.Errorf("list lambda containers: %w", err)
	}
	data, err := dockerResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var containers []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
		Ports  []struct {
			PublicPort  int `json:"PublicPort"`
			PrivatePort int `json:"PrivatePort"`
		} `json:"Ports"`
	}
	if err := json.Unmarshal(data, &containers); err != nil {
		if len(data) > 0 && data[0] == '{' {
			return []map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("parse lambda containers: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		hostPort := 0
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				hostPort = p.PublicPort
				break
			}
		}
		result = append(result, map[string]interface{}{
			"container_id":   c.ID,
			"container_name": name,
			"state":          c.State,
			"route_id":       c.Labels["ingress.route"],
			"host_port":      hostPort,
		})
	}

	return result, nil
}

// lambdaContainerName returns the DNS-resolvable container name for a lambda.
func lambdaContainerName(routeID, functionName string) string {
	shortID := routeID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	safeName := strings.ReplaceAll(functionName, " ", "-")
	safeName = strings.ToLower(safeName)
	return fmt.Sprintf("lambda-%s-%s", shortID, safeName)
}
