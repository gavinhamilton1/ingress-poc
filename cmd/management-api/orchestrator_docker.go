package main

import "log"

// DockerOrchestrator wraps the existing Docker Engine API functions from docker.go,
// implementing the Orchestrator interface for local/Docker Compose deployments.
type DockerOrchestrator struct {
	networkName string
}

// NewDockerOrchestrator creates a DockerOrchestrator that delegates to the existing
// docker.go package-level functions. The Docker network name is resolved from the
// DOCKER_NETWORK env var or auto-detected from running containers.
func NewDockerOrchestrator() *DockerOrchestrator {
	return &DockerOrchestrator{
		networkName: getDockerNetworkName(),
	}
}

func (d *DockerOrchestrator) CreateFleetNodes(fleetID, gatewayType string, count int) ([]FleetNode, error) {
	return createFleetContainers(fleetID, gatewayType, count, d.networkName)
}

func (d *DockerOrchestrator) ListFleetNodes(fleetID string) ([]FleetNode, error) {
	return listFleetContainers(fleetID)
}

func (d *DockerOrchestrator) ScaleFleetNodes(fleetID, gatewayType string, desiredCount int) ([]FleetNode, error) {
	return scaleFleetContainers(fleetID, gatewayType, desiredCount, d.networkName)
}

func (d *DockerOrchestrator) RemoveFleetNodes(fleetID, _ string) error {
	return removeFleetContainers(fleetID)
}

func (d *DockerOrchestrator) StopFleetNodes(fleetID string) error {
	return stopFleetContainers(fleetID)
}

func (d *DockerOrchestrator) StartFleetNodes(fleetID string) error {
	return startFleetContainers(fleetID)
}

func (d *DockerOrchestrator) DeploySingleNode(fleetID, gatewayType, datacenter, nodeName string, startIndex int) ([]FleetNode, error) {
	// Set the global overrides used by createFleetContainersStartingAt in docker.go.
	// These are reset after the call to avoid leaking state.
	prevDC := overrideDatacenter
	prevName := overrideContainerName
	overrideDatacenter = datacenter
	overrideContainerName = nodeName
	defer func() {
		overrideDatacenter = prevDC
		overrideContainerName = prevName
	}()

	return createFleetContainersStartingAt(fleetID, gatewayType, 1, startIndex, d.networkName)
}

func (d *DockerOrchestrator) CreateLambdaContainer(routeID, funcName, code string) (containerID string, port int, err error) {
	return createLambdaContainer(routeID, funcName, code, d.networkName)
}

func (d *DockerOrchestrator) RemoveLambdaContainer(containerID string) error {
	return removeLambdaContainer(containerID)
}

func (d *DockerOrchestrator) ListLambdaContainers() ([]map[string]interface{}, error) {
	return listLambdaContainers()
}

func (d *DockerOrchestrator) StopLambdaContainersForFleet(fleetID string) error {
	// The underlying function uses the global db from main.go (same package).
	if db == nil {
		log.Printf("Warning: db is nil, cannot stop lambda containers for fleet %s", fleetID)
		return nil
	}
	stopLambdaContainersForFleet(db, fleetID)
	return nil
}

func (d *DockerOrchestrator) StartLambdaContainersForFleet(fleetID string) error {
	// The underlying function uses the global db from main.go (same package).
	if db == nil {
		log.Printf("Warning: db is nil, cannot start lambda containers for fleet %s", fleetID)
		return nil
	}
	startLambdaContainersForFleet(db, fleetID)
	return nil
}

// WriteRouteCRD is a no-op in Docker mode (no GitOps repo).
func (d *DockerOrchestrator) WriteRouteCRD(route Route, fleetSubdomain string) error {
	return nil
}

// DeleteRouteCRD is a no-op in Docker mode (no GitOps repo).
func (d *DockerOrchestrator) DeleteRouteCRD(routeID string) error {
	return nil
}

// UpdateFleetManifest is a no-op in Docker mode (no GitOps repo).
func (d *DockerOrchestrator) UpdateFleetManifest(fleet Fleet) error {
	return nil
}
