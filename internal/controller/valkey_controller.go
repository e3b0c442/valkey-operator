/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	valkeyv1alpha1 "github.com/e3b0c442/valkey-operator/api/v1alpha1"
)

const (
	valkeyImage    = "valkey/valkey:latest"
	valkeyPort     = int32(6379)
	valkeyReplicas = int32(1)
	valkeyAppName  = "valkey"
)

// Condition types
const (
	conditionTypeAvailable   = "Available"
	conditionTypeProgressing = "Progressing"
	conditionTypeDegraded    = "Degraded"
)

// State machine reasons for Progressing condition
const (
	reasonCreatingService     = "CreatingService"
	reasonCreatingStatefulSet = "CreatingStatefulSet"
	reasonWaitingForReady     = "WaitingForReady"
)

// Condition reasons
const (
	reasonAvailable = "Available"
)

// ValkeyReconciler reconciles a Valkey object
type ValkeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=valkey.e3b0c442.dev,resources=valkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=valkey.e3b0c442.dev,resources=valkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=valkey.e3b0c442.dev,resources=valkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *ValkeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Valkey instance
	valkey := &valkeyv1alpha1.Valkey{}
	if err := r.Get(ctx, req.NamespacedName, valkey); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Valkey")
		return ctrl.Result{}, err
	}

	// Check if already Available - if so, verify state and return early
	available := findCondition(valkey.Status.Conditions, conditionTypeAvailable)
	if available != nil && available.Status == metav1.ConditionTrue {
		// Resource is available, verify StatefulSet is still ready
		return r.reconcileWaitingForReady(ctx, valkey)
	}

	// Determine current state from Progressing condition
	currentState := r.getCurrentState(valkey)

	// Perform one state transition per reconcile loop
	switch currentState {
	case "":
		// No state set, start with CreatingService
		return r.reconcileCreatingService(ctx, valkey)
	case reasonCreatingService:
		return r.reconcileCreatingService(ctx, valkey)
	case reasonCreatingStatefulSet:
		return r.reconcileCreatingStatefulSet(ctx, valkey)
	case reasonWaitingForReady:
		return r.reconcileWaitingForReady(ctx, valkey)
	default:
		// Unknown state, reset to CreatingService
		log.Info("Unknown state, resetting to CreatingService", "state", currentState)
		return r.reconcileCreatingService(ctx, valkey)
	}
}

// getCurrentState returns the current state from the Progressing condition reason
func (r *ValkeyReconciler) getCurrentState(valkey *valkeyv1alpha1.Valkey) string {
	progressing := findCondition(valkey.Status.Conditions, conditionTypeProgressing)
	if progressing == nil || progressing.Status != metav1.ConditionTrue {
		return ""
	}
	return progressing.Reason
}

// reconcileCreatingService handles the CreatingService state
func (r *ValkeyReconciler) reconcileCreatingService(ctx context.Context, valkey *valkeyv1alpha1.Valkey) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	desiredService := r.buildService(valkey)
	service := &corev1.Service{}
	service.Name = desiredService.Name
	service.Namespace = desiredService.Namespace

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		// For headless services, the spec is mostly immutable, but we ensure labels and selectors match
		service.Labels = desiredService.Labels
		service.Spec = desiredService.Spec
		// Ensure owner reference is set for garbage collection
		if err := ctrl.SetControllerReference(valkey, service, r.Scheme); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Error(err, "Failed to create or update Service")
		r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionTrue, "ServiceCreationFailed", fmt.Sprintf("Failed to create Service: %v", err))
		if updateErr := r.Status().Update(ctx, valkey); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	if op == controllerutil.OperationResultCreated {
		log.Info("Created Service", "service", service.Name)
		r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonCreatingService, "Creating headless Service")
		// Clear Degraded condition if it was previously set due to a transient error
		r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionFalse, reasonAvailable, "Service creation succeeded")
		if err := r.Status().Update(ctx, valkey); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Service exists, transition to CreatingStatefulSet
	r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonCreatingStatefulSet, "Service exists, creating StatefulSet")
	// Clear Degraded condition if it was previously set due to a transient error
	r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionFalse, reasonAvailable, "Service operation succeeded")
	if err := r.Status().Update(ctx, valkey); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// reconcileCreatingStatefulSet handles the CreatingStatefulSet state
