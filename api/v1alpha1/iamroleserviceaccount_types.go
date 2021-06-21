package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewIamRoleServiceAccount is the IamRoleServiceAccount constructor
func NewIamRoleServiceAccount(name, ns string, policyspec PolicySpec) *IamRoleServiceAccount {
	return &IamRoleServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "irsa.voodoo.io/v1alpha1",
			Kind:       "IamRoleServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: IamRoleServiceAccountSpec{
			Policy: policyspec,
		},
	}
}

func (irsa IamRoleServiceAccount) FullName() string {
	return irsa.ObjectMeta.Namespace + "/" + irsa.ObjectMeta.Name
}

// HasStatus is used in tests, should be moved there
func (irsa IamRoleServiceAccount) HasStatus(st fmt.Stringer) bool {
	return irsa.Status.Condition.String() == st.String()
}

// IsPendingDeletion helps us to detect if the resource should be deleted
func (irsa IamRoleServiceAccount) IsPendingDeletion() bool {
	return !irsa.ObjectMeta.DeletionTimestamp.IsZero()
}

// Validate returns an error if the IamRoleServiceAccountSpec is not valid
func (irsa IamRoleServiceAccount) Validate() error {
	return irsa.Spec.Policy.Validate()
}

// IamRoleServiceAccountSpec defines the desired state of IamRoleServiceAccount
type IamRoleServiceAccountSpec struct {
	Policy PolicySpec `json:"policy"`
}

// IamRoleServiceAccountStatus defines the observed state of IamRoleServiceAccount
type IamRoleServiceAccountStatus struct {
	Condition IrsaCondition `json:"condition"`
	Reason    string        `json:"reason,omitempty"`
}

type IrsaCondition string

var (
	IrsaSubmitted      IrsaCondition = ""
	IrsaPending        IrsaCondition = "pending"
	IrsaSaNameConflict IrsaCondition = "saNameConflict"
	IrsaForbidden      IrsaCondition = "forbidden"
	IrsaFailed         IrsaCondition = "failed"
	IrsaProgressing    IrsaCondition = "progressing"
	IrsaOK             IrsaCondition = "created"
)

// String is just used for comparison in HasStatus
func (i IrsaCondition) String() string {
	return string(i)
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// IamRoleServiceAccount is the Schema for the iamroleserviceaccounts API
type IamRoleServiceAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IamRoleServiceAccountSpec   `json:"spec,omitempty"`
	Status IamRoleServiceAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IamRoleServiceAccountList contains a list of IamRoleServiceAccount
type IamRoleServiceAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IamRoleServiceAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IamRoleServiceAccount{}, &IamRoleServiceAccountList{})
}
