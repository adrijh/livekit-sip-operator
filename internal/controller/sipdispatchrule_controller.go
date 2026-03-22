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

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// SIPDispatchRuleReconciler reconciles a SIPDispatchRule object.
type SIPDispatchRuleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *SIPDispatchRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	rule := &sipv1alpha1.SIPDispatchRule{}
	if err := r.Get(ctx, req.NamespacedName, rule); err != nil {
		if errors.IsNotFound(err) {
			log.Info("SIPDispatchRule resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching SIPDispatchRule: %w", err)
	}

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !rule.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, rule)
	}

	// ── 3. Ensure finalizer ──────────────────────────────────────────────
	if !controllerutil.ContainsFinalizer(rule, finalizerName) {
		controllerutil.AddFinalizer(rule, finalizerName)
		if err := r.Update(ctx, rule); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ── 4. Resolve LiveKit client ────────────────────────────────────────
	sipClient, err := resolveLiveKitClient(ctx, r.Client, rule.Namespace, rule.Spec.LivekitRef)
	if err != nil {
		r.setCondition(rule, conditionLiveKitReady, metav1.ConditionFalse, "LiveKitSecretError", err.Error())
		r.setReadyCondition(rule, false, "LiveKitSecretError", err.Error())
		if statusErr := r.Status().Update(ctx, rule); statusErr != nil {
			log.Error(statusErr, "failed to update status after LiveKit secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	r.setCondition(rule, conditionLiveKitReady, metav1.ConditionTrue, "LiveKitSecretResolved", "LiveKit credentials read successfully")

	// ── 5. Create or Update the dispatch rule in LiveKit ─────────────────
	if rule.Status.DispatchRuleID == "" {
		return r.reconcileCreate(ctx, rule, sipClient)
	}
	return r.reconcileUpdate(ctx, rule, sipClient)
}

// ─── Reconcile sub-routines ──────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) reconcileCreate(
	ctx context.Context,
	rule *sipv1alpha1.SIPDispatchRule,
	sipClient *lksdk.SIPClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP dispatch rule in LiveKit", "name", rule.Spec.Name)

	req := &livekit.CreateSIPDispatchRuleRequest{
		DispatchRule: r.buildRuleInfo(rule),
	}

	info, err := sipClient.CreateSIPDispatchRule(ctx, req)
	if err != nil {
		r.setCondition(rule, conditionSynced, metav1.ConditionFalse, "CreateFailed", err.Error())
		r.setReadyCondition(rule, false, "CreateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, rule); statusErr != nil {
			log.Error(statusErr, "failed to update status after create error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("creating dispatch rule in LiveKit: %w", err)
	}

	rule.Status.DispatchRuleID = info.SipDispatchRuleId
	rule.Status.ObservedGeneration = rule.Generation
	now := metav1.Now()
	rule.Status.LastSyncedAt = &now

	r.setCondition(rule, conditionSynced, metav1.ConditionTrue, "Created", "Dispatch rule created in LiveKit")
	r.setReadyCondition(rule, true, "Ready", "Dispatch rule is active")

	if err := r.Status().Update(ctx, rule); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after create: %w", err)
	}

	log.Info("SIP dispatch rule created", "ruleID", info.SipDispatchRuleId)
	return ctrl.Result{}, nil
}

func (r *SIPDispatchRuleReconciler) reconcileUpdate(
	ctx context.Context,
	rule *sipv1alpha1.SIPDispatchRule,
	sipClient *lksdk.SIPClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if rule.Status.ObservedGeneration == rule.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP dispatch rule in LiveKit", "ruleID", rule.Status.DispatchRuleID)

	ruleInfo := r.buildRuleInfo(rule)
	ruleInfo.SipDispatchRuleId = rule.Status.DispatchRuleID

	_, err := sipClient.UpdateSIPDispatchRule(ctx, &livekit.UpdateSIPDispatchRuleRequest{
		SipDispatchRuleId: rule.Status.DispatchRuleID,
		Action: &livekit.UpdateSIPDispatchRuleRequest_Replace{
			Replace: ruleInfo,
		},
	})
	if err != nil {
		r.setCondition(rule, conditionSynced, metav1.ConditionFalse, "UpdateFailed", err.Error())
		r.setReadyCondition(rule, false, "UpdateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, rule); statusErr != nil {
			log.Error(statusErr, "failed to update status after update error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("updating dispatch rule in LiveKit: %w", err)
	}

	rule.Status.ObservedGeneration = rule.Generation
	now := metav1.Now()
	rule.Status.LastSyncedAt = &now

	r.setCondition(rule, conditionSynced, metav1.ConditionTrue, "Updated", "Dispatch rule updated in LiveKit")
	r.setReadyCondition(rule, true, "Ready", "Dispatch rule is active")

	if err := r.Status().Update(ctx, rule); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after update: %w", err)
	}

	log.Info("SIP dispatch rule updated", "ruleID", rule.Status.DispatchRuleID)
	return ctrl.Result{}, nil
}

func (r *SIPDispatchRuleReconciler) reconcileDelete(
	ctx context.Context,
	rule *sipv1alpha1.SIPDispatchRule,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(rule, finalizerName) {
		return ctrl.Result{}, nil
	}

	if rule.Status.DispatchRuleID != "" {
		sipClient, err := resolveLiveKitClient(ctx, r.Client, rule.Namespace, rule.Spec.LivekitRef)
		if err != nil {
			log.Error(err, "failed to resolve LiveKit client for deletion, proceeding with finalizer removal")
		} else {
			log.Info("Deleting SIP dispatch rule from LiveKit", "ruleID", rule.Status.DispatchRuleID)
			_, err := sipClient.DeleteSIPDispatchRule(ctx, &livekit.DeleteSIPDispatchRuleRequest{
				SipDispatchRuleId: rule.Status.DispatchRuleID,
			})
			if err != nil {
				log.Error(err, "failed to delete dispatch rule from LiveKit, proceeding with finalizer removal")
			}
		}
	}

	controllerutil.RemoveFinalizer(rule, finalizerName)
	if err := r.Update(ctx, rule); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	log.Info("SIP dispatch rule deleted", "name", rule.Name)
	return ctrl.Result{}, nil
}

// ─── LiveKit request builders ────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) buildRuleInfo(
	rule *sipv1alpha1.SIPDispatchRule,
) *livekit.SIPDispatchRuleInfo {
	info := &livekit.SIPDispatchRuleInfo{
		Name:            rule.Spec.Name,
		Metadata:        rule.Spec.Metadata,
		TrunkIds:        rule.Spec.TrunkIDs,
		HidePhoneNumber: rule.Spec.HidePhoneNumber,
		InboundNumbers:  rule.Spec.InboundNumbers,
		Numbers:         rule.Spec.Numbers,
		Attributes:      rule.Spec.Attributes,
		RoomPreset:      rule.Spec.RoomPreset,
		KrispEnabled:    rule.Spec.KrispEnabled,
	}

	if rule.Spec.MediaEncryption != "" {
		info.MediaEncryption = mediaEncryptionToProto(rule.Spec.MediaEncryption)
	}

	info.Rule = r.buildDispatchRule(rule)

	return info
}

func (r *SIPDispatchRuleReconciler) buildDispatchRule(
	rule *sipv1alpha1.SIPDispatchRule,
) *livekit.SIPDispatchRule {
	switch rule.Spec.Type {
	case sipv1alpha1.SIPDispatchRuleTypeDirect:
		if rule.Spec.Direct == nil {
			return nil
		}
		return &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleDirect{
				DispatchRuleDirect: &livekit.SIPDispatchRuleDirect{
					RoomName: rule.Spec.Direct.RoomName,
					Pin:      rule.Spec.Direct.Pin,
				},
			},
		}
	case sipv1alpha1.SIPDispatchRuleTypeIndividual:
		if rule.Spec.Individual == nil {
			return nil
		}
		return &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleIndividual{
				DispatchRuleIndividual: &livekit.SIPDispatchRuleIndividual{
					RoomPrefix:   rule.Spec.Individual.RoomPrefix,
					Pin:          rule.Spec.Individual.Pin,
					NoRandomness: rule.Spec.Individual.NoRandomness,
				},
			},
		}
	case sipv1alpha1.SIPDispatchRuleTypeCallee:
		if rule.Spec.Callee == nil {
			return nil
		}
		return &livekit.SIPDispatchRule{
			Rule: &livekit.SIPDispatchRule_DispatchRuleCallee{
				DispatchRuleCallee: &livekit.SIPDispatchRuleCallee{
					RoomPrefix: rule.Spec.Callee.RoomPrefix,
					Pin:        rule.Spec.Callee.Pin,
					Randomize:  rule.Spec.Callee.Randomize,
				},
			},
		}
	default:
		return nil
	}
}

// ─── Condition helpers ───────────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) setCondition(
	rule *sipv1alpha1.SIPDispatchRule,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: rule.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *SIPDispatchRuleReconciler) setReadyCondition(
	rule *sipv1alpha1.SIPDispatchRule,
	ready bool,
	reason, message string,
) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	r.setCondition(rule, conditionReady, status, reason, message)
}

// ─── Controller setup ────────────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.SIPDispatchRule{}).
		Named("sipdispatchrule").
		Complete(r)
}
