package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Git→DB Reconciler
//
// Treats Git as the authoritative source of truth for fleet nodes and routes.
// On each reconcile cycle, for every fleet that has a Git repo:
//   1. Pull latest from remote (best-effort)
//   2. Read the Fleet CRD YAML → reconcile fleet_nodes table
//   3. Read each Route CRD YAML → reconcile routes + fleet_instances tables
//
// DB rows that differ from Git are corrected.
// DB rows that don't exist in Git are flagged as "drifted" (not deleted —
// deletions are explicit operations).
// ---------------------------------------------------------------------------

// ReconcileChange records a single corrective action taken during reconciliation.
type ReconcileChange struct {
	Fleet   string `json:"fleet"`
	Kind    string `json:"kind"`   // "node" or "route"
	ID      string `json:"id"`     // node name or route ID
	Action  string `json:"action"` // "added", "updated", "flagged_drifted"
	Detail  string `json:"detail"`
}

// ReconcileResult holds the outcome of one full reconcile pass.
type ReconcileResult struct {
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Changes    []ReconcileChange `json:"changes"`
	Errors     []string          `json:"errors"`
}

// lastReconcileResult is updated after each reconcile pass; read by the HTTP handler.
var lastReconcileResult *ReconcileResult
var lastReconcileMu sync.RWMutex

// startGitOpsReconciler launches a background goroutine that reconciles Git→DB
// on the given interval.
func startGitOpsReconciler(interval time.Duration) {
	go func() {
		// Initial delay so the server is fully up before first pass.
		time.Sleep(15 * time.Second)
		log.Printf("gitops-reconciler: starting (interval %s)", interval)
		for {
			result := runGitOpsReconcile()
			lastReconcileMu.Lock()
			lastReconcileResult = result
			lastReconcileMu.Unlock()
			time.Sleep(interval)
		}
	}()
}

// runGitOpsReconcile performs one full reconcile pass and returns the result.
func runGitOpsReconcile() *ReconcileResult {
	result := &ReconcileResult{
		StartedAt: time.Now(),
		Changes:   []ReconcileChange{},
		Errors:    []string{},
	}

	k8sOrch, ok := orch.(*K8sOrchestrator)
	if !ok {
		result.FinishedAt = time.Now()
		return result
	}
	if db == nil {
		result.Errors = append(result.Errors, "db not initialized")
		result.FinishedAt = time.Now()
		return result
	}

	// Gather all fleets that have a Git backing.
	var fleets []Fleet
	if k8sOrch.fleetRepos != nil {
		// Per-fleet GitHub repo mode: reconcile all fleets that have a github repo URL.
		_ = db.Select(&fleets, "SELECT * FROM fleets WHERE git_manifest_path LIKE 'https://%'")
	} else {
		// Single-repo mode: reconcile all fleets.
		_ = db.Select(&fleets, "SELECT * FROM fleets WHERE fleet_type = 'data-plane' OR fleet_type = ''")
	}

	for _, fleet := range fleets {
		if err := reconcileFleet(k8sOrch, fleet, result); err != nil {
			msg := fmt.Sprintf("fleet %s (%s): %v", fleet.ID, fleet.Name, err)
			result.Errors = append(result.Errors, msg)
			log.Printf("gitops-reconciler: error reconciling %s: %v", fleet.ID, err)
		}
	}

	result.FinishedAt = time.Now()
	log.Printf("gitops-reconciler: pass complete — %d changes, %d errors (%.1fs)",
		len(result.Changes), len(result.Errors),
		result.FinishedAt.Sub(result.StartedAt).Seconds())
	return result
}

// reconcileFleet reconciles a single fleet's Git state against the DB.
func reconcileFleet(k *K8sOrchestrator, fleet Fleet, result *ReconcileResult) error {
	// Get the Git repo for this fleet.
	var repo *GitOpsRepo
	if k.fleetRepos != nil {
		var err error
		repo, err = k.fleetRepos.GetFleetRepo(fleet.ID, fleet.Name)
		if err != nil {
			return fmt.Errorf("get fleet repo: %w", err)
		}
	} else {
		repo = k.repo
	}

	// Pull latest (best-effort — local-only repos won't have a remote).
	if pullErr := repo.Pull(); pullErr != nil {
		log.Printf("gitops-reconciler: pull skipped for fleet %s: %v", fleet.ID, pullErr)
	}

	// --- Reconcile fleet nodes ---
	if err := reconcileFleetNodesFromGit(k, repo, fleet, result); err != nil {
		log.Printf("gitops-reconciler: node reconcile failed for %s: %v", fleet.ID, err)
		result.Errors = append(result.Errors, fmt.Sprintf("fleet %s nodes: %v", fleet.ID, err))
	}

	// --- Reconcile routes ---
	if err := reconcileFleetRoutesFromGit(repo, fleet, result); err != nil {
		log.Printf("gitops-reconciler: route reconcile failed for %s: %v", fleet.ID, err)
		result.Errors = append(result.Errors, fmt.Sprintf("fleet %s routes: %v", fleet.ID, err))
	}

	return nil
}

