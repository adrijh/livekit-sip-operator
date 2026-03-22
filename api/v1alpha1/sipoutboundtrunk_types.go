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

// SIPTransport defines the SIP transport protocol.
// +kubebuilder:validation:Enum=auto;udp;tcp;tls
type SIPTransport string

const (
	SIPTransportAuto SIPTransport = "auto"
	SIPTransportUDP  SIPTransport = "udp"
	SIPTransportTCP  SIPTransport = "tcp"
	SIPTransportTLS  SIPTransport = "tls"
)

// SIPOutboundTrunkSpec defines the desired state of a SIP outbound trunk.
type SIPOutboundTrunkSpec struct {
	// Reference to a Secret containing LiveKit server credentials (keys: url, api-key, api-secret).
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

	// Name of the trunk.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Initial metadata to assign to the trunk.
	// +optional
	Metadata string `json:"metadata,omitempty"`

	// Hostname or IP that the SIP INVITE is sent to.
	// +kubebuilder:validation:Required
	Address string `json:"address"`

	// ISO 3166-1 alpha-2 country code for the destination.
	// +optional
	DestinationCountry string `json:"destinationCountry,omitempty"`

	// Transport protocol (auto, udp, tcp, tls).
	// +optional
	Transport SIPTransport `json:"transport,omitempty"`

	// Phone numbers associated with the trunk. An empty list is valid.
	Numbers []string `json:"numbers"`

	// Reference to a Secret containing auth credentials (keys: username, password).
	// +optional
	AuthSecretRef *SecretReference `json:"authSecretRef,omitempty"`

	// SIP X-* headers to include in INVITE requests.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// Map SIP X-* header names from 200 OK to participant attribute names.
	// +optional
	HeadersToAttributes map[string]string `json:"headersToAttributes,omitempty"`

	// Map participant attributes to SIP headers on INVITE.
	// +optional
	AttributesToHeaders map[string]string `json:"attributesToHeaders,omitempty"`

	// How SIP headers should be mapped to attributes.
	// +optional
	IncludeHeaders SIPHeaderOptions `json:"includeHeaders,omitempty"`

	// Media encryption mode.
	// +optional
	MediaEncryption SIPMediaEncryption `json:"mediaEncryption,omitempty"`

	// Custom hostname for the From SIP header.
	// +optional
	FromHost string `json:"fromHost,omitempty"`
}

// SIPOutboundTrunkStatus defines the observed state of a SIP outbound trunk.
type SIPOutboundTrunkStatus struct {
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

// SIPOutboundTrunk is the Schema for the sipoutboundtrunks API.
type SIPOutboundTrunk struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SIPOutboundTrunkSpec   `json:"spec,omitempty"`
	Status SIPOutboundTrunkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SIPOutboundTrunkList contains a list of SIPOutboundTrunk.
type SIPOutboundTrunkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SIPOutboundTrunk `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SIPOutboundTrunk{}, &SIPOutboundTrunkList{})
}
