package controllers

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ingressv1alpha1 "github.com/jpmc/ingress-poc/cmd/ingress-operator/api/v1alpha1"
)

const (
	fleetFinalizer = "ingress.jpmc.com/fleet-finalizer"

	labelFleet   = "ingress.jpmc.com/fleet"
	labelGateway = "ingress.jpmc.com/gateway"
	labelManaged = "ingress.jpmc.com/managed"

	envoyImage = "envoyproxy/envoy:v1.30-latest"
	kongImage  = "kong:3.6"

	envoyAdminPort = 9901
	gatewayPort    = 8000
	kongAdminPort  = 8001
)

// FleetReconciler reconciles a Fleet object.
type FleetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=fleets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=fleets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ingress.jpmc.com,resources=fleets/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles create/update/delete of Fleet custom resources.
func (r *FleetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	fleet := &ingressv1alpha1.Fleet{}
	if err := r.Get(ctx, req.NamespacedName, fleet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Fleet resource not found; likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Fleet")
		return ctrl.Result{}, err
	}

	// Handle deletion via finalizer.
	if !fleet.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(fleet, fleetFinalizer) {
			logger.Info("Cleaning up fleet resources", "fleet", fleet.Name)
			// Owned resources (Deployment, Service, ConfigMap) are garbage-collected
			// via OwnerReferences, so no explicit cleanup is needed.
			controllerutil.RemoveFinalizer(fleet, fleetFinalizer)
			if err := r.Update(ctx, fleet); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present.
	if !controllerutil.ContainsFinalizer(fleet, fleetFinalizer) {
		controllerutil.AddFinalizer(fleet, fleetFinalizer)
		if err := r.Update(ctx, fleet); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set phase to Provisioning while we reconcile.
	fleet.Status.Phase = "Provisioning"
	if err := r.Status().Update(ctx, fleet); err != nil {
		logger.Error(err, "unable to update Fleet status to Provisioning")
	}

	// Ensure the Envoy bootstrap ConfigMap (only for envoy or mixed fleets).
	if fleet.Spec.GatewayType == "envoy" || fleet.Spec.GatewayType == "mixed" {
		if err := r.ensureEnvoyBootstrapConfigMap(ctx, fleet); err != nil {
			return ctrl.Result{}, r.setFailedStatus(ctx, fleet, "ConfigMap", err)
		}
	}

	// Ensure the Deployment.
	if err := r.ensureDeployment(ctx, fleet); err != nil {
		return ctrl.Result{}, r.setFailedStatus(ctx, fleet, "Deployment", err)
	}

	// Ensure the Service.
	if err := r.ensureService(ctx, fleet); err != nil {
		return ctrl.Result{}, r.setFailedStatus(ctx, fleet, "Service", err)
	}

	// Update status from Deployment.
	if err := r.updateFleetStatus(ctx, fleet); err != nil {
		logger.Error(err, "unable to update Fleet status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// setFailedStatus marks the Fleet as Failed and records a condition.
func (r *FleetReconciler) setFailedStatus(ctx context.Context, fleet *ingressv1alpha1.Fleet, resource string, err error) error {
	logger := log.FromContext(ctx)
	fleet.Status.Phase = "Failed"
	meta.SetStatusCondition(&fleet.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             fmt.Sprintf("%sReconcileFailed", resource),
		Message:            err.Error(),
		LastTransitionTime: metav1.Now(),
	})
	if statusErr := r.Status().Update(ctx, fleet); statusErr != nil {
		logger.Error(statusErr, "unable to update Fleet status after failure")
	}
	return err
}

// ensureDeployment creates or updates the Deployment for the fleet's gateway pods.
func (r *FleetReconciler) ensureDeployment(ctx context.Context, fleet *ingressv1alpha1.Fleet) error {
	deploy := &appsv1.Deployment{}
	deployName := types.NamespacedName{Name: fleet.Name, Namespace: fleet.Namespace}

	replicas := fleet.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	labels := map[string]string{
		labelFleet:   fleet.Name,
		labelGateway: fleet.Spec.GatewayType,
		labelManaged: "true",
	}

	desired := r.buildDeployment(fleet, labels, replicas)

	// Set owner reference so the Deployment is garbage-collected with the Fleet.
	if err := controllerutil.SetControllerReference(fleet, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference on Deployment: %w", err)
	}

	err := r.Get(ctx, deployName, deploy)
	if errors.IsNotFound(err) {
		log.FromContext(ctx).Info("Creating Deployment", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update existing Deployment.
	deploy.Spec = desired.Spec
	deploy.Labels = desired.Labels
	return r.Update(ctx, deploy)
}

// buildDeployment returns the desired Deployment object for the fleet.
func (r *FleetReconciler) buildDeployment(fleet *ingressv1alpha1.Fleet, labels map[string]string, replicas int32) *appsv1.Deployment {
	container := r.buildContainer(fleet)

	volumes := []corev1.Volume{}
	if fleet.Spec.GatewayType == "envoy" || fleet.Spec.GatewayType == "mixed" {
		volumes = append(volumes, corev1.Volume{
			Name: "envoy-bootstrap",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-envoy-bootstrap", fleet.Name),
					},
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "envoy-bootstrap",
			MountPath: "/etc/envoy",
			ReadOnly:  true,
		})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.Name,
			Namespace: fleet.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
					Volumes:    volumes,
				},
			},
		},
	}
}

// buildContainer returns the primary container spec for the gateway type.
func (r *FleetReconciler) buildContainer(fleet *ingressv1alpha1.Fleet) corev1.Container {
	resources := resourcesForProfile(fleet.Spec.ResourceProfile)

	switch fleet.Spec.GatewayType {
	case "kong":
		// Kong requires significantly more memory than Envoy
		kongResources := resources
		if fleet.Spec.ResourceProfile == "" || fleet.Spec.ResourceProfile == "small" || fleet.Spec.ResourceProfile == "medium" {
			kongResources = resourcesForProfile("large")
		}
		return corev1.Container{
			Name:  "kong",
			Image: kongImage,
			Ports: []corev1.ContainerPort{
				{Name: "proxy", ContainerPort: gatewayPort, Protocol: corev1.ProtocolTCP},
				{Name: "admin", ContainerPort: kongAdminPort, Protocol: corev1.ProtocolTCP},
			},
			Env: []corev1.EnvVar{
				{Name: "KONG_DATABASE", Value: "off"},
				{Name: "KONG_PROXY_LISTEN", Value: "0.0.0.0:8000"},
				{Name: "KONG_ADMIN_LISTEN", Value: "0.0.0.0:8001"},
			},
			Resources: kongResources,
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/status",
						Port: intstr.FromInt32(kongAdminPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/status",
						Port: intstr.FromInt32(kongAdminPort),
					},
				},
				InitialDelaySeconds: 15,
				PeriodSeconds:       20,
			},
		}

	default: // envoy or mixed (envoy is the default sidecar)
		return corev1.Container{
			Name:  "envoy",
			Image: envoyImage,
			Args:  []string{"-c", "/etc/envoy/envoy.yaml", "--service-cluster", fleet.Name},
			Ports: []corev1.ContainerPort{
				{Name: "proxy", ContainerPort: gatewayPort, Protocol: corev1.ProtocolTCP},
				{Name: "admin", ContainerPort: envoyAdminPort, Protocol: corev1.ProtocolTCP},
			},
			Resources: resources,
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/ready",
						Port: intstr.FromInt32(envoyAdminPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/server_info",
						Port: intstr.FromInt32(envoyAdminPort),
					},
				},
				InitialDelaySeconds: 15,
				PeriodSeconds:       20,
			},
		}
	}
}

