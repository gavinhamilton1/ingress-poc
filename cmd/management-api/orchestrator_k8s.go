package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sOrchestrator implements the Orchestrator interface using a Git-first GitOps
// approach. It writes CRD manifests to a GitOps repository AND applies them
// directly to the cluster so the ingress-operator can act on them immediately.
//
// When a FleetRepoManager is configured (GitHub integration), each fleet gets its
// own GitHub repo. Otherwise, a single local repo is used (legacy/fallback mode).
type K8sOrchestrator struct {
	repo         *GitOpsRepo        // Single-repo fallback (used when fleetRepos is nil)
	fleetRepos   *FleetRepoManager  // Per-fleet GitHub repos (nil if not configured)
	clusterName  string             // e.g. "data-plane-1" (default / single-region fallback)
	dynClient    dynamic.Interface  // Dynamic K8s client for applying CRDs to cluster (nil if unavailable)
	clientset    *kubernetes.Clientset // Typed K8s client for Deployments/Services (nil if unavailable)
}

// NewK8sOrchestrator creates a K8sOrchestrator. If GitHub config is available,
// it uses per-fleet repos. Otherwise it falls back to a single local repo.
func NewK8sOrchestrator(repoPath, clusterName string) (*K8sOrchestrator, error) {
	orch := &K8sOrchestrator{clusterName: clusterName}

	// Always initialize a local repo (used for lambdas and as a fallback).
	repo, err := NewGitOpsRepo(repoPath)
	if err != nil {
		return nil, fmt.Errorf("init gitops repo: %w", err)
	}
	orch.repo = repo

	// Check for GitHub integration (per-fleet repos).
	ghConfig := GitHubConfigFromEnv()
	if ghConfig != nil {
		orch.fleetRepos = NewFleetRepoManager(ghConfig, repoPath)
		log.Printf("k8s: using per-fleet GitHub repos (github.com/%s/fleet-*) with local fallback at %s", ghConfig.Username, repoPath)
	} else {
		log.Printf("k8s: using single local GitOps repo at %s (no GitHub configured)", repoPath)
	}

	// Initialize Kubernetes dynamic client for direct CRD application.
	// This allows the orchestrator to apply Fleet/Route CRDs directly to the
	// cluster so the ingress-operator can act immediately (without waiting for
	// Argo CD to sync from Git).
	var restCfg *rest.Config
	dpKubeconfig := os.Getenv("DP_KUBECONFIG")
	dpContext := os.Getenv("DP_CLUSTER_CONTEXT")

	if dpKubeconfig != "" {
		// Explicit kubeconfig file (used for cross-cluster access in multi-cluster setups)
		restCfg, err = clientcmd.BuildConfigFromFlags("", dpKubeconfig)
		if err != nil {
			log.Printf("k8s: warning: could not build config from DP_KUBECONFIG=%s: %v", dpKubeconfig, err)
		}
	} else if dpContext != "" {
		// Explicit context name (used for local dev with multiple kind clusters)
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			rules, &clientcmd.ConfigOverrides{CurrentContext: dpContext},
		).ClientConfig()
		if err != nil {
			log.Printf("k8s: warning: could not build config for context %s: %v", dpContext, err)
		}
	} else {
		// In-cluster config (single-cluster setup — management-api and operator share the same cluster)
		restCfg, err = rest.InClusterConfig()
		if err != nil {
			log.Printf("k8s: warning: no in-cluster config available: %v (CRDs will only be written to Git)", err)
		}
	}

	if restCfg != nil {
		dynClient, err := dynamic.NewForConfig(restCfg)
		if err != nil {
			log.Printf("k8s: warning: could not create dynamic client: %v", err)
		} else {
			orch.dynClient = dynClient
			log.Printf("k8s: dynamic client initialized — CRDs will be applied directly to cluster")
		}
		cs, err := kubernetes.NewForConfig(restCfg)
		if err != nil {
			log.Printf("k8s: warning: could not create typed clientset: %v", err)
		} else {
			orch.clientset = cs
			log.Printf("k8s: typed clientset initialized — lambda Deployments/Services will be managed directly")
		}
	}

	return orch, nil
}

// ---------------------------------------------------------------------------
// Direct cluster application helpers
// ---------------------------------------------------------------------------

var (
	fleetGVR = schema.GroupVersionResource{Group: "ingress.jpmc.com", Version: "v1alpha1", Resource: "fleets"}
	routeGVR = schema.GroupVersionResource{Group: "ingress.jpmc.com", Version: "v1alpha1", Resource: "routes"}
)

// applyToCluster parses a YAML manifest and creates/updates it in the cluster.
// This is a best-effort operation — Git remains authoritative.
func (k *K8sOrchestrator) applyToCluster(manifest string) error {
	if k.dynClient == nil {
		return nil // No cluster client configured — Git-only mode
	}

	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var obj unstructured.Unstructured
	if err := decoder.Decode(&obj); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}

	gvr := fleetGVR
	if obj.GetKind() == "Route" {
		gvr = routeGVR
	}

	ns := obj.GetNamespace()
	if ns == "" {
		ns = "ingress-dp"
		obj.SetNamespace(ns)
	}

	// Remove status block — status is a subresource managed by the operator, not by us.
	delete(obj.Object, "status")

	ctx := context.Background()
	client := k.dynClient.Resource(gvr).Namespace(ns)

	// Try to get existing resource first
	existing, err := client.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		// Resource doesn't exist — create it
		_, createErr := client.Create(ctx, &obj, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("create %s/%s in cluster: %w", obj.GetKind(), obj.GetName(), createErr)
		}
		log.Printf("k8s: created %s/%s in cluster namespace %s", obj.GetKind(), obj.GetName(), ns)
		return nil
	}

	// Resource exists — update it (preserve resourceVersion for optimistic concurrency)
	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = client.Update(ctx, &obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update %s/%s in cluster: %w", obj.GetKind(), obj.GetName(), err)
	}
	log.Printf("k8s: updated %s/%s in cluster namespace %s", obj.GetKind(), obj.GetName(), ns)
	return nil
}

// deleteFromCluster removes a resource from the cluster by GVR, namespace, and name.
func (k *K8sOrchestrator) deleteFromCluster(gvr schema.GroupVersionResource, namespace, name string) error {
	if k.dynClient == nil {
		return nil
	}
	err := k.dynClient.Resource(gvr).Namespace(namespace).Delete(
		context.Background(), name, metav1.DeleteOptions{},
	)
	if err != nil {
		log.Printf("k8s: warning: could not delete %s/%s from cluster: %v", gvr.Resource, name, err)
		return nil // Non-fatal — Git is authoritative
	}
	log.Printf("k8s: deleted %s/%s from cluster namespace %s", gvr.Resource, name, namespace)
	return nil
}

