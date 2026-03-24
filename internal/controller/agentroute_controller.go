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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sipv1alpha1 "github.com/adrijh/livekit-sip-operator/api/v1alpha1"
)

// AgentRouteReconciler reconciles an AgentRoute object.
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
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=egressconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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

	// ── 2. Handle deletion (finalizer) ───────────────────────────────────
	if !route.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, route)
	}

	// ── 3. Ensure finalizer ──────────────────────────────────────────────
	if !controllerutil.ContainsFinalizer(route, finalizerName) {
		controllerutil.AddFinalizer(route, finalizerName)
		if err := r.Update(ctx, route); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ── 4. Resolve LiveKit client ────────────────────────────────────────
	sipClient, err := resolveLiveKitClient(ctx, r.Client, route.Namespace, route.Spec.LivekitRef)
	if err != nil {
		r.setCondition(route, conditionLiveKitReady, metav1.ConditionFalse, "LiveKitSecretError", err.Error())
		r.setReadyCondition(route, false, "LiveKitSecretError", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after LiveKit secret error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}
	r.setCondition(route, conditionLiveKitReady, metav1.ConditionTrue, "LiveKitSecretResolved", "LiveKit credentials read successfully")

	// ── 5. Collect trunk IDs from referenced SIPNumbers ──────────────────
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

	// ── 6. Resolve room config (EgressConfig references + secrets) ───────
	var roomConfig *livekit.RoomConfiguration
	roomConfig, err = r.buildRoomConfig(ctx, route)
	if err != nil {
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "RoomConfigError", err.Error())
		r.setReadyCondition(route, false, "RoomConfigError", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after room config error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, nil
	}

	// ── 7. Create or Update the dispatch rule in LiveKit ─────────────────
	if route.Status.DispatchRuleID == "" {
		return r.reconcileCreate(ctx, route, sipClient, trunkIDs, roomConfig)
	}
	return r.reconcileUpdate(ctx, route, sipClient, trunkIDs, roomConfig)
}

// ─── Reconcile sub-routines ──────────────────────────────────────────────────

func (r *AgentRouteReconciler) reconcileCreate(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
	sipClient *lksdk.SIPClient,
	trunkIDs []string,
	roomConfig *livekit.RoomConfiguration,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP dispatch rule for agent", "agent", route.Spec.AgentName, "trunkIDs", trunkIDs)

	ruleInfo := r.buildRuleInfo(route, trunkIDs)
	ruleInfo.RoomConfig = roomConfig

	info, err := sipClient.CreateSIPDispatchRule(ctx, &livekit.CreateSIPDispatchRuleRequest{
		DispatchRule: ruleInfo,
	})
	if err != nil {
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "CreateFailed", err.Error())
		r.setReadyCondition(route, false, "CreateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after create error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("creating dispatch rule in LiveKit: %w", err)
	}

	route.Status.DispatchRuleID = info.SipDispatchRuleId
	route.Status.TrunkIDs = trunkIDs
	route.Status.ObservedGeneration = route.Generation
	now := metav1.Now()
	route.Status.LastSyncedAt = &now

	r.setCondition(route, conditionSynced, metav1.ConditionTrue, "Created", "Dispatch rule created in LiveKit")
	r.setReadyCondition(route, true, "Ready", fmt.Sprintf("Agent %s routed via %d number(s)", route.Spec.AgentName, len(trunkIDs)))

	if err := r.Status().Update(ctx, route); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after create: %w", err)
	}

	log.Info("SIP dispatch rule created", "ruleID", info.SipDispatchRuleId, "agent", route.Spec.AgentName)
	return ctrl.Result{}, nil
}

