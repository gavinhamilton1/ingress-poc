package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	appMiddleware "github.com/jpmc/ingress-poc/pkg/middleware"
	appOtel "github.com/jpmc/ingress-poc/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	db     *sqlx.DB
	tracer trace.Tracer

	envoyControlPlaneURL string
	kongAdminProxyURL    string
)

func main() {
	port := getEnvOr("PORT", "8003")
	envoyControlPlaneURL = getEnvOr("ENVOY_CONTROL_PLANE_URL", "http://envoy-control-plane:8080")
	kongAdminProxyURL = getEnvOr("KONG_ADMIN_PROXY_URL", "http://kong-admin-proxy:8102")

	tp, t := appOtel.InitOTEL("management-api")
	tracer = t
	defer tp.Shutdown(context.Background())

	db = initDB()
	seedDefaults(db)

	r := chi.NewRouter()
	r.Use(appMiddleware.CORS())

	// Routes
	r.Get("/routes", listRoutes)
	r.Get("/routes/{id}", getRoute)
	r.Post("/routes", createRoute)
	r.Put("/routes/{id}", updateRoute)
	r.Put("/routes/{id}/status", updateRouteStatus)
	r.Delete("/routes/{id}", deleteRoute)

	// Audit
	r.Get("/audit-log", listAuditLog)

	// Policy
	r.Get("/policy/validate", policyValidate)

	// Actuals & Drift
	r.Get("/actuals", listActuals)
	r.Get("/drift", listDrift)

	// Fleets
	r.Get("/fleets", listFleets)
	r.Get("/fleets/{fleet_id}", getFleet)
	r.Post("/fleets", createFleet)
	r.Delete("/fleets/{fleet_id}", deleteFleet)
	r.Post("/fleets/{fleet_id}/deploy", deployToFleet)
	r.Get("/fleets/{fleet_id}/nodes", getFleetNodes)
	r.Post("/fleets/{fleet_id}/scale", scaleFleet)
	r.Delete("/fleets/{fleet_id}/instances/{instance_id}", removeFleetInstance)
	r.Post("/fleets/{fleet_id}/suspend", handleSuspendFleet)
	r.Post("/fleets/{fleet_id}/resume", handleResumeFleet)
	r.Post("/fleets/{fleet_id}/nodes/{container_id}/stop", handleStopNode)
	r.Post("/fleets/{fleet_id}/nodes/{container_id}/start", handleStartNode)
	r.Delete("/fleets/{fleet_id}/nodes/{container_id}", handleDeleteNode)
	r.Post("/fleets/{fleet_id}/nodes/deploy", handleDeploySingleNode)
	r.Get("/fleets/{fleet_id}/nodes/{container_id}/routes", getNodeRoutes)

	// Route-node assignments
	r.Get("/routes/{id}/nodes", getRouteNodes)

	// Health Reports
	r.Post("/health-reports", receiveHealthReports)
	r.Get("/health-reports", listHealthReports)

	// Lambdas
	r.Get("/lambdas", listLambdas)

	// Health
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "service": "management-api"})
	})

	// Restore fleet and lambda containers from DB state
	go ensureFleetContainers(db)
	go restoreLambdaContainers()

	// Background tasks
	go detectDrift()
	go computeFleetStatus()

	log.Printf("management-api listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

// --- Route Handlers ---

func listRoutes(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	gatewayType := r.URL.Query().Get("gateway_type")
	nodeID := r.URL.Query().Get("node_id")
	fleetID := r.URL.Query().Get("fleet_id")
	unassigned := r.URL.Query().Get("unassigned")
	hostnameFilter := r.URL.Query().Get("hostname")

	// If node_id is specified, return only routes assigned to that specific node
	if nodeID != "" {
		query := `SELECT r.* FROM routes r
			INNER JOIN route_node_assignments rna ON rna.route_id = r.id
			WHERE rna.node_container_id = $1 AND rna.status = 'active'`
		args := []interface{}{nodeID}
		n := 1
		if status != "" {
			n++
			query += fmt.Sprintf(" AND r.status=$%d", n)
			args = append(args, status)
		}
		if gatewayType != "" {
			n++
			query += fmt.Sprintf(" AND r.gateway_type=$%d", n)
			args = append(args, gatewayType)
		}
		if fleetID != "" {
			n++
			query += fmt.Sprintf(" AND rna.fleet_id=$%d", n)
			args = append(args, fleetID)
		}
		query += " ORDER BY r.hostname, r.path"

		var routes []Route
		db.Select(&routes, query, args...)
		if routes == nil {
			routes = []Route{}
		}
		writeJSON(w, 200, routes)
		return
	}

	query := "SELECT * FROM routes WHERE 1=1"
	args := []interface{}{}
	n := 0
	if status != "" {
		n++
		query += fmt.Sprintf(" AND status=$%d", n)
		args = append(args, status)
	}
	if gatewayType != "" {
		n++
		query += fmt.Sprintf(" AND gateway_type=$%d", n)
		args = append(args, gatewayType)
	}
	if fleetID != "" {
		n++
		query += fmt.Sprintf(" AND hostname IN (SELECT subdomain FROM fleets WHERE id=$%d)", n)
		args = append(args, fleetID)
	}
	if unassigned == "true" {
		query += " AND id NOT IN (SELECT route_id FROM route_node_assignments WHERE status='active')"
	}
	if hostnameFilter != "" {
		n++
		query += fmt.Sprintf(" AND hostname=$%d", n)
		args = append(args, hostnameFilter)
	}
	query += " ORDER BY hostname, path"

	var routes []Route
	db.Select(&routes, query, args...)
	if routes == nil {
		routes = []Route{}
	}
	writeJSON(w, 200, routes)
}

func getRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var route Route
	err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Route not found"})
		return
	}
	writeJSON(w, 200, route)
}

