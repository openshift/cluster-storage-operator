package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterStorageList is a list of ClusterStorages
type ClusterStorageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []ClusterStorage `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterStorage is a cluster's storage
type ClusterStorage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              ClusterStorageSpec   `json:"spec"`
	Status            ClusterStorageStatus `json:"status,omitempty"`
}

// ClusterStorageSpec is a ClusterStorage's spec
type ClusterStorageSpec struct {
	// Fill me
}

// ClusterStorageStatus is a ClusterStorage's status
type ClusterStorageStatus struct {
	// Fill me
}