// ---------------------------------------------------------------------------
// Node reconciliation
// ---------------------------------------------------------------------------

// reconcileFleetNodesFromGit reads the fleet's Fleet CRD from git and makes
// DB fleet_nodes match.
func reconcileFleetNodesFromGit(k *K8sOrchestrator, repo *GitOpsRepo, fleet Fleet, result *ReconcileResult) error {
	// Determine manifest path.
	var manifestPath string
	if k.fleetRepos != nil {
		manifestPath = filepath.Join("fleets", fleet.ID+".yaml")
	} else {
		manifestPath = k.fleetManifestPath(fleet.ID)
	}

	data, err := repo.ReadManifest(manifestPath)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			// No manifest in git yet — nothing to reconcile.
			return nil
		}
		return fmt.Errorf("read fleet manifest: %w", err)
	}

	_, _, gitNodes := parseFleetManifest(data)
	if len(gitNodes) == 0 {
		return nil
	}

	// Load current DB nodes.
	var dbNodes []FleetNodeRecord
	_ = db.Select(&dbNodes, "SELECT * FROM fleet_nodes WHERE fleet_id=$1", fleet.ID)

	dbByName := make(map[string]FleetNodeRecord, len(dbNodes))
	for _, n := range dbNodes {
		dbByName[n.NodeName] = n
	}

	// For each node in git: ensure it exists in DB with correct fields.
	for _, gn := range gitNodes {
		// Resolve effective gateway type (fall back to fleet-level if unset).
		gwType := gn.GatewayType
		if gwType == "" {
			gwType = fleet.GatewayType
		}
		if gwType == "mixed" {
			gwType = "" // cannot infer for individual node
		}

		dbNode, exists := dbByName[gn.Name]
		if !exists {
			// Node is in git but not in DB → add it.
			dc := gn.Datacenter
			if dc == "" {
				dc = "dc1"
			}
			status := gn.Status
			if status == "" {
				status = "running"
			}
			_, insertErr := db.Exec(`
				INSERT INTO fleet_nodes (id, fleet_id, node_name, gateway_type, datacenter, status, port, container_id, created_at)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, 0, '', extract(epoch from now()))`,
				fleet.ID, gn.Name, gwType, dc, status,
			)
			if insertErr != nil {
				log.Printf("gitops-reconciler: insert node %s for fleet %s: %v", gn.Name, fleet.ID, insertErr)
				result.Errors = append(result.Errors, fmt.Sprintf("insert node %s: %v", gn.Name, insertErr))
				continue
			}
			result.Changes = append(result.Changes, ReconcileChange{
				Fleet:  fleet.Name,
				Kind:   "node",
				ID:     gn.Name,
				Action: "added",
				Detail: fmt.Sprintf("added node %s (%s) from git manifest", gn.Name, gwType),
			})
			log.Printf("gitops-reconciler: added missing node %s (%s) for fleet %s", gn.Name, gwType, fleet.ID)
		} else {
			// Node exists — check if any fields drift from git.
			var updates []string
			if gwType != "" && dbNode.GatewayType != gwType {
				updates = append(updates, fmt.Sprintf("gateway_type: %q → %q", dbNode.GatewayType, gwType))
				db.Exec("UPDATE fleet_nodes SET gateway_type=$1 WHERE id=$2", gwType, dbNode.ID)
			}
			dc := gn.Datacenter
			if dc != "" && dbNode.Datacenter != dc {
				updates = append(updates, fmt.Sprintf("datacenter: %q → %q", dbNode.Datacenter, dc))
				db.Exec("UPDATE fleet_nodes SET datacenter=$1 WHERE id=$2", dc, dbNode.ID)
			}
			if len(updates) > 0 {
				result.Changes = append(result.Changes, ReconcileChange{
					Fleet:  fleet.Name,
					Kind:   "node",
					ID:     gn.Name,
					Action: "updated",
					Detail: strings.Join(updates, "; "),
				})
				log.Printf("gitops-reconciler: updated node %s for fleet %s: %s", gn.Name, fleet.ID, strings.Join(updates, "; "))
			}
		}
	}

	// Identify DB nodes that are not in git → flag as drifted.
	gitNodeNames := make(map[string]bool, len(gitNodes))
	for _, gn := range gitNodes {
		gitNodeNames[gn.Name] = true
	}
	for _, dbNode := range dbNodes {
		if !gitNodeNames[dbNode.NodeName] && dbNode.Status != "drifted" {
			db.Exec("UPDATE fleet_nodes SET status='drifted' WHERE id=$1", dbNode.ID)
			result.Changes = append(result.Changes, ReconcileChange{
				Fleet:  fleet.Name,
				Kind:   "node",
				ID:     dbNode.NodeName,
				Action: "flagged_drifted",
				Detail: fmt.Sprintf("node %s exists in DB but not in git manifest", dbNode.NodeName),
			})
			log.Printf("gitops-reconciler: flagged DB-only node %s as drifted for fleet %s", dbNode.NodeName, fleet.ID)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Route reconciliation
// ---------------------------------------------------------------------------

// gitRouteCRD holds the parsed fields from a Route CRD YAML.
type gitRouteCRD struct {
	ID              string
	Path            string
	Hostname        string
	BackendURL      string
	GatewayType     string
	Audience        string
	Team            string
	AuthnMechanism  string
	AuthIssuer      string
	TLSRequired     bool
	TargetFleet     string
	HealthPath      string
	Notes           string
	FunctionCode    string
	FunctionLanguage string
	Methods         []string
}

// parseRouteCRD does a lightweight line-by-line parse of a Route CRD YAML.
// routeIDFallback is used only if metadata.name is not found in the YAML
// (e.g. for very old manifests where the filename was the ID).
func parseRouteCRD(data []byte, routeIDFallback string) gitRouteCRD {
	r := gitRouteCRD{ID: routeIDFallback}
	lines := strings.Split(string(data), "\n")
	inSpec := false
	inMetadata := false
	inMethods := false
	inFunctionCode := false
	functionCodeIndent := ""
	var functionCodeLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track metadata: section to pick up name: (the real route ID).
		if trimmed == "metadata:" {
			inMetadata = true
			inSpec = false
			continue
		}
		// Detect spec: section
		if trimmed == "spec:" {
			inSpec = true
			inMetadata = false
			inMethods = false
			continue
		}
		// Stop at status:
		if trimmed == "status:" {
			inSpec = false
			inMetadata = false
			inMethods = false
			inFunctionCode = false
			continue
		}

		// Parse metadata.name as the authoritative route ID.
		if inMetadata && strings.HasPrefix(trimmed, "name:") {
			r.ID = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "name:")))
			inMetadata = false
			continue
		}
		// Any top-level key (no leading whitespace) ends a section.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			if !strings.HasSuffix(trimmed, ":") && trimmed != "spec:" && trimmed != "metadata:" && trimmed != "status:" {
				inMetadata = false
			}
		}

		if !inSpec {
			continue
		}

		// Handle multi-line functionCode block scalar
		if inFunctionCode {
			// Detect end of literal block: a line with less indentation than the code block
			if line != "" && !strings.HasPrefix(line, functionCodeIndent) && !strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
				inFunctionCode = false
				r.FunctionCode = strings.Join(functionCodeLines, "\n")
				// Fall through to process this line normally
			} else {
				// Strip the leading indent and collect
				stripped := line
				if len(line) >= len(functionCodeIndent) {
					stripped = line[len(functionCodeIndent):]
				}
				functionCodeLines = append(functionCodeLines, stripped)
				continue
			}
		}

		// methods: list
		if inMethods {
			if strings.HasPrefix(trimmed, "- ") {
				r.Methods = append(r.Methods, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
				continue
			}
			inMethods = false
		}

		if trimmed == "methods:" {
			inMethods = true
			continue
		}

		// Literal block scalar for functionCode
		if strings.HasPrefix(trimmed, "functionCode: |") {
			// The code lines will have extra indentation relative to this line's indent+2
			// Compute the code block indent level
			baseIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			functionCodeIndent = strings.Repeat(" ", baseIndent+2)
			inFunctionCode = true
			functionCodeLines = []string{}
			continue
		}

		if strings.HasPrefix(trimmed, "path:") {
			r.Path = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "path:")))
		} else if strings.HasPrefix(trimmed, "hostname:") {
			r.Hostname = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "hostname:")))
		} else if strings.HasPrefix(trimmed, "backendUrl:") {
			r.BackendURL = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "backendUrl:")))
		} else if strings.HasPrefix(trimmed, "gatewayType:") {
			r.GatewayType = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "gatewayType:")))
		} else if strings.HasPrefix(trimmed, "audience:") {
			r.Audience = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "audience:")))
		} else if strings.HasPrefix(trimmed, "team:") {
			r.Team = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "team:")))
		} else if strings.HasPrefix(trimmed, "authnMechanism:") {
			r.AuthnMechanism = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "authnMechanism:")))
		} else if strings.HasPrefix(trimmed, "authIssuer:") {
			r.AuthIssuer = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "authIssuer:")))
		} else if strings.HasPrefix(trimmed, "tlsRequired:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "tlsRequired:"))
			r.TLSRequired = val == "true"
		} else if strings.HasPrefix(trimmed, "targetFleet:") {
			r.TargetFleet = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "targetFleet:")))
		} else if strings.HasPrefix(trimmed, "healthPath:") {
			r.HealthPath = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "healthPath:")))
		} else if strings.HasPrefix(trimmed, "notes:") {
			r.Notes = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "notes:")))
		} else if strings.HasPrefix(trimmed, "functionLanguage:") {
			r.FunctionLanguage = stripQuotes(strings.TrimSpace(strings.TrimPrefix(trimmed, "functionLanguage:")))
		}
	}

	// Flush functionCode if file ended inside the block
	if inFunctionCode && len(functionCodeLines) > 0 {
		r.FunctionCode = strings.Join(functionCodeLines, "\n")
	}

	return r
}