// getFleetRepo returns the GitOpsRepo for a given fleet. In per-fleet mode,
// it looks up or creates the fleet-specific repo. In single-repo mode, returns the shared repo.
func (k *K8sOrchestrator) getFleetRepo(fleetID string) (*GitOpsRepo, error) {
	if k.fleetRepos != nil {
		// Look up fleet name from DB for repo naming
		fleetName := fleetID
		if db != nil {
			var f Fleet
			if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err == nil && f.Name != "" {
				fleetName = f.Name
			}
		}
		return k.fleetRepos.GetFleetRepo(fleetID, fleetName)
	}
	return k.repo, nil
}

// readFleetManifest reads the fleet YAML from whichever store is active
// (per-fleet GitHub repo if configured, otherwise the single local repo).
func (k *K8sOrchestrator) readFleetManifest(fleetID string) ([]byte, error) {
	if k.fleetRepos != nil {
		repo, err := k.getFleetRepo(fleetID)
		if err != nil {
			return nil, fmt.Errorf("get fleet repo for %s: %w", fleetID, err)
		}
		return repo.ReadManifest(filepath.Join("fleets", fleetID+".yaml"))
	}
	return k.repo.ReadManifest(k.fleetManifestPath(fleetID))
}

// writeAndPushFleetManifest writes the fleet YAML to the correct store and commits.
func (k *K8sOrchestrator) writeAndPushFleetManifest(fleetID, manifest, commitMsg string) error {
	if k.fleetRepos != nil {
		repo, err := k.getFleetRepo(fleetID)
		if err != nil {
			// Repo may not exist yet — try creating it.
			fleetName := fleetID
			if db != nil {
				var f Fleet
				if err2 := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err2 == nil && f.Name != "" {
					fleetName = f.Name
				}
			}
			repo, err = k.fleetRepos.CreateFleetRepo(fleetID, fleetName)
			if err != nil {
				return fmt.Errorf("create fleet repo for %s: %w", fleetID, err)
			}
		}
		fleetManifestPath := filepath.Join("fleets", fleetID+".yaml")
		if err := repo.EnsureDirectory("fleets"); err != nil {
			return fmt.Errorf("ensure fleets dir: %w", err)
		}
		if err := repo.WriteManifest(fleetManifestPath, []byte(manifest)); err != nil {
			return fmt.Errorf("write fleet manifest: %w", err)
		}
		// Use CommitFiles to stage ONLY the fleet manifest, not accidental deletions.
		if err := repo.CommitFiles([]string{fleetManifestPath}, commitMsg); err != nil {
			return fmt.Errorf("commit fleet manifest: %w", err)
		}
		return nil
	}
	// Single-repo mode: write to all target clusters.
	clusters := k.targetClusters(fleetID)
	var manifestPaths []string
	for _, cluster := range clusters {
		p := fleetManifestPathFor(cluster, fleetID)
		if err := k.repo.WriteManifest(p, []byte(manifest)); err != nil {
			return fmt.Errorf("write fleet manifest for %s: %w", cluster, err)
		}
		manifestPaths = append(manifestPaths, p)
	}
	if err := k.repo.CommitFiles(manifestPaths, commitMsg); err != nil {
		return fmt.Errorf("commit fleet manifest: %w", err)
	}
	return nil
}

// getFleetRepoByName returns the GitOpsRepo using a fleet name (for creation before DB has the record).
func (k *K8sOrchestrator) getFleetRepoByName(fleetID, fleetName string) (*GitOpsRepo, error) {
	if k.fleetRepos != nil {
		return k.fleetRepos.GetFleetRepo(fleetID, fleetName)
	}
	return k.repo, nil
}

// ---------------------------------------------------------------------------
// Multi-region helpers
// ---------------------------------------------------------------------------

// clusterNames returns all configured cluster names from the GITOPS_CLUSTER_NAMES
// env var (comma-separated). Falls back to the single clusterName field.
func (k *K8sOrchestrator) clusterNames() []string {
	env := os.Getenv("GITOPS_CLUSTER_NAMES")
	if env != "" {
		var names []string
		for _, s := range strings.Split(env, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				names = append(names, s)
			}
		}
		if len(names) > 0 {
			return names
		}
	}
	return []string{k.clusterName}
}

// clusterNamesForRegions returns the subset of configured cluster names that
// match the given region list. Matching is done by checking if the cluster
// name contains the region string (e.g. "data-plane-us-east-1" matches region
// "us-east-1"). If no regions are specified, all configured clusters are returned.
func (k *K8sOrchestrator) clusterNamesForRegions(regions []string) []string {
	all := k.clusterNames()
	if len(regions) == 0 {
		return all
	}

	var matched []string
	for _, cn := range all {
		for _, r := range regions {
			if strings.Contains(cn, r) {
				matched = append(matched, cn)
				break
			}
		}
	}
	// If none matched (misconfiguration), broadcast to all.
	if len(matched) == 0 {
		return all
	}
	return matched
}

// fleetRegions decodes the Fleet.Regions JSON field from the database and
// returns a list of region strings. Returns nil if the fleet has no regions set.
func fleetRegions(fleetID string) []string {
	if db == nil {
		return nil
	}
	var fleet Fleet
	if err := db.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		return nil
	}
	if len(fleet.Regions) == 0 || string(fleet.Regions) == "null" {
		return nil
	}
	var regions []string
	if err := json.Unmarshal(fleet.Regions, &regions); err != nil {
		return nil
	}
	return regions
}

// targetClusters returns the list of cluster names a fleet should be written to.
func (k *K8sOrchestrator) targetClusters(fleetID string) []string {
	regions := fleetRegions(fleetID)
	return k.clusterNamesForRegions(regions)
}

// ---------------------------------------------------------------------------
// Path helpers (cluster-parameterised)
// ---------------------------------------------------------------------------

// fleetDir returns the relative path to the fleets directory for this cluster.
func (k *K8sOrchestrator) fleetDir() string {
	return filepath.Join("clusters", k.clusterName, "fleets")
}

// fleetDirFor returns the fleets directory for a specific cluster name.
func fleetDirFor(cluster string) string {
	return filepath.Join("clusters", cluster, "fleets")
}

// fleetManifestPath returns the relative path for a fleet's manifest.
func (k *K8sOrchestrator) fleetManifestPath(fleetID string) string {
	return filepath.Join(k.fleetDir(), fleetID+".yaml")
}

// fleetManifestPathFor returns the manifest path for a specific cluster.
func fleetManifestPathFor(cluster, fleetID string) string {
	return filepath.Join(fleetDirFor(cluster), fleetID+".yaml")
}

// routeDir returns the relative path to the routes directory for this cluster.
func (k *K8sOrchestrator) routeDir() string {
	return filepath.Join("clusters", k.clusterName, "routes")
}

// routeDirFor returns the routes directory for a specific cluster name.
func routeDirFor(cluster string) string {
	return filepath.Join("clusters", cluster, "routes")
}

