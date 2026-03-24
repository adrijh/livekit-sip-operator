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

// SIPNumberReference references a SIPNumber resource by name (same namespace).
type SIPNumberReference struct {
	// Name of the SIPNumber resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// AgentRouteSpec defines the desired state of AgentRoute.
// An AgentRoute binds one or more SIPNumbers to an agent by creating
// a SIP dispatch rule whose trunk_ids list is kept in sync with the
// referenced SIPNumbers' trunk IDs.
type AgentRouteSpec struct {
	// Reference to the LiveKit server and API credentials.
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

	// Name of the agent to dispatch calls to.
	// +kubebuilder:validation:Required
	AgentName string `json:"agentName"`

	// References to SIPNumber resources whose trunk IDs should be
	// included in the dispatch rule.
	// +kubebuilder:validation:MinItems=1
	NumberRefs []SIPNumberReference `json:"numberRefs"`

	// Hide the caller's phone number from participants.
	// +optional
	HidePhoneNumber bool `json:"hidePhoneNumber,omitempty"`

	// Enable Krisp noise cancellation.
	// +optional
	KrispEnabled bool `json:"krispEnabled,omitempty"`

	// Media encryption mode.
	// +optional
	MediaEncryption SIPMediaEncryption `json:"mediaEncryption,omitempty"`

	// Metadata to pass to the dispatched agent.
	// +optional
	AgentMetadata string `json:"agentMetadata,omitempty"`

	// Room configuration applied when the dispatch rule creates or joins a room.
	// +optional
	RoomConfig *RoomConfig `json:"roomConfig,omitempty"`
}

// AgentRouteStatus defines the observed state of AgentRoute.
type AgentRouteStatus struct {
	// The LiveKit dispatch rule ID returned after creation.
	// +optional
	DispatchRuleID string `json:"dispatchRuleID,omitempty"`

	// Trunk IDs currently synced into the dispatch rule.
	// +optional
	TrunkIDs []string `json:"trunkIDs,omitempty"`

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
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentName`
// +kubebuilder:printcolumn:name="Rule ID",type=string,JSONPath=`.status.dispatchRuleID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentRoute is the Schema for the agentroutes API.
// It binds one or more SIPNumbers to a LiveKit agent by managing
// a SIP dispatch rule with the correct trunk IDs.
type AgentRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentRouteSpec   `json:"spec,omitempty"`
	Status AgentRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentRouteList contains a list of AgentRoute.
type AgentRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentRoute{}, &AgentRouteList{})
}