// reconcileFleetRoutesFromGit reads all Route CRDs from the git repo's routes/
// directory and reconciles the DB routes + fleet_instances tables.
func reconcileFleetRoutesFromGit(repo *GitOpsRepo, fleet Fleet, result *ReconcileResult) error {
	routeFiles, err := repo.ListManifests("routes")
	if err != nil {
		return fmt.Errorf("list route manifests: %w", err)
	}
	if len(routeFiles) == 0 {
		return nil
	}

	for _, fname := range routeFiles {
		data, readErr := repo.ReadManifest(filepath.Join("routes", fname))
		if readErr != nil {
			log.Printf("gitops-reconciler: read route %s: %v", fname, readErr)
			continue
		}

		// Pass the filename stem as a fallback ID; parseRouteCRD will prefer metadata.name.
		filenameStem := strings.TrimSuffix(strings.TrimSuffix(fname, ".yaml"), ".yml")
		gitRoute := parseRouteCRD(data, filenameStem)
		if gitRoute.Path == "" || gitRoute.ID == "" {
			log.Printf("gitops-reconciler: skipping malformed/empty route manifest %s", fname)
			continue
		}

		reconcileRouteFromGit(gitRoute, fleet, result)
	}

	return nil
}

// reconcileRouteFromGit ensures the DB route matches the git Route CRD.
func reconcileRouteFromGit(gr gitRouteCRD, fleet Fleet, result *ReconcileResult) {
	// Check if route exists in DB.
	var dbRoute Route
	err := db.Get(&dbRoute, "SELECT * FROM routes WHERE id=$1", gr.ID)
	if err != nil {
		// Route not in DB → insert it.
		hostname := gr.Hostname
		if hostname == "" {
			hostname = fleet.Subdomain
		}
		gwType := gr.GatewayType
		if gwType == "" {
			gwType = "envoy"
		}

		methodsJSON := `["GET","POST","PUT","DELETE"]`
		if len(gr.Methods) > 0 {
			quoted := make([]string, len(gr.Methods))
			for i, m := range gr.Methods {
				quoted[i] = fmt.Sprintf("%q", m)
			}
			methodsJSON = "[" + strings.Join(quoted, ",") + "]"
		}

		_, insertErr := db.Exec(`
			INSERT INTO routes (
				id, path, hostname, backend_url, gateway_type, audience, team,
				authn_mechanism, auth_issuer, tls_required, health_path, notes,
				function_code, function_language, methods, status, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb,
				'active', extract(epoch from now()), extract(epoch from now())
			)`,
			gr.ID, gr.Path, hostname, gr.BackendURL, gwType,
			gr.Audience, gr.Team, gr.AuthnMechanism, gr.AuthIssuer, gr.TLSRequired,
			gr.HealthPath, gr.Notes, gr.FunctionCode, gr.FunctionLanguage, methodsJSON,
		)
		if insertErr != nil {
			log.Printf("gitops-reconciler: insert route %s: %v", gr.ID, insertErr)
			result.Errors = append(result.Errors, fmt.Sprintf("insert route %s: %v", gr.ID, insertErr))
			return
		}

		// Also ensure fleet_instance link.
		ensureFleetInstance(gr.ID, fleet.ID, gr.Path, gr.BackendURL, gwType)

		result.Changes = append(result.Changes, ReconcileChange{
			Fleet:  fleet.Name,
			Kind:   "route",
			ID:     gr.ID,
			Action: "added",
			Detail: fmt.Sprintf("added route %s (%s → %s) from git", gr.ID, gr.Path, gr.BackendURL),
		})
		log.Printf("gitops-reconciler: added missing route %s (%s) for fleet %s", gr.ID, gr.Path, fleet.ID)
		return
	}

	// Route exists — check for field drift.
	var drifts []string

	if gr.Path != "" && dbRoute.Path != gr.Path {
		drifts = append(drifts, fmt.Sprintf("path: %q → %q", dbRoute.Path, gr.Path))
	}
	if gr.BackendURL != "" && dbRoute.BackendURL != gr.BackendURL {
		drifts = append(drifts, fmt.Sprintf("backend_url: %q → %q", dbRoute.BackendURL, gr.BackendURL))
	}
	if gr.GatewayType != "" && dbRoute.GatewayType != gr.GatewayType {
		drifts = append(drifts, fmt.Sprintf("gateway_type: %q → %q", dbRoute.GatewayType, gr.GatewayType))
	}
	if gr.Audience != "" && dbRoute.Audience != gr.Audience {
		drifts = append(drifts, fmt.Sprintf("audience: %q → %q", dbRoute.Audience, gr.Audience))
	}
	if gr.Team != "" && dbRoute.Team != gr.Team {
		drifts = append(drifts, fmt.Sprintf("team: %q → %q", dbRoute.Team, gr.Team))
	}
	if gr.HealthPath != "" && dbRoute.HealthPath != gr.HealthPath {
		drifts = append(drifts, fmt.Sprintf("health_path: %q → %q", dbRoute.HealthPath, gr.HealthPath))
	}
	if gr.TLSRequired != dbRoute.TLSRequired {
		drifts = append(drifts, fmt.Sprintf("tls_required: %v → %v", dbRoute.TLSRequired, gr.TLSRequired))
	}

	if len(drifts) > 0 {
		// Apply corrections.
		gwType := gr.GatewayType
		if gwType == "" {
			gwType = dbRoute.GatewayType
		}
		path := gr.Path
		if path == "" {
			path = dbRoute.Path
		}
		backendURL := gr.BackendURL
		if backendURL == "" {
			backendURL = dbRoute.BackendURL
		}

		_, updateErr := db.Exec(`
			UPDATE routes SET
				path=$1, backend_url=$2, gateway_type=$3, audience=$4, team=$5,
				tls_required=$6, health_path=$7, updated_at=extract(epoch from now()),
				sync_status='synced'
			WHERE id=$8`,
			path, backendURL, gwType,
			gr.Audience, gr.Team, gr.TLSRequired, gr.HealthPath,
			gr.ID,
		)
		if updateErr != nil {
			log.Printf("gitops-reconciler: update route %s: %v", gr.ID, updateErr)
			result.Errors = append(result.Errors, fmt.Sprintf("update route %s: %v", gr.ID, updateErr))
			return
		}
		result.Changes = append(result.Changes, ReconcileChange{
			Fleet:  fleet.Name,
			Kind:   "route",
			ID:     gr.ID,
			Action: "updated",
			Detail: strings.Join(drifts, "; "),
		})
		log.Printf("gitops-reconciler: corrected route %s drift: %s", gr.ID, strings.Join(drifts, "; "))
	}

	// Ensure the fleet_instance link exists.
	gwType := gr.GatewayType
	if gwType == "" {
		gwType = dbRoute.GatewayType
	}
	ensureFleetInstance(gr.ID, fleet.ID, dbRoute.Path, dbRoute.BackendURL, gwType)
}