func (r *ValkeyReconciler) reconcileCreatingStatefulSet(ctx context.Context, valkey *valkeyv1alpha1.Valkey) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	desiredStatefulSet := r.buildStatefulSet(valkey)
	statefulSet := &appsv1.StatefulSet{}
	statefulSet.Name = desiredStatefulSet.Name
	statefulSet.Namespace = desiredStatefulSet.Namespace

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, statefulSet, func() error {
		// Update StatefulSet spec to match desired state
		statefulSet.Labels = desiredStatefulSet.Labels
		statefulSet.Spec = desiredStatefulSet.Spec
		// Ensure owner reference is set for garbage collection
		if err := ctrl.SetControllerReference(valkey, statefulSet, r.Scheme); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Error(err, "Failed to create or update StatefulSet")
		r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionTrue, "StatefulSetCreationFailed", fmt.Sprintf("Failed to create StatefulSet: %v", err))
		if updateErr := r.Status().Update(ctx, valkey); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	if op == controllerutil.OperationResultCreated {
		log.Info("Created StatefulSet", "statefulset", statefulSet.Name)
		r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonCreatingStatefulSet, "Creating StatefulSet")
		// Clear Degraded condition if it was previously set due to a transient error
		r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionFalse, reasonAvailable, "StatefulSet creation succeeded")
		if err := r.Status().Update(ctx, valkey); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// StatefulSet exists, transition to WaitingForReady
	r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonWaitingForReady, "StatefulSet exists, waiting for ready")
	// Clear Degraded condition if it was previously set due to a transient error
	r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionFalse, reasonAvailable, "StatefulSet operation succeeded")
	if err := r.Status().Update(ctx, valkey); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// reconcileWaitingForReady handles the WaitingForReady state