// routeManifestPathFor returns the manifest path for a route in a specific cluster.
func routeManifestPathFor(cluster, routeID string) string {
	return filepath.Join(routeDirFor(cluster), routeID+".yaml")
}

// lambdaDir returns the relative path to the lambdas directory for this cluster.
func (k *K8sOrchestrator) lambdaDir() string {
	return filepath.Join("clusters", k.clusterName, "lambdas")
}

// lambdaDirFor returns the lambdas directory for a specific cluster name.
func lambdaDirFor(cluster string) string {
	return filepath.Join("clusters", cluster, "lambdas")
}

// lambdaManifestPath returns the relative path for a lambda's manifest.
func (k *K8sOrchestrator) lambdaManifestPath(routeID string) string {
	shortID := routeID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return filepath.Join(k.lambdaDir(), shortID+".yaml")
}

// ---------------------------------------------------------------------------
// YAML generation helpers (template strings - avoids external YAML deps)
// ---------------------------------------------------------------------------

// fleetNameToK8sSlug converts a human-readable fleet display name into a
// Kubernetes-safe resource name. The result is prefixed with "fleet-" and
// uses only lowercase alphanumerics and hyphens, capped at 52 chars.
// Example: "JPMM Markets" → "fleet-jpmm-markets"
func fleetNameToK8sSlug(name string) string {
	slug := strings.ToLower(name)
	var b strings.Builder
	prev := '-'
	for _, ch := range slug {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
			prev = ch
		default:
			if prev != '-' {
				b.WriteRune('-')
			}
			prev = '-'
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 46 { // leave room for "fleet-" prefix
		s = s[:46]
	}
	if s == "" {
		return "fleet-unnamed"
	}
	return "fleet-" + s
}

func generateFleetCRD(fleetID, fallbackGatewayType string, replicas int, nodes []gitopsNodeSpec) string {
	// Look up fleet name, subdomain, and k8s_name from DB for CRD generation.
	fleetName := fleetID
	fleetSubdomain := fleetID + ".jpm.com"
	k8sName := fleetID // default: use UUID (backwards compat for existing fleets)
	if db != nil {
		var f Fleet
		if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err == nil {
			if f.Name != "" {
				fleetName = f.Name
			}
			if f.Subdomain != "" {
				fleetSubdomain = f.Subdomain
			}
			if f.K8sName != "" {
				k8sName = f.K8sName
			}
		}
	}

	// Determine top-level gatewayType from actual nodes; use "mixed" for multi-type fleets.
	typeSet := map[string]bool{}
	for _, n := range nodes {
		if n.GatewayType != "" {
			typeSet[n.GatewayType] = true
		}
	}
	gatewayType := fallbackGatewayType
	if len(typeSet) == 1 {
		for t := range typeSet {
			gatewayType = t
		}
	} else if len(typeSet) > 1 {
		gatewayType = "mixed"
	}

	nodesYAML := ""
	for _, n := range nodes {
		nodesYAML += fmt.Sprintf("    - name: %q\n", n.Name)
		nodesYAML += fmt.Sprintf("      index: %d\n", n.Index)
		if n.GatewayType != "" {
			nodesYAML += fmt.Sprintf("      gatewayType: %q\n", n.GatewayType)
		}
		nodesYAML += fmt.Sprintf("      datacenter: %q\n", n.Datacenter)
		nodesYAML += fmt.Sprintf("      region: %q\n", n.Region)
		nodesYAML += fmt.Sprintf("      status: %q\n", n.Status)
	}

	return fmt.Sprintf(`apiVersion: ingress.jpmc.com/v1alpha1
kind: Fleet
metadata:
  name: %s
  namespace: ingress-dp
  labels:
    app.kubernetes.io/managed-by: management-api
    fleet.jpmc.com/id: %q
spec:
  name: %q
  subdomain: %q
  gatewayType: %s
  replicas: %d
  nodes:
%s`, k8sName, fleetID, fleetName, fleetSubdomain, gatewayType, replicas, nodesYAML)
}

type gitopsNodeSpec struct {
	Name        string
	Index       int
	GatewayType string // per-node type ("envoy" or "kong")
	Datacenter  string
	Region      string
	Status      string
}

func generateLambdaCRD(routeID, funcName, code string) string {
	// Escape the function code for safe embedding in YAML by using a literal block scalar.
	indentedCode := ""
	for _, line := range strings.Split(code, "\n") {
		indentedCode += "      " + line + "\n"
	}

	return fmt.Sprintf(`apiVersion: ingress.jpmc.com/v1alpha1
kind: Lambda
metadata:
  name: lambda-%s
  namespace: ingress-dp
  labels:
    app.kubernetes.io/managed-by: management-api
    ingress.jpmc.com/route: %s
spec:
  functionName: %s
  runtime: nodejs20
  code: |
%sstatus:
  phase: Pending
`, routeID, routeID, funcName, indentedCode)
}

// ---------------------------------------------------------------------------
// Manifest parsing helpers
// ---------------------------------------------------------------------------

// parseFleetManifest does a lightweight parse of a Fleet CRD YAML to extract
// key fields. This avoids pulling in a full YAML library for read-path operations.
func parseFleetManifest(data []byte) (gatewayType string, replicas int, nodes []gitopsNodeSpec) {
	lines := strings.Split(string(data), "\n")
	replicas = 0
	inNodes := false
	var currentNode *gitopsNodeSpec

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "gatewayType:") {
			val := stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "gatewayType:")))
			if inNodes && currentNode != nil {
				currentNode.GatewayType = val // per-node gateway type
			} else if !inNodes {
				gatewayType = val // fleet-level gateway type
			}
			continue
		}
		if strings.HasPrefix(trimmed, "replicas:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "replicas:"))
			replicas, _ = strconv.Atoi(val)
			continue
		}
		if strings.HasPrefix(trimmed, "nodes:") {
			inNodes = true
			continue
		}
		if inNodes && strings.HasPrefix(trimmed, "- name:") {
			if currentNode != nil {
				nodes = append(nodes, *currentNode)
			}
			currentNode = &gitopsNodeSpec{
				Name:   stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))),
				Status: "running",
			}
			continue
		}
		if inNodes && currentNode != nil {
			if strings.HasPrefix(trimmed, "index:") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "index:"))
				currentNode.Index, _ = strconv.Atoi(val)
			} else if strings.HasPrefix(trimmed, "datacenter:") {
				currentNode.Datacenter = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "datacenter:")))
			} else if strings.HasPrefix(trimmed, "region:") {
				currentNode.Region = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "region:")))
			} else if strings.HasPrefix(trimmed, "status:") {
				currentNode.Status = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "status:")))
			}
		}
		// End of nodes section when we encounter a top-level key.
		if inNodes && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			inNodes = false
		}
	}
	if currentNode != nil {
		nodes = append(nodes, *currentNode)
	}
	return
}

func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