// ---------------------------------------------------------------------------
// Route filename migration
// ---------------------------------------------------------------------------

// MigrateRouteFilenames renames all route YAML files in every fleet repo that
// still use UUID-based names (e.g. "3f8a1c2e-...yaml") to path-based names
// (e.g. "api-users-profile.yaml").  The function is idempotent — files already
// using the correct name are left untouched.
//
// Returns slices of renamed filenames and error strings.
func MigrateRouteFilenames(k *K8sOrchestrator) (renamed []string, errors []string) {
	if db == nil {
		return nil, []string{"db not initialized"}
	}

	if k.fleetRepos != nil {
		// Per-fleet GitHub repo mode — iterate all fleets with repos.
		var fleets []Fleet
		_ = db.Select(&fleets, "SELECT * FROM fleets WHERE git_manifest_path LIKE 'https://%'")

		for _, fleet := range fleets {
			repo, err := k.fleetRepos.GetFleetRepo(fleet.ID, fleet.Name)
			if err != nil {
				errors = append(errors, fmt.Sprintf("fleet %s: get repo: %v", fleet.Name, err))
				continue
			}
			r, e := migrateRouteFilenamesInRepo(repo, "routes", fleet.Name)
			renamed = append(renamed, r...)
			errors = append(errors, e...)
		}
	} else {
		// Single-repo mode — iterate per-cluster route directories.
		for _, cluster := range k.clusterNames() {
			dir := routeDirFor(cluster)
			r, e := migrateRouteFilenamesInRepo(k.repo, dir, cluster)
			renamed = append(renamed, r...)
			errors = append(errors, e...)
		}
	}
	return renamed, errors
}

