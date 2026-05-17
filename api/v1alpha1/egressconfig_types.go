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

// SecretKeySelector references a specific key within a Secret.
type SecretKeySelector struct {
	// Name of the Secret in the same namespace.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key within the Secret. If empty, the default key for the field is used.
	// +optional
	Key string `json:"key,omitempty"`
}

// S3Config defines S3-compatible storage configuration.
//
// Credentials are optional. When the credential refs are omitted, the operator
// will not include any S3 fields in the egress request and the LiveKit Egress
// worker will use the credentials configured in its own `storage:` block.
// When the refs are provided, both AccessKeyRef and SecretKeyRef must be set.
type S3Config struct {
	// AWS region.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// Custom S3 endpoint (for MinIO, etc.).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// S3 bucket name.
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`

	// Force path-style URLs (required for MinIO).
	// +optional
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`

	// Reference to a Secret and key for the access key. Default key: "access_key".
	// +optional
	AccessKeyRef *SecretKeySelector `json:"accessKeyRef,omitempty"`

	// Reference to a Secret and key for the secret key. Default key: "secret".
	// +optional
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty"`

	// Reference to a Secret and key for an optional session token. Default key: "session_token".
	// +optional
	SessionTokenRef *SecretKeySelector `json:"sessionTokenRef,omitempty"`
}

// AzureConfig defines Azure Blob Storage configuration.
//
// Credentials are optional. When the credential refs are omitted, the operator
// will not include any Azure fields in the egress request and the LiveKit Egress
// worker will use the credentials configured in its own `storage:` block.
// When the refs are provided, both AccountNameRef and AccountKeyRef must be set.
type AzureConfig struct {
	// Azure storage container name.
	// +kubebuilder:validation:Required
	ContainerName string `json:"containerName"`

	// Reference to a Secret and key for the account name. Default key: "account_name".
	// +optional
	AccountNameRef *SecretKeySelector `json:"accountNameRef,omitempty"`

	// Reference to a Secret and key for the account key. Default key: "account_key".
	// +optional
	AccountKeyRef *SecretKeySelector `json:"accountKeyRef,omitempty"`
}

// GCPConfig defines Google Cloud Storage configuration.
//
// Credentials are optional. When CredentialsRef is omitted, the operator will
// not include any GCP fields in the egress request and the LiveKit Egress
// worker will use the credentials configured in its own `storage:` block.
type GCPConfig struct {
	// GCS bucket name.
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`

	// Reference to a Secret and key for the service account JSON credentials. Default key: "credentials".
	// +optional
	CredentialsRef *SecretKeySelector `json:"credentialsRef,omitempty"`
}

// AliOSSConfig defines Alibaba Cloud OSS configuration.
//
// Credentials are optional. When the credential refs are omitted, the operator
// will not include any AliOSS fields in the egress request and the LiveKit
// Egress worker will use the credentials configured in its own `storage:` block.
// When the refs are provided, both AccessKeyRef and SecretKeyRef must be set.
type AliOSSConfig struct {
	// OSS region.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// Custom OSS endpoint.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// OSS bucket name.
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`

	// Reference to a Secret and key for the access key. Default key: "access_key".
	// +optional
	AccessKeyRef *SecretKeySelector `json:"accessKeyRef,omitempty"`

	// Reference to a Secret and key for the secret key. Default key: "secret".
	// +optional
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// EgressConfigSpec defines a reusable egress storage backend configuration.
// Exactly one storage backend must be specified.
type EgressConfigSpec struct {
	// S3-compatible storage configuration.
	// +optional
	S3 *S3Config `json:"s3,omitempty"`

	// Azure Blob Storage configuration.
	// +optional
	Azure *AzureConfig `json:"azure,omitempty"`

	// Google Cloud Storage configuration.
	// +optional
	GCP *GCPConfig `json:"gcp,omitempty"`

	// Alibaba Cloud OSS configuration.
	// +optional
	AliOSS *AliOSSConfig `json:"aliOSS,omitempty"`
}

// EgressConfigStatus defines the observed state of EgressConfig.
type EgressConfigStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EgressConfig is the Schema for the egressconfigs API.
// It defines a reusable storage backend that can be referenced by SIPDispatchRule room egress configurations.
type EgressConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressConfigSpec   `json:"spec,omitempty"`
	Status EgressConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EgressConfigList contains a list of EgressConfig.
type EgressConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EgressConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressConfig{}, &EgressConfigList{})
}