func (r *ValkeyReconciler) reconcileWaitingForReady(ctx context.Context, valkey *valkeyv1alpha1.Valkey) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	statefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: valkey.Name, Namespace: valkey.Namespace}, statefulSet); err != nil {
		if errors.IsNotFound(err) {
			// StatefulSet was deleted externally, reset state machine to recreate it
			log.Info("StatefulSet not found, resetting state machine to recreate", "valkey", valkey.Name)
			r.setCondition(valkey, conditionTypeAvailable, metav1.ConditionFalse, "StatefulSetNotFound", "StatefulSet was deleted, recreating")
			r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonCreatingStatefulSet, "StatefulSet was deleted, recreating")
			if updateErr := r.Status().Update(ctx, valkey); updateErr != nil {
				log.Error(updateErr, "Failed to update status")
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to get StatefulSet")
		return ctrl.Result{}, err
	}

	// Check if StatefulSet is ready
	if statefulSet.Status.Replicas == valkeyReplicas && statefulSet.Status.ReadyReplicas == valkeyReplicas {
		// StatefulSet is ready, set Available and clear Progressing
		r.setCondition(valkey, conditionTypeAvailable, metav1.ConditionTrue, "StatefulSetReady", "StatefulSet is ready")
		r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionFalse, reasonAvailable, "Valkey is available")
		r.setCondition(valkey, conditionTypeDegraded, metav1.ConditionFalse, reasonAvailable, "Valkey is available")
		if err := r.Status().Update(ctx, valkey); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		log.Info("Valkey is available", "valkey", valkey.Name)
		return ctrl.Result{}, nil
	}

	// StatefulSet exists but is not ready - set Available=False and Progressing=True
	r.setCondition(valkey, conditionTypeAvailable, metav1.ConditionFalse, "StatefulSetNotReady", fmt.Sprintf("StatefulSet is not ready (replicas: %d, ready: %d)", statefulSet.Status.Replicas, statefulSet.Status.ReadyReplicas))
	r.setCondition(valkey, conditionTypeProgressing, metav1.ConditionTrue, reasonWaitingForReady, fmt.Sprintf("Waiting for StatefulSet to be ready (replicas: %d, ready: %d)", statefulSet.Status.Replicas, statefulSet.Status.ReadyReplicas))
	if err := r.Status().Update(ctx, valkey); err != nil {
		log.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// buildService builds a headless Service for the Valkey instance
func (r *ValkeyReconciler) buildService(valkey *valkeyv1alpha1.Valkey) *corev1.Service {
	labels := map[string]string{
		"app.kubernetes.io/name":       valkeyAppName,
		"app.kubernetes.io/instance":   valkey.Name,
		"app.kubernetes.io/managed-by": "valkey-operator",
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkey.Name,
			Namespace: valkey.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{
				{
					Name:       "valkey",
					Port:       valkeyPort,
					TargetPort: intstr.FromInt32(valkeyPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/name":     valkeyAppName,
				"app.kubernetes.io/instance": valkey.Name,
			},
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(valkey, service, r.Scheme); err != nil {
		// This should not happen in normal operation
		panic(fmt.Sprintf("failed to set controller reference: %v", err))
	}
	return service
}

// buildStatefulSet builds a StatefulSet for the Valkey instance
func (r *ValkeyReconciler) buildStatefulSet(valkey *valkeyv1alpha1.Valkey) *appsv1.StatefulSet {
	labels := map[string]string{
		"app.kubernetes.io/name":       valkeyAppName,
		"app.kubernetes.io/instance":   valkey.Name,
		"app.kubernetes.io/managed-by": "valkey-operator",
	}

	replicas := valkeyReplicas
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkey.Name,
			Namespace: valkey.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: valkey.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":     valkeyAppName,
					"app.kubernetes.io/instance": valkey.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "valkey",
							Image: valkeyImage,
							Ports: []corev1.ContainerPort{
								{
									Name:          "valkey",
									ContainerPort: valkeyPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(valkey, statefulSet, r.Scheme); err != nil {
		// This should not happen in normal operation
		panic(fmt.Sprintf("failed to set controller reference: %v", err))
	}
	return statefulSet
}

// setCondition sets a condition on the Valkey status
// According to Kubernetes API conventions, LastTransitionTime should only be updated
// when the condition status or reason actually changes.
func (r *ValkeyReconciler) setCondition(valkey *valkeyv1alpha1.Valkey, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()

	// Find existing condition
	var existingCondition *metav1.Condition
	for i := range valkey.Status.Conditions {
		if valkey.Status.Conditions[i].Type == conditionType {
			existingCondition = &valkey.Status.Conditions[i]
			break
		}
	}

	// Determine if this is a transition (status or reason changed)
	lastTransitionTime := now
	if existingCondition != nil {
		// Only update LastTransitionTime if status or reason changed
		if existingCondition.Status == status && existingCondition.Reason == reason {
			// No transition - preserve existing LastTransitionTime
			lastTransitionTime = existingCondition.LastTransitionTime
		}
	}

	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: lastTransitionTime,
		ObservedGeneration: valkey.Generation,
	}

	// Update or add the condition
	if existingCondition != nil {
		// Update existing condition
		for i := range valkey.Status.Conditions {
			if valkey.Status.Conditions[i].Type == conditionType {
				valkey.Status.Conditions[i] = condition
				break
			}
		}
	} else {
		// Add new condition
		valkey.Status.Conditions = append(valkey.Status.Conditions, condition)
	}
}

// findCondition finds a condition in the slice by type
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&valkeyv1alpha1.Valkey{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Named("valkey").
		Complete(r)
}
