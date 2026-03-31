package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ingressv1alpha1 "github.com/jpmc/ingress-poc/cmd/ingress-operator/api/v1alpha1"
)

const (
	routeFinalizer = "ingress.jpmc.com/route-finalizer"
)

// routeEntry is the JSON representation of a route written into the aggregated ConfigMap.
type routeEntry struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	Hostname         string   `json:"hostname,omitempty"`
	BackendURL       string   `json:"backend_url"`
	Audience         string   `json:"audience,omitempty"`
	AllowedRoles     []string `json:"allowed_roles,omitempty"`
	Methods          []string `json:"methods,omitempty"`
	GatewayType      string   `json:"gateway_type,omitempty"`
	HealthPath       string   `json:"health_path,omitempty"`
	AuthnMechanism   string   `json:"authn_mechanism,omitempty"`
	AuthIssuer       string   `json:"auth_issuer,omitempty"`
	AuthzScopes      []string `json:"authz_scopes,omitempty"`
	TLSRequired      bool     `json:"tls_required,omitempty"`
	FunctionCode     string   `json:"function_code,omitempty"`
	FunctionLanguage string   `json:"function_language,omitempty"`
}

// RouteReconciler reconciles a Route object.
type RouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=routes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=routes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete of Route custom resources.
func (r *RouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	route := &ingressv1alpha1.Route{}
	if err := r.Get(ctx, req.NamespacedName, route); err != nil {
		if errors.IsNotFound(err) {
			// Route was deleted. We need to re-sync the ConfigMap for any fleet that
			// might have referenced it. We cannot know the fleet from the deleted object,
			// so we re-aggregate all routes for every fleet ConfigMap in the namespace.
			logger.Info("Route deleted, triggering full re-sync of route ConfigMaps")
			return ctrl.Result{}, r.resyncAllFleetRouteMaps(ctx, req.Namespace)
		}
		logger.Error(err, "unable to fetch Route")
		return ctrl.Result{}, err
	}

	// Handle deletion via finalizer.
	if !route.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(route, routeFinalizer) {
			logger.Info("Cleaning up route resources", "route", route.Name)
			if err := r.syncXdsConfig(ctx, route); err != nil {
				logger.Error(err, "failed to sync xDS config during cleanup")
			}
			controllerutil.RemoveFinalizer(route, routeFinalizer)
			if err := r.Update(ctx, route); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(route, routeFinalizer) {
		controllerutil.AddFinalizer(route, routeFinalizer)
		if err := r.Update(ctx, route); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate that the target fleet exists.
	if route.Spec.TargetFleet == "" {
		route.Status.Phase = "Failed"
		meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "NoTargetFleet",
			Message:            "spec.targetFleet is required",
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, r.Status().Update(ctx, route)
	}

	fleet := &ingressv1alpha1.Fleet{}
	fleetKey := types.NamespacedName{Name: route.Spec.TargetFleet, Namespace: route.Namespace}
	if err := r.Get(ctx, fleetKey, fleet); err != nil {
		if errors.IsNotFound(err) {
			route.Status.Phase = "Pending"
			meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{
				Type:               "Synced",
				Status:             metav1.ConditionFalse,
				Reason:             "FleetNotFound",
				Message:            fmt.Sprintf("target fleet %q not found", route.Spec.TargetFleet),
				LastTransitionTime: metav1.Now(),
			})
			return ctrl.Result{}, r.Status().Update(ctx, route)
		}
		return ctrl.Result{}, err
	}

	// Sync the route ConfigMap for the target fleet.
	if err := r.syncXdsConfig(ctx, route); err != nil {
		route.Status.Phase = "Failed"
		meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{
			Type:               "Synced",
			Status:             metav1.ConditionFalse,
			Reason:             "SyncFailed",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			logger.Error(statusErr, "unable to update Route status after sync failure")
		}
		return ctrl.Result{}, err
	}

	// Update status to Synced.
	route.Status.Phase = "Synced"
	route.Status.DeployedToNodes = route.Spec.TargetNodes
	route.Status.Drift = false
	route.Status.DriftDetail = ""
	now := metav1.Now()
	route.Status.LastChecked = &now
	meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{
		Type:               "Synced",
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigMapUpdated",
		Message:            fmt.Sprintf("Routes aggregated into routes-%s ConfigMap", route.Spec.TargetFleet),
		LastTransitionTime: metav1.Now(),
	})

	return ctrl.Result{}, r.Status().Update(ctx, route)
}

