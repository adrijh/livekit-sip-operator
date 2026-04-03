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

// DeploymentReference references a Kubernetes Deployment in the same namespace.
type DeploymentReference struct {
	// Name of the Deployment resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// LivekitAgentSpec defines the desired state of LivekitAgent.
// A LivekitAgent represents an agent registered with a LiveKit server,
// providing a discoverable registry entry and optional health tracking
// via a referenced Kubernetes workload.
type LivekitAgentSpec struct {
	// Name of the agent as registered in LiveKit.
	// +kubebuilder:validation:Required
	AgentName string `json:"agentName"`

	// Reference to the Kubernetes Deployment running the agent.
	// When set, the controller tracks the Deployment's health and
	// reflects replica status.
	// +optional
	DeploymentRef *DeploymentReference `json:"deploymentRef,omitempty"`

	// Arbitrary metadata about the agent (e.g. language, capabilities).
	// Useful for backend discovery and filtering.
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`
}

// LivekitAgentStatus defines the observed state of LivekitAgent.
type LivekitAgentStatus struct {
	// Whether the agent workload is healthy (all replicas ready).
	// Always true if no workloadRef is set.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// Total desired replicas from the referenced workload.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Number of ready replicas from the referenced workload.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

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
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Replicas",type=string,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.availableReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LivekitAgent is the Schema for the livekitagents API.
// It represents a LiveKit agent, providing a discoverable registry entry
// with optional health tracking via a referenced Kubernetes workload.
type LivekitAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LivekitAgentSpec   `json:"spec,omitempty"`
	Status LivekitAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LivekitAgentList contains a list of LivekitAgent.
type LivekitAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LivekitAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LivekitAgent{}, &LivekitAgentList{})
}