// ---------------------------------------------------------------------------
// Orchestrator interface implementation
// ---------------------------------------------------------------------------

func (k *K8sOrchestrator) CreateFleetNodes(fleetID, gatewayType string, count int) ([]FleetNode, error) {
	specs := make([]gitopsNodeSpec, count)
	fleetNodes := make([]FleetNode, count)
	for i := 0; i < count; i++ {
		index := i + 1
		name := fmt.Sprintf("%s-%s-%d", fleetID, gatewayType, index)
		dc := assignDatacenter(index)
		region := assignRegion(index)

		specs[i] = gitopsNodeSpec{
			Name:        name,
			Index:       index,
			GatewayType: gatewayType,
			Datacenter:  dc,
			Region:      region,
			Status:      "running",
		}
		fleetNodes[i] = FleetNode{
			ContainerID:   fmt.Sprintf("k8s-%s-%d", fleetID, index),
			ContainerName: name,
			FleetID:       fleetID,
			GatewayType:   gatewayType,
			Port:          0, // Ports are managed by the K8s Service/Ingress
			Status:        "pending",
			Index:         index,
			Datacenter:    dc,
			Region:        region,
		}
	}

	manifest := generateFleetCRD(fleetID, gatewayType, count, specs)

	if k.fleetRepos != nil {
		// Per-fleet GitHub repo mode: create the repo if it doesn't exist,
		// then write manifests directly to root-level fleets/ directory.
		fleetName := fleetID
		if db != nil {
			var f Fleet
			if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err == nil && f.Name != "" {
				fleetName = f.Name
			}
		}

		repo, err := k.fleetRepos.CreateFleetRepo(fleetID, fleetName)
		if err != nil {
			return nil, fmt.Errorf("create fleet GitHub repo: %w", err)
		}

		if err := repo.EnsureDirectory("fleets"); err != nil {
			return nil, fmt.Errorf("ensure fleets dir: %w", err)
		}
		if err := repo.WriteManifest(filepath.Join("fleets", fleetID+".yaml"), []byte(manifest)); err != nil {
			return nil, fmt.Errorf("write fleet manifest: %w", err)
		}
		if err := repo.CommitAndPush(fmt.Sprintf("Create fleet %s (%s x%d)", fleetID, gatewayType, count)); err != nil {
			return nil, fmt.Errorf("commit fleet manifest: %w", err)
		}

		log.Printf("k8s: committed fleet manifest to GitHub repo for %s (%s x%d)", fleetID, gatewayType, count)

		// Apply CRD directly to cluster so operator can act immediately
		if err := k.applyToCluster(manifest); err != nil {
			log.Printf("k8s: warning: direct cluster apply failed for fleet %s: %v", fleetID, err)
		}

		// Update DB with git repo URL
		if db != nil {
			repoURL := k.fleetRepos.GetRepoURL(fleetName)
			db.Exec("UPDATE fleets SET git_manifest_path=$1 WHERE id=$2", repoURL, fleetID)
		}
	} else {
		// Single-repo fallback mode: write to clusters/{cluster}/fleets/
		clusters := k.targetClusters(fleetID)
		for _, cluster := range clusters {
			dir := fleetDirFor(cluster)
			if err := k.repo.EnsureDirectory(dir); err != nil {
				return nil, fmt.Errorf("ensure fleet dir for %s: %w", cluster, err)
			}
			path := fleetManifestPathFor(cluster, fleetID)
			if err := k.repo.WriteManifest(path, []byte(manifest)); err != nil {
				return nil, fmt.Errorf("write fleet manifest for %s: %w", cluster, err)
			}
		}

		if err := k.repo.CommitAndPush(fmt.Sprintf("Create fleet %s (%s x%d) in %s", fleetID, gatewayType, count, strings.Join(clusters, ", "))); err != nil {
			return nil, fmt.Errorf("commit fleet manifest: %w", err)
		}

		log.Printf("k8s: committed fleet manifest for %s (%s x%d) to clusters: %s", fleetID, gatewayType, count, strings.Join(clusters, ", "))

		// Apply CRD directly to cluster
		if err := k.applyToCluster(manifest); err != nil {
			log.Printf("k8s: warning: direct cluster apply failed for fleet %s: %v", fleetID, err)
		}
	}

	return fleetNodes, nil
}

func (k *K8sOrchestrator) ListFleetNodes(fleetID string) ([]FleetNode, error) {
	var data []byte
	var err error
	if k.fleetRepos != nil {
		repo, repoErr := k.getFleetRepo(fleetID)
		if repoErr != nil {
			return []FleetNode{}, nil
		}
		data, err = repo.ReadManifest(filepath.Join("fleets", fleetID+".yaml"))
	} else {
		data, err = k.repo.ReadManifest(k.fleetManifestPath(fleetID))
	}
	// Original logic continues below using data/err
	if err != nil {
		if os.IsNotExist(err) {
			return []FleetNode{}, nil
		}
		return nil, fmt.Errorf("read fleet manifest: %w", err)
	}

	fleetGwType, _, nodes := parseFleetManifest(data)

	fleetNodes := make([]FleetNode, len(nodes))
	for i, n := range nodes {
		// Per-node gatewayType takes precedence; fall back to fleet-level type.
		nodeGwType := n.GatewayType
		if nodeGwType == "" {
			nodeGwType = fleetGwType
		}
		fleetNodes[i] = FleetNode{
			ContainerID:   fmt.Sprintf("k8s-%s-%d", fleetID, n.Index),
			ContainerName: n.Name,
			FleetID:       fleetID,
			GatewayType:   nodeGwType,
			Port:          0,
			Status:        n.Status,
			Index:         n.Index,
			Datacenter:    n.Datacenter,
			Region:        n.Region,
		}
	}
	return fleetNodes, nil
}