// syncXdsConfig lists all Route CRs targeting the same fleet and creates/updates
// an aggregated ConfigMap that the envoy-control-plane reads.
func (r *RouteReconciler) syncXdsConfig(ctx context.Context, route *ingressv1alpha1.Route) error {
	logger := log.FromContext(ctx)
	fleetName := route.Spec.TargetFleet

	// List all Routes in the namespace that target this fleet.
	routeList := &ingressv1alpha1.RouteList{}
	if err := r.List(ctx, routeList, client.InNamespace(route.Namespace)); err != nil {
		return fmt.Errorf("listing routes: %w", err)
	}

	var entries []routeEntry
	for i := range routeList.Items {
		rt := &routeList.Items[i]
		if rt.Spec.TargetFleet != fleetName {
			continue
		}
		// Skip routes being deleted.
		if !rt.DeletionTimestamp.IsZero() {
			continue
		}
		entries = append(entries, routeEntry{
			Name:             rt.Name,
			Path:             rt.Spec.Path,
			Hostname:         rt.Spec.Hostname,
			BackendURL:       rt.Spec.BackendURL,
			Audience:         rt.Spec.Audience,
			AllowedRoles:     rt.Spec.AllowedRoles,
			Methods:          rt.Spec.Methods,
			GatewayType:      rt.Spec.GatewayType,
			HealthPath:       rt.Spec.HealthPath,
			AuthnMechanism:   rt.Spec.AuthnMechanism,
			AuthIssuer:       rt.Spec.AuthIssuer,
			AuthzScopes:      rt.Spec.AuthzScopes,
			TLSRequired:      rt.Spec.TLSRequired,
			FunctionCode:     rt.Spec.FunctionCode,
			FunctionLanguage: rt.Spec.FunctionLanguage,
		})
	}

	routesJSON, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling routes JSON: %w", err)
	}

	cmName := types.NamespacedName{
		Name:      fmt.Sprintf("routes-%s", fleetName),
		Namespace: route.Namespace,
	}

	// Try to look up the Fleet to set owner reference.
	fleet := &ingressv1alpha1.Fleet{}
	fleetKey := types.NamespacedName{Name: fleetName, Namespace: route.Namespace}
	fleetFound := r.Get(ctx, fleetKey, fleet) == nil

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName.Name,
			Namespace: cmName.Namespace,
			Labels: map[string]string{
				labelFleet:   fleetName,
				labelManaged: "true",
				"ingress.jpmc.com/route-config": "true",
			},
		},
		Data: map[string]string{
			"routes.json": string(routesJSON),
		},
	}

	if fleetFound {
		if err := controllerutil.SetOwnerReference(fleet, desired, r.Scheme); err != nil {
			logger.Error(err, "unable to set owner reference on routes ConfigMap")
		}
	}

	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, cmName, existing)
	if errors.IsNotFound(err) {
		logger.Info("Creating routes ConfigMap", "name", cmName.Name, "routeCount", len(entries))
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Data = desired.Data
	existing.Labels = desired.Labels
	logger.Info("Updating routes ConfigMap", "name", cmName.Name, "routeCount", len(entries))
	return r.Update(ctx, existing)
}

// resyncAllFleetRouteMaps re-aggregates route ConfigMaps for every fleet in the namespace.
// This is called when a Route is deleted and we can no longer read its spec to know which fleet it targeted.
func (r *RouteReconciler) resyncAllFleetRouteMaps(ctx context.Context, namespace string) error {
	logger := log.FromContext(ctx)

	routeList := &ingressv1alpha1.RouteList{}
	if err := r.List(ctx, routeList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("listing routes for resync: %w", err)
	}

	// Group routes by target fleet.
	fleetRoutes := make(map[string][]routeEntry)
	for i := range routeList.Items {
		rt := &routeList.Items[i]
		if rt.DeletionTimestamp.IsZero() && rt.Spec.TargetFleet != "" {
			fleetRoutes[rt.Spec.TargetFleet] = append(fleetRoutes[rt.Spec.TargetFleet], routeEntry{
				Name:             rt.Name,
				Path:             rt.Spec.Path,
				Hostname:         rt.Spec.Hostname,
				BackendURL:       rt.Spec.BackendURL,
				Audience:         rt.Spec.Audience,
				AllowedRoles:     rt.Spec.AllowedRoles,
				Methods:          rt.Spec.Methods,
				GatewayType:      rt.Spec.GatewayType,
				HealthPath:       rt.Spec.HealthPath,
				AuthnMechanism:   rt.Spec.AuthnMechanism,
				AuthIssuer:       rt.Spec.AuthIssuer,
				AuthzScopes:      rt.Spec.AuthzScopes,
				TLSRequired:      rt.Spec.TLSRequired,
				FunctionCode:     rt.Spec.FunctionCode,
				FunctionLanguage: rt.Spec.FunctionLanguage,
			})
		}
	}

	// Also find route ConfigMaps that might now be empty (fleet has no routes).
	cmList := &corev1.ConfigMapList{}
	if err := r.List(ctx, cmList, client.InNamespace(namespace), client.MatchingLabels{
		labelManaged:                    "true",
		"ingress.jpmc.com/route-config": "true",
	}); err != nil {
		return fmt.Errorf("listing route configmaps: %w", err)
	}

	// Track which fleet ConfigMaps exist so we can update empty ones.
	for _, cm := range cmList.Items {
		fleetName := cm.Labels[labelFleet]
		if fleetName != "" {
			if _, found := fleetRoutes[fleetName]; !found {
				fleetRoutes[fleetName] = nil // empty list - will write []
			}
		}
	}

	// Write each fleet's aggregated routes.
	for fleetName, entries := range fleetRoutes {
		if entries == nil {
			entries = []routeEntry{}
		}
		routesJSON, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			logger.Error(err, "marshalling routes JSON during resync", "fleet", fleetName)
			continue
		}

		cmName := types.NamespacedName{
			Name:      fmt.Sprintf("routes-%s", fleetName),
			Namespace: namespace,
		}

		existing := &corev1.ConfigMap{}
		err = r.Get(ctx, cmName, existing)
		if errors.IsNotFound(err) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName.Name,
					Namespace: cmName.Namespace,
					Labels: map[string]string{
						labelFleet:                      fleetName,
						labelManaged:                    "true",
						"ingress.jpmc.com/route-config": "true",
					},
				},
				Data: map[string]string{
					"routes.json": string(routesJSON),
				},
			}
			if createErr := r.Create(ctx, cm); createErr != nil {
				logger.Error(createErr, "creating routes ConfigMap during resync", "fleet", fleetName)
			}
			continue
		}
		if err != nil {
			logger.Error(err, "getting routes ConfigMap during resync", "fleet", fleetName)
			continue
		}

		existing.Data["routes.json"] = string(routesJSON)
		if updateErr := r.Update(ctx, existing); updateErr != nil {
			logger.Error(updateErr, "updating routes ConfigMap during resync", "fleet", fleetName)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ingressv1alpha1.Route{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