func (r *AgentRouteReconciler) reconcileUpdate(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
	sipClient *lksdk.SIPClient,
	trunkIDs []string,
	roomConfig *livekit.RoomConfiguration,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if anything actually changed: spec generation or trunk IDs.
	specChanged := route.Status.ObservedGeneration != route.Generation
	trunkIDsChanged := !trunkIDsEqual(route.Status.TrunkIDs, trunkIDs)

	if !specChanged && !trunkIDsChanged {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP dispatch rule for agent",
		"agent", route.Spec.AgentName,
		"ruleID", route.Status.DispatchRuleID,
		"trunkIDs", trunkIDs,
		"specChanged", specChanged,
		"trunkIDsChanged", trunkIDsChanged,
	)

	ruleInfo := r.buildRuleInfo(route, trunkIDs)
	ruleInfo.SipDispatchRuleId = route.Status.DispatchRuleID
	ruleInfo.RoomConfig = roomConfig

	_, err := sipClient.UpdateSIPDispatchRule(ctx, &livekit.UpdateSIPDispatchRuleRequest{
		SipDispatchRuleId: route.Status.DispatchRuleID,
		Action: &livekit.UpdateSIPDispatchRuleRequest_Replace{
			Replace: ruleInfo,
		},
	})
	if err != nil {
		r.setCondition(route, conditionSynced, metav1.ConditionFalse, "UpdateFailed", err.Error())
		r.setReadyCondition(route, false, "UpdateFailed", err.Error())
		if statusErr := r.Status().Update(ctx, route); statusErr != nil {
			log.Error(statusErr, "failed to update status after update error")
		}
		return ctrl.Result{RequeueAfter: requeueOnError}, fmt.Errorf("updating dispatch rule in LiveKit: %w", err)
	}

	route.Status.TrunkIDs = trunkIDs
	route.Status.ObservedGeneration = route.Generation
	now := metav1.Now()
	route.Status.LastSyncedAt = &now

	r.setCondition(route, conditionSynced, metav1.ConditionTrue, "Updated", "Dispatch rule updated in LiveKit")
	r.setReadyCondition(route, true, "Ready", fmt.Sprintf("Agent %s routed via %d number(s)", route.Spec.AgentName, len(trunkIDs)))

	if err := r.Status().Update(ctx, route); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status after update: %w", err)
	}

	log.Info("SIP dispatch rule updated", "ruleID", route.Status.DispatchRuleID, "agent", route.Spec.AgentName)
	return ctrl.Result{}, nil
}