func (k *K8sOrchestrator) ScaleFleetNodes(fleetID, gatewayType string, desiredCount int) ([]FleetNode, error) {
	data, err := k.readFleetManifest(fleetID)
	if err != nil {
		return nil, fmt.Errorf("read fleet manifest for scaling: %w", err)
	}

	fleetGW, _, existingNodes := parseFleetManifest(data)
	if fleetGW == "" {
		fleetGW = gatewayType
	}

	// Only scale nodes of the requested gateway type; keep other-type nodes untouched.
	var matchingNodes []gitopsNodeSpec
	var otherNodes []gitopsNodeSpec
	for _, n := range existingNodes {
		nodeGW := n.GatewayType
		if nodeGW == "" {
			nodeGW = fleetGW
		}
		if nodeGW == gatewayType {
			matchingNodes = append(matchingNodes, n)
		} else {
			otherNodes = append(otherNodes, n)
		}
	}

	currentCount := len(matchingNodes)
	if desiredCount == currentCount {
		return k.ListFleetNodes(fleetID)
	}

	var newSpecs []gitopsNodeSpec
	if desiredCount > currentCount {
		// Scale up: keep existing matching nodes and append new ones.
		maxIndex := 0
		for _, n := range existingNodes {
			if n.Index > maxIndex {
				maxIndex = n.Index
			}
		}
		newSpecs = append(newSpecs, matchingNodes...)
		for i := 0; i < desiredCount-currentCount; i++ {
			idx := maxIndex + 1 + i
			name := fmt.Sprintf("%s-%s-%d", fleetID, gatewayType, idx)
			newSpecs = append(newSpecs, gitopsNodeSpec{
				Name:        name,
				Index:       idx,
				GatewayType: gatewayType,
				Datacenter:  assignDatacenter(idx),
				Region:      assignRegion(idx),
				Status:      "running",
			})
		}
	} else {
		// Scale down: keep the first desiredCount matching nodes.
		newSpecs = matchingNodes[:desiredCount]
	}

	allNodes := append(otherNodes, newSpecs...)
	manifest := generateFleetCRD(fleetID, fleetGW, len(allNodes), allNodes)

	if err := k.writeAndPushFleetManifest(fleetID, manifest,
		fmt.Sprintf("Scale fleet %s (%s) to %d nodes", fleetID, gatewayType, desiredCount)); err != nil {
		return nil, err
	}

	log.Printf("k8s: scaled fleet %s (%s) to %d nodes", fleetID, gatewayType, desiredCount)

	if err := k.applyToCluster(manifest); err != nil {
		log.Printf("k8s: warning: direct cluster apply failed for scaled fleet %s: %v", fleetID, err)
	}

	return k.ListFleetNodes(fleetID)
}

func (k *K8sOrchestrator) RemoveFleetNodes(fleetID, k8sName string) error {
	clusters := k.targetClusters(fleetID)
	for _, cluster := range clusters {
		path := fleetManifestPathFor(cluster, fleetID)
		if err := k.repo.DeleteManifest(path); err != nil {
			log.Printf("k8s: warning: could not delete fleet manifest in %s: %v", cluster, err)
		}
	}
	if err := k.repo.CommitAndPush(fmt.Sprintf("Remove fleet %s from %s", fleetID, strings.Join(clusters, ", "))); err != nil {
		return fmt.Errorf("commit fleet removal: %w", err)
	}
	log.Printf("k8s: removed fleet manifest for %s from clusters: %s", fleetID, strings.Join(clusters, ", "))

	// Delete Fleet CR from cluster using the correct resource name.
	// New fleets use a human-readable k8sName slug; existing fleets have k8sName == UUID.
	crName := k8sName
	if crName == "" {
		crName = fleetID
	}
	k.deleteFromCluster(fleetGVR, "ingress-dp", crName)

	return nil
}

func (k *K8sOrchestrator) StopFleetNodes(fleetID string) error {
	data, err := k.readFleetManifest(fleetID)
	if err != nil {
		return fmt.Errorf("read fleet manifest for stop: %w", err)
	}

	gwType, replicas, nodes := parseFleetManifest(data)
	for i := range nodes {
		nodes[i].Status = "stopped"
	}

	manifest := generateFleetCRD(fleetID, gwType, replicas, nodes)
	if err := k.writeAndPushFleetManifest(fleetID, manifest, fmt.Sprintf("Stop fleet %s", fleetID)); err != nil {
		return err
	}

	log.Printf("k8s: stopped fleet %s (all nodes → stopped)", fleetID)
	return nil
}

func (k *K8sOrchestrator) StartFleetNodes(fleetID string) error {
	data, err := k.readFleetManifest(fleetID)
	if err != nil {
		return fmt.Errorf("read fleet manifest for start: %w", err)
	}

	gwType, replicas, nodes := parseFleetManifest(data)
	for i := range nodes {
		nodes[i].Status = "running"
	}

	manifest := generateFleetCRD(fleetID, gwType, replicas, nodes)
	if err := k.writeAndPushFleetManifest(fleetID, manifest, fmt.Sprintf("Start fleet %s", fleetID)); err != nil {
		return err
	}

	log.Printf("k8s: started fleet %s (all nodes → running)", fleetID)
	return nil
}

func (k *K8sOrchestrator) DeploySingleNode(fleetID, gatewayType, datacenter, nodeName string, startIndex int) ([]FleetNode, error) {
	data, err := k.readFleetManifest(fleetID)
	if err != nil {
		return nil, fmt.Errorf("read fleet manifest for deploy: %w", err)
	}

	fleetGW, replicas, existingNodes := parseFleetManifest(data)
	if fleetGW == "" {
		fleetGW = gatewayType
	}

	dc := datacenter
	if dc == "" {
		dc = assignDatacenter(startIndex)
	}
	region := assignRegion(startIndex)
	if datacenter != "" {
		for _, d := range datacenters {
			if d.DC == datacenter {
				region = d.Region
				break
			}
		}
	}

	name := nodeName
	if name == "" {
		name = fmt.Sprintf("%s-%s-%d", fleetID, gatewayType, startIndex)
	}

	newNode := gitopsNodeSpec{
		Name:        name,
		Index:       startIndex,
		GatewayType: gatewayType,
		Datacenter:  dc,
		Region:      region,
		Status:      "running",
	}

	allNodes := append(existingNodes, newNode)
	manifest := generateFleetCRD(fleetID, fleetGW, replicas+1, allNodes)

	if err := k.writeAndPushFleetManifest(fleetID, manifest,
		fmt.Sprintf("Deploy %s node %s to fleet %s", gatewayType, name, fleetID)); err != nil {
		return nil, err
	}

	log.Printf("k8s: deployed node %s (%s) to fleet %s", name, gatewayType, fleetID)

	if err := k.applyToCluster(manifest); err != nil {
		log.Printf("k8s: warning: direct cluster apply failed for fleet %s after node deploy: %v", fleetID, err)
	}

	return []FleetNode{{
		ContainerID:   fmt.Sprintf("k8s-%s-%d", fleetID, startIndex),
		ContainerName: name,
		FleetID:       fleetID,
		GatewayType:   gatewayType,
		Port:          0,
		Status:        "pending",
		Index:         startIndex,
		Datacenter:    dc,
		Region:        region,
	}}, nil
}

// ---------------------------------------------------------------------------
// Route CRD operations
// ---------------------------------------------------------------------------

// routeFilename returns a human-readable YAML filename for a route based on its
// path.  e.g. "/api/users/profile" → "api-users-profile.yaml", "/" → "root.yaml".
// Only alphanumeric characters and hyphens are kept; everything else becomes a hyphen.
func routeFilename(route Route) string {
	p := strings.TrimPrefix(route.Path, "/")
	if p == "" {
		return "root.yaml"
	}
	var b strings.Builder
	for _, c := range strings.ToLower(p) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	name := b.String()
	// Collapse consecutive hyphens produced by multi-char separators (e.g. "//", "/{id}").
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if name == "" {
		// Absolute fallback: use first 8 chars of the route ID.
		if len(route.ID) >= 8 {
			return route.ID[:8] + ".yaml"
		}
		return route.ID + ".yaml"
	}
	return name + ".yaml"
}

