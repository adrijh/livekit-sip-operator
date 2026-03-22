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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SIPDispatchRuleType defines the type of dispatch rule.
// +kubebuilder:validation:Enum=direct;individual;callee
type SIPDispatchRuleType string

const (
	SIPDispatchRuleTypeDirect     SIPDispatchRuleType = "direct"
	SIPDispatchRuleTypeIndividual SIPDispatchRuleType = "individual"
	SIPDispatchRuleTypeCallee     SIPDispatchRuleType = "callee"
)

// SIPDispatchRuleDirect routes calls to an existing room.
type SIPDispatchRuleDirect struct {
	// Name of the room to route calls to.
	// +kubebuilder:validation:Required
	RoomName string `json:"roomName"`

	// Optional PIN required to enter the room.
	// +optional
	Pin string `json:"pin,omitempty"`
}

// SIPDispatchRuleIndividual creates a new room for each call.
type SIPDispatchRuleIndividual struct {
	// Prefix for generated room names.
	// +kubebuilder:validation:Required
	RoomPrefix string `json:"roomPrefix"`

	// Optional PIN required.
	// +optional
	Pin string `json:"pin,omitempty"`

	// If true, don't append a random suffix to the room name.
	// +optional
	NoRandomness bool `json:"noRandomness,omitempty"`
}

// SIPDispatchRuleCallee routes based on the callee number.
type SIPDispatchRuleCallee struct {
	// Prefix for generated room names.
	// +kubebuilder:validation:Required
	RoomPrefix string `json:"roomPrefix"`

	// Optional PIN required.
	// +optional
	Pin string `json:"pin,omitempty"`

	// If true, append a random suffix to the room name.
	// +optional
	Randomize bool `json:"randomize,omitempty"`
}

// SIPDispatchRuleSpec defines the desired state of a SIP dispatch rule.
type SIPDispatchRuleSpec struct {
	// Reference to a Secret containing LiveKit server credentials (keys: url, api-key, api-secret).
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

	// Name of the dispatch rule.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// User-defined metadata.
	// +optional
	Metadata string `json:"metadata,omitempty"`

	// Type of dispatch rule.
	// +kubebuilder:validation:Required
	Type SIPDispatchRuleType `json:"type"`

	// Configuration for direct dispatch (type=direct).
	// +optional
	Direct *SIPDispatchRuleDirect `json:"direct,omitempty"`

	// Configuration for individual dispatch (type=individual).
	// +optional
	Individual *SIPDispatchRuleIndividual `json:"individual,omitempty"`

	// Configuration for callee dispatch (type=callee).
	// +optional
	Callee *SIPDispatchRuleCallee `json:"callee,omitempty"`

	// Trunk IDs that are accepted for this rule. If empty, all trunks match.
	// +optional
	TrunkIDs []string `json:"trunkIDs,omitempty"`

	// Hide the caller's phone number from participants.
	// +optional
	HidePhoneNumber bool `json:"hidePhoneNumber,omitempty"`

	// Phone numbers that this rule accepts calls from.
	// +optional
	InboundNumbers []string `json:"inboundNumbers,omitempty"`

	// Phone numbers that this rule accepts calls to.
	// +optional
	Numbers []string `json:"numbers,omitempty"`

	// Attributes to set on SIP participants.
	// +optional
	Attributes map[string]string `json:"attributes,omitempty"`

	// Room preset (cloud-only).
	// +optional
	RoomPreset string `json:"roomPreset,omitempty"`

	// Enable Krisp noise cancellation.
	// +optional
	KrispEnabled bool `json:"krispEnabled,omitempty"`

	// Media encryption mode.
	// +optional
	MediaEncryption SIPMediaEncryption `json:"mediaEncryption,omitempty"`
}

// SIPDispatchRuleStatus defines the observed state of a SIP dispatch rule.
type SIPDispatchRuleStatus struct {
	// The LiveKit dispatch rule ID returned after creation.
	// +optional
	DispatchRuleID string `json:"dispatchRuleID,omitempty"`

	// Human-readable message about the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// Last time the resource was successfully reconciled.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// The generation last observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Standard conditions for the resource.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Rule ID",type=string,JSONPath=`.status.dispatchRuleID`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SIPDispatchRule is the Schema for the sipdispatchrules API.
type SIPDispatchRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SIPDispatchRuleSpec   `json:"spec,omitempty"`
	Status SIPDispatchRuleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SIPDispatchRuleList contains a list of SIPDispatchRule.
type SIPDispatchRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SIPDispatchRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SIPDispatchRule{}, &SIPDispatchRuleList{})
}