func (r *AgentRouteReconciler) reconcileDelete(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(route, finalizerName) {
		return ctrl.Result{}, nil
	}

	if route.Status.DispatchRuleID != "" {
		sipClient, err := resolveLiveKitClient(ctx, r.Client, route.Namespace, route.Spec.LivekitRef)
		if err != nil {
			log.Error(err, "failed to resolve LiveKit client for deletion, proceeding with finalizer removal")
		} else {
			log.Info("Deleting SIP dispatch rule from LiveKit", "ruleID", route.Status.DispatchRuleID)
			_, err := sipClient.DeleteSIPDispatchRule(ctx, &livekit.DeleteSIPDispatchRuleRequest{
				SipDispatchRuleId: route.Status.DispatchRuleID,
			})
			if err != nil {
				log.Error(err, "failed to delete dispatch rule from LiveKit, proceeding with finalizer removal")
			}
		}
	}

	controllerutil.RemoveFinalizer(route, finalizerName)
	if err := r.Update(ctx, route); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	log.Info("AgentRoute deleted", "agent", route.Spec.AgentName)
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

// ─── LiveKit request builders ────────────────────────────────────────────────

func (r *AgentRouteReconciler) buildRuleInfo(
	route *sipv1alpha1.AgentRoute,
	trunkIDs []string,
) *livekit.SIPDispatchRuleInfo {
	info := &livekit.SIPDispatchRuleInfo{
		Name:            fmt.Sprintf("agentroute-%s", route.Name),
		TrunkIds:        trunkIDs,
		HidePhoneNumber: route.Spec.HidePhoneNumber,
		KrispEnabled:    route.Spec.KrispEnabled,
	}

	if route.Spec.MediaEncryption != "" {
		info.MediaEncryption = mediaEncryptionToProto(route.Spec.MediaEncryption)
	}

	// AgentRoute always uses "individual" dispatch: one room per call,
	// prefixed with the agent name. This is the natural pattern for
	// agent-handled phone calls.
	info.Rule = &livekit.SIPDispatchRule{
		Rule: &livekit.SIPDispatchRule_DispatchRuleIndividual{
			DispatchRuleIndividual: &livekit.SIPDispatchRuleIndividual{
				RoomPrefix: fmt.Sprintf("call-%s-", route.Spec.AgentName),
			},
		},
	}

	return info
}

// buildRoomConfig builds the RoomConfiguration for the dispatch rule.
// It always includes the agent dispatch; optionally includes egress config.
func (r *AgentRouteReconciler) buildRoomConfig(
	ctx context.Context,
	route *sipv1alpha1.AgentRoute,
) (*livekit.RoomConfiguration, error) {
	config := &livekit.RoomConfiguration{}

	// Always dispatch the agent
	config.Agents = []*livekit.RoomAgentDispatch{
		{
			AgentName: route.Spec.AgentName,
			Metadata:  route.Spec.AgentMetadata,
		},
	}

	// Handle additional room config (egress, extra agents, etc.)
	if route.Spec.RoomConfig != nil {
		rc := route.Spec.RoomConfig

		// Add any extra agents from roomConfig
		for _, a := range rc.Agents {
			config.Agents = append(config.Agents, &livekit.RoomAgentDispatch{
				AgentName: a.AgentName,
				Metadata:  a.Metadata,
			})
		}

		// Egress
		if rc.Egress != nil && rc.Egress.Room != nil {
			roomEgress, err := r.buildRoomCompositeEgress(ctx, route.Namespace, rc.Egress.Room)
			if err != nil {
				return nil, fmt.Errorf("building room egress: %w", err)
			}
			config.Egress = &livekit.RoomEgress{
				Room: roomEgress,
			}
		}
	}

	return config, nil
}

func (r *AgentRouteReconciler) buildRoomCompositeEgress(
	ctx context.Context,
	namespace string,
	room *sipv1alpha1.RoomCompositeEgress,
) (*livekit.RoomCompositeEgressRequest, error) {
	req := &livekit.RoomCompositeEgressRequest{
		RoomName:  room.RoomName,
		AudioOnly: room.AudioOnly,
	}

	if room.AudioMixing == sipv1alpha1.AudioMixingDefault {
		req.AudioMixing = livekit.AudioMixing_DEFAULT_MIXING
	}

	for _, fo := range room.FileOutputs {
		output, err := r.buildEncodedFileOutput(ctx, namespace, &fo)
		if err != nil {
			return nil, err
		}
		req.FileOutputs = append(req.FileOutputs, output)
	}

	return req, nil
}

func (r *AgentRouteReconciler) buildEncodedFileOutput(
	ctx context.Context,
	namespace string,
	fo *sipv1alpha1.FileOutput,
) (*livekit.EncodedFileOutput, error) {
	egressConfig := &sipv1alpha1.EgressConfig{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      fo.EgressConfigRef.Name,
		Namespace: namespace,
	}, egressConfig); err != nil {
		return nil, fmt.Errorf("fetching EgressConfig %q: %w", fo.EgressConfigRef.Name, err)
	}

	output := &livekit.EncodedFileOutput{
		Filepath: fo.Filepath,
	}

	spec := egressConfig.Spec
	switch {
	case spec.S3 != nil:
		s3Upload, err := r.resolveS3Config(ctx, namespace, spec.S3)
		if err != nil {
			return nil, err
		}
		output.Output = &livekit.EncodedFileOutput_S3{S3: s3Upload}

	case spec.Azure != nil:
		azureUpload, err := r.resolveAzureConfig(ctx, namespace, spec.Azure)
		if err != nil {
			return nil, err
		}
		output.Output = &livekit.EncodedFileOutput_Azure{Azure: azureUpload}

	case spec.GCP != nil:
		gcpUpload, err := r.resolveGCPConfig(ctx, namespace, spec.GCP)
		if err != nil {
			return nil, err
		}
		output.Output = &livekit.EncodedFileOutput_Gcp{Gcp: gcpUpload}

	case spec.AliOSS != nil:
		aliUpload, err := r.resolveAliOSSConfig(ctx, namespace, spec.AliOSS)
		if err != nil {
			return nil, err
		}
		output.Output = &livekit.EncodedFileOutput_AliOSS{AliOSS: aliUpload}

	default:
		return nil, fmt.Errorf("EgressConfig %q has no storage backend configured", egressConfig.Name)
	}

	return output, nil
}

// ─── Secret resolution helpers (same pattern as SIPDispatchRule) ──────────────