func generateRouteCRD(route Route, fleetSubdomain string) string {
	// Build targetFleet from the fleet subdomain (the fleet name used in CRDs).
	targetFleet := fleetSubdomain

	// Build methods array
	var methods []string
	if len(route.Methods) > 0 && string(route.Methods) != "null" {
		json.Unmarshal(route.Methods, &methods)
	}
	methodsYAML := ""
	for _, m := range methods {
		methodsYAML += fmt.Sprintf("    - %s\n", m)
	}

	// Build allowed roles array
	var roles []string
	if len(route.AllowedRoles) > 0 && string(route.AllowedRoles) != "null" {
		json.Unmarshal(route.AllowedRoles, &roles)
	}
	rolesYAML := ""
	for _, r := range roles {
		rolesYAML += fmt.Sprintf("    - %s\n", r)
	}

	// Build target nodes array
	var targetNodes []string
	if len(route.TargetNodes) > 0 && string(route.TargetNodes) != "null" {
		json.Unmarshal(route.TargetNodes, &targetNodes)
	}
	targetNodesYAML := ""
	for _, n := range targetNodes {
		targetNodesYAML += fmt.Sprintf("    - %s\n", n)
	}

	// Build authz scopes array
	var scopes []string
	if len(route.AuthzScopes) > 0 && string(route.AuthzScopes) != "null" {
		json.Unmarshal(route.AuthzScopes, &scopes)
	}
	scopesYAML := ""
	for _, s := range scopes {
		scopesYAML += fmt.Sprintf("    - %s\n", s)
	}

	yaml := fmt.Sprintf(`apiVersion: ingress.jpmc.com/v1alpha1
kind: Route
metadata:
  name: %s
  namespace: ingress-dp
  labels:
    app.kubernetes.io/managed-by: management-api
    ingress.jpmc.com/gateway: %s
spec:
  path: %q
  hostname: %q
  backendUrl: %q
  gatewayType: %s
  audience: %q
  team: %q
  authnMechanism: %q
  authIssuer: %q
  tlsRequired: %v
`, route.ID, route.GatewayType,
		route.Path, route.Hostname, route.BackendURL,
		route.GatewayType, route.Audience, route.Team,
		route.AuthnMechanism, route.AuthIssuer, route.TLSRequired)

	if targetFleet != "" && targetFleet != "*" {
		yaml += fmt.Sprintf("  targetFleet: %q\n", targetFleet)
	}

	if len(methods) > 0 {
		yaml += "  methods:\n" + methodsYAML
	}
	if len(roles) > 0 {
		yaml += "  allowedRoles:\n" + rolesYAML
	}
	if len(targetNodes) > 0 {
		yaml += "  targetNodes:\n" + targetNodesYAML
	}
	if len(scopes) > 0 {
		yaml += "  authzScopes:\n" + scopesYAML
	}
	if route.HealthPath != "" {
		yaml += fmt.Sprintf("  healthPath: %q\n", route.HealthPath)
	}
	if route.Notes != "" {
		yaml += fmt.Sprintf("  notes: %q\n", route.Notes)
	}
	if route.FunctionCode != "" {
		indentedCode := ""
		for _, line := range strings.Split(route.FunctionCode, "\n") {
			indentedCode += "      " + line + "\n"
		}
		yaml += "  functionCode: |\n" + indentedCode
		yaml += fmt.Sprintf("  functionLanguage: %s\n", route.FunctionLanguage)
	}

	yaml += `status:
  phase: Pending
`
	return yaml
}

func (k *K8sOrchestrator) WriteRouteCRD(route Route, fleetSubdomain string) error {
	manifest := generateRouteCRD(route, fleetSubdomain)

	if k.fleetRepos != nil {
		// Per-fleet repo mode: find the fleet by subdomain and write to its repo.
		if fleetSubdomain == "" || fleetSubdomain == "*" {
			log.Printf("k8s: skipping route CRD write for %s (no fleet association)", route.ID)
			return nil
		}
		var fleet Fleet
		if db == nil {
			return nil
		}
		if err := db.Get(&fleet, "SELECT * FROM fleets WHERE subdomain=$1", fleetSubdomain); err != nil {
			log.Printf("k8s: fleet not found for subdomain %s, skipping route CRD write", fleetSubdomain)
			return nil
		}

		repo, err := k.getFleetRepo(fleet.ID)
		if err != nil {
			return fmt.Errorf("get fleet repo for route: %w", err)
		}

		if err := repo.EnsureDirectory("routes"); err != nil {
			return fmt.Errorf("ensure routes dir: %w", err)
		}
		fname := routeFilename(route)
		if err := repo.WriteManifest(filepath.Join("routes", fname), []byte(manifest)); err != nil {
			return fmt.Errorf("write route manifest: %w", err)
		}
		// Migrate: remove the old ID-based file if it still exists (silent best-effort).
		oldPath := filepath.Join("routes", route.ID+".yaml")
		_ = repo.DeleteManifest(oldPath)

		if err := repo.CommitAndPush(fmt.Sprintf("Write route %s (%s → %s)", fname, route.Path, route.BackendURL)); err != nil {
			return fmt.Errorf("commit route manifest: %w", err)
		}

		log.Printf("k8s: committed route manifest %s to fleet repo %s", fname, fleet.Name)

		// Apply Route CRD directly to cluster
		if err := k.applyToCluster(manifest); err != nil {
			log.Printf("k8s: warning: direct cluster apply failed for route %s: %v", route.ID, err)
		}
	} else {
		// Single-repo mode: write to clusters/{cluster}/routes/
		var regions []string
		if fleetSubdomain != "" && fleetSubdomain != "*" && db != nil {
			var fleet Fleet
			if err := db.Get(&fleet, "SELECT * FROM fleets WHERE subdomain=$1", fleetSubdomain); err == nil {
				if len(fleet.Regions) > 0 && string(fleet.Regions) != "null" {
					json.Unmarshal(fleet.Regions, &regions)
				}
			}
		}
		clusters := k.clusterNamesForRegions(regions)

		fname := routeFilename(route)
		for _, cluster := range clusters {
			dir := routeDirFor(cluster)
			if err := k.repo.EnsureDirectory(dir); err != nil {
				return fmt.Errorf("ensure route dir for %s: %w", cluster, err)
			}
			if err := k.repo.WriteManifest(filepath.Join(dir, fname), []byte(manifest)); err != nil {
				return fmt.Errorf("write route manifest for %s: %w", cluster, err)
			}
			// Migrate: remove old ID-based file if present (best-effort).
			_ = k.repo.DeleteManifest(routeManifestPathFor(cluster, route.ID))
		}

		if err := k.repo.CommitAndPush(fmt.Sprintf("Write route %s (%s → %s) to %s",
			fname, route.Path, route.BackendURL, strings.Join(clusters, ", "))); err != nil {
			return fmt.Errorf("commit route manifest: %w", err)
		}

		log.Printf("k8s: committed route manifest %s (%s) to clusters: %s",
			fname, route.Path, strings.Join(clusters, ", "))

		// Apply Route CRD directly to cluster
		if err := k.applyToCluster(manifest); err != nil {
			log.Printf("k8s: warning: direct cluster apply failed for route %s: %v", route.ID, err)
		}
	}
	return nil
}

