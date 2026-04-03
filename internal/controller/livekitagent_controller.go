/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// LivekitAgentReconciler reconciles a LivekitAgent object.
// It watches the referenced Kubernetes workload (Deployment or StatefulSet)
// and reflects its health status on the LivekitAgent resource.
type LivekitAgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=livekitagents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=livekitagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=livekitagents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *LivekitAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	agent := &sipv1alpha1.LivekitAgent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		if errors.IsNotFound(err) {
			log.Info("LivekitAgent resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching LivekitAgent: %w", err)
	}

	// ── 2. Handle deletion ──────────────────────────────────────────────
	if !agent.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// ── 3. Resolve workload health ──────────────────────────────────────
	agent.Status.ObservedGeneration = agent.Generation

	if agent.Spec.DeploymentRef == nil {
		// No deployment reference — agent is always considered ready
		agent.Status.Ready = true
		agent.Status.Replicas = 0
		agent.Status.AvailableReplicas = 0
		now := metav1.Now()
		agent.Status.LastSyncedAt = &now
		r.setReadyCondition(agent, true, "Ready", fmt.Sprintf("Agent %s registered (no deployment tracking)", agent.Spec.AgentName))
	} else {
		replicas, available, err := r.getDeploymentStatus(ctx, agent)
		if err != nil {
			r.setReadyCondition(agent, false, "DeploymentError", err.Error())
			if statusErr := r.Status().Update(ctx, agent); statusErr != nil {
				log.Error(statusErr, "failed to update status after workload error")
			}
			return ctrl.Result{RequeueAfter: requeueOnError}, nil
		}

		agent.Status.Replicas = replicas
		agent.Status.AvailableReplicas = available

		if replicas > 0 && available == replicas {
			agent.Status.Ready = true
			now := metav1.Now()
			agent.Status.LastSyncedAt = &now
			r.setReadyCondition(agent, true, "Ready", fmt.Sprintf("Agent %s healthy (%d/%d replicas)", agent.Spec.AgentName, available, replicas))
		} else if available > 0 {
			agent.Status.Ready = false
			r.setReadyCondition(agent, false, "Degraded", fmt.Sprintf("Agent %s degraded (%d/%d replicas)", agent.Spec.AgentName, available, replicas))
		} else {
			agent.Status.Ready = false
			r.setReadyCondition(agent, false, "Unavailable", fmt.Sprintf("Agent %s unavailable (0/%d replicas)", agent.Spec.AgentName, replicas))
		}
	}

	if err := r.Status().Update(ctx, agent); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating LivekitAgent status: %w", err)
	}

	return ctrl.Result{}, nil
}

// ─── Deployment status resolution ───────────────────────────────────────────

// getDeploymentStatus reads the replica counts from the referenced Deployment.
func (r *LivekitAgentReconciler) getDeploymentStatus(
	ctx context.Context,
	agent *sipv1alpha1.LivekitAgent,
) (replicas int32, available int32, err error) {
	ref := agent.Spec.DeploymentRef
	key := types.NamespacedName{
		Namespace: agent.Namespace,
		Name:      ref.Name,
	}

	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, key, dep); err != nil {
		if errors.IsNotFound(err) {
			return 0, 0, fmt.Errorf("Deployment %q not found", ref.Name)
		}
		return 0, 0, fmt.Errorf("fetching Deployment %q: %w", ref.Name, err)
	}

	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	return desired, dep.Status.AvailableReplicas, nil
}

// ─── Condition helpers ─────────────────────────────────────────────────────

func (r *LivekitAgentReconciler) setCondition(
	agent *sipv1alpha1.LivekitAgent,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: agent.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *LivekitAgentReconciler) setReadyCondition(
	agent *sipv1alpha1.LivekitAgent,
	ready bool,
	reason, message string,
) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	r.setCondition(agent, conditionReady, status, reason, message)
}

// ─── Controller setup ──────────────────────────────────────────────────────

// SetupWithManager sets up the controller with the Manager.
// It watches LivekitAgent resources, Deployments, and StatefulSets
// (enqueueing LivekitAgents that reference changed workloads).
func (r *LivekitAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.LivekitAgent{}).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.findAgentsForDeployment),
		).
		Named("livekitagent").
		Complete(r)
}

// findAgentsForDeployment returns reconcile requests for all LivekitAgents
// that reference the given Deployment.
func (r *LivekitAgentReconciler) findAgentsForDeployment(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	agentList := &sipv1alpha1.LivekitAgentList{}
	if err := r.List(ctx, agentList, client.InNamespace(obj.GetNamespace())); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list LivekitAgents for Deployment watch")
		return nil
	}

	var requests []reconcile.Request
	for _, agent := range agentList.Items {
		if agent.Spec.DeploymentRef != nil &&
			agent.Spec.DeploymentRef.Name == obj.GetName() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      agent.Name,
					Namespace: agent.Namespace,
				},
			})
		}
	}

	return requests
}