func (r *AgentRouteReconciler) resolveSecretKey(
	ctx context.Context,
	namespace string,
	ref sipv1alpha1.SecretKeySelector,
	defaultKey string,
) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      ref.Name,
		Namespace: namespace,
	}, secret); err != nil {
		return "", fmt.Errorf("fetching secret %q: %w", ref.Name, err)
	}

	key := defaultKey
	if ref.Key != "" {
		key = ref.Key
	}

	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", key, ref.Name)
	}
	return string(val), nil
}

func (r *AgentRouteReconciler) resolveS3Config(
	ctx context.Context,
	namespace string,
	cfg *sipv1alpha1.S3Config,
) (*livekit.S3Upload, error) {
	accessKey, err := r.resolveSecretKey(ctx, namespace, cfg.AccessKeyRef, "access_key")
	if err != nil {
		return nil, fmt.Errorf("resolving S3 access key: %w", err)
	}

	secretKey, err := r.resolveSecretKey(ctx, namespace, cfg.SecretKeyRef, "secret")
	if err != nil {
		return nil, fmt.Errorf("resolving S3 secret: %w", err)
	}

	upload := &livekit.S3Upload{
		AccessKey:      accessKey,
		Secret:         secretKey,
		Region:         cfg.Region,
		Endpoint:       cfg.Endpoint,
		Bucket:         cfg.Bucket,
		ForcePathStyle: cfg.ForcePathStyle,
	}

	if cfg.SessionTokenRef != nil {
		token, err := r.resolveSecretKey(ctx, namespace, *cfg.SessionTokenRef, "session_token")
		if err != nil {
			return nil, fmt.Errorf("resolving S3 session token: %w", err)
		}
		upload.SessionToken = token
	}

	return upload, nil
}

func (r *AgentRouteReconciler) resolveAzureConfig(
	ctx context.Context,
	namespace string,
	cfg *sipv1alpha1.AzureConfig,
) (*livekit.AzureBlobUpload, error) {
	accountName, err := r.resolveSecretKey(ctx, namespace, cfg.AccountNameRef, "account_name")
	if err != nil {
		return nil, fmt.Errorf("resolving Azure account name: %w", err)
	}

	accountKey, err := r.resolveSecretKey(ctx, namespace, cfg.AccountKeyRef, "account_key")
	if err != nil {
		return nil, fmt.Errorf("resolving Azure account key: %w", err)
	}

	return &livekit.AzureBlobUpload{
		AccountName:   accountName,
		AccountKey:    accountKey,
		ContainerName: cfg.ContainerName,
	}, nil
}

func (r *AgentRouteReconciler) resolveGCPConfig(
	ctx context.Context,
	namespace string,
	cfg *sipv1alpha1.GCPConfig,
) (*livekit.GCPUpload, error) {
	credentials, err := r.resolveSecretKey(ctx, namespace, cfg.CredentialsRef, "credentials")
	if err != nil {
		return nil, fmt.Errorf("resolving GCP credentials: %w", err)
	}

	return &livekit.GCPUpload{
		Credentials: credentials,
		Bucket:      cfg.Bucket,
	}, nil
}

func (r *AgentRouteReconciler) resolveAliOSSConfig(
	ctx context.Context,
	namespace string,
	cfg *sipv1alpha1.AliOSSConfig,
) (*livekit.AliOSSUpload, error) {
	accessKey, err := r.resolveSecretKey(ctx, namespace, cfg.AccessKeyRef, "access_key")
	if err != nil {
		return nil, fmt.Errorf("resolving AliOSS access key: %w", err)
	}

	secretKey, err := r.resolveSecretKey(ctx, namespace, cfg.SecretKeyRef, "secret")
	if err != nil {
		return nil, fmt.Errorf("resolving AliOSS secret: %w", err)
	}

	return &livekit.AliOSSUpload{
		AccessKey: accessKey,
		Secret:    secretKey,
		Region:    cfg.Region,
		Endpoint:  cfg.Endpoint,
		Bucket:    cfg.Bucket,
	}, nil
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
// It watches AgentRoute resources and also watches SIPNumber resources,
// enqueueing the AgentRoutes that reference any changed SIPNumber.
func (r *AgentRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sipv1alpha1.AgentRoute{}).
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
