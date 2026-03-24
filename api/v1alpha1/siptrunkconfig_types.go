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

// SIPTrunkConfigReference references a SIPTrunkConfig resource by name (same namespace).
type SIPTrunkConfigReference struct {
	// Name of the SIPTrunkConfig resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// SIPTrunkConfigSpec defines the desired state of SIPTrunkConfig.
// It holds shared trunk configuration that multiple SIPNumbers can inherit,
// avoiding repetition of provider-specific settings like allowed addresses and auth.
type SIPTrunkConfigSpec struct {
	// Reference to the LiveKit server and API credentials.
	// +kubebuilder:validation:Required
	LivekitRef LivekitReference `json:"livekitRef"`

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

// SIPTrunkConfigStatus defines the observed state of SIPTrunkConfig.
type SIPTrunkConfigStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SIPTrunkConfig is the Schema for the siptrunkconfigs API.
// It defines shared SIP trunk configuration (provider credentials, allowed IPs, etc.)
// that can be referenced by multiple SIPNumber resources.
type SIPTrunkConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SIPTrunkConfigSpec   `json:"spec,omitempty"`
	Status SIPTrunkConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SIPTrunkConfigList contains a list of SIPTrunkConfig.
type SIPTrunkConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SIPTrunkConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SIPTrunkConfig{}, &SIPTrunkConfigList{})
}
