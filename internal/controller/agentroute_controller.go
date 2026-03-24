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
	"slices"
	"sort"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// AgentRouteReconciler reconciles an AgentRoute object.
// It creates and manages a child SIPDispatchRule CR, delegating the actual
// LiveKit API interaction to the SIPDispatchRule controller.
// It watches SIPNumber resources and keeps the dispatch rule's trunk_ids
// list in sync with the referenced SIPNumbers' trunk IDs.
type AgentRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=agentroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=agentroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=agentroutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers,verbs=get;list;watch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=egressconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	route := &sipv1alpha1.AgentRoute{}
	if err := r.Get(ctx, req.NamespacedName, route); err != nil {
		if errors.IsNotFound(err) {
			log.Info("AgentRoute resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching AgentRoute: %w", err)
	}

	// ── 2. Handle deletion ──────────────────────────────────────────────
	// Owner references handle cascading deletion: when AgentRoute is deleted,
	// the owned SIPDispatchRule is garbage collected, and its finalizer
	// handles the LiveKit API cleanup.
	if !route.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// ── 3. Collect trunk IDs from referenced SIPNumbers ──────────────────
	trunkIDs, pendingNumbers, err := r.collectTrunkIDs(ctx, route)
	if err != nil {
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "TrunkIDCollectionError", err.Error())
		r.setReadyCondition(route, false, "TrunkIDCollectionError", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after trunk ID collection error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}

	if len(pendingNumbers) > 0 {
		msg := fmt.Sprintf("Waiting for SIPNumbers to become ready: %v", pendingNumbers)
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "NumbersPending", msg)
		r.setReadyCondition(route, false, "NumbersPending", msg)
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status while waiting for numbers")
		}
		return ctrl.Result{RequeueAfter: requeueOnPending}, nil
	}

	// ── 4. Build the room config for the dispatch rule ───────────────────
	roomConfig := r.buildRoomConfig(route)

	// ── 5. Create or update the child SIPDispatchRule CR ─────────────────
	childRule, err := r.reconcileDispatchRule(ctx, route, trunkIDs, roomConfig)
	if err != nil {
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "SyncFailed", err.Error())
		r.setReadyCondition(route, false, "SyncFailed", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after sync error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}

	// ── 6. Propagate child status to AgentRoute status ───────────────────
	route.Status.DispatchRuleID = childRule.Status.DispatchRuleID
	route.Status.TrunkIDs = trunkIDs
	route.Status.ObservedGeneration = route.Generation

	if childRule.Status.DispatchRuleID == "" {
		r.setCondition(route, conditionSynced, metav1.ConditionTrue, "RuleCreated", "SIPDispatchRule CR created, waiting for provisioning")
		r.setReadyCondition(route, false, "Pending", "Waiting for SIPDispatchRule to be provisioned")
	} else {
		childReady := meta.FindStatusCondition(childRule.Status.Conditions, conditionReady)
		if childReady != nil && childReady.Status == metav1.ConditionTrue {
			now := metav1.Now()
			route.Status.LastSyncedAt = &now
			r.setCondition(route, conditionSynced, metav1.ConditionTrue, "Synced", "SIPDispatchRule is synced")
			r.setReadyCondition(route, true, "Ready", fmt.Sprintf("Agent %s routed via %d number(s)", route.Spec.AgentName, len(trunkIDs)))
		} else {
			reason := "Pending"
			msg := "SIPDispatchRule is not yet ready"
			if childReady != nil {
				reason = childReady.Reason
				msg = childReady.Message
			}
			r.setCondition(route, conditionSynced, metav1.ConditionTrue, "RuleCreated", "SIPDispatchRule CR exists")
			r.setReadyCondition(route, false, reason, msg)
		}
	}

	if err := r.Status().Update(ctx, route); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating AgentRoute status: %w", err)
	}

	return ctrl.Result{}, nil
}

// ─── Trunk ID collection ─────────────────────────────────────────────────────

// collectTrunkIDs resolves the trunk IDs from referenced SIPNumber resources.
// Returns the list of trunk IDs and a list of SIPNumber names that are not yet ready.
func (r *AgentRouteReconciler) collectTrunkIDs(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
) ([]string, []string, error) {
	var trunkIDs []string
	var pendingNumbers []string

	for _, ref := range route.Spec.NumberRefs {
		sipNumber := &sipv1alpha1.SIPNumber{}
		key := types.NamespacedName{
			Namespace: route.Namespace,
			Name:      ref.Name,
		}

		if err := r.Get(ctx, key, sipNumber); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil, fmt.Errorf("SIPNumber %q not found", ref.Name)
			}
			return nil, nil, fmt.Errorf("fetching SIPNumber %q: %w", ref.Name, err)
		}

		if sipNumber.Status.TrunkID == "" {
			pendingNumbers = append(pendingNumbers, ref.Name)
			continue
		}

		trunkIDs = append(trunkIDs, sipNumber.Status.TrunkID)
	}

	// Sort for deterministic comparison
	sort.Strings(trunkIDs)
	return trunkIDs, pendingNumbers, nil
}

// ─── Child resource management ───────────────────────────────────────────────

