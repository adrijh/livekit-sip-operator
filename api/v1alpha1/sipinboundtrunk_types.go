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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SIPHeaderOptions defines how SIP headers should be mapped to attributes.
// +kubebuilder:validation:Enum=none;all;x-headers
type SIPHeaderOptions string

const (
	SIPHeaderOptionsNone     SIPHeaderOptions = "none"
	SIPHeaderOptionsAll      SIPHeaderOptions = "all"
	SIPHeaderOptionsXHeaders SIPHeaderOptions = "x-headers"
)

// SIPMediaEncryption defines the media encryption mode.
// +kubebuilder:validation:Enum=disable;allow;require
type SIPMediaEncryption string

const (
	SIPMediaEncryptionDisable SIPMediaEncryption = "disable"
	SIPMediaEncryptionAllow   SIPMediaEncryption = "allow"
	SIPMediaEncryptionRequire SIPMediaEncryption = "require"
)

// SecretReference holds a reference to a Kubernetes Secret.
type SecretReference struct {
	// Name of the Secret in the same namespace.
	Name string `json:"name"`
}

// LivekitReference holds a reference to a LiveKit server and its credentials.
type LivekitReference struct {
	// Name of the LiveKit Kubernetes Service (in the same namespace).
	// The operator connects via http://<service>:<port>.
	// +kubebuilder:validation:Required
	Service string `json:"service"`

	// Port of the LiveKit HTTP API on the Service.
	// +kubebuilder:default=7880
	// +optional
	Port int32 `json:"port,omitempty"`

	// Name of the Secret containing LiveKit API credentials (keys: LIVEKIT_API_KEY, LIVEKIT_API_SECRET).
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`
}

// SIPInboundTrunkSpec defines the desired state of a SIP inbound trunk.
type SIPInboundTrunkSpec struct {
	// Reference to the LiveKit server and API credentials.
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

	// Name of the trunk.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Initial metadata to assign to the trunk. Added to every SIP participant that uses it.
	// +optional
	Metadata string `json:"metadata,omitempty"`

	// Phone numbers associated with the trunk. An empty list is valid.
	Numbers []string `json:"numbers"`

	// IP addresses or CIDR blocks allowed to use the trunk.
	// +optional
	AllowedAddresses []string `json:"allowedAddresses,omitempty"`

	// Phone numbers allowed to use the trunk. If empty, access must be limited
	// via auth credentials or allowedAddresses.
	// +optional
	AllowedNumbers []string `json:"allowedNumbers,omitempty"`

	// Reference to a Secret containing auth credentials (keys: username, password).
	// +optional
	AuthSecretRef *SecretReference `json:"authSecretRef,omitempty"`

	// SIP X-* headers to include in INVITE requests.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// Map SIP X-* header names to participant attribute names.
	// +optional
	HeadersToAttributes map[string]string `json:"headersToAttributes,omitempty"`

	// Map participant attributes to SIP headers on INVITE.
	// +optional
	AttributesToHeaders map[string]string `json:"attributesToHeaders,omitempty"`

	// How SIP headers should be mapped to attributes.
	// +optional
	IncludeHeaders SIPHeaderOptions `json:"includeHeaders,omitempty"`

	// Maximum time for the call to ring before timing out.
	// +optional
	RingingTimeout *metav1.Duration `json:"ringingTimeout,omitempty"`

	// Maximum call duration.
	// +optional
	MaxCallDuration *metav1.Duration `json:"maxCallDuration,omitempty"`

	// Enable Krisp noise cancellation for the caller.
	// +optional
	KrispEnabled bool `json:"krispEnabled,omitempty"`

	// Media encryption mode.
	// +optional
	MediaEncryption SIPMediaEncryption `json:"mediaEncryption,omitempty"`
}

// SIPInboundTrunkStatus defines the observed state of a SIP inbound trunk.
type SIPInboundTrunkStatus struct {
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
// +kubebuilder:printcolumn:name="Trunk ID",type=string,JSONPath=`.status.trunkID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SIPInboundTrunk is the Schema for the sipinboundtrunks API.
type SIPInboundTrunk struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SIPInboundTrunkSpec   `json:"spec,omitempty"`
	Status SIPInboundTrunkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SIPInboundTrunkList contains a list of SIPInboundTrunk.
type SIPInboundTrunkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SIPInboundTrunk `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SIPInboundTrunk{}, &SIPInboundTrunkList{})
}
