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

// SIPOutboundTrunkReconciler reconciles a SIPOutboundTrunk object.
type SIPOutboundTrunkReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipoutboundtrunks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipoutboundtrunks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipoutboundtrunks/finalizers,verbs=update

func (r *SIPOutboundTrunkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	trunk := &sipv1alpha1.SIPOutboundTrunk{}
	if err := r.Get(ctx, req.NamespacedName, trunk); err != nil {
		if errors.IsNotFound(err) {
			log.Info("SIPOutboundTrunk resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching SIPOutboundTrunk: %w", err)
	}

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !trunk.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, trunk)
	}

	// ── 3. Ensure finalizer ──────────────────────────────────────────────
	if !controllerutil.ContainsFinalizer(trunk, finalizerName) {
		controllerutil.AddFinalizer(trunk, finalizerName)
		if err := r.Update(ctx, trunk); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ── 4. Resolve LiveKit client ────────────────────────────────────────
	sipClient, err := resolveLiveKitClient(ctx, r.Client, trunk.Namespace, trunk.Spec.LivekitRef)
	if err != nil {
		r.setCondition(trunk, conditionLiveKitReady, metav1.ConditionFalse, "LiveKitSecretError", err.Error())
		r.setReadyCondition(trunk, false, "LiveKitSecretError", err.Error())
		if statusErr := r.Status().Update(ctx, trunk); statusErr != nil {
			log.Error(statusErr, "failed to update status after LiveKit secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	r.setCondition(trunk, conditionLiveKitReady, metav1.ConditionTrue, "LiveKitSecretResolved", "LiveKit credentials read successfully")

	// ── 5. Resolve auth secret (if referenced) ──────────────────────────
	authUser, authPass, err := r.resolveAuthSecret(ctx, trunk)
	if err != nil {
		r.setCondition(trunk, conditionSecretRead, metav1.ConditionFalse, "SecretReadError", err.Error())
		if statusErr := r.Status().Update(ctx, trunk); statusErr != nil {
			log.Error(statusErr, "failed to update status after secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	if trunk.Spec.AuthSecretRef != nil {
		r.setCondition(trunk, conditionSecretRead, metav1.ConditionTrue, "SecretResolved", "Auth secret read successfully")
	}

	// ── 6. Create or Update the trunk in LiveKit ─────────────────────────
	if trunk.Status.TrunkID == "" {
		return r.reconcileCreate(ctx, trunk, sipClient, authUser, authPass)
	}
	return r.reconcileUpdate(ctx, trunk, sipClient, authUser, authPass)
}

// ─── Reconcile sub-routines ──────────────────────────────────────────────────

func (r *SIPOutboundTrunkReconciler) reconcileCreate(
	ctx context.Context,
	trunk *sipv1alpha1.SIPOutboundTrunk,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP outbound trunk in LiveKit", "name", trunk.Spec.Name)

	req := &livekit.CreateSIPOutboundTrunkRequest{
		Trunk: r.buildTrunkInfo(trunk, authUser, authPass),
	}

	info, err := sipClient.CreateSIPOutboundTrunk(ctx, req)
	if err != nil {
		r.setCondition(trunk, conditionSynced, metav1.ConditionFalse, "CreateFailed", err.Error())
		r.setReadyCondition(trunk, false, "CreateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, trunk); statusErr != nil {
			log.Error(statusErr, "failed to update status after create error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("creating trunk in LiveKit: %w", err)
	}

	trunk.Status.TrunkID = info.SipTrunkId
	trunk.Status.ObservedGeneration = trunk.Generation
	now := metav1.Now()
	trunk.Status.LastSyncedAt = &now

	r.setCondition(trunk, conditionSynced, metav1.ConditionTrue, "Created", "Trunk created in LiveKit")
	r.setReadyCondition(trunk, true, "Ready", "Trunk is active")

	if err := r.Status().Update(ctx, trunk); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after create: %w", err)
	}

	log.Info("SIP outbound trunk created", "trunkID", info.SipTrunkId)
	return ctrl.Result{}, nil
}

func (r *SIPOutboundTrunkReconciler) reconcileUpdate(
	ctx context.Context,
	trunk *sipv1alpha1.SIPOutboundTrunk,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if trunk.Status.ObservedGeneration == trunk.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP outbound trunk in LiveKit", "trunkID", trunk.Status.TrunkID)

	trunkInfo := r.buildTrunkInfo(trunk, authUser, authPass)
	trunkInfo.SipTrunkId = trunk.Status.TrunkID

	_, err := sipClient.UpdateSIPOutboundTrunk(ctx, &livekit.UpdateSIPOutboundTrunkRequest{
		SipTrunkId: trunk.Status.TrunkID,
		Action: &livekit.UpdateSIPOutboundTrunkRequest_Replace{
			Replace: trunkInfo,
		},
	})
	if err != nil {
		r.setCondition(trunk, conditionSynced, metav1.ConditionFalse, "UpdateFailed", err.Error())
		r.setReadyCondition(trunk, false, "UpdateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, trunk); statusErr != nil {
			log.Error(statusErr, "failed to update status after update error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("updating trunk in LiveKit: %w", err)
	}

	trunk.Status.ObservedGeneration = trunk.Generation
	now := metav1.Now()
	trunk.Status.LastSyncedAt = &now

	r.setCondition(trunk, conditionSynced, metav1.ConditionTrue, "Updated", "Trunk updated in LiveKit")
	r.setReadyCondition(trunk, true, "Ready", "Trunk is active")

	if err := r.Status().Update(ctx, trunk); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after update: %w", err)
	}

	log.Info("SIP outbound trunk updated", "trunkID", trunk.Status.TrunkID)
	return ctrl.Result{}, nil
}

func (r *SIPOutboundTrunkReconciler) reconcileDelete(
	ctx context.Context,
	trunk *sipv1alpha1.SIPOutboundTrunk,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(trunk, finalizerName) {
		return ctrl.Result{}, nil
	}

	if trunk.Status.TrunkID != "" {
		sipClient, err := resolveLiveKitClient(ctx, r.Client, trunk.Namespace, trunk.Spec.LivekitRef)
		if err != nil {
			log.Error(err, "failed to resolve LiveKit client for deletion, proceeding with finalizer removal")
		} else {
			log.Info("Deleting SIP outbound trunk from LiveKit", "trunkID", trunk.Status.TrunkID)
			_, err := sipClient.DeleteSIPTrunk(ctx, &livekit.DeleteSIPTrunkRequest{
				SipTrunkId: trunk.Status.TrunkID,
			})
			if err != nil {
				log.Error(err, "failed to delete trunk from LiveKit, proceeding with finalizer removal")
			}
		}
	}

	controllerutil.RemoveFinalizer(trunk, finalizerName)
	if err := r.Update(ctx, trunk); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	log.Info("SIP outbound trunk deleted", "name", trunk.Name)
	return ctrl.Result{}, nil
}

// ─── LiveKit request builders ────────────────────────────────────────────────

func (r *SIPOutboundTrunkReconciler) buildTrunkInfo(
	trunk *sipv1alpha1.SIPOutboundTrunk,
	authUser, authPass string,
) *livekit.SIPOutboundTrunkInfo {
	info := &livekit.SIPOutboundTrunkInfo{
		Name:                trunk.Spec.Name,
		Metadata:            trunk.Spec.Metadata,
		Address:             trunk.Spec.Address,
		DestinationCountry:  trunk.Spec.DestinationCountry,
		Numbers:             trunk.Spec.Numbers,
		AuthUsername:        authUser,
		AuthPassword:        authPass,
		Headers:             trunk.Spec.Headers,
		HeadersToAttributes: trunk.Spec.HeadersToAttributes,
		AttributesToHeaders: trunk.Spec.AttributesToHeaders,
		FromHost:            trunk.Spec.FromHost,
	}

	if trunk.Spec.Transport != "" {
		info.Transport = sipTransportToProto(trunk.Spec.Transport)
	}

	if trunk.Spec.IncludeHeaders != "" {
		info.IncludeHeaders = sipHeaderOptionToProto(trunk.Spec.IncludeHeaders)
	}

	if trunk.Spec.MediaEncryption != "" {
		info.MediaEncryption = mediaEncryptionToProto(trunk.Spec.MediaEncryption)
	}

	return info
}

// ─── Secret resolution ───────────────────────────────────────────────────────

func (r *SIPOutboundTrunkReconciler) resolveAuthSecret(
	ctx context.Context,
	trunk *sipv1alpha1.SIPOutboundTrunk,
) (string, string, error) {
	if trunk.Spec.AuthSecretRef == nil {
		return "", "", nil
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: trunk.Namespace,
		Name:      trunk.Spec.AuthSecretRef.Name,
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

func (r *SIPOutboundTrunkReconciler) setCondition(
	trunk *sipv1alpha1.SIPOutboundTrunk,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&trunk.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: trunk.Generation,
		Reason:             reason,
		Message:            message,
	})
}

func (r *SIPOutboundTrunkReconciler) setReadyCondition(
	trunk *sipv1alpha1.SIPOutboundTrunk,
	ready bool,
	reason, message string,
) {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	r.setCondition(trunk, conditionReady, status, reason, message)
}

// ─── Proto conversion helpers ────────────────────────────────────────────────

func sipTransportToProto(t sipv1alpha1.SIPTransport) livekit.SIPTransport {
	switch t {
	case sipv1alpha1.SIPTransportUDP:
		return livekit.SIPTransport_SIP_TRANSPORT_UDP
	case sipv1alpha1.SIPTransportTCP:
		return livekit.SIPTransport_SIP_TRANSPORT_TCP
	case sipv1alpha1.SIPTransportTLS:
		return livekit.SIPTransport_SIP_TRANSPORT_TLS
	default:
		return livekit.SIPTransport_SIP_TRANSPORT_AUTO
	}
}

// ─── Controller setup ────────────────────────────────────────────────────────

func (r *SIPOutboundTrunkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.SIPOutboundTrunk{}).
		Named("sipoutboundtrunk").
		Complete(r)
}