// reconcileDispatchRule creates or updates the child SIPDispatchRule CR
// that is owned by the AgentRoute. The SIPDispatchRule controller handles
// the actual LiveKit API interaction.
func (r *AgentRouteReconciler) reconcileDispatchRule(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
	trunkIDs []string,
	roomConfig *sipv1alpha1.RoomConfig,
) (*sipv1alpha1.SIPDispatchRule, error) {
	log := logf.FromContext(ctx)

	childName := route.Name
	child := &sipv1alpha1.SIPDispatchRule{}
	child.Name = childName
	child.Namespace = route.Namespace

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, child, func() error {
		// Set the owner reference so the child is garbage collected
		// when the AgentRoute is deleted.
		if err := controllerutil.SetControllerReference(route, child, r.Scheme); err != nil {
			return fmt.Errorf("setting owner reference: %w", err)
		}

		// Set labels for easy identification
		if child.Labels == nil {
			child.Labels = make(map[string]string)
		}
		child.Labels["sip.livekit-sip.io/managed-by"] = "agentroute"
		child.Labels["sip.livekit-sip.io/agentroute"] = route.Name

		// Build the individual dispatch config, defaulting roomPrefix if not set
		individual := route.Spec.Individual
		if individual == nil {
			individual = &sipv1alpha1.SIPDispatchRuleIndividual{
				RoomPrefix: fmt.Sprintf("call-%s-", route.Spec.AgentName),
			}
		}

		// Build the desired spec
		child.Spec = sipv1alpha1.SIPDispatchRuleSpec{
			LivekitRef:      route.Spec.LivekitRef,
			Name:            fmt.Sprintf("agentroute-%s", route.Name),
			Type:            sipv1alpha1.SIPDispatchRuleTypeIndividual,
			Individual:      individual,
			TrunkIDs:        trunkIDs,
			HidePhoneNumber: route.Spec.HidePhoneNumber,
			KrispEnabled:    route.Spec.KrispEnabled,
			MediaEncryption: route.Spec.MediaEncryption,
			RoomConfig:      roomConfig,
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("creating/updating SIPDispatchRule %q: %w", childName, err)
	}

	log.Info("Reconciled child SIPDispatchRule",
		"name", childName,
		"operation", result,
		"agent", route.Spec.AgentName,
		"trunkIDs", trunkIDs,
	)

	return child, nil
}

// ─── Room config builder ─────────────────────────────────────────────────────

// buildRoomConfig builds the RoomConfig for the child SIPDispatchRule.
// If roomConfig.agents is specified, those are used as-is.
// Otherwise, the primary agent (from agentName) is dispatched automatically.
func (r *AgentRouteReconciler) buildRoomConfig(
	route *sipv1alpha1.AgentRoute,
) *sipv1alpha1.RoomConfig {
	config := &sipv1alpha1.RoomConfig{}

	if route.Spec.RoomConfig != nil && len(route.Spec.RoomConfig.Agents) > 0 {
		// Use the explicitly listed agents
		config.Agents = route.Spec.RoomConfig.Agents
		config.Egress = route.Spec.RoomConfig.Egress
	} else {
		// No agents specified in roomConfig — auto-dispatch the primary agent
		config.Agents = []sipv1alpha1.RoomAgentDispatchConfig{
			{
				AgentName: route.Spec.AgentName,
				Metadata:  route.Spec.AgentMetadata,
			},
		}
		if route.Spec.RoomConfig != nil {
			config.Egress = route.Spec.RoomConfig.Egress
		}
	}

	return config
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// trunkIDsEqual compares two sorted trunk ID slices.
func trunkIDsEqual(a, b []string) bool {
	return slices.Equal(a, b)
}

// ─── Condition helpers ───────────────────────────────────────────────────────

func (r *AgentRouteReconciler) setCondition(
	route *sipv1alpha1.AgentRoute,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: route.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *AgentRouteReconciler) setReadyCondition(
	route *sipv1alpha1.AgentRoute,
	ready bool,
	reason, message string,
) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	r.setCondition(route, conditionReady, status, reason, message)
}

// ─── Controller setup ────────────────────────────────────────────────────────

// SetupWithManager sets up the controller with the Manager.
// It watches AgentRoute resources, owned SIPDispatchRule resources,
// and SIPNumber resources (enqueueing AgentRoutes that reference changed SIPNumbers).
func (r *AgentRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.AgentRoute{}).
		Owns(&sipv1alpha1.SIPDispatchRule{}).
		Watches(
			&sipv1alpha1.SIPNumber{},
			handler.EnqueueRequestsFromMapFunc(r.findAgentRoutesForSIPNumber),
		).
		Named("agentroute").
		Complete(r)
}

// findAgentRoutesForSIPNumber returns reconcile requests for all AgentRoutes
// that reference the given SIPNumber.
func (r *AgentRouteReconciler) findAgentRoutesForSIPNumber(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	sipNumber, ok := obj.(*sipv1alpha1.SIPNumber)
	if !ok {
		return nil
	}

	// List all AgentRoutes in the same namespace
	routeList := &sipv1alpha1.AgentRouteList{}
	if err := r.List(ctx, routeList, client.InNamespace(sipNumber.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list AgentRoutes for SIPNumber watch")
		return nil
	}

	var requests []reconcile.Request
	for _, route := range routeList.Items {
		for _, ref := range route.Spec.NumberRefs {
			if ref.Name == sipNumber.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      route.Name,
						Namespace: route.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}