func (k *K8sOrchestrator) DeleteRouteCRD(routeID string) error {
	if k.fleetRepos != nil {
		// Per-fleet mode: look up which fleet this route belongs to.
		if db == nil {
			return nil
		}
		var route Route
		if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err != nil {
			log.Printf("k8s: route %s not found in DB, trying all fleet repos", routeID)
			return nil
		}
		var fleet Fleet
		if err := db.Get(&fleet, "SELECT * FROM fleets WHERE subdomain=$1", route.Hostname); err != nil {
			return nil
		}

		repo, err := k.getFleetRepo(fleet.ID)
		if err != nil {
			return nil // Repo might not exist anymore
		}

		// Try path-based filename first; fall back to old ID-based name.
		fname := routeFilename(route)
		pathBased := filepath.Join("routes", fname)
		idBased := filepath.Join("routes", routeID+".yaml")
		deleted := false
		if err := repo.DeleteManifest(pathBased); err == nil {
			deleted = true
		}
		if !deleted {
			_ = repo.DeleteManifest(idBased)
		} else {
			// Also clean up any lingering ID-based file.
			_ = repo.DeleteManifest(idBased)
		}

		if err := repo.CommitAndPush(fmt.Sprintf("Remove route %s (%s)", fname, routeID)); err != nil {
			return fmt.Errorf("commit route removal: %w", err)
		}
		log.Printf("k8s: removed route %s from fleet repo %s", fname, fleet.Name)
	} else {
		// Single-repo mode: look up route to derive path-based filename.
		var route Route
		var fname string
		if db != nil {
			if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err == nil {
				fname = routeFilename(route)
			}
		}

		clusters := k.clusterNames()
		deleted := false
		for _, cluster := range clusters {
			if fname != "" {
				if err := k.repo.DeleteManifest(filepath.Join(routeDirFor(cluster), fname)); err == nil {
					deleted = true
				}
			}
			// Also remove old ID-based file (migration).
			if err := k.repo.DeleteManifest(routeManifestPathFor(cluster, routeID)); err == nil {
				deleted = true
			}
		}

		if deleted {
			label := routeID
			if fname != "" {
				label = fname
			}
			if err := k.repo.CommitAndPush(fmt.Sprintf("Remove route %s from %s", label, strings.Join(clusters, ", "))); err != nil {
				return fmt.Errorf("commit route removal: %w", err)
			}
			log.Printf("k8s: removed route manifest for %s from clusters: %s", label, strings.Join(clusters, ", "))
		}
	}
	return nil
}

func (k *K8sOrchestrator) UpdateFleetManifest(fleet Fleet) error {
	// Re-read the existing manifest to preserve node specs, then rewrite with updated metadata.
	data, err := k.readFleetManifest(fleet.ID)
	if err != nil {
		log.Printf("k8s: no existing manifest for fleet %s to update", fleet.ID)
		return nil
	}

	gwType, replicas, nodes := parseFleetManifest(data)
	if fleet.GatewayType != "" && fleet.GatewayType != "mixed" {
		gwType = fleet.GatewayType
	}

	manifest := generateFleetCRD(fleet.ID, gwType, replicas, nodes)
	if err := k.writeAndPushFleetManifest(fleet.ID, manifest,
		fmt.Sprintf("Update fleet %s metadata", fleet.ID)); err != nil {
		return err
	}

	log.Printf("k8s: updated fleet manifest for %s", fleet.ID)
	return nil
}

// ---------------------------------------------------------------------------
// Lambda operations
// ---------------------------------------------------------------------------

func (k *K8sOrchestrator) CreateLambdaContainer(routeID, funcName, code string) (containerID string, port int, err error) {
	// 1. Write GitOps manifest for audit trail (non-fatal if it fails)
	if dirErr := k.repo.EnsureDirectory(k.lambdaDir()); dirErr != nil {
		log.Printf("k8s: warning: could not ensure lambda dir: %v", dirErr)
	} else {
		manifest := generateLambdaCRD(routeID, funcName, code)
		if writeErr := k.repo.WriteManifest(k.lambdaManifestPath(routeID), []byte(manifest)); writeErr != nil {
			log.Printf("k8s: warning: could not write lambda manifest: %v", writeErr)
		} else if commitErr := k.repo.CommitAndPush(fmt.Sprintf("Create lambda %s for route %s", funcName, routeID)); commitErr != nil {
			log.Printf("k8s: warning: could not commit lambda manifest: %v", commitErr)
		}
	}

	containerName := lambdaContainerName(routeID, funcName)
	namespace := "ingress-cp"

	// 2. Create actual Deployment + Service in the cluster
	if k.clientset == nil {
		log.Printf("k8s: warning: no clientset — lambda %s will only exist in Git, not as a running pod", containerName)
		return containerName, 0, nil
	}

	ctx := context.Background()
	replicas := int32(1)
	labels := map[string]string{
		"app":                          containerName,
		"ingress.jpmc.com/component":   "lambda",
		"ingress.jpmc.com/route":       routeID,
		"ingress.jpmc.com/managed-by":  "management-api",
	}

	serverCode := generateLambdaServerJS()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      containerName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": containerName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "lambda",
						Image:   lambdaImage,
						Command: []string{"sh", "-c", `echo "$LAMBDA_SERVER_CODE" > /tmp/server.js && node /tmp/server.js`},
						Env: []corev1.EnvVar{
							{Name: "FUNCTION_CODE", Value: code},
							{Name: "FUNCTION_NAME", Value: funcName},
							{Name: "LAMBDA_SERVER_CODE", Value: serverCode},
							{Name: "PORT", Value: fmt.Sprintf("%d", lambdaInternalPort)},
							{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://jaeger:4318"},
						},
						Ports: []corev1.ContainerPort{{
							ContainerPort: int32(lambdaInternalPort),
							Protocol:      corev1.ProtocolTCP,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      containerName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": containerName},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       int32(lambdaInternalPort),
				TargetPort: intstr.FromInt(lambdaInternalPort),
				Protocol:   corev1.ProtocolTCP,
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if _, err := k.clientset.AppsV1().Deployments(namespace).Create(ctx, deploy, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return "", 0, fmt.Errorf("create lambda deployment: %w", err)
		}
		log.Printf("k8s: lambda deployment %s already exists, updating", containerName)
		if _, err := k.clientset.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{}); err != nil {
			return "", 0, fmt.Errorf("update lambda deployment: %w", err)
		}
	}

	if _, err := k.clientset.CoreV1().Services(namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return "", 0, fmt.Errorf("create lambda service: %w", err)
		}
	}

	log.Printf("k8s: created lambda Deployment+Service %s in %s for route %s", containerName, namespace, routeID)
	return containerName, 0, nil
}

