package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// GET /gitops/status
// Returns GitOps sync status. In local-dev mode (no Argo CD API) this reads
// sync_status from fleets in the database and reports per-cluster directory
// existence from the GitOps repo.
// ---------------------------------------------------------------------------

func getGitOpsStatus(w http.ResponseWriter, r *http.Request) {
	type clusterStatus struct {
		Cluster    string `json:"cluster"`
		SyncStatus string `json:"sync_status"`
		FleetCount int    `json:"fleet_count"`
	}

	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		// Not in K8s mode -- return a minimal response.
		writeJSON(w, 200, map[string]interface{}{
			"mode":     "docker",
			"clusters": []clusterStatus{},
		})
		return
	}

	clusters := k8sOrch.clusterNames()
	statuses := make([]clusterStatus, 0, len(clusters))

	for _, cn := range clusters {
		dir := fleetDirFor(cn)
		files, _ := k8sOrch.repo.ListManifests(dir)
		status := "unknown"
		// Derive an aggregate status from DB fleets if available.
		if db != nil {
			var fleets []Fleet
			_ = db.Select(&fleets, "SELECT * FROM fleets WHERE status IS NOT NULL")
			synced := true
			for _, f := range fleets {
				if f.Status != "" && f.Status != "healthy" && f.Status != "synced" {
					synced = false
					break
				}
			}
			if synced && len(fleets) > 0 {
				status = "synced"
			} else if len(fleets) > 0 {
				status = "progressing"
			}
		}
		statuses = append(statuses, clusterStatus{
			Cluster:    cn,
			SyncStatus: status,
			FleetCount: len(files),
		})
	}

	writeJSON(w, 200, map[string]interface{}{
		"mode":     "k8s",
		"clusters": statuses,
	})
}

// ---------------------------------------------------------------------------
// GET /gitops/commits
// Returns recent commits aggregated from all per-fleet repos (if available)
// or the single shared repo.
// ---------------------------------------------------------------------------

func getRecentCommits(w http.ResponseWriter, r *http.Request) {
	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{
			"commits": []map[string]string{},
			"mode":    "docker",
		})
		return
	}

	var allCommits []map[string]string

	// If per-fleet repos exist, aggregate commits from all of them.
	if k8sOrch.fleetRepos != nil && db != nil {
		var fleets []Fleet
		_ = db.Select(&fleets, "SELECT * FROM fleets WHERE git_manifest_path LIKE 'https://%'")
		for _, f := range fleets {
			repo, err := k8sOrch.fleetRepos.GetFleetRepo(f.ID, f.Name)
			if err != nil {
				continue
			}
			commits, err := repo.RecentCommits(5)
			if err != nil {
				continue
			}
			for _, c := range commits {
				c["fleet_name"] = f.Name
				c["fleet_id"] = f.ID
				allCommits = append(allCommits, c)
			}
		}
	}

	// Also include commits from the shared repo (if any).
	if k8sOrch.repo != nil {
		commits, err := k8sOrch.repo.RecentCommits(10)
		if err == nil {
			for _, c := range commits {
				c["fleet_name"] = ""
				c["fleet_id"] = ""
				allCommits = append(allCommits, c)
			}
		}
	}

	// Sort by date descending and limit to 20.
	sort.Slice(allCommits, func(i, j int) bool {
		return allCommits[i]["date"] > allCommits[j]["date"]
	})
	if len(allCommits) > 20 {
		allCommits = allCommits[:20]
	}

	writeJSON(w, 200, map[string]interface{}{
		"commits": allCommits,
		"mode":    "k8s",
	})
}

// ---------------------------------------------------------------------------
// GET /gitops/repos
// Returns all per-fleet GitHub repos with their manifest files.
// ---------------------------------------------------------------------------

