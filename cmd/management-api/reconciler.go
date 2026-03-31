package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var reconcilerClientset *kubernetes.Clientset

// startReconciler launches a background goroutine that periodically syncs the
// database fleet_nodes status with the actual pod states in the data-plane
// namespace. This ensures the console always reflects reality.
// It respects user-set statuses like "suspended" — it will NOT override those.
func startReconciler(interval time.Duration) {
	clientset, err := buildK8sClientset()
	if err != nil {
		log.Printf("reconciler: cannot build K8s client, reconciliation disabled: %v", err)
		return
	}
	reconcilerClientset = clientset

	go func() {
		// Initial reconciliation after startup delay (give seed data time to populate)
		time.Sleep(15 * time.Second)
		reconcileOnce(clientset)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			reconcileOnce(clientset)
		}
	}()

	log.Printf("reconciler: started (interval=%s)", interval)
}

func buildK8sClientset() (*kubernetes.Clientset, error) {
	// Try in-cluster config first (running inside K8s)
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig (local dev)
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		cfg, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("no K8s config available: %w", err)
		}
	}
	return kubernetes.NewForConfig(cfg)
}

// scaleFleetDeployment scales a fleet's Deployment in the data-plane namespace.
// replicas=0 stops all pods; replicas>0 starts/scales them.
func scaleFleetDeployment(fleetID string, replicas int32) error {
	clientset := reconcilerClientset
	if clientset == nil {
		var err error
		clientset, err = buildK8sClientset()
		if err != nil {
			return fmt.Errorf("no K8s client: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dpNamespace := "ingress-dp"

	// Get the deployment
	deploy, err := clientset.AppsV1().Deployments(dpNamespace).Get(ctx, fleetID, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s: %w", fleetID, err)
	}

	// Scale it
	deploy.Spec.Replicas = &replicas
	_, err = clientset.AppsV1().Deployments(dpNamespace).Update(ctx, deploy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("scale deployment %s to %d: %w", fleetID, replicas, err)
	}

	log.Printf("reconciler: scaled deployment %s to %d replicas", fleetID, replicas)
	return nil
}

func reconcileOnce(clientset *kubernetes.Clientset) {
	if db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dpNamespace := "ingress-dp"

	// List all pods in the data-plane namespace
	pods, err := clientset.CoreV1().Pods(dpNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("reconciler: failed to list pods in %s: %v", dpNamespace, err)
		return
	}

	// Build a map of fleet-name → running pod count
	fleetPodCount := map[string]int{}
	for _, pod := range pods.Items {
		fleetID := extractFleetID(pod.Name, pod.OwnerReferences)
		if fleetID == "" {
			continue
		}
		if pod.Status.Phase == "Running" {
			allReady := true
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					allReady = false
					break
				}
			}
			if allReady {
				fleetPodCount[fleetID]++
			}
		}
	}

	// Get fleet statuses from DB — skip suspended/stopped fleets (user intent)
	var fleets []struct {
		ID     string `db:"id"`
		Status string `db:"status"`
	}
	if err := db.Select(&fleets, "SELECT id, status FROM fleets WHERE fleet_type='data'"); err != nil {
		log.Printf("reconciler: failed to query fleets: %v", err)
		return
	}

	// Build set of user-suspended fleet IDs (don't override these)
	suspendedFleets := map[string]bool{}
	for _, f := range fleets {
		if f.Status == "suspended" || f.Status == "stopped" {
			suspendedFleets[f.ID] = true
		}
	}

	// Update DB fleet_nodes to match actual pod states (skip suspended fleets)
	var dbNodes []struct {
		NodeName string `db:"node_name"`
		FleetID  string `db:"fleet_id"`
		Status   string `db:"status"`
	}
	if err := db.Select(&dbNodes, "SELECT node_name, fleet_id, status FROM fleet_nodes"); err != nil {
		log.Printf("reconciler: failed to query fleet_nodes: %v", err)
		return
	}

	updatedCount := 0
	for _, node := range dbNodes {
		// Skip nodes belonging to user-managed fleets
		if suspendedFleets[node.FleetID] {
			continue
		}
		// Skip nodes explicitly stopped by the user — don't override their intent
		if node.Status == "stopped" {
			continue
		}

		runningCount := fleetPodCount[node.FleetID]
		if runningCount > 0 {
			if node.Status != "running" {
				db.Exec("UPDATE fleet_nodes SET status='running' WHERE node_name=$1", node.NodeName)
				updatedCount++
			}
		} else {
			if node.Status == "running" {
				db.Exec("UPDATE fleet_nodes SET status='stopped' WHERE node_name=$1", node.NodeName)
				updatedCount++
			}
		}
	}

	// Update fleet status (skip user-managed fleets)
	for _, f := range fleets {
		if suspendedFleets[f.ID] {
			continue
		}
		runningCount := fleetPodCount[f.ID]
		// Check if any nodes are explicitly stopped (user action)
		var stoppedNodes int
		db.Get(&stoppedNodes, "SELECT COUNT(*) FROM fleet_nodes WHERE fleet_id=$1 AND status='stopped'", f.ID)
		if stoppedNodes > 0 && runningCount > 0 {
			// Some nodes stopped by user, some pods still running = degraded
			if f.Status != "degraded" {
				db.Exec("UPDATE fleets SET status='degraded' WHERE id=$1", f.ID)
				updatedCount++
			}
		} else if runningCount > 0 && f.Status != "healthy" {
			db.Exec("UPDATE fleets SET status='healthy' WHERE id=$1", f.ID)
			updatedCount++
		} else if runningCount == 0 && f.Status == "healthy" {
			db.Exec("UPDATE fleets SET status='not_deployed' WHERE id=$1", f.ID)
			updatedCount++
		}
	}

	if updatedCount > 0 {
		log.Printf("reconciler: synced %d records with actual cluster state (%d fleet pods found)", updatedCount, len(pods.Items))
	}
}

func extractFleetID(podName string, ownerRefs []metav1.OwnerReference) string {
	for _, ref := range ownerRefs {
		if ref.Kind == "ReplicaSet" {
			parts := strings.Split(ref.Name, "-")
			if len(parts) >= 3 {
				return strings.Join(parts[:len(parts)-1], "-")
			}
		}
	}
	// Fallback: extract from pod name
	parts := strings.Split(podName, "-")
	if len(parts) >= 4 && parts[0] == "fleet" {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return ""
}

// Ensure appsv1 import is used
var _ = &appsv1.Deployment{}
