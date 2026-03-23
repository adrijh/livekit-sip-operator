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

// SIPDispatchRuleReconciler reconciles a SIPDispatchRule object.
type SIPDispatchRuleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=sipdispatchrules/finalizers,verbs=update
// +kubebuilder:rbac:groups=sip.livekit-sip.io,resources=egressconfigs,verbs=get;list;watch
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

	// ── 5. Resolve room config (EgressConfig references + secrets) ───────
	var roomConfig *livekit.RoomConfiguration
	if rule.Spec.RoomConfig != nil {
		var err error
		roomConfig, err = r.buildRoomConfig(ctx, rule)
		if err != nil {
			r.setCondition(rule, conditionSynced, metav1.ConditionFalse, "RoomConfigError", err.Error())
			r.setReadyCondition(rule, false, "RoomConfigError", err.Error())
			if statusErr := r.Status().Update(ctx, rule); statusErr != nil {
				log.Error(statusErr, "failed to update status after room config error")
			}
			return ctrl.Result{RequeueAfter: requeueOnError}, nil
		}
	}

	// ── 6. Create or Update the dispatch rule in LiveKit ─────────────────
	if rule.Status.DispatchRuleID == "" {
		return r.reconcileCreate(ctx, rule, sipClient, roomConfig)
	}
	return r.reconcileUpdate(ctx, rule, sipClient, roomConfig)
}

// ─── Reconcile sub-routines ──────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) reconcileCreate(
	ctx context.Context,
	rule *sipv1alpha1.SIPDispatchRule,
	sipClient *lksdk.SIPClient,
	roomConfig *livekit.RoomConfiguration,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating SIP dispatch rule in LiveKit", "name", rule.Spec.Name)

	ruleInfo := r.buildRuleInfo(rule)
	ruleInfo.RoomConfig = roomConfig

	req := &livekit.CreateSIPDispatchRuleRequest{
		DispatchRule: ruleInfo,
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
	roomConfig *livekit.RoomConfiguration,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if rule.Status.ObservedGeneration == rule.Generation {
		return ctrl.Result{}, nil
	}

	log.Info("Updating SIP dispatch rule in LiveKit", "ruleID", rule.Status.DispatchRuleID)

	ruleInfo := r.buildRuleInfo(rule)
	ruleInfo.SipDispatchRuleId = rule.Status.DispatchRuleID
	ruleInfo.RoomConfig = roomConfig

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

// ─── Room config builders ────────────────────────────────────────────────────

func (r *SIPDispatchRuleReconciler) buildRoomConfig(
	ctx context.Context,
	rule *sipv1alpha1.SIPDispatchRule,
) (*livekit.RoomConfiguration, error) {
	rc := rule.Spec.RoomConfig
	config := &livekit.RoomConfiguration{}

	// Agents
	for _, a := range rc.Agents {
		config.Agents = append(config.Agents, &livekit.RoomAgentDispatch{
			AgentName: a.AgentName,
			Metadata:  a.Metadata,
		})
	}

	// Egress
	if rc.Egress != nil && rc.Egress.Room != nil {
		roomEgress, err := r.buildRoomCompositeEgress(ctx, rule.Namespace, rc.Egress.Room)
		if err != nil {
			return nil, fmt.Errorf("building room egress: %w", err)
		}
		config.Egress = &livekit.RoomEgress{
			Room: roomEgress,
		}
	}

	return config, nil
}

func (r *SIPDispatchRuleReconciler) buildRoomCompositeEgress(
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

func (r *SIPDispatchRuleReconciler) buildEncodedFileOutput(
	ctx context.Context,
	namespace string,
	fo *sipv1alpha1.FileOutput,
) (*livekit.EncodedFileOutput, error) {
	// Resolve the EgressConfig resource
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

// resolveSecretKey reads a single key from a Secret, using the provided default if no override is set.
func (r *SIPDispatchRuleReconciler) resolveSecretKey(
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

func (r *SIPDispatchRuleReconciler) resolveS3Config(
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

func (r *SIPDispatchRuleReconciler) resolveAzureConfig(
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

func (r *SIPDispatchRuleReconciler) resolveGCPConfig(
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

func (r *SIPDispatchRuleReconciler) resolveAliOSSConfig(
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
