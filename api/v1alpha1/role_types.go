package v1alpha1

import (
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewRole constructs a Role, setting mandatory fields for us
func NewRole(name, ns string) *Role {
	return &Role{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "irsa.voodoo.io/v1alpha1",
			Kind:       "Role",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: RoleSpec{
			ServiceAccountName: name,
		},
	}
}

// HasStatus is used in tests, should be moved there
func (r Role) HasStatus(st fmt.Stringer) bool {
	return r.Status.Condition.String() == st.String()
}

// Validate returns an error if the Policy is not valid
func (r Role) Validate(cN string) error {
	if err := r.Spec.Validate(); err != nil {
		return err
	}

	awsName := r.AwsName(cN)
	if len(awsName) > 64 {
		return fmt.Errorf("aws name is too long : %s", awsName)
	}

	return nil
}

// AwsName is the name the resource will have on AWS
// It must be unique per AWS account thus the naming convention
func (r Role) AwsName(cN string) string {
	return fmt.Sprintf("irsa-op-%s-%s-%s", cN, r.ObjectMeta.Namespace, r.ObjectMeta.Name)
}

// IsPendingDeletion helps us to detect if the resource should be deleted
func (r Role) IsPendingDeletion() bool {
	return !r.ObjectMeta.DeletionTimestamp.IsZero()
}

// RoleSpec defines the desired state of Role
type RoleSpec struct {
	ServiceAccountName string `json:"serviceAccountName"`

	PolicyARN string `json:"policyarn,omitempty"`
	RoleARN   string `json:"rolearn,omitempty"`
}

// Validate returns an error if the RoleSpec is not valid
func (spec RoleSpec) Validate() error {
	if spec.ServiceAccountName == "" {
		return errors.New("empty string provided as spec.ServiceAccountName")
	}

	return nil
}

// RoleStatus defines the observed state of Role
type RoleStatus struct {
	Condition CrCondition `json:"condition"`
	Reason    string      `json:"reason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Role is the Schema for the awsroles API
type Role struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RoleSpec   `json:"spec,omitempty"`
	Status RoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RoleList contains a list of Role
type RoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Role `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Role{}, &RoleList{})
}