func createRoute(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "route.create")
	defer span.End()
	_ = ctx

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	// Duplicate check
	hostname := strOr(body["hostname"], "*")
	path := strOr(body["path"], "")
	gwType := strOr(body["gateway_type"], "kong")
	var existingCount int
	db.Get(&existingCount, "SELECT COUNT(*) FROM routes WHERE hostname=$1 AND path=$2 AND gateway_type=$3 AND status='active'",
		hostname, path, gwType)
	if existingCount > 0 {
		writeJSON(w, 409, map[string]string{"detail": fmt.Sprintf("Route %s %s (%s) already exists", hostname, path, gwType)})
		return
	}

	now := float64(time.Now().Unix())
	id := uuid.New().String()

	roles, _ := json.Marshal(toStringSlice(body["allowed_roles"]))
	methods, _ := json.Marshal(toStringSliceOr(body["methods"], []string{"GET", "POST", "PUT", "DELETE"}))
	scopes, _ := json.Marshal(toStringSlice(body["authz_scopes"]))
	targetNodes, _ := json.Marshal(toStringSlice(body["target_nodes"]))

	functionCode := strOr(body["function_code"], "")
	functionLanguage := strOr(body["function_language"], "javascript")
	backendURL := strOr(body["backend_url"], "")
	lambdaContainerIDVal := ""
	lambdaPortVal := 0

	// If function_code is provided, spin up a lambda container
	if functionCode != "" {
		funcName := strOr(body["function_name"], strOr(body["path"], "func"))
		// Clean up function name for container naming
		funcName = strings.TrimPrefix(funcName, "/")
		if funcName == "" {
			funcName = "func"
		}

		networkName := getEnvOr("DOCKER_NETWORK", "")
		cid, port, err := createLambdaContainer(id, funcName, functionCode, networkName)
		if err != nil {
			log.Printf("Warning: failed to create lambda container for route %s: %v", id, err)
		} else {
			lambdaContainerIDVal = cid
			lambdaPortVal = port
			// Set backend_url to the Docker-internal DNS name
			containerName := lambdaContainerName(id, funcName)
			backendURL = fmt.Sprintf("http://%s:%d", containerName, lambdaInternalPort)
			log.Printf("Lambda container created for route %s: %s (host port %d)", id, containerName, port)
		}
	}

	db.MustExec(`INSERT INTO routes (id, path, hostname, backend_url, audience, allowed_roles, methods,
		status, team, created_by, gateway_type, health_path, authn_mechanism, auth_issuer, authz_scopes,
		tls_required, notes, target_nodes, function_code, function_language, lambda_container_id, lambda_port,
		created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		id,
		strOr(body["path"], ""),
		strOr(body["hostname"], "*"),
		backendURL,
		strOr(body["audience"], ""),
		string(roles), string(methods),
		strOr(body["status"], "active"),
		strOr(body["team"], "platform"),
		strOr(body["created_by"], "system"),
		strOr(body["gateway_type"], "kong"),
		strOr(body["health_path"], "/health"),
		strOr(body["authn_mechanism"], "bearer"),
		strOr(body["auth_issuer"], ""),
		string(scopes),
		true,
		strOr(body["notes"], ""),
		string(targetNodes),
		functionCode,
		functionLanguage,
		lambdaContainerIDVal,
		lambdaPortVal,
		now, now,
	)

	// Audit
	addAudit(id, "CREATE", strOr(body["created_by"], "system"),
		fmt.Sprintf("Created route %s → %s", strOr(body["path"], ""), backendURL))

	span.SetAttributes(attribute.String("route.id", id))
	span.SetAttributes(attribute.String("route.path", strOr(body["path"], "")))

	var route Route
	db.Get(&route, "SELECT * FROM routes WHERE id=$1", id)
	writeJSON(w, 201, route)
}

func updateRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var existing Route
	if err := db.Get(&existing, "SELECT * FROM routes WHERE id=$1", id); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Route not found"})
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	now := float64(time.Now().Unix())

	// Build SET clause dynamically
	sets := []string{"updated_at=$1"}
	args := []interface{}{now}
	n := 1
	fieldMap := map[string]string{
		"path": "path", "hostname": "hostname", "backend_url": "backend_url",
		"audience": "audience", "status": "status", "team": "team",
		"gateway_type": "gateway_type", "notes": "notes",
		"health_path": "health_path", "authn_mechanism": "authn_mechanism", "auth_issuer": "auth_issuer",
		"function_code": "function_code", "function_language": "function_language",
	}
	for jsonKey, col := range fieldMap {
		if v, ok := body[jsonKey]; ok {
			n++
			sets = append(sets, fmt.Sprintf("%s=$%d", col, n))
			args = append(args, v)
		}
	}
	// JSON fields
	for _, jsonKey := range []string{"allowed_roles", "methods", "authz_scopes", "target_nodes"} {
		if v, ok := body[jsonKey]; ok {
			n++
			j, _ := json.Marshal(v)
			col := jsonKey
			sets = append(sets, fmt.Sprintf("%s=$%d", col, n))
			args = append(args, string(j))
		}
	}

	n++
	args = append(args, id)
	query := fmt.Sprintf("UPDATE routes SET %s WHERE id=$%d", strings.Join(sets, ","), n)
	db.MustExec(query, args...)

	actor := strOr(body["actor"], "system")
	addAudit(id, "UPDATE", actor, fmt.Sprintf("Updated route %s", existing.Path))

	// If function_code was updated and there's an existing lambda container, redeploy it
	if newCode, ok := body["function_code"]; ok {
		var updated Route
		db.Get(&updated, "SELECT * FROM routes WHERE id=$1", id)
		codeStr, _ := newCode.(string)
		if codeStr != "" && updated.LambdaContainerID != "" {
			// Remove old container
			log.Printf("Redeploying lambda for route %s (container %s)", id, updated.LambdaContainerID[:12])
			removeLambdaContainer(updated.LambdaContainerID)

			// Create new container with updated code
			funcName := strings.TrimPrefix(updated.Path, "/")
			if funcName == "" {
				funcName = "lambda"
			}
			networkName := getEnvOr("DOCKER_NETWORK", "")
			newContainerID, newPort, err := createLambdaContainer(id, funcName, codeStr, networkName)
			if err != nil {
				log.Printf("Warning: failed to redeploy lambda for route %s: %v", id, err)
			} else {
				containerName := lambdaContainerName(id, funcName)
				backendURL := fmt.Sprintf("http://%s:8080", containerName)
				db.MustExec("UPDATE routes SET lambda_container_id=$1, lambda_port=$2, backend_url=$3, updated_at=$4 WHERE id=$5",
					newContainerID, newPort, backendURL, float64(time.Now().Unix()), id)
				log.Printf("Redeployed lambda for route %s: container=%s port=%d", id, newContainerID[:12], newPort)
			}
		} else if codeStr != "" && updated.LambdaContainerID == "" {
			// New lambda on an existing route that didn't have one before
			funcName := strings.TrimPrefix(updated.Path, "/")
			if funcName == "" {
				funcName = "lambda"
			}
			networkName := getEnvOr("DOCKER_NETWORK", "")
			newContainerID, newPort, err := createLambdaContainer(id, funcName, codeStr, networkName)
			if err != nil {
				log.Printf("Warning: failed to create lambda for route %s: %v", id, err)
			} else {
				containerName := lambdaContainerName(id, funcName)
				backendURL := fmt.Sprintf("http://%s:8080", containerName)
				db.MustExec("UPDATE routes SET lambda_container_id=$1, lambda_port=$2, backend_url=$3, function_language=$4, updated_at=$5 WHERE id=$6",
					newContainerID, newPort, backendURL, "javascript", float64(time.Now().Unix()), id)
				log.Printf("Created lambda for route %s: container=%s port=%d", id, newContainerID[:12], newPort)
			}
		}
	}

	var route Route
	db.Get(&route, "SELECT * FROM routes WHERE id=$1", id)
	writeJSON(w, 200, route)
}

func updateRouteStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var existing Route
	if err := db.Get(&existing, "SELECT * FROM routes WHERE id=$1", id); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Route not found"})
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	newStatus := strOr(body["status"], "")
	if newStatus != "active" && newStatus != "inactive" && newStatus != "pending" {
		writeJSON(w, 400, map[string]string{"detail": "Invalid status"})
		return
	}

	now := float64(time.Now().Unix())
	db.MustExec("UPDATE routes SET status=$1, updated_at=$2 WHERE id=$3", newStatus, now, id)

	actor := strOr(body["actor"], "system")
	addAudit(id, "STATUS_CHANGE", actor, fmt.Sprintf("Status changed: %s → %s for %s", existing.Status, newStatus, existing.Path))

	var route Route
	db.Get(&route, "SELECT * FROM routes WHERE id=$1", id)
	writeJSON(w, 200, route)
}

func deleteRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var existing Route
	found := db.Get(&existing, "SELECT * FROM routes WHERE id=$1", id) == nil

	if found {
		// Clean up lambda container if one exists
		if existing.LambdaContainerID != "" {
			if err := removeLambdaContainer(existing.LambdaContainerID); err != nil {
				log.Printf("Warning: failed to remove lambda container %s for route %s: %v",
					existing.LambdaContainerID, id, err)
			}
		}

		db.MustExec("DELETE FROM routes WHERE id=$1", id)
		db.MustExec("DELETE FROM actual_routes WHERE route_id=$1", id)
		// Also remove matching fleet instances (matched by hostname + path)
		db.MustExec("DELETE FROM fleet_instances WHERE context_path=$1 AND fleet_id IN (SELECT id FROM fleets WHERE subdomain=$2)",
			existing.Path, existing.Hostname)
		addAudit(id, "DELETE", "system", fmt.Sprintf("Deleted route %s → %s", existing.Path, existing.BackendURL))
	}

	// Always clean up assignments and fleet instances by route_id (even if route is already gone)
	db.MustExec("DELETE FROM route_node_assignments WHERE route_id=$1", id)
	db.MustExec("DELETE FROM fleet_instances WHERE route_id=$1", id)
	// Also try deleting fleet instance by its own ID (handles case where ID is fleet_instance.id not route.id)
	db.MustExec("DELETE FROM fleet_instances WHERE id=$1", id)

	writeJSON(w, 200, map[string]interface{}{"deleted": true, "id": id})
}

// --- Audit ---

func addAudit(routeID, action, actor, detail string) {
	db.MustExec(`INSERT INTO audit_log (id, route_id, action, actor, detail, ts) VALUES ($1,$2,$3,$4,$5,$6)`,
		uuid.New().String(), routeID, action, actor, detail, float64(time.Now().Unix()))
}

func listAuditLog(w http.ResponseWriter, r *http.Request) {
	var logs []AuditLog
	db.Select(&logs, "SELECT * FROM audit_log ORDER BY ts DESC LIMIT 100")
	if logs == nil {
		logs = []AuditLog{}
	}
	writeJSON(w, 200, logs)
}

// --- Policy Validate ---

func policyValidate(w http.ResponseWriter, r *http.Request) {
	violations := []string{}
	path := r.URL.Query().Get("path")
	backendURL := r.URL.Query().Get("backend_url")
	team := r.URL.Query().Get("team")

	if path == "" {
		violations = append(violations, "path is required")
	}
	if backendURL == "" {
		violations = append(violations, "backend_url is required")
	}
	if team == "" {
		violations = append(violations, "team is required")
	}

	writeJSON(w, 200, map[string]interface{}{"valid": len(violations) == 0, "violations": violations})
}

// --- Actuals & Drift ---

func listActuals(w http.ResponseWriter, r *http.Request) {
	var actuals []ActualRoute
	db.Select(&actuals, "SELECT * FROM actual_routes")
	if actuals == nil {
		actuals = []ActualRoute{}
	}
	writeJSON(w, 200, actuals)
}

func listDrift(w http.ResponseWriter, r *http.Request) {
	var routes []Route
	db.Select(&routes, "SELECT * FROM routes")

	var actuals []ActualRoute
	db.Select(&actuals, "SELECT * FROM actual_routes")

	actualMap := map[string]ActualRoute{}
	for _, a := range actuals {
		actualMap[a.RouteID] = a
	}

	result := []DriftReport{}
	for _, route := range routes {
		actual, exists := actualMap[route.ID]
		dr := DriftReport{
			RouteID:        route.ID,
			Path:           route.Path,
			DesiredStatus:  route.Status,
			GatewayType:    route.GatewayType,
			DesiredBackend: route.BackendURL,
		}
		if exists {
			dr.ActualStatus = actual.ActualStatus
			dr.ActualBackend = actual.ActualBackend
			dr.Drift = actual.Drift
			dr.DriftDetail = actual.DriftDetail
			dr.LastChecked = actual.LastChecked
		}
		result = append(result, dr)
	}
	writeJSON(w, 200, result)
}

// --- Fleets ---

func listFleets(w http.ResponseWriter, r *http.Request) {
	var fleets []Fleet
	db.Select(&fleets, "SELECT * FROM fleets ORDER BY lob, name")

	result := []FleetWithNodes{}
	for _, f := range fleets {
		var instances []FleetInstance
		db.Select(&instances, "SELECT * FROM fleet_instances WHERE fleet_id=$1 ORDER BY context_path", f.ID)
		if instances == nil {
			instances = []FleetInstance{}
		}
		// Enrich instances with route data (node assignments, function code, methods, audience)
		for i, inst := range instances {
			if inst.RouteID != "" {
				var nodeIDs []string
				db.Select(&nodeIDs, "SELECT node_container_id FROM route_node_assignments WHERE route_id=$1 AND status='active'", inst.RouteID)
				instances[i].AssignedNodeIDs = nodeIDs
				// Fetch route fields that fleet_instances doesn't have
				var route Route
				if db.Get(&route, "SELECT * FROM routes WHERE id=$1", inst.RouteID) == nil {
					instances[i].FunctionCode = route.FunctionCode
					instances[i].FunctionLanguage = route.FunctionLanguage
					instances[i].LambdaContainerID = route.LambdaContainerID
					instances[i].Audience = route.Audience
					instances[i].Methods = route.Methods
				}
			}
		}
		// Merge DB nodes with live Docker status
		var dbNodes []FleetNodeRecord
		db.Select(&dbNodes, "SELECT * FROM fleet_nodes WHERE fleet_id=$1 ORDER BY node_name", f.ID)
		liveNodes, _ := listFleetContainers(f.ID)
		liveMap := map[string]FleetNode{}
		for _, n := range liveNodes {
			liveMap[n.ContainerName] = n
		}
		nodes := []FleetNode{}
		for _, dn := range dbNodes {
			node := FleetNode{
				ContainerID:   dn.ContainerID,
				ContainerName: dn.NodeName,
				FleetID:       dn.FleetID,
				GatewayType:   dn.GatewayType,
				Port:          dn.Port,
				Datacenter:    dn.Datacenter,
				Region:        dn.Datacenter,
				Status:        dn.Status,
			}
			if live, ok := liveMap[dn.NodeName]; ok {
				node.ContainerID = live.ContainerID
				node.Status = live.Status
				node.Port = live.Port
			}
			nodes = append(nodes, node)
		}
		// Include any live containers not in DB
		for _, live := range liveNodes {
			found := false
			for _, dn := range dbNodes {
				if dn.NodeName == live.ContainerName {
					found = true
					break
				}
			}
			if !found {
				nodes = append(nodes, live)
			}
		}
		// For CP fleets, use cp_nodes
		if f.FleetType == "control" {
			var cpNodes []CpNode
			db.Select(&cpNodes, "SELECT * FROM cp_nodes WHERE fleet_id=$1 ORDER BY container_name", f.ID)
			nodes = []FleetNode{}
			for _, cn := range cpNodes {
				nodes = append(nodes, FleetNode{
					ContainerID: cn.ID, ContainerName: cn.ContainerName,
					FleetID: cn.FleetID, GatewayType: cn.GatewayType,
					Port: cn.Port, Status: "running", Datacenter: cn.Datacenter, Region: cn.Datacenter,
				})
			}
		}
		result = append(result, FleetWithNodes{Fleet: f, Instances: instances, Nodes: nodes})
	}
	writeJSON(w, 200, result)
}

func getFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}
	var instances []FleetInstance
	db.Select(&instances, "SELECT * FROM fleet_instances WHERE fleet_id=$1 ORDER BY context_path", f.ID)
	if instances == nil {
		instances = []FleetInstance{}
	}
	// Include live nodes with their gateway_type clearly set
	nodes, _ := listFleetContainers(fleetID)
	if nodes == nil {
		nodes = []FleetNode{}
	}
	writeJSON(w, 200, FleetWithNodes{Fleet: f, Instances: instances, Nodes: nodes})
}

func createFleet(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	now := float64(time.Now().Unix())
	id := uuid.New().String()
	regions, _ := json.Marshal([]string{"us-east-1", "us-east-2"})

	// Derive gateway_type from traffic_type if not explicitly set
	trafficType := strOr(body["traffic_type"], "web")
	gatewayType := strOr(body["gateway_type"], "envoy")
	if _, ok := body["gateway_type"]; !ok {
		if trafficType == "api" {
			gatewayType = "kong"
		} else {
			gatewayType = "envoy"
		}
	}

	// Marshal JSONB fields
	kongPlugins, _ := json.Marshal(toStringSlice(body["kong_plugins"]))
	defaultAuthzScopes, _ := json.Marshal(toStringSlice(body["default_authz_scopes"]))

	db.MustExec(`INSERT INTO fleets (id, name, subdomain, lob, host_env, gateway_type, region, regions,
		auth_provider, instances_count, status,
		description, traffic_type, tls_termination, http2_enabled, connection_limit,
		timeout_connect_ms, timeout_request_ms, rate_limit_rps, kong_plugins,
		health_check_path, health_check_interval_s, authn_mechanism, default_authz_scopes,
		tls_required, waf_profile, resource_profile,
		autoscale_enabled, autoscale_min, autoscale_max, autoscale_cpu_threshold,
		created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)`,
		id, strOr(body["name"], ""), strOr(body["subdomain"], ""),
		strOr(body["lob"], ""), strOr(body["host_env"], "psaas"),
		gatewayType, strOr(body["region"], "us-east"),
		string(regions), strOr(body["auth_provider"], ""), 4, "healthy",
		strOr(body["description"], ""),
		trafficType,
		strOr(body["tls_termination"], "edge"),
		boolOr(body, "http2_enabled", true),
		intOr(body, "connection_limit", 1024),
		intOr(body, "timeout_connect_ms", 5000),
		intOr(body, "timeout_request_ms", 30000),
		intOr(body, "rate_limit_rps", 0),
		string(kongPlugins),
		strOr(body["health_check_path"], "/health"),
		intOr(body, "health_check_interval_s", 10),
		strOr(body["authn_mechanism"], "bearer"),
		string(defaultAuthzScopes),
		strOr(body["tls_required"], "required"),
		strOr(body["waf_profile"], "standard"),
		strOr(body["resource_profile"], "medium"),
		boolOr(body, "autoscale_enabled", false),
		intOr(body, "autoscale_min", 2),
		intOr(body, "autoscale_max", 16),
		intOr(body, "autoscale_cpu_threshold", 70),
		now, now)

	var f Fleet
	db.Get(&f, "SELECT * FROM fleets WHERE id=$1", id)

	// Spin up gateway containers for this fleet
	containerCount := intOr(body, "container_count", 2)
	networkName := getEnvOr("DOCKER_NETWORK", "")
	nodes, err := createFleetContainers(id, gatewayType, containerCount, networkName)
	if err != nil {
		log.Printf("Warning: could not create fleet containers for %s: %v", id, err)
		// Fleet is created in DB even if containers fail — caller can retry via /scale
	} else {
		log.Printf("Created %d %s containers for fleet %s", len(nodes), gatewayType, id)
	}

	writeJSON(w, 201, map[string]interface{}{
		"fleet":     FleetWithInstances{Fleet: f, Instances: []FleetInstance{}},
		"nodes":     nodes,
		"container_count": len(nodes),
	})
}

func deployToFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	now := float64(time.Now().Unix())
	gwType := strOr(body["gateway_type"], "envoy")
	backend := strOr(body["backend"], "http://svc-web:8004")
	contextPath := strOr(body["context_path"], "/")

	// Duplicate check: prevent same hostname+path+gateway_type
	var existingCount int
	db.Get(&existingCount, "SELECT COUNT(*) FROM routes WHERE hostname=$1 AND path=$2 AND gateway_type=$3 AND status='active'",
		f.Subdomain, contextPath, gwType)
	if existingCount > 0 {
		writeJSON(w, 409, map[string]string{"detail": fmt.Sprintf("Route %s %s already exists on this fleet", f.Subdomain, contextPath)})
		return
	}

	instID := uuid.New().String()

	functionCode := strOr(body["function_code"], "")
	functionLanguage := strOr(body["function_language"], "javascript")
	lambdaContainerIDVal := ""
	lambdaPortVal := 0

	// Also create a route in the registry
	routeID := uuid.New().String()

	// If function_code is provided, spin up a lambda container
	if functionCode != "" {
		funcName := strings.TrimPrefix(contextPath, "/")
		if funcName == "" {
			funcName = "func"
		}

		networkName := getEnvOr("DOCKER_NETWORK", "")
		cid, port, err := createLambdaContainer(routeID, funcName, functionCode, networkName)
		if err != nil {
			log.Printf("Warning: failed to create lambda container for deploy %s: %v", routeID, err)
		} else {
			lambdaContainerIDVal = cid
			lambdaPortVal = port
			containerName := lambdaContainerName(routeID, funcName)
			backend = fmt.Sprintf("http://%s:%d", containerName, lambdaInternalPort)
			log.Printf("Lambda container created for fleet deploy %s: %s (host port %d)", routeID, containerName, port)
		}
	}

	// Resolve target_nodes: if empty, find all running nodes of the matching gateway type in the fleet
	requestedTargetNodes := toStringSlice(body["target_nodes"])
	if len(requestedTargetNodes) == 0 {
		// Auto-discover running nodes of the matching type in this fleet
		fleetNodes, err := listFleetContainers(fleetID)
		if err == nil {
			for _, n := range fleetNodes {
				if n.Status == "running" && n.GatewayType == gwType {
					requestedTargetNodes = append(requestedTargetNodes, n.ContainerID)
				}
			}
		}
	}

	db.MustExec(`INSERT INTO fleet_instances (id, fleet_id, context_path, backend, gateway_type, status, latency_p99, route_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		instID, fleetID, contextPath, backend, gwType, "active", 0, routeID, now)

	methods, _ := json.Marshal(toStringSliceOr(body["methods"], []string{"GET", "POST", "PUT", "DELETE"}))
	roles, _ := json.Marshal(toStringSlice(body["allowed_roles"]))
	scopes, _ := json.Marshal([]string{})
	targetNodes, _ := json.Marshal(requestedTargetNodes)

	db.MustExec(`INSERT INTO routes (id, path, hostname, backend_url, audience, allowed_roles, methods,
		status, team, created_by, gateway_type, health_path, authn_mechanism, auth_issuer, authz_scopes,
		tls_required, notes, target_nodes, function_code, function_language, lambda_container_id, lambda_port,
		created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		routeID, contextPath, f.Subdomain, backend,
		strOr(body["audience"], ""), string(roles), string(methods),
		"active", strOr(body["team"], f.LOB), "fleet-deploy", gwType,
		"/health", "bearer", f.AuthProvider, string(scopes), true,
		fmt.Sprintf("Deployed to fleet %s", f.Name), string(targetNodes),
		functionCode, functionLanguage, lambdaContainerIDVal, lambdaPortVal,
		now, now)

	// Create route_node_assignments for each target node
	assignedNodes := []string{}
	for _, nodeID := range requestedTargetNodes {
		db.MustExec(`INSERT INTO route_node_assignments (id, route_id, node_container_id, fleet_id, status, created_at)
			VALUES ($1, $2, $3, $4, 'active', $5)`,
			uuid.New().String(), routeID, nodeID, fleetID, now)
		assignedNodes = append(assignedNodes, nodeID)
	}

	addAudit(routeID, "CREATE", "fleet-deploy",
		fmt.Sprintf("Deployed %s to fleet %s (%s), assigned to %d nodes", contextPath, f.Name, f.Subdomain, len(assignedNodes)))

	resp := map[string]interface{}{
		"id": instID, "fleet_id": fleetID, "context_path": contextPath,
		"backend": backend, "status": "active", "route_id": routeID,
		"assigned_nodes": assignedNodes,
	}
	if lambdaContainerIDVal != "" {
		resp["lambda_container_id"] = lambdaContainerIDVal
		resp["lambda_port"] = lambdaPortVal
	}
	writeJSON(w, 201, resp)
}

func removeFleetInstance(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	instID := chi.URLParam(r, "instance_id")

	result, err := db.Exec("DELETE FROM fleet_instances WHERE id=$1 AND fleet_id=$2", instID, fleetID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"detail": "Database error"})
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeJSON(w, 404, map[string]string{"detail": "Instance not found"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"deleted": true, "id": instID})
}

func deleteFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	// Remove all Docker containers for this fleet (gateway instances)
	if err := removeFleetContainers(fleetID); err != nil {
		log.Printf("Warning: error removing fleet containers for %s: %v", fleetID, err)
	}

	// Remove lambda containers for routes on this fleet's subdomain
	var fleetRoutes []Route
	db.Select(&fleetRoutes, "SELECT * FROM routes WHERE hostname=$1", f.Subdomain)
	for _, route := range fleetRoutes {
		if route.LambdaContainerID != "" {
			if err := removeLambdaContainer(route.LambdaContainerID); err != nil {
				log.Printf("Warning: failed to remove lambda %s: %v", route.LambdaContainerID, err)
			}
		}
	}

	// Remove fleet instances, route assignments, routes, and fleet from DB
	db.MustExec("DELETE FROM route_node_assignments WHERE fleet_id=$1", fleetID)
	db.MustExec("DELETE FROM fleet_instances WHERE fleet_id=$1", fleetID)
	db.MustExec("DELETE FROM actual_routes WHERE route_id IN (SELECT id FROM routes WHERE hostname=$1)", f.Subdomain)
	db.MustExec("DELETE FROM routes WHERE hostname=$1", f.Subdomain)
	db.MustExec("DELETE FROM fleets WHERE id=$1", fleetID)

	addAudit(fleetID, "DELETE_FLEET", "system", fmt.Sprintf("Deleted fleet %s (%s) and %d routes", f.Name, f.Subdomain, len(fleetRoutes)))

	writeJSON(w, 200, map[string]interface{}{"deleted": true, "id": fleetID, "name": f.Name})
}

func getFleetNodes(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	// For control-plane fleets, return virtual nodes from cp_nodes table
	if f.FleetType == "control" {
		var cpNodes []CpNode
		db.Select(&cpNodes, "SELECT * FROM cp_nodes WHERE fleet_id=$1 ORDER BY container_name", fleetID)
		nodes := make([]FleetNode, 0, len(cpNodes))
		for _, cn := range cpNodes {
			nodes = append(nodes, FleetNode{
				ContainerID:   cn.ID,
				ContainerName: cn.ContainerName,
				FleetID:       cn.FleetID,
				GatewayType:   cn.GatewayType,
				Port:          cn.Port,
				Status:        "running",
				Datacenter:    cn.Datacenter,
				Region:        cn.Datacenter,
			})
		}
		writeJSON(w, 200, map[string]interface{}{
			"fleet_id":     fleetID,
			"fleet_name":   f.Name,
			"fleet_type":   f.FleetType,
			"gateway_type": f.GatewayType,
			"nodes":        nodes,
			"count":        len(nodes),
		})
		return
	}

	// Get desired nodes from DB
	var dbNodes []FleetNodeRecord
	db.Select(&dbNodes, "SELECT * FROM fleet_nodes WHERE fleet_id=$1 ORDER BY node_name", fleetID)

	// Get live Docker containers
	liveNodes, _ := listFleetContainers(fleetID)
	liveMap := map[string]FleetNode{}
	for _, n := range liveNodes {
		liveMap[n.ContainerName] = n
	}

	// Merge: DB nodes enriched with live status
	nodes := make([]FleetNode, 0, len(dbNodes))
	for _, dn := range dbNodes {
		node := FleetNode{
			ContainerID:   dn.ContainerID,
			ContainerName: dn.NodeName,
			FleetID:       dn.FleetID,
			GatewayType:   dn.GatewayType,
			Port:          dn.Port,
			Datacenter:    dn.Datacenter,
			Region:        dn.Datacenter,
			Status:        dn.Status, // default from DB: "running" or "stopped"
		}
		// Override with live Docker status if container exists
		if live, ok := liveMap[dn.NodeName]; ok {
			node.ContainerID = live.ContainerID
			node.Status = live.Status
			node.Port = live.Port
		}
		nodes = append(nodes, node)
	}

	// Also include any live containers not in DB (manually created)
	for _, live := range liveNodes {
		found := false
		for _, dn := range dbNodes {
			if dn.NodeName == live.ContainerName {
				found = true
				break
			}
		}
		if !found {
			nodes = append(nodes, live)
		}
	}

	writeJSON(w, 200, map[string]interface{}{
		"fleet_id":     fleetID,
		"fleet_name":   f.Name,
		"fleet_type":   f.FleetType,
		"gateway_type": f.GatewayType,
		"nodes":        nodes,
		"count":        len(nodes),
	})
}

func scaleFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	desiredCount := intOr(body, "count", 0)
	if desiredCount <= 0 {
		writeJSON(w, 400, map[string]string{"detail": "count must be a positive integer"})
		return
	}
	if desiredCount > 50 {
		writeJSON(w, 400, map[string]string{"detail": "count must not exceed 50"})
		return
	}

	// Accept gateway_type in request body — a fleet can have both envoy and kong nodes.
	// If not provided, fall back to the fleet's informational gateway_type for backward compat.
	gatewayType := strOr(body["gateway_type"], f.GatewayType)
	if gatewayType == "" || gatewayType == "mixed" {
		gatewayType = "envoy" // default to envoy if fleet has no type set
	}

	networkName := getEnvOr("DOCKER_NETWORK", "")

	// If a datacenter is specified, override the auto-assignment for new nodes
	dc := strOr(body["datacenter"], "")
	if dc != "" {
		overrideDatacenter = dc
	}

	nodes, err := scaleFleetContainers(fleetID, gatewayType, desiredCount, networkName)
	if err != nil {
		log.Printf("Error scaling fleet %s: %v", fleetID, err)
		writeJSON(w, 500, map[string]string{"detail": fmt.Sprintf("Failed to scale fleet: %v", err)})
		return
	}

	// Clear the override
	overrideDatacenter = ""

	// Update instances_count in the fleet record (total across all types)
	allNodes, _ := listFleetContainers(fleetID)
	now := float64(time.Now().Unix())
	db.MustExec("UPDATE fleets SET instances_count=$1, updated_at=$2 WHERE id=$3",
		float64(len(allNodes)), now, fleetID)

	writeJSON(w, 200, map[string]interface{}{
		"fleet_id":     fleetID,
		"fleet_name":   f.Name,
		"gateway_type": gatewayType,
		"nodes":        nodes,
		"count":        len(nodes),
	})
}

// --- Fleet Suspend / Resume ---

func handleSuspendFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	// 1. Stop all fleet gateway containers
	if err := stopFleetContainers(fleetID); err != nil {
		log.Printf("Error stopping fleet containers for %s: %v", f.Name, err)
	}

	// 2. Stop lambda containers for this fleet's routes
	stopLambdaContainersForFleet(db, fleetID)

	// 3. Set all routes for this fleet to inactive
	now := float64(time.Now().Unix())
	db.MustExec("UPDATE routes SET status='inactive', updated_at=$1 WHERE hostname=$2 AND status='active'", now, f.Subdomain)

	// 4. Set fleet status to suspended
	db.MustExec("UPDATE fleets SET status='suspended', updated_at=$1 WHERE id=$2", now, fleetID)

	// 5. Update fleet instances to suspended
	db.MustExec("UPDATE fleet_instances SET status='suspended' WHERE fleet_id=$1", fleetID)

	addAudit(fleetID, "FLEET_SUSPENDED", "system", fmt.Sprintf("Fleet %s (%s) suspended — containers stopped, routes deactivated", f.Name, f.Subdomain))

	log.Printf("Fleet %s suspended: containers stopped, %s routes deactivated", f.Name, f.Subdomain)
	writeJSON(w, 200, map[string]interface{}{
		"fleet_id": fleetID,
		"status":   "suspended",
		"message":  fmt.Sprintf("Fleet %s suspended. All containers stopped and routes deactivated.", f.Name),
	})
}

func handleResumeFleet(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	// 1. Start all fleet gateway containers
	if err := startFleetContainers(fleetID); err != nil {
		log.Printf("Error starting fleet containers for %s: %v", f.Name, err)
	}

	// 2. Start lambda containers for this fleet's routes
	startLambdaContainersForFleet(db, fleetID)

	// 3. Reactivate all routes for this fleet
	now := float64(time.Now().Unix())
	db.MustExec("UPDATE routes SET status='active', updated_at=$1 WHERE hostname=$2 AND status='inactive'", now, f.Subdomain)

	// 4. Set fleet status back to healthy
	db.MustExec("UPDATE fleets SET status='healthy', updated_at=$1 WHERE id=$2", now, fleetID)

	// 5. Update fleet instances to active
	db.MustExec("UPDATE fleet_instances SET status='active' WHERE fleet_id=$1", fleetID)

	addAudit(fleetID, "FLEET_RESUMED", "system", fmt.Sprintf("Fleet %s (%s) resumed — containers started, routes reactivated", f.Name, f.Subdomain))

	log.Printf("Fleet %s resumed: containers started, %s routes reactivated", f.Name, f.Subdomain)
	writeJSON(w, 200, map[string]interface{}{
		"fleet_id": fleetID,
		"status":   "healthy",
		"message":  fmt.Sprintf("Fleet %s resumed. All containers started and routes reactivated.", f.Name),
	})
}

// --- Individual Node Management ---

func handleStopNode(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "container_id")
	resp, err := dockerRequest("POST", "/v1.46/containers/"+containerID+"/stop?t=5", nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"detail": fmt.Sprintf("Failed to stop node: %v", err)})
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 304 {
		writeJSON(w, resp.StatusCode, map[string]string{"detail": fmt.Sprintf("Docker returned status %d", resp.StatusCode)})
		return
	}
	addAudit(containerID, "NODE_STOPPED", "system", fmt.Sprintf("Container %.12s stopped", containerID))
	writeJSON(w, 200, map[string]interface{}{"stopped": true, "container_id": containerID})
}

func handleStartNode(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "container_id")
	resp, err := dockerRequest("POST", "/v1.46/containers/"+containerID+"/start", nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"detail": fmt.Sprintf("Failed to start node: %v", err)})
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 304 {
		writeJSON(w, resp.StatusCode, map[string]string{"detail": fmt.Sprintf("Docker returned status %d", resp.StatusCode)})
		return
	}
	addAudit(containerID, "NODE_STARTED", "system", fmt.Sprintf("Container %.12s started", containerID))
	writeJSON(w, 200, map[string]interface{}{"started": true, "container_id": containerID})
}

func handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "container_id")
	// Stop first
	stopResp, _ := dockerRequest("POST", "/v1.46/containers/"+containerID+"/stop?t=5", nil)
	if stopResp != nil {
		stopResp.Body.Close()
	}
	// Remove
	rmResp, err := dockerRequest("DELETE", "/v1.46/containers/"+containerID+"?force=true&v=true", nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"detail": fmt.Sprintf("Failed to delete node: %v", err)})
		return
	}
	rmResp.Body.Close()

	// Remove route_node_assignments for this node
	db.MustExec("DELETE FROM route_node_assignments WHERE node_container_id=$1", containerID)

	addAudit(containerID, "NODE_DELETED", "system", fmt.Sprintf("Container %.12s deleted", containerID))
	writeJSON(w, 200, map[string]interface{}{"deleted": true, "container_id": containerID})
}

// --- Single Node Deploy ---

// handleDeploySingleNode deploys a single gateway node to a fleet.
// POST /fleets/{fleet_id}/nodes/deploy {"gateway_type": "envoy", "datacenter": "us-east-1"}
func handleDeploySingleNode(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	var f Fleet
	if err := db.Get(&f, "SELECT * FROM fleets WHERE id=$1", fleetID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Fleet not found"})
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	gatewayType := strOr(body["gateway_type"], "envoy")
	if gatewayType != "envoy" && gatewayType != "kong" {
		writeJSON(w, 400, map[string]string{"detail": "gateway_type must be 'envoy' or 'kong'"})
		return
	}

	dc := strOr(body["datacenter"], "")
	if dc != "" {
		overrideDatacenter = dc
	}
	customName := strOr(body["name"], "")
	if customName != "" {
		overrideContainerName = customName
	}

	networkName := getEnvOr("DOCKER_NETWORK", "")

	// Find existing containers to determine next index
	existing, _ := listFleetContainers(fleetID)
	// Filter to same gateway type to find the next index
	maxIndex := 0
	for _, n := range existing {
		if n.GatewayType == gatewayType && n.Index > maxIndex {
			maxIndex = n.Index
		}
	}
	startIndex := maxIndex + 1

	nodes, err := createFleetContainersStartingAt(fleetID, gatewayType, 1, startIndex, networkName)
	overrideDatacenter = ""
	overrideContainerName = ""

	if err != nil {
		log.Printf("Error deploying node to fleet %s: %v", fleetID, err)
		writeJSON(w, 500, map[string]string{"detail": fmt.Sprintf("Failed to deploy node: %v", err)})
		return
	}

	// Update instances_count
	allNodes, _ := listFleetContainers(fleetID)
	now := float64(time.Now().Unix())
	db.MustExec("UPDATE fleets SET instances_count=$1, updated_at=$2 WHERE id=$3",
		float64(len(allNodes)), now, fleetID)

	node := nodes[0]
	addAudit(fleetID, "NODE_DEPLOYED", "system",
		fmt.Sprintf("Deployed %s node to fleet %s (container: %s, datacenter: %s)",
			gatewayType, f.Name, node.ContainerName, node.Datacenter))

	writeJSON(w, 201, node)
}

// --- Route-Node Assignment Queries ---

// getRouteNodes returns which nodes a route is attached to.
// GET /routes/{id}/nodes
func getRouteNodes(w http.ResponseWriter, r *http.Request) {
	routeID := chi.URLParam(r, "id")
	var route Route
	if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err != nil {
		writeJSON(w, 404, map[string]string{"detail": "Route not found"})
		return
	}

	var assignments []RouteNodeAssignment
	db.Select(&assignments, "SELECT * FROM route_node_assignments WHERE route_id=$1 AND status='active' ORDER BY created_at", routeID)
	if assignments == nil {
		assignments = []RouteNodeAssignment{}
	}

	// Enrich with live node info where possible
	type enrichedAssignment struct {
		RouteNodeAssignment
		NodeName    string `json:"node_name,omitempty"`
		GatewayType string `json:"gateway_type,omitempty"`
		NodeStatus  string `json:"node_status,omitempty"`
		Datacenter  string `json:"datacenter,omitempty"`
	}

	enriched := make([]enrichedAssignment, 0, len(assignments))
	for _, a := range assignments {
		ea := enrichedAssignment{RouteNodeAssignment: a}
		// Try to look up live container info
		if a.FleetID != "" {
			nodes, _ := listFleetContainers(a.FleetID)
			for _, n := range nodes {
				if n.ContainerID == a.NodeContainerID {
					ea.NodeName = n.ContainerName
					ea.GatewayType = n.GatewayType
					ea.NodeStatus = n.Status
					ea.Datacenter = n.Datacenter
					break
				}
			}
		}
		enriched = append(enriched, ea)
	}

	writeJSON(w, 200, map[string]interface{}{
		"route_id":    routeID,
		"route_path":  route.Path,
		"hostname":    route.Hostname,
		"assignments": enriched,
		"count":       len(enriched),
	})
}

// getNodeRoutes returns routes deployed to a specific node.
// GET /fleets/{fleet_id}/nodes/{container_id}/routes
func getNodeRoutes(w http.ResponseWriter, r *http.Request) {
	fleetID := chi.URLParam(r, "fleet_id")
	containerID := chi.URLParam(r, "container_id")

	var routes []Route
	db.Select(&routes, `SELECT r.* FROM routes r
		INNER JOIN route_node_assignments rna ON rna.route_id = r.id
		WHERE rna.node_container_id = $1 AND rna.fleet_id = $2 AND rna.status = 'active'
		ORDER BY r.hostname, r.path`, containerID, fleetID)
	if routes == nil {
		routes = []Route{}
	}

	writeJSON(w, 200, map[string]interface{}{
		"fleet_id":     fleetID,
		"container_id": containerID,
		"routes":       routes,
		"count":        len(routes),
	})
}

// --- Health Reports ---

func receiveHealthReports(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reports []map[string]interface{} `json:"reports"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	now := float64(time.Now().Unix())
	for _, report := range body.Reports {
		gw := strOr(report["gateway_type"], "")
		cluster := strOr(report["cluster_name"], "")

		var existing HealthReport
		err := db.Get(&existing, "SELECT * FROM health_reports WHERE gateway_type=$1 AND cluster_name=$2", gw, cluster)
		if err == nil {
			db.MustExec(`UPDATE health_reports SET health_status=$1, latency_ms=$2, backend_host=$3,
				backend_port=$4, consecutive_failures=$5, last_check_time=$6, reporter=$7
				WHERE id=$8`,
				strOr(report["health_status"], "unknown"),
				floatOr(report["latency_ms"], 0),
				strOr(report["backend_host"], existing.BackendHost),
				floatOr(report["backend_port"], existing.BackendPort),
				floatOr(report["consecutive_failures"], 0),
				now, strOr(report["reporter"], existing.Reporter),
				existing.ID)
		} else {
			db.MustExec(`INSERT INTO health_reports (id, gateway_type, cluster_name, backend_host, backend_port,
				health_status, latency_ms, consecutive_failures, last_check_time, reporter)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				uuid.New().String(), gw, cluster,
				strOr(report["backend_host"], ""),
				floatOr(report["backend_port"], 0),
				strOr(report["health_status"], "unknown"),
				floatOr(report["latency_ms"], 0),
				floatOr(report["consecutive_failures"], 0),
				now, strOr(report["reporter"], ""))
		}
	}

	writeJSON(w, 200, map[string]interface{}{"accepted": len(body.Reports)})
}

func listHealthReports(w http.ResponseWriter, r *http.Request) {
	gatewayType := r.URL.Query().Get("gateway_type")
	var reports []HealthReport
	if gatewayType != "" {
		db.Select(&reports, "SELECT * FROM health_reports WHERE gateway_type=$1", gatewayType)
	} else {
		db.Select(&reports, "SELECT * FROM health_reports")
	}
	if reports == nil {
		reports = []HealthReport{}
	}
	writeJSON(w, 200, reports)
}

// --- Lambdas ---

func listLambdas(w http.ResponseWriter, r *http.Request) {
	containers, err := listLambdaContainers()
	if err != nil {
		log.Printf("Error listing lambda containers: %v", err)
		writeJSON(w, 500, map[string]string{"detail": "Failed to list lambda containers"})
		return
	}

	// Enrich with route info
	for i, c := range containers {
		routeID, _ := c["route_id"].(string)
		if routeID != "" {
			var route Route
			if err := db.Get(&route, "SELECT * FROM routes WHERE id=$1", routeID); err == nil {
				containers[i]["route_path"] = route.Path
				containers[i]["route_hostname"] = route.Hostname
				containers[i]["function_language"] = route.FunctionLanguage
			}
		}
	}

	writeJSON(w, 200, containers)
}

// restoreLambdaContainers checks all routes with lambda containers and re-creates
// any that are no longer running. Called on startup.
func restoreLambdaContainers() {
	// Wait for DB to be ready
	time.Sleep(2 * time.Second)

	var routes []Route
	if err := db.Select(&routes, "SELECT * FROM routes WHERE lambda_container_id != '' AND lambda_container_id IS NOT NULL"); err != nil {
		log.Printf("Warning: could not query lambda routes for restore: %v", err)
		return
	}

	if len(routes) == 0 {
		log.Println("No lambda containers to restore")
		return
	}

	networkName := getEnvOr("DOCKER_NETWORK", "")
	restored := 0

	for _, route := range routes {
		// Check if the container is still running
		resp, err := dockerRequest("GET", "/v1.43/containers/"+route.LambdaContainerID+"/json", nil)
		if err == nil {
			data, _ := dockerResponseBody(resp)
			if resp.StatusCode == 200 {
				var info struct {
					State struct {
						Running bool `json:"Running"`
					} `json:"State"`
				}
				json.Unmarshal(data, &info)
				if info.State.Running {
					log.Printf("Lambda container %.12s for route %s is still running", route.LambdaContainerID, route.ID)
					continue
				}
			}
		}

		// Container is not running — re-create it
		log.Printf("Restoring lambda container for route %s (old container: %.12s)", route.ID, route.LambdaContainerID)

		funcName := strings.TrimPrefix(route.Path, "/")
		if funcName == "" {
			funcName = "func"
		}

		cid, port, err := createLambdaContainer(route.ID, funcName, route.FunctionCode, networkName)
		if err != nil {
			log.Printf("Warning: failed to restore lambda container for route %s: %v", route.ID, err)
			continue
		}

		// Update the route record with the new container ID and port
		containerName := lambdaContainerName(route.ID, funcName)
		backendURL := fmt.Sprintf("http://%s:%d", containerName, lambdaInternalPort)
		now := float64(time.Now().Unix())
		db.MustExec("UPDATE routes SET lambda_container_id=$1, lambda_port=$2, backend_url=$3, updated_at=$4 WHERE id=$5",
			cid, port, backendURL, now, route.ID)
		restored++
		log.Printf("Restored lambda container %s for route %s (host port %d)", containerName, route.ID, port)
	}

	log.Printf("Lambda restore complete: %d/%d containers restored", restored, len(routes))
}

// --- Background Tasks ---

func detectDrift() {
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		func() {
			_, span := tracer.Start(context.Background(), "registry.drift_check")
			defer span.End()

			var routes []Route
			db.Select(&routes, "SELECT * FROM routes")

			envoyRoutes := map[string]map[string]interface{}{}
			kongRoutes := map[string]map[string]interface{}{}

			// Fetch Envoy snapshot
			if resp, err := client.Get(envoyControlPlaneURL + "/snapshot/routes"); err == nil {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				var items []map[string]interface{}
				json.Unmarshal(body, &items)
				for _, item := range items {
					if path, ok := item["path"].(string); ok {
						envoyRoutes[path] = item
					}
				}
			}

			// Fetch Kong sync status
			if resp, err := client.Get(kongAdminProxyURL + "/sync-status/routes"); err == nil {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				var items []map[string]interface{}
				json.Unmarshal(body, &items)
				for _, item := range items {
					if path, ok := item["path"].(string); ok {
						kongRoutes[path] = item
					}
				}
			}

			driftedCount := 0
			now := float64(time.Now().Unix())
			for _, route := range routes {
				var actual map[string]interface{}
				if route.GatewayType == "envoy" {
					actual = envoyRoutes[route.Path]
				} else {
					// Kong uses hostname:path compound keys
					hostname := route.Hostname
					if hostname == "" {
						hostname = "*"
					}
					actual = kongRoutes[hostname+":"+route.Path]
				}

				drift := false
				driftDetail := ""
				if actual == nil && route.Status == "active" {
					drift = true
					gw := "Envoy"
					if route.GatewayType == "kong" {
						gw = "Kong"
					}
					driftDetail = fmt.Sprintf("Route is active in Registry but absent from %s — gateway has not yet reconciled", gw)
				} else if actual != nil && route.Status == "inactive" {
					drift = true
					gw := "Envoy"
					if route.GatewayType == "kong" {
						gw = "Kong"
					}
					driftDetail = fmt.Sprintf("Route is inactive in Registry but still present in %s — gateway has not yet reconciled", gw)
				} else if actual != nil {
					actualBackend := ""
					if b, ok := actual["backend_url"].(string); ok {
						actualBackend = b
					} else if b, ok := actual["backend"].(string); ok {
						actualBackend = b
					}
					if actualBackend != "" && actualBackend != route.BackendURL {
						drift = true
						driftDetail = "Backend URL mismatch between Registry and gateway"
					} else {
						driftDetail = "In sync"
					}
				} else {
					driftDetail = "In sync"
				}

				if drift {
					driftedCount++
				}

				actualStatus := "absent"
				actualBackend := ""
				if actual != nil {
					actualStatus = "active"
					if b, ok := actual["backend_url"].(string); ok {
						actualBackend = b
					}
				}

				var existing ActualRoute
				err := db.Get(&existing, "SELECT * FROM actual_routes WHERE route_id=$1", route.ID)
				if err == nil {
					db.MustExec(`UPDATE actual_routes SET actual_status=$1, actual_backend=$2, drift=$3, drift_detail=$4, last_checked=$5 WHERE id=$6`,
						actualStatus, actualBackend, drift, driftDetail, now, existing.ID)
				} else {
					db.MustExec(`INSERT INTO actual_routes (id, route_id, gateway_type, path, actual_status, actual_backend, drift, drift_detail, last_checked)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
						uuid.New().String(), route.ID, route.GatewayType, route.Path,
						actualStatus, actualBackend, drift, driftDetail, now)
				}
			}

			span.SetAttributes(attribute.Int("routes.checked", len(routes)))
			span.SetAttributes(attribute.Int("routes.drifted", driftedCount))
		}()

		time.Sleep(3 * time.Second)
	}
}