// resourcesForProfile returns resource requirements based on the ResourceProfile string.
func resourcesForProfile(profile string) corev1.ResourceRequirements {
	switch profile {
	case "large":
		return corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("1Gi"),
				corev1.ResourceCPU:    resource.MustParse("1000m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("512Mi"),
				corev1.ResourceCPU:    resource.MustParse("500m"),
			},
		}
	case "medium":
		return corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("512Mi"),
				corev1.ResourceCPU:    resource.MustParse("500m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("256Mi"),
				corev1.ResourceCPU:    resource.MustParse("250m"),
			},
		}
	default: // small or unset
		return corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("256Mi"),
				corev1.ResourceCPU:    resource.MustParse("250m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
				corev1.ResourceCPU:    resource.MustParse("125m"),
			},
		}
	}
}

// ensureService creates or updates the ClusterIP Service for the fleet.
func (r *FleetReconciler) ensureService(ctx context.Context, fleet *ingressv1alpha1.Fleet) error {
	svc := &corev1.Service{}
	svcName := types.NamespacedName{Name: fleet.Name, Namespace: fleet.Namespace}

	labels := map[string]string{
		labelFleet:   fleet.Name,
		labelGateway: fleet.Spec.GatewayType,
		labelManaged: "true",
	}

	ports := []corev1.ServicePort{
		{
			Name:       "proxy",
			Port:       gatewayPort,
			TargetPort: intstr.FromInt32(gatewayPort),
			Protocol:   corev1.ProtocolTCP,
		},
	}

	switch fleet.Spec.GatewayType {
	case "kong":
		ports = append(ports, corev1.ServicePort{
			Name:       "admin",
			Port:       kongAdminPort,
			TargetPort: intstr.FromInt32(kongAdminPort),
			Protocol:   corev1.ProtocolTCP,
		})
	case "envoy", "mixed":
		ports = append(ports, corev1.ServicePort{
			Name:       "admin",
			Port:       envoyAdminPort,
			TargetPort: intstr.FromInt32(envoyAdminPort),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.Name,
			Namespace: fleet.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports:    ports,
		},
	}

	if err := controllerutil.SetControllerReference(fleet, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference on Service: %w", err)
	}

	err := r.Get(ctx, svcName, svc)
	if errors.IsNotFound(err) {
		log.FromContext(ctx).Info("Creating Service", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Preserve ClusterIP on updates.
	desired.Spec.ClusterIP = svc.Spec.ClusterIP
	svc.Spec = desired.Spec
	svc.Labels = desired.Labels
	return r.Update(ctx, svc)
}

// ensureEnvoyBootstrapConfigMap creates/updates a ConfigMap containing the Envoy bootstrap YAML.
// The bootstrap points Envoy at the envoy-control-plane xDS server in the same namespace.
func (r *FleetReconciler) ensureEnvoyBootstrapConfigMap(ctx context.Context, fleet *ingressv1alpha1.Fleet) error {
	cm := &corev1.ConfigMap{}
	cmName := types.NamespacedName{
		Name:      fmt.Sprintf("%s-envoy-bootstrap", fleet.Name),
		Namespace: fleet.Namespace,
	}

	xdsClusterName := "envoy-control-plane"
	// The xDS control plane runs in the ingress-cp namespace (control plane),
	// not in the fleet's namespace (ingress-dp / data plane).
	xdsNamespace := "ingress-cp"
	xdsAddress := fmt.Sprintf("%s.%s.svc.cluster.local", xdsClusterName, xdsNamespace)

	bootstrapYAML := fmt.Sprintf(`node:
  cluster: %s
  id: %s

dynamic_resources:
  lds_config:
    api_config_source:
      api_type: REST
      cluster_names: [xds_cluster]
      refresh_delay: 5s
      transport_api_version: V3
    resource_api_version: V3
  cds_config:
    api_config_source:
      api_type: REST
      cluster_names: [xds_cluster]
      refresh_delay: 5s
      transport_api_version: V3
    resource_api_version: V3

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
                      address: %s
                      port_value: 8080

admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 9901
`, fleet.Name, fleet.Name, xdsAddress)

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName.Name,
			Namespace: cmName.Namespace,
			Labels: map[string]string{
				labelFleet:   fleet.Name,
				labelManaged: "true",
			},
		},
		Data: map[string]string{
			"envoy.yaml": bootstrapYAML,
		},
	}

	if err := controllerutil.SetControllerReference(fleet, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference on ConfigMap: %w", err)
	}

	err := r.Get(ctx, cmName, cm)
	if errors.IsNotFound(err) {
		log.FromContext(ctx).Info("Creating Envoy bootstrap ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	cm.Data = desired.Data
	cm.Labels = desired.Labels
	return r.Update(ctx, cm)
}

// updateFleetStatus reads the Deployment status and updates the Fleet CR status accordingly.
func (r *FleetReconciler) updateFleetStatus(ctx context.Context, fleet *ingressv1alpha1.Fleet) error {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: fleet.Name, Namespace: fleet.Namespace}, deploy); err != nil {
		if errors.IsNotFound(err) {
			fleet.Status.Phase = "Pending"
			fleet.Status.ReadyReplicas = 0
			fleet.Status.DesiredReplicas = fleet.Spec.Replicas
			return r.Status().Update(ctx, fleet)
		}
		return err
	}

	fleet.Status.DesiredReplicas = *deploy.Spec.Replicas
	fleet.Status.ReadyReplicas = deploy.Status.ReadyReplicas

	// Build node status from pods.
	fleet.Status.AvailableNodes = nil
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(fleet.Namespace),
		client.MatchingLabels{labelFleet: fleet.Name, labelManaged: "true"},
	}
	if err := r.List(ctx, podList, listOpts...); err == nil {
		for _, pod := range podList.Items {
			ready := false
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			fleet.Status.AvailableNodes = append(fleet.Status.AvailableNodes, ingressv1alpha1.NodeStatus{
				Name:        pod.Name,
				Ready:       ready,
				GatewayType: fleet.Spec.GatewayType,
				Address:     pod.Status.PodIP,
			})
		}
	}

	// Determine phase.
	switch {
	case deploy.Status.ReadyReplicas == *deploy.Spec.Replicas && deploy.Status.ReadyReplicas > 0:
		fleet.Status.Phase = "Ready"
		meta.SetStatusCondition(&fleet.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "AllReplicasReady",
			Message:            fmt.Sprintf("%d/%d replicas ready", deploy.Status.ReadyReplicas, *deploy.Spec.Replicas),
			LastTransitionTime: metav1.Now(),
		})
	case deploy.Status.ReadyReplicas > 0:
		fleet.Status.Phase = "Degraded"
		meta.SetStatusCondition(&fleet.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "PartialReplicasReady",
			Message:            fmt.Sprintf("%d/%d replicas ready", deploy.Status.ReadyReplicas, *deploy.Spec.Replicas),
			LastTransitionTime: metav1.Now(),
		})
	default:
		fleet.Status.Phase = "Provisioning"
		meta.SetStatusCondition(&fleet.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "NoReplicasReady",
			Message:            "Waiting for replicas to become ready",
			LastTransitionTime: metav1.Now(),
		})
	}

	return r.Status().Update(ctx, fleet)
}

// SetupWithManager sets up the controller with the Manager.
func (r *FleetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ingressv1alpha1.Fleet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