// migrateRouteFilenamesInRepo renames all route files in routesDir inside repo
// from UUID/ID-based names to path-based names derived from the YAML content.
func migrateRouteFilenamesInRepo(repo *GitOpsRepo, routesDir, label string) (renamed []string, errors []string) {
	files, err := repo.ListManifests(routesDir)
	if err != nil {
		if !os.IsNotExist(err) {
			errors = append(errors, fmt.Sprintf("%s: list manifests: %v", label, err))
		}
		return
	}

	var toCommit []string

	for _, fname := range files {
		data, readErr := repo.ReadManifest(filepath.Join(routesDir, fname))
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: read: %v", label, fname, readErr))
			continue
		}

		// Parse just enough to get the path and derive the desired filename.
		stem := strings.TrimSuffix(strings.TrimSuffix(fname, ".yaml"), ".yml")
		gr := parseRouteCRD(data, stem)
		if gr.Path == "" {
			continue // malformed — skip
		}

		// Build a synthetic Route to reuse routeFilename.
		desiredName := routeFilename(Route{Path: gr.Path, ID: gr.ID})

		if fname == desiredName {
			continue // already correct
		}

		// Write under the new name, then delete the old file.
		newPath := filepath.Join(routesDir, desiredName)
		if writeErr := repo.WriteManifest(newPath, data); writeErr != nil {
			errors = append(errors, fmt.Sprintf("%s/%s → %s: write: %v", label, fname, desiredName, writeErr))
			continue
		}
		if delErr := repo.DeleteManifest(filepath.Join(routesDir, fname)); delErr != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: delete old: %v", label, fname, delErr))
		}

		toCommit = append(toCommit, newPath)
		renamed = append(renamed, fmt.Sprintf("%s: %s → %s", label, fname, desiredName))
		log.Printf("gitops-migrate: %s/%s → %s/%s", routesDir, fname, routesDir, desiredName)
	}

	if len(toCommit) > 0 {
		msg := fmt.Sprintf("Rename %d route manifest(s) to path-based names", len(toCommit))
		if err := repo.CommitAndPush(msg); err != nil {
			errors = append(errors, fmt.Sprintf("%s: commit renames: %v", label, err))
		}
	}
	return
}

// ensureFleetInstance ensures a fleet_instances row links the route to the fleet.
func ensureFleetInstance(routeID, fleetID, contextPath, backend, gwType string) {
	var count int
	_ = db.Get(&count, "SELECT COUNT(*) FROM fleet_instances WHERE route_id=$1 AND fleet_id=$2", routeID, fleetID)
	if count > 0 {
		return
	}
	_, err := db.Exec(`
		INSERT INTO fleet_instances (id, fleet_id, context_path, backend, gateway_type, status, route_id, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 'active', $5, extract(epoch from now()))`,
		fleetID, contextPath, backend, gwType, routeID,
	)
	if err != nil {
		log.Printf("gitops-reconciler: ensure fleet_instance for route %s fleet %s: %v", routeID, fleetID, err)
	}
}
