package main

import (
	"fmt"
	"os"
)

// Orchestrator abstracts fleet/lambda lifecycle management.
// Implementations: DockerOrchestrator (existing Docker Engine API) and K8sOrchestrator (Git-first GitOps).
type Orchestrator interface {
	// CreateFleetNodes provisions gateway nodes for a fleet.
	CreateFleetNodes(fleetID, gatewayType string, count int) ([]FleetNode, error)

	// ListFleetNodes returns the current nodes for a fleet.
	ListFleetNodes(fleetID string) ([]FleetNode, error)

	// ScaleFleetNodes adjusts the fleet to the desired node count.
	ScaleFleetNodes(fleetID, gatewayType string, desiredCount int) ([]FleetNode, error)

	// RemoveFleetNodes tears down all nodes for a fleet.
	RemoveFleetNodes(fleetID string) error

	// StopFleetNodes suspends all nodes in a fleet without removing them.
	StopFleetNodes(fleetID string) error

	// StartFleetNodes resumes all stopped nodes in a fleet.
	StartFleetNodes(fleetID string) error

	// DeploySingleNode adds a single node to an existing fleet.
	DeploySingleNode(fleetID, gatewayType, datacenter, nodeName string, startIndex int) ([]FleetNode, error)

	// CreateLambdaContainer provisions a serverless function container.
	CreateLambdaContainer(routeID, funcName, code string) (containerID string, port int, err error)

	// RemoveLambdaContainer tears down a serverless function container.
	RemoveLambdaContainer(containerID string) error

	// ListLambdaContainers returns all running lambda containers.
	ListLambdaContainers() ([]map[string]interface{}, error)

	// StopLambdaContainersForFleet suspends lambda containers associated with a fleet's routes.
	StopLambdaContainersForFleet(fleetID string) error

	// StartLambdaContainersForFleet resumes lambda containers associated with a fleet's routes.
	StartLambdaContainersForFleet(fleetID string) error

	// WriteRouteCRD writes a Route CRD manifest to the GitOps repo and commits.
	// In Docker mode this is a no-op.
	WriteRouteCRD(route Route, fleetSubdomain string) error

	// DeleteRouteCRD removes a Route CRD manifest from the GitOps repo and commits.
	// In Docker mode this is a no-op.
	DeleteRouteCRD(routeID string) error

	// UpdateFleetManifest rewrites the Fleet CRD manifest in the GitOps repo.
	// Used when fleet metadata changes (not scaling). In Docker mode this is a no-op.
	UpdateFleetManifest(fleet Fleet) error
}

// NewOrchestrator creates an Orchestrator based on the given mode.
// Supported modes: "docker" (default), "k8s".
// The mode is typically read from the ORCHESTRATOR_MODE environment variable.
func NewOrchestrator(mode string) (Orchestrator, error) {
	switch mode {
	case "", "docker":
		return NewDockerOrchestrator(), nil
	case "k8s", "kubernetes":
		repoPath := os.Getenv("GITOPS_REPO_PATH")
		if repoPath == "" {
			return nil, fmt.Errorf("GITOPS_REPO_PATH must be set for k8s orchestrator mode")
		}
		clusterName := os.Getenv("GITOPS_CLUSTER_NAME")
		if clusterName == "" {
			clusterName = "data-plane-1"
		}
		return NewK8sOrchestrator(repoPath, clusterName)
	default:
		return nil, fmt.Errorf("unsupported orchestrator mode: %q (expected \"docker\" or \"k8s\")", mode)
	}
}
