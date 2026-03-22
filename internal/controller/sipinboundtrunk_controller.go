package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/types/known/durationpb"
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

const (
	finalizerName = "sip.livekit-sip.io/finalizer"

	// Status conditions
	conditionReady        = "Ready"
	conditionSynced       = "Synced"
	conditionSecretRead   = "SecretRead"
	conditionLiveKitReady = "LiveKitReady"

	// Requeue intervals
	requeueOnError   = 15 * time.Second
	requeueOnPending = 5 * time.Second
)

// SIPInboundTrunkReconciler reconciles a SIPInboundTrunk object.
type SIPInboundTrunkReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipinboundtrunks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipinboundtrunks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipinboundtrunks/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *SIPInboundTrunkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// ── 1. Fetch the CR ──────────────────────────────────────────────────
	trunk := &sipv1alpha1.SIPInboundTrunk{}
	if err := r.Get(ctx, req.NamespacedName, trunk); err != nil {
		if errors.IsNotFound(err) {
			log.Info("SIPInboundTrunk resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching SIPInboundTrunk: %w", err)
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

func (r *SIPInboundTrunkReconciler) reconcileCreate(
	ctx context.Context,
	trunk *sipv1alpha1.SIPInboundTrunk,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP inbound trunk in LiveKit", "name", trunk.Spec.Name)

	req := &livekit.CreateSIPInboundTrunkRequest{
		Trunk: r.buildTrunkInfo(trunk, authUser, authPass),
	}

	info, err := sipClient.CreateSIPInboundTrunk(ctx, req)
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

	log.Info("SIP inbound trunk created", "trunkID", info.SipTrunkId)
	return ctrl.Result{}, nil
}

func (r *SIPInboundTrunkReconciler) reconcileUpdate(
	ctx context.Context,
	trunk *sipv1alpha1.SIPInboundTrunk,
	sipClient *lksdk.SIPClient,
	authUser, authPass string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if trunk.Status.ObservedGeneration == trunk.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP inbound trunk in LiveKit", "trunkID", trunk.Status.TrunkID)

	trunkInfo := r.buildTrunkInfo(trunk, authUser, authPass)
	trunkInfo.SipTrunkId = trunk.Status.TrunkID

	_, err := sipClient.UpdateSIPInboundTrunk(ctx, &livekit.UpdateSIPInboundTrunkRequest{
		SipTrunkId: trunk.Status.TrunkID,
		Action: &livekit.UpdateSIPInboundTrunkRequest_Replace{
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

	log.Info("SIP inbound trunk updated", "trunkID", trunk.Status.TrunkID)
	return ctrl.Result{}, nil
}

func (r *SIPInboundTrunkReconciler) reconcileDelete(
	ctx context.Context,
	trunk *sipv1alpha1.SIPInboundTrunk,
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
			log.Info("Deleting SIP inbound trunk from LiveKit", "trunkID", trunk.Status.TrunkID)
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

	log.Info("SIP inbound trunk deleted", "name", trunk.Name)
	return ctrl.Result{}, nil
}

// ─── LiveKit request builders ────────────────────────────────────────────────

func (r *SIPInboundTrunkReconciler) buildTrunkInfo(
	trunk *sipv1alpha1.SIPInboundTrunk,
	authUser, authPass string,
) *livekit.SIPInboundTrunkInfo {
	info := &livekit.SIPInboundTrunkInfo{
		Name:                trunk.Spec.Name,
		Metadata:            trunk.Spec.Metadata,
		Numbers:             trunk.Spec.Numbers,
		AllowedAddresses:    trunk.Spec.AllowedAddresses,
		AllowedNumbers:      trunk.Spec.AllowedNumbers,
		AuthUsername:        authUser,
		AuthPassword:        authPass,
		Headers:             trunk.Spec.Headers,
		HeadersToAttributes: trunk.Spec.HeadersToAttributes,
		AttributesToHeaders: trunk.Spec.AttributesToHeaders,
		KrispEnabled:        trunk.Spec.KrispEnabled,
	}

	if trunk.Spec.IncludeHeaders != "" {
		info.IncludeHeaders = sipHeaderOptionToProto(trunk.Spec.IncludeHeaders)
	}

	if trunk.Spec.RingingTimeout != nil {
		info.RingingTimeout = durationToProto(trunk.Spec.RingingTimeout.Duration)
	}

	if trunk.Spec.MaxCallDuration != nil {
		info.MaxCallDuration = durationToProto(trunk.Spec.MaxCallDuration.Duration)
	}

	if trunk.Spec.MediaEncryption != "" {
		info.MediaEncryption = mediaEncryptionToProto(trunk.Spec.MediaEncryption)
	}

	return info
}

// ─── Secret resolution ───────────────────────────────────────────────────────

func (r *SIPInboundTrunkReconciler) resolveAuthSecret(
	ctx context.Context,
	trunk *sipv1alpha1.SIPInboundTrunk,
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

func (r *SIPInboundTrunkReconciler) setCondition(
	trunk *sipv1alpha1.SIPInboundTrunk,
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

func (r *SIPInboundTrunkReconciler) setReadyCondition(
	trunk *sipv1alpha1.SIPInboundTrunk,
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

func sipHeaderOptionToProto(opt sipv1alpha1.SIPHeaderOptions) livekit.SIPHeaderOptions {
	switch opt {
	case sipv1alpha1.SIPHeaderOptionsAll:
		return livekit.SIPHeaderOptions_SIP_ALL_HEADERS
	case sipv1alpha1.SIPHeaderOptionsXHeaders:
		return livekit.SIPHeaderOptions_SIP_X_HEADERS
	default:
		return livekit.SIPHeaderOptions_SIP_NO_HEADERS
	}
}

func mediaEncryptionToProto(enc sipv1alpha1.SIPMediaEncryption) livekit.SIPMediaEncryption {
	switch enc {
	case sipv1alpha1.SIPMediaEncryptionAllow:
		return livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_ALLOW
	case sipv1alpha1.SIPMediaEncryptionRequire:
		return livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_REQUIRE
	default:
		return livekit.SIPMediaEncryption_SIP_MEDIA_ENCRYPT_DISABLE
	}
}

func durationToProto(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}

// ─── Controller setup ────────────────────────────────────────────────────────

func (r *SIPInboundTrunkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.SIPInboundTrunk{}).
		Named("sipinboundtrunk").
		Complete(r)
}
