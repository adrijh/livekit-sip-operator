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

// SIPNumberSpec defines the desired state of SIPNumber.
// A SIPNumber represents a single phone number (DID) and manages
// the lifecycle of the SIP inbound trunk backing it.
type SIPNumberSpec struct {
	// Reference to the LiveKit server and API credentials.
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

	// The phone number (DID) in E.164 format (e.g. "+15551234567").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\+[1-9]\d{1,14}$`
	Number string `json:"number"`

	// Reference to a Secret containing SIP auth credentials (keys: username, password).
	// +optional
	AuthSecretRef *SecretReference `json:"authSecretRef,omitempty"`

	// IP addresses or CIDR blocks allowed to use the trunk.
	// +optional
	AllowedAddresses []string `json:"allowedAddresses,omitempty"`

	// Enable Krisp noise cancellation for the caller.
	// +optional
	KrispEnabled bool `json:"krispEnabled,omitempty"`

	// Media encryption mode.
	// +optional
	MediaEncryption SIPMediaEncryption `json:"mediaEncryption,omitempty"`

	// Maximum time for the call to ring before timing out.
	// +optional
	RingingTimeout *metav1.Duration `json:"ringingTimeout,omitempty"`

	// Maximum call duration.
	// +optional
	MaxCallDuration *metav1.Duration `json:"maxCallDuration,omitempty"`
}

// SIPNumberStatus defines the observed state of SIPNumber.
type SIPNumberStatus struct {
	// The LiveKit trunk ID returned after creation.
	// +optional
	TrunkID string `json:"trunkID,omitempty"`

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
// +kubebuilder:printcolumn:name="Number",type=string,JSONPath=`.spec.number`
// +kubebuilder:printcolumn:name="Trunk ID",type=string,JSONPath=`.status.trunkID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SIPNumber is the Schema for the sipnumbers API.
// It represents a single phone number (DID) and manages the lifecycle
// of the underlying SIP inbound trunk in LiveKit.
type SIPNumber struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SIPNumberSpec   `json:"spec,omitempty"`
	Status SIPNumberStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SIPNumberList contains a list of SIPNumber.
type SIPNumberList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SIPNumber `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SIPNumber{}, &SIPNumberList{})
}