func getGitOpsRepos(w http.ResponseWriter, r *http.Request) {
	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 200, []interface{}{})
		return
	}

	if k8sOrch.fleetRepos == nil || db == nil {
		writeJSON(w, 200, []interface{}{})
		return
	}

	type manifestEntry struct {
		Path   string `json:"path"`
		Type   string `json:"type"`
		Source string `json:"source"`
	}

	type repoEntry struct {
		FleetID      string          `json:"fleet_id"`
		FleetName    string          `json:"fleet_name"`
		Subdomain    string          `json:"subdomain"`
		RepoURL      string          `json:"repo_url"`
		SyncStatus   string          `json:"sync_status"`
		GitCommitSHA string          `json:"git_commit_sha"`
		Manifests    []manifestEntry `json:"manifests"`
	}

	var fleets []Fleet
	_ = db.Select(&fleets, "SELECT * FROM fleets WHERE git_manifest_path LIKE 'https://%'")

	repos := make([]repoEntry, 0, len(fleets))

	for _, f := range fleets {
		entry := repoEntry{
			FleetID:      f.ID,
			FleetName:    f.Name,
			Subdomain:    f.Subdomain,
			RepoURL:      f.GitManifestPath,
			SyncStatus:   f.SyncStatus,
			GitCommitSHA: f.GitCommitSHA,
			Manifests:    []manifestEntry{},
		}

		repo, err := k8sOrch.fleetRepos.GetFleetRepo(f.ID, f.Name)
		if err != nil {
			log.Printf("gitops/repos: failed to get repo for fleet %s: %v", f.ID, err)
			repos = append(repos, entry)
			continue
		}

		// List fleet manifests
		fleetFiles, _ := repo.ListManifests("fleets")
		for _, file := range fleetFiles {
			entry.Manifests = append(entry.Manifests, manifestEntry{
				Path:   "fleets/" + file,
				Type:   "Fleet",
				Source: "management-api",
			})
		}

		// List route manifests
		routeFiles, _ := repo.ListManifests("routes")
		for _, file := range routeFiles {
			entry.Manifests = append(entry.Manifests, manifestEntry{
				Path:   "routes/" + file,
				Type:   "Route",
				Source: "management-api",
			})
		}

		// List lambda manifests
		lambdaFiles, _ := repo.ListManifests("lambdas")
		for _, file := range lambdaFiles {
			entry.Manifests = append(entry.Manifests, manifestEntry{
				Path:   "lambdas/" + file,
				Type:   "Lambda",
				Source: "management-api",
			})
		}

		repos = append(repos, entry)
	}

	writeJSON(w, 200, repos)
}

// ---------------------------------------------------------------------------
// POST /gitops/sync
// Triggers an Argo CD sync. In local dev (no Argo CD API), this just does a
// git add + commit + push to ensure the repo is up to date.
// ---------------------------------------------------------------------------

func triggerSync(w http.ResponseWriter, r *http.Request) {
	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 200, map[string]string{
			"status":  "skipped",
			"message": "not in k8s mode",
		})
		return
	}

	argoAPI := os.Getenv("ARGOCD_API_URL")
	if argoAPI != "" {
		// In production, call the Argo CD API to trigger a sync.
		// For each cluster application, POST /api/v1/applications/{name}/sync.
		clusters := k8sOrch.clusterNames()
		for _, cn := range clusters {
			appName := "ingress-dp-" + cn
			syncURL := strings.TrimRight(argoAPI, "/") + "/api/v1/applications/" + appName + "/sync"
			req, _ := http.NewRequest("POST", syncURL, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			token := os.Getenv("ARGOCD_AUTH_TOKEN")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("gitops: sync failed for %s: %v", appName, err)
			} else {
				resp.Body.Close()
				log.Printf("gitops: triggered sync for %s (status %d)", appName, resp.StatusCode)
			}
		}
		writeJSON(w, 200, map[string]string{
			"status":  "synced",
			"message": "Argo CD sync triggered for all clusters",
		})
		return
	}

	// Local dev: just ensure repo is committed.
	if err := k8sOrch.repo.CommitAndPush("Manual sync trigger"); err != nil {
		log.Printf("gitops: manual sync commit failed: %v", err)
	}

	writeJSON(w, 200, map[string]string{
		"status":  "synced",
		"message": "Local GitOps repo committed and pushed (no Argo CD API configured)",
	})
}

