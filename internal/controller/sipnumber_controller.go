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

// resolvedNumberConfig holds the merged configuration for a SIPNumber,
// combining SIPTrunkConfig defaults with inline overrides.
type resolvedNumberConfig struct {
	livekitRef       sipv1alpha1.LivekitReference
	authSecretRef    *sipv1alpha1.SecretReference
	allowedAddresses []string
	krispEnabled     bool
	mediaEncryption  sipv1alpha1.SIPMediaEncryption
	ringingTimeout   *metav1.Duration
	maxCallDuration  *metav1.Duration
}

// SIPNumberReconciler reconciles a SIPNumber object.
// It manages the lifecycle of a SIPInboundTrunk CR for each phone number,
// delegating the actual LiveKit API interaction to the SIPInboundTrunk controller.
type SIPNumberReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers/finalizers,verbs=update
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipinboundtrunks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=siptrunkconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *SIPNumberReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	number := &sipv1alpha1.SIPNumber{}
	if err := r.Get(ctx, req.NamespacedName, number); err != nil {
		if errors.IsNotFound(err) {
			log.Info("SIPNumber resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching SIPNumber: %w", err)
	}

	// ── 2. Handle deletion ──────────────────────────────────────────────
	// Owner references handle cascading deletion: when SIPNumber is deleted,
	// the owned SIPInboundTrunk is garbage collected, and its finalizer
	// handles the LiveKit API cleanup.
	if !number.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// ── 3. Resolve merged config (SIPTrunkConfig + inline overrides) ─────
	cfg, err := r.resolveConfig(ctx, number)
	if err != nil {
		r.setCondition(number, conditionSynced, metav1.ConditionFalse, "ConfigError", err.Error())
		r.setReadyCondition(number, false, "ConfigError", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after config error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}

	// ── 4. Create or update the child SIPInboundTrunk CR ─────────────────
	childTrunk, err := r.reconcileInboundTrunk(ctx, number, cfg)
	if err != nil {
		r.setCondition(number, conditionSynced, metav1.ConditionFalse, "SyncFailed", err.Error())
		r.setReadyCondition(number, false, "SyncFailed", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after sync error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}

	// ── 5. Propagate child status to SIPNumber status ────────────────────
	number.Status.TrunkID = childTrunk.Status.TrunkID
	number.Status.ObservedGeneration = number.Generation

	if childTrunk.Status.TrunkID == "" {
		// Child trunk hasn't been reconciled yet
		r.setCondition(number, conditionSynced, metav1.ConditionTrue, "TrunkCreated", "SIPInboundTrunk CR created, waiting for trunk provisioning")
		r.setReadyCondition(number, false, "Pending", "Waiting for SIPInboundTrunk to be provisioned")
	} else {
		// Check the child's Ready condition
		childReady := meta.FindStatusCondition(childTrunk.Status.Conditions, conditionReady)
		if childReady != nil && childReady.Status == metav1.ConditionTrue {
			now := metav1.Now()
			number.Status.LastSyncedAt = &now
			r.setCondition(number, conditionSynced, metav1.ConditionTrue, "Synced", "SIPInboundTrunk is synced")
			r.setReadyCondition(number, true, "Ready", fmt.Sprintf("Number %s is active (trunk %s)", number.Spec.Number, childTrunk.Status.TrunkID))
		} else {
			reason := "Pending"
			msg := "SIPInboundTrunk is not yet ready"
			if childReady != nil {
				reason = childReady.Reason
				msg = childReady.Message
			}
			r.setCondition(number, conditionSynced, metav1.ConditionTrue, "TrunkCreated", "SIPInboundTrunk CR exists")
			r.setReadyCondition(number, false, reason, msg)
		}
	}

	if err := r.Status().Update(ctx, number); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating SIPNumber status: %w", err)
	}

	return ctrl.Result{}, nil
}

// ─── Config resolution ───────────────────────────────────────────────────────

// resolveConfig merges SIPTrunkConfig defaults with inline SIPNumber overrides.
func (r *SIPNumberReconciler) resolveConfig(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
) (*resolvedNumberConfig, error) {
	cfg := &resolvedNumberConfig{}

	// Start with SIPTrunkConfig defaults if referenced
	if number.Spec.TrunkConfigRef != nil {
		trunkConfig := &sipv1alpha1.SIPTrunkConfig{}
		key := types.NamespacedName{
			Namespace: number.Namespace,
			Name:      number.Spec.TrunkConfigRef.Name,
		}
		if err := r.Get(ctx, key, trunkConfig); err != nil {
			return nil, fmt.Errorf("fetching SIPTrunkConfig %q: %w", key.Name, err)
		}

		cfg.livekitRef = trunkConfig.Spec.LivekitRef
		cfg.authSecretRef = trunkConfig.Spec.AuthSecretRef
		cfg.allowedAddresses = trunkConfig.Spec.AllowedAddresses
		cfg.krispEnabled = trunkConfig.Spec.KrispEnabled
		cfg.mediaEncryption = trunkConfig.Spec.MediaEncryption
		cfg.ringingTimeout = trunkConfig.Spec.RingingTimeout
		cfg.maxCallDuration = trunkConfig.Spec.MaxCallDuration
	}

	// Apply inline overrides
	if number.Spec.LivekitRef != nil {
		cfg.livekitRef = *number.Spec.LivekitRef
	}
	if number.Spec.AuthSecretRef != nil {
		cfg.authSecretRef = number.Spec.AuthSecretRef
	}
	if len(number.Spec.AllowedAddresses) > 0 {
		cfg.allowedAddresses = number.Spec.AllowedAddresses
	}
	if number.Spec.KrispEnabled != nil {
		cfg.krispEnabled = *number.Spec.KrispEnabled
	}
	if number.Spec.MediaEncryption != "" {
		cfg.mediaEncryption = number.Spec.MediaEncryption
	}
	if number.Spec.RingingTimeout != nil {
		cfg.ringingTimeout = number.Spec.RingingTimeout
	}
	if number.Spec.MaxCallDuration != nil {
		cfg.maxCallDuration = number.Spec.MaxCallDuration
	}

	// Validate that we have a livekitRef from somewhere
	if cfg.livekitRef.Service == "" || cfg.livekitRef.SecretName == "" {
		return nil, fmt.Errorf("livekitRef is required: set it inline or via trunkConfigRef")
	}

	return cfg, nil
}

// ─── Child resource management ───────────────────────────────────────────────

// reconcileInboundTrunk creates or updates the child SIPInboundTrunk CR
// that is owned by the SIPNumber. The SIPInboundTrunk controller handles
// the actual LiveKit API interaction.
func (r *SIPNumberReconciler) reconcileInboundTrunk(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
	cfg *resolvedNumberConfig,
) (*sipv1alpha1.SIPInboundTrunk, error) {
	log := logf.FromContext(ctx)

	childName := number.Name
	child := &sipv1alpha1.SIPInboundTrunk{}
	child.Name = childName
	child.Namespace = number.Namespace

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, child, func() error {
		// Set the owner reference so the child is garbage collected
		// when the SIPNumber is deleted.
		if err := controllerutil.SetControllerReference(number, child, r.Scheme); err != nil {
			return fmt.Errorf("setting owner reference: %w", err)
		}

		// Set labels for easy identification
		if child.Labels == nil {
			child.Labels = make(map[string]string)
		}
		child.Labels["sip.livekit-sip.io/managed-by"] = "sipnumber"
		child.Labels["sip.livekit-sip.io/sipnumber"] = number.Name

		// Build the desired spec
		child.Spec = sipv1alpha1.SIPInboundTrunkSpec{
			LivekitRef:       cfg.livekitRef,
			Name:             fmt.Sprintf("sipnumber-%s", number.Name),
			Numbers:          []string{number.Spec.Number},
			AllowedAddresses: cfg.allowedAddresses,
			AuthSecretRef:    cfg.authSecretRef,
			KrispEnabled:     cfg.krispEnabled,
			MediaEncryption:  cfg.mediaEncryption,
			RingingTimeout:   cfg.ringingTimeout,
			MaxCallDuration:  cfg.maxCallDuration,
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("creating/updating SIPInboundTrunk %q: %w", childName, err)
	}

	log.Info("Reconciled child SIPInboundTrunk",
		"name", childName,
		"operation", result,
		"number", number.Spec.Number,
	)

	return child, nil
}

// ─── Condition helpers ───────────────────────────────────────────────────────

func (r *SIPNumberReconciler) setCondition(
	number *sipv1alpha1.SIPNumber,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&number.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: number.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *SIPNumberReconciler) setReadyCondition(
	number *sipv1alpha1.SIPNumber,
	ready bool,
	reason, message string,
) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	r.setCondition(number, conditionReady, status, reason, message)
}

// ─── Controller setup ────────────────────────────────────────────────────────

// SetupWithManager sets up the controller with the Manager.
// It watches SIPNumber resources, owned SIPInboundTrunk resources,
// and SIPTrunkConfig resources (enqueueing SIPNumbers that reference changed configs).
func (r *SIPNumberReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.SIPNumber{}).
		Owns(&sipv1alpha1.SIPInboundTrunk{}).
		Watches(
			&sipv1alpha1.SIPTrunkConfig{},
			handler.EnqueueRequestsFromMapFunc(r.findSIPNumbersForTrunkConfig),
		).
		Named("sipnumber").
		Complete(r)
}

// findSIPNumbersForTrunkConfig returns reconcile requests for all SIPNumbers
// that reference the given SIPTrunkConfig.
func (r *SIPNumberReconciler) findSIPNumbersForTrunkConfig(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	trunkConfig, ok := obj.(*sipv1alpha1.SIPTrunkConfig)
	if !ok {
		return nil
	}

	numberList := &sipv1alpha1.SIPNumberList{}
	if err := r.List(ctx, numberList, client.InNamespace(trunkConfig.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list SIPNumbers for SIPTrunkConfig watch")
		return nil
	}

	var requests []reconcile.Request
	for _, number := range numberList.Items {
		if number.Spec.TrunkConfigRef != nil && number.Spec.TrunkConfigRef.Name == trunkConfig.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      number.Name,
					Namespace: number.Namespace,
				},
			})
		}
	}

	return requests
}
