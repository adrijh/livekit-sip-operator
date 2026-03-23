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
	// +kubebuilder:validation:Required
	AccessKeyRef SecretKeySelector `json:"accessKeyRef"`

	// Reference to a Secret and key for the secret key. Default key: "secret".
	// +kubebuilder:validation:Required
	SecretKeyRef SecretKeySelector `json:"secretKeyRef"`

	// Reference to a Secret and key for an optional session token. Default key: "session_token".
	// +optional
	SessionTokenRef *SecretKeySelector `json:"sessionTokenRef,omitempty"`
}

// AzureConfig defines Azure Blob Storage configuration.
type AzureConfig struct {
	// Azure storage container name.
	// +kubebuilder:validation:Required
	ContainerName string `json:"containerName"`

	// Reference to a Secret and key for the account name. Default key: "account_name".
	// +kubebuilder:validation:Required
	AccountNameRef SecretKeySelector `json:"accountNameRef"`

	// Reference to a Secret and key for the account key. Default key: "account_key".
	// +kubebuilder:validation:Required
	AccountKeyRef SecretKeySelector `json:"accountKeyRef"`
}

// GCPConfig defines Google Cloud Storage configuration.
type GCPConfig struct {
	// GCS bucket name.
	// +kubebuilder:validation:Required
	Bucket string `json:"bucket"`

	// Reference to a Secret and key for the service account JSON credentials. Default key: "credentials".
	// +kubebuilder:validation:Required
	CredentialsRef SecretKeySelector `json:"credentialsRef"`
}

// AliOSSConfig defines Alibaba Cloud OSS configuration.
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
	// +kubebuilder:validation:Required
	AccessKeyRef SecretKeySelector `json:"accessKeyRef"`

	// Reference to a Secret and key for the secret key. Default key: "secret".
	// +kubebuilder:validation:Required
	SecretKeyRef SecretKeySelector `json:"secretKeyRef"`
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