// ---------------------------------------------------------------------------
// GET /gitops/diff/{fleet_id}
// Returns the diff between the Git manifest and the DB state for a fleet.
// ---------------------------------------------------------------------------

func getGitOpsDiff(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	if fleetID == "" {
		writeJSON(w, 400, map[string]string{"error": "fleet_id is required"})
		return
	}

	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{
			"fleet_id": fleetID,
			"mode":     "docker",
			"diff":     "not available in docker mode",
		})
		return
	}

	// Read the manifest from the primary cluster directory.
	manifestPath := k8sOrch.fleetManifestPath(fleetID)
	data, err := k8sOrch.repo.ReadManifest(manifestPath)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "fleet manifest not found in git"})
		return
	}

	gitGW, gitReplicas, gitNodes := parseFleetManifest(data)

	// Read the DB state.
	var dbFleet Fleet
	dbState := map[string]interface{}{"found": false}
	if db != nil {
		if err := db.Get(&dbFleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err == nil {
			dbState = map[string]interface{}{
				"found":        true,
				"status":       dbFleet.Status,
				"gateway_type": dbFleet.GatewayType,
				"instances":    dbFleet.InstancesCount,
			}
		}
	}

	// Read live K8s state from actual routes if available.
	var liveRoutes []ActualRoute
	liveState := map[string]interface{}{"found": false}
	if db != nil {
		_ = db.Select(&liveRoutes, `SELECT ar.* FROM actual_routes ar
			INNER JOIN routes ro ON ar.route_id = ro.id
			WHERE ro.hostname = $1`, dbFleet.Subdomain)
		if len(liveRoutes) > 0 {
			liveState = map[string]interface{}{
				"found":       true,
				"route_count": len(liveRoutes),
			}
		}
	}

	gitNodeList := make([]map[string]interface{}, len(gitNodes))
	for i, n := range gitNodes {
		gitNodeList[i] = map[string]interface{}{
			"name":       n.Name,
			"index":      n.Index,
			"datacenter": n.Datacenter,
			"region":     n.Region,
			"status":     n.Status,
		}
	}

	// Also check which clusters have this fleet.
	clusters := k8sOrch.clusterNames()
	var presentIn []string
	for _, cn := range clusters {
		path := filepath.Join(k8sOrch.repo.RepoPath(), fleetManifestPathFor(cn, fleetID))
		if _, err := os.Stat(path); err == nil {
			presentIn = append(presentIn, cn)
		}
	}

	writeJSON(w, 200, map[string]interface{}{
		"fleet_id": fleetID,
		"git_state": map[string]interface{}{
			"gateway_type": gitGW,
			"replicas":     gitReplicas,
			"nodes":        gitNodeList,
			"clusters":     presentIn,
		},
		"db_state":   dbState,
		"live_state": liveState,
	})
}

// ---------------------------------------------------------------------------
// POST /fleets/{fleet_id}/gitops/sync
// Rebuilds the fleet GitOps manifest from the database (all fleet_nodes rows)
// and pushes it to the Git repo, then applies it directly to the cluster.
// Use this to repair a fleet whose manifest is out-of-sync with the DB, e.g.
// after manually adding nodes or when nodes of a different gateway type were
// missed during an earlier Deploy Node operation.
// ---------------------------------------------------------------------------

