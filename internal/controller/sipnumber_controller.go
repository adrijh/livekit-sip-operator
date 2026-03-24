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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// SIPNumberReconciler reconciles a SIPNumber object.
// It manages the lifecycle of a single SIP inbound trunk per phone number.
type SIPNumberReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipnumbers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !number.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, number)
	}

	// ── 3. Ensure finalizer ──────────────────────────────────────────────
	if !controllerutil.ContainsFinalizer(number, finalizerName) {
		controllerutil.AddFinalizer(number, finalizerName)
		if err := r.Update(ctx, number); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ── 4. Resolve LiveKit client ────────────────────────────────────────
	sipClient, err := resolveLiveKitClient(ctx, r.Client, number.Namespace, number.Spec.LivekitRef)
	if err != nil {
		r.setCondition(number, conditionLiveKitReady, metav1.ConditionFalse, "LiveKitSecretError", err.Error())
		r.setReadyCondition(number, false, "LiveKitSecretError", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after LiveKit secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	r.setCondition(number, conditionLiveKitReady, metav1.ConditionTrue, "LiveKitSecretResolved", "LiveKit credentials read successfully")

	// ── 5. Resolve auth secret (if referenced) ──────────────────────────
	authUser, authPass, err := r.resolveAuthSecret(ctx, number)
	if err != nil {
		r.setCondition(number, conditionSecretRead, metav1.ConditionFalse, "SecretReadError", err.Error())
		r.setReadyCondition(number, false, "SecretReadError", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	if number.Spec.AuthSecretRef != nil {
		r.setCondition(number, conditionSecretRead, metav1.ConditionTrue, "SecretResolved", "Auth secret read successfully")
	}

	// ── 6. Create or Update the trunk in LiveKit ─────────────────────────
	if number.Status.TrunkID == "" {
		return r.reconcileCreate(ctx, number, sipClient, authUser, authPass)
	}
	return r.reconcileUpdate(ctx, number, sipClient, authUser, authPass)
}

// ─── Reconcile sub-routines ──────────────────────────────────────────────────

func (r *SIPNumberReconciler) reconcileCreate(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP inbound trunk for number", "number", number.Spec.Number)

	req := &livekit.CreateSIPInboundTrunkRequest{
		Trunk: r.buildTrunkInfo(number, authUser, authPass),
	}

	info, err := sipClient.CreateSIPInboundTrunk(ctx, req)
	if err != nil {
		r.setCondition(number, conditionSynced, metav1.ConditionFalse, "CreateFailed", err.Error())
		r.setReadyCondition(number, false, "CreateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after create error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("creating trunk in LiveKit: %w", err)
	}

	number.Status.TrunkID = info.SipTrunkId
	number.Status.ObservedGeneration = number.Generation
	now := metav1.Now()
	number.Status.LastSyncedAt = &now

	r.setCondition(number, conditionSynced, metav1.ConditionTrue, "Created", "Trunk created in LiveKit")
	r.setReadyCondition(number, true, "Ready", fmt.Sprintf("Number %s is active (trunk %s)", number.Spec.Number, info.SipTrunkId))

	if err := r.Status().Update(ctx, number); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after create: %w", err)
	}

	log.Info("SIP inbound trunk created for number", "number", number.Spec.Number, "trunkID", info.SipTrunkId)
	return ctrl.Result{}, nil
}

func (r *SIPNumberReconciler) reconcileUpdate(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if number.Status.ObservedGeneration == number.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP inbound trunk for number", "number", number.Spec.Number, "trunkID", number.Status.TrunkID)

	trunkInfo := r.buildTrunkInfo(number, authUser, authPass)
	trunkInfo.SipTrunkId = number.Status.TrunkID

	_, err := sipClient.UpdateSIPInboundTrunk(ctx, &livekit.UpdateSIPInboundTrunkRequest{
		SipTrunkId: number.Status.TrunkID,
		Action: &livekit.UpdateSIPInboundTrunkRequest_Replace{
			Replace: trunkInfo,
		},
	})
	if err != nil {
		r.setCondition(number, conditionSynced, metav1.ConditionFalse, "UpdateFailed", err.Error())
		r.setReadyCondition(number, false, "UpdateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, number); statusErr != nil {
			log.Error(statusErr, "failed to update status after update error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("updating trunk in LiveKit: %w", err)
	}

	number.Status.ObservedGeneration = number.Generation
	now := metav1.Now()
	number.Status.LastSyncedAt = &now

	r.setCondition(number, conditionSynced, metav1.ConditionTrue, "Updated", "Trunk updated in LiveKit")
	r.setReadyCondition(number, true, "Ready", fmt.Sprintf("Number %s is active (trunk %s)", number.Spec.Number, number.Status.TrunkID))

	if err := r.Status().Update(ctx, number); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after update: %w", err)
	}

	log.Info("SIP inbound trunk updated for number", "number", number.Spec.Number, "trunkID", number.Status.TrunkID)
	return ctrl.Result{}, nil
}

func (r *SIPNumberReconciler) reconcileDelete(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(number, finalizerName) {
		return ctrl.Result{}, nil
	}

	if number.Status.TrunkID != "" {
		sipClient, err := resolveLiveKitClient(ctx, r.Client, number.Namespace, number.Spec.LivekitRef)
		if err != nil {
			log.Error(err, "failed to resolve LiveKit client for deletion, proceeding with finalizer removal")
		} else {
			log.Info("Deleting SIP inbound trunk from LiveKit", "trunkID", number.Status.TrunkID)
			_, err := sipClient.DeleteSIPTrunk(ctx, &livekit.DeleteSIPTrunkRequest{
				SipTrunkId: number.Status.TrunkID,
			})
			if err != nil {
				log.Error(err, "failed to delete trunk from LiveKit, proceeding with finalizer removal")
			}
		}
	}

	controllerutil.RemoveFinalizer(number, finalizerName)
	if err := r.Update(ctx, number); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	log.Info("SIPNumber deleted", "number", number.Spec.Number)
	return ctrl.Result{}, nil
}

// ─── LiveKit request builders ────────────────────────────────────────────────

func (r *SIPNumberReconciler) buildTrunkInfo(
	number *sipv1alpha1.SIPNumber,
	authUser, authPass string,
) *livekit.SIPInboundTrunkInfo {
	info := &livekit.SIPInboundTrunkInfo{
		Name:             fmt.Sprintf("sipnumber-%s", number.Name),
		Numbers:          []string{number.Spec.Number},
		AllowedAddresses: number.Spec.AllowedAddresses,
		AuthUsername:     authUser,
		AuthPassword:     authPass,
		KrispEnabled:     number.Spec.KrispEnabled,
	}

	if number.Spec.MediaEncryption != "" {
		info.MediaEncryption = mediaEncryptionToProto(number.Spec.MediaEncryption)
	}

	if number.Spec.RingingTimeout != nil {
		info.RingingTimeout = durationToProto(number.Spec.RingingTimeout.Duration)
	}

	if number.Spec.MaxCallDuration != nil {
		info.MaxCallDuration = durationToProto(number.Spec.MaxCallDuration.Duration)
	}

	return info
}

// ─── Secret resolution ───────────────────────────────────────────────────────

func (r *SIPNumberReconciler) resolveAuthSecret(
	ctx context.Context,
	number *sipv1alpha1.SIPNumber,
) (string, string, error) {
	if number.Spec.AuthSecretRef == nil {
		return "", "", nil
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: number.Namespace,
		Name:      number.Spec.AuthSecretRef.Name,
	}

	if err := r.Get(ctx, key, secret); err != nil {
		return "", "", fmt.Errorf("reading auth secret %q: %w", key, err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])

	if username == "" || password == "" {
		return "", "", fmt.Errorf("auth secret %q missing 'username' or 'password' key", key)
	}

	return username, password, nil
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
func (r *SIPNumberReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.SIPNumber{}).
		Named("sipnumber").
		Complete(r)
}