func computeFleetStatus() {
	time.Sleep(8 * time.Second) // wait for health reports to start flowing
	for {
		func() {
			var reports []HealthReport
			db.Select(&reports, "SELECT * FROM health_reports")

			reportByBackend := map[string]HealthReport{}
			reportByPort := map[int]HealthReport{}
			for _, r := range reports {
				key := fmt.Sprintf("%s:%d", r.BackendHost, int(r.BackendPort))
				if existing, ok := reportByBackend[key]; !ok || r.LastCheckTime > existing.LastCheckTime {
					reportByBackend[key] = r
				}
				port := int(r.BackendPort)
				if existing, ok := reportByPort[port]; !ok || r.LastCheckTime > existing.LastCheckTime {
					reportByPort[port] = r
				}
			}

			// Build route status lookup
			var allRoutes []Route
			db.Select(&allRoutes, "SELECT * FROM routes")
			routeStatusMap := map[string]string{}
			for _, r := range allRoutes {
				key := r.Hostname + ":" + r.Path
				if r.Hostname == "" {
					key = "*:" + r.Path
				}
				routeStatusMap[key] = r.Status
			}

			var fleets []Fleet
			db.Select(&fleets, "SELECT * FROM fleets")

			now := float64(time.Now().Unix())
			for _, fleet := range fleets {
				// Skip suspended fleets — don't let health checks overwrite suspended status
				if fleet.Status == "suspended" {
					continue
				}
				// Control plane fleets are always healthy (managed by docker-compose)
				if fleet.FleetType == "control" {
					if fleet.Status != "healthy" {
						db.MustExec("UPDATE fleets SET status='healthy', updated_at=$1 WHERE id=$2", now, fleet.ID)
					}
					continue
				}

				// Check if fleet has any running nodes (Docker containers)
				nodes, _ := listFleetContainers(fleet.ID)
				hasRunningNodes := len(nodes) > 0

				// If fleet has no running nodes, mark as not_deployed
				if !hasRunningNodes {
					if fleet.Status != "not_deployed" {
						db.MustExec("UPDATE fleets SET status='not_deployed', updated_at=$1 WHERE id=$2", now, fleet.ID)
					}
					continue
				}

				var instances []FleetInstance
				db.Select(&instances, "SELECT * FROM fleet_instances WHERE fleet_id=$1", fleet.ID)

				statuses := []string{}
				for _, inst := range instances {
					// Check if route is suspended
					routeKey := fleet.Subdomain + ":" + inst.ContextPath
					if routeStatus, ok := routeStatusMap[routeKey]; ok && routeStatus == "inactive" {
						db.MustExec("UPDATE fleet_instances SET status=$1, latency_p99=$2 WHERE id=$3", "suspended", 0, inst.ID)
						statuses = append(statuses, "suspended")
						continue
					}

					host, port := parseBackend(inst.Backend)
					key := fmt.Sprintf("%s:%d", host, port)
					report, ok := reportByBackend[key]
					if !ok {
						report, ok = reportByPort[port]
					}

					if ok && (now-report.LastCheckTime) < 300 {
						if report.HealthStatus == "healthy" {
							db.MustExec("UPDATE fleet_instances SET status=$1, latency_p99=$2 WHERE id=$3", "active", report.LatencyMS, inst.ID)
							statuses = append(statuses, "active")
						} else {
							db.MustExec("UPDATE fleet_instances SET status=$1, latency_p99=$2 WHERE id=$3", "offline", 0, inst.ID)
							statuses = append(statuses, "offline")
						}
					} else if ok && (now-report.LastCheckTime) >= 300 {
						// Stale health report — check if fleet has running nodes
						// If nodes are running, assume healthy (health reporter might be lagging)
						if hasRunningNodes {
							db.MustExec("UPDATE fleet_instances SET status=$1, latency_p99=$2 WHERE id=$3", "active", 0, inst.ID)
							statuses = append(statuses, "active")
						} else {
							db.MustExec("UPDATE fleet_instances SET status=$1 WHERE id=$2", "warning", inst.ID)
							statuses = append(statuses, "warning")
						}
					} else {
						// No health report at all — if nodes running, assume active
						if hasRunningNodes {
							statuses = append(statuses, "active")
							if inst.Status != "active" {
								db.MustExec("UPDATE fleet_instances SET status='active' WHERE id=$1", inst.ID)
							}
						} else {
							statuses = append(statuses, inst.Status)
						}
					}
				}

				// Compute fleet-level status
				activeStatuses := []string{}
				for _, s := range statuses {
					if s != "suspended" {
						activeStatuses = append(activeStatuses, s)
					}
				}

				newStatus := "healthy"
				if len(activeStatuses) == 0 {
					newStatus = "offline"
				} else if allEqual(activeStatuses, "offline") {
					newStatus = "offline"
				} else if allEqual(activeStatuses, "active") {
					if hasSuspended(statuses) {
						newStatus = "degraded"
					} else {
						newStatus = "healthy"
					}
				} else {
					newStatus = "degraded"
				}

				db.MustExec("UPDATE fleets SET status=$1, updated_at=$2 WHERE id=$3", newStatus, now, fleet.ID)
			}
		}()

		time.Sleep(3 * time.Second)
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func strOr(v interface{}, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func boolOr(m map[string]interface{}, key string, def bool) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func intOr(m map[string]interface{}, key string, def int) int {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return def
}

func floatOr(v interface{}, def float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return def
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return []string{}
	}
	if arr, ok := v.([]interface{}); ok {
		result := make([]string, len(arr))
		for i, item := range arr {
			result[i], _ = item.(string)
		}
		return result
	}
	return []string{}
}

func toStringSliceOr(v interface{}, def []string) []string {
	result := toStringSlice(v)
	if len(result) == 0 {
		return def
	}
	return result
}

func parseBackend(backend string) (string, int) {
	// Parse "http://host:port" into (host, port)
	noScheme := backend
	if idx := strings.Index(backend, "://"); idx >= 0 {
		noScheme = backend[idx+3:]
	}
	parts := strings.SplitN(noScheme, ":", 2)
	host := parts[0]
	port := 80
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &port)
	}
	return host, port
}

func allEqual(ss []string, target string) bool {
	for _, s := range ss {
		if s != target {
			return false
		}
	}
	return len(ss) > 0
}

func hasSuspended(ss []string) bool {
	for _, s := range ss {
		if s == "suspended" {
			return true
		}
	}
	return false
}