func syncFleetToGit(w http.ResponseWriter, r *http.Request) {
	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 400, map[string]string{"detail": "GitOps sync only available in K8s mode"})
		return
	}

	fleetID := chi.URLParam(r, "fleet_id")
	var fleet Fleet
	if err := db.Get(&fleet, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "fleet not found"})
		return
	}

	// Load all nodes from the database.
	var dbNodes []FleetNodeRecord
	db.Select(&dbNodes, "SELECT * FROM fleet_nodes WHERE fleet_id=$1 ORDER BY created_at", fleetID)

	if len(dbNodes) == 0 {
		writeJSON(w, 200, map[string]string{"detail": "no nodes in DB — nothing to sync"})
		return
	}

	specs := make([]gitopsNodeSpec, len(dbNodes))
	for i, n := range dbNodes {
		// Derive index from the node name (e.g. "fleet-jpmm-envoy-1" → 1)
		idx := i + 1
		parts := strings.Split(n.NodeName, "-")
		if len(parts) > 0 {
			if v, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				idx = v
			}
		}
		status := n.Status
		if status == "" {
			status = "running"
		}
		specs[i] = gitopsNodeSpec{
			Name:        n.NodeName,
			Index:       idx,
			GatewayType: n.GatewayType,
			Datacenter:  n.Datacenter,
			Region:      n.Datacenter,
			Status:      status,
		}
	}

	manifest := generateFleetCRD(fleetID, fleet.GatewayType, len(specs), specs)

	if err := k8sOrch.writeAndPushFleetManifest(fleetID, manifest,
		"Sync fleet "+fleetID+" manifest from DB"); err != nil {
		log.Printf("syncFleetToGit: push failed for %s: %v", fleetID, err)
		writeJSON(w, 500, map[string]string{"detail": "git push failed: " + err.Error()})
		return
	}

	if err := k8sOrch.applyToCluster(manifest); err != nil {
		log.Printf("syncFleetToGit: cluster apply warning for %s: %v", fleetID, err)
	}

	writeJSON(w, 200, map[string]interface{}{
		"detail":     "fleet manifest rebuilt from DB and pushed to git",
		"fleet_id":   fleetID,
		"node_count": len(specs),
	})
}

// ---------------------------------------------------------------------------
// POST /gitops/migrate-route-names
// One-shot migration: renames all UUID-based route YAML files in every fleet
// repo to human-readable path-based names.
// ---------------------------------------------------------------------------

func migrateRouteNames(w http.ResponseWriter, r *http.Request) {
	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 400, map[string]string{"detail": "only available in k8s mode"})
		return
	}

	renamed, errors := MigrateRouteFilenames(k8sOrch)

	writeJSON(w, 200, map[string]interface{}{
		"renamed": renamed,
		"errors":  errors,
		"count":   len(renamed),
	})
}

// ---------------------------------------------------------------------------
// POST /gitops/reconcile
// Triggers a manual Git→DB reconcile pass (Git is authoritative).
// ---------------------------------------------------------------------------

func triggerReconcile(w http.ResponseWriter, r *http.Request) {
	_, ok := orch.(*K8sOrchestrator)
	if !ok {
		writeJSON(w, 200, map[string]string{
			"status":  "skipped",
			"message": "not in k8s mode",
		})
		return
	}

	result := runGitOpsReconcile()

	// Update the shared last-result so GET /gitops/reconcile/status reflects it too.
	lastReconcileMu.Lock()
	lastReconcileResult = result
	lastReconcileMu.Unlock()

	writeJSON(w, 200, result)
}

// ---------------------------------------------------------------------------
// GET /gitops/reconcile/status
// Returns the result of the most recent reconcile pass.
// ---------------------------------------------------------------------------

func getReconcileStatus(w http.ResponseWriter, r *http.Request) {
	lastReconcileMu.RLock()
	result := lastReconcileResult
	lastReconcileMu.RUnlock()

	if result == nil {
		writeJSON(w, 200, map[string]interface{}{
			"status":  "never_run",
			"message": "no reconcile pass has run yet",
		})
		return
	}
	writeJSON(w, 200, result)
}