func (k *K8sOrchestrator) RemoveLambdaContainer(containerID string) error {
	// 1. Clean up GitOps manifest (non-fatal)
	shortID := strings.TrimPrefix(containerID, "k8s-lambda-")
	if shortID == containerID {
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
	}
	relPath := filepath.Join(k.lambdaDir(), shortID+".yaml")
	if err := k.repo.DeleteManifest(relPath); err != nil {
		log.Printf("k8s: warning: could not delete lambda manifest %s: %v", relPath, err)
	} else if err := k.repo.CommitAndPush(fmt.Sprintf("Remove lambda %s", shortID)); err != nil {
		log.Printf("k8s: warning: could not commit lambda removal: %v", err)
	}

	// 2. Delete actual Deployment + Service from cluster
	if k.clientset == nil {
		log.Printf("k8s: no clientset — skipping lambda resource cleanup for %s", containerID)
		return nil
	}

	namespace := "ingress-cp"
	ctx := context.Background()
	resourceName := containerID // containerID is now the deployment/service name

	if err := k.clientset.AppsV1().Deployments(namespace).Delete(ctx, resourceName, metav1.DeleteOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Printf("k8s: warning: could not delete lambda deployment %s: %v", resourceName, err)
		}
	}

	if err := k.clientset.CoreV1().Services(namespace).Delete(ctx, resourceName, metav1.DeleteOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Printf("k8s: warning: could not delete lambda service %s: %v", resourceName, err)
		}
	}

	log.Printf("k8s: removed lambda Deployment+Service %s from %s", resourceName, namespace)
	return nil
}

func (k *K8sOrchestrator) ListLambdaContainers() ([]map[string]interface{}, error) {
	files, err := k.repo.ListManifests(k.lambdaDir())
	if err != nil {
		return nil, fmt.Errorf("list lambda manifests: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(files))
	for _, f := range files {
		relPath := filepath.Join(k.lambdaDir(), f)
		data, err := k.repo.ReadManifest(relPath)
		if err != nil {
			log.Printf("k8s: warning: could not read lambda manifest %s: %v", f, err)
			continue
		}

		// Lightweight parse to extract route ID and function name.
		routeID := ""
		funcName := ""
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ingress.jpmc.com/route:") {
				routeID = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "ingress.jpmc.com/route:")))
			}
			if strings.HasPrefix(trimmed, "functionName:") {
				funcName = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "functionName:")))
			}
		}

		shortID := strings.TrimSuffix(f, ".yaml")
		shortID = strings.TrimSuffix(shortID, ".yml")

		result = append(result, map[string]interface{}{
			"container_id":   "k8s-lambda-" + shortID,
			"container_name": fmt.Sprintf("lambda-%s-%s", shortID, funcName),
			"state":          "pending",
			"route_id":       routeID,
			"host_port":      0,
		})
	}

	return result, nil
}

func (k *K8sOrchestrator) StopLambdaContainersForFleet(fleetID string) error {
	// In GitOps mode, stopping lambdas for a fleet means updating each lambda manifest
	// to set a suspended annotation. For now, we log and rely on the fleet-level stop
	// which the K8s operator will handle.
	log.Printf("k8s: stop lambda containers for fleet %s (handled by K8s operator via fleet status)", fleetID)

	files, err := k.repo.ListManifests(k.lambdaDir())
	if err != nil {
		return fmt.Errorf("list lambda manifests: %w", err)
	}

	if db == nil {
		return nil
	}

	var fleet Fleet
	if err := db.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		return nil
	}

	changed := false
	for _, f := range files {
		relPath := filepath.Join(k.lambdaDir(), f)
		data, err := k.repo.ReadManifest(relPath)
		if err != nil {
			continue
		}

		// Check if this lambda belongs to the fleet by matching route hostname.
		routeID := ""
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ingress.jpmc.com/route:") {
				routeID = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "ingress.jpmc.com/route:")))
			}
		}
		if routeID == "" {
			continue
		}

		var route Route
		if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err != nil {
			continue
		}
		if route.Hostname != fleet.Subdomain {
			continue
		}

		// Add suspended annotation.
		content := string(data)
		if !strings.Contains(content, "ingress.jpmc.com/suspended") {
			content = strings.Replace(content,
				"  labels:",
				"  annotations:\n    ingress.jpmc.com/suspended: \"true\"\n  labels:",
				1)
			if err := k.repo.WriteManifest(relPath, []byte(content)); err != nil {
				log.Printf("k8s: warning: could not update lambda manifest %s: %v", f, err)
				continue
			}
			changed = true
		}
	}

	if changed {
		if err := k.repo.CommitAndPush(fmt.Sprintf("Suspend lambdas for fleet %s", fleetID)); err != nil {
			return fmt.Errorf("commit lambda suspend: %w", err)
		}
	}

	return nil
}

func (k *K8sOrchestrator) StartLambdaContainersForFleet(fleetID string) error {
	log.Printf("k8s: start lambda containers for fleet %s (handled by K8s operator via fleet status)", fleetID)

	files, err := k.repo.ListManifests(k.lambdaDir())
	if err != nil {
		return fmt.Errorf("list lambda manifests: %w", err)
	}

	if db == nil {
		return nil
	}

	var fleet Fleet
	if err := db.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		return nil
	}

	changed := false
	for _, f := range files {
		relPath := filepath.Join(k.lambdaDir(), f)
		data, err := k.repo.ReadManifest(relPath)
		if err != nil {
			continue
		}

		routeID := ""
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ingress.jpmc.com/route:") {
				routeID = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "ingress.jpmc.com/route:")))
			}
		}
		if routeID == "" {
			continue
		}

		var route Route
		if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err != nil {
			continue
		}
		if route.Hostname != fleet.Subdomain {
			continue
		}

		// Remove suspended annotation.
		content := string(data)
		if strings.Contains(content, "ingress.jpmc.com/suspended") {
			content = strings.Replace(content, "  annotations:\n    ingress.jpmc.com/suspended: \"true\"\n", "", 1)
			if err := k.repo.WriteManifest(relPath, []byte(content)); err != nil {
				log.Printf("k8s: warning: could not update lambda manifest %s: %v", f, err)
				continue
			}
			changed = true
		}
	}

	if changed {
		if err := k.repo.CommitAndPush(fmt.Sprintf("Resume lambdas for fleet %s", fleetID)); err != nil {
			return fmt.Errorf("commit lambda resume: %w", err)
		}
	}

	return nil
}
