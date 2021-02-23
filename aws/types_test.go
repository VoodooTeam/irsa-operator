package aws_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	irsaws "github.com/VoodooTeam/irsa-operator/aws"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("PolicyDocument creation", func() {
	Context("given a valid policySpec", func() {
		validPolicy := api.PolicySpec{
			Statement: []api.StatementSpec{
				{Resource: "bla", Action: []string{"act1"}},
			},
		}

		It("generates a valid policy document", func() {
			expectedPolicyDocument := irsaws.PolicyDocument{
				Version: "2012-10-17",
				Statement: []irsaws.Statement{
					{
						Effect:   irsaws.StatementAllow,
						Resource: "bla",
						Action:   []string{"act1"},
					},
				},
			}
			policyJSON, err := irsaws.NewPolicyDocumentString(validPolicy)
			Expect(err).NotTo(HaveOccurred())

			genPolicy := &irsaws.PolicyDocument{}
			err = json.Unmarshal([]byte(policyJSON), genPolicy)
			Expect(err).NotTo(HaveOccurred())
			Expect(*genPolicy).Should(Equal(expectedPolicyDocument))
		})
	})

	Context("given a valid role", func() {
		expectedRoleDoc := irsaws.RoleDocument{
			Version: "2012-10-17",
			Statement: []irsaws.RoleStatement{
				{
					Effect: irsaws.StatementAllow,
					Principal: struct{ Federated string }{
						Federated: "arn:aws.iam::111122223333:oidc-provider/oidc.REGION.eks.amazonaws.com/CLUSTER_ID",
					},
					Action: "sts:AssumeRoleWithWebIdentity",
					Condition: struct {
						StringEquals map[string]string
					}{
						StringEquals: map[string]string{"oidc.REGION.eks.amazonaws.com/CLUSTER_ID:sub": "system:serviceaccount:namespace:serviceAccountName"},
					},
				},
			},
		}

		Context("role document", func() {
			It("generates a valid role document", func() {
				r := api.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
					},
					Spec: api.RoleSpec{
						ServiceAccountName: "serviceAccountName",
						PolicyARN:          "not used here",
					},
				}

				roleJSON, err := irsaws.NewAssumeRolePolicyDoc(r, "arn:aws.iam::111122223333:oidc-provider/oidc.REGION.eks.amazonaws.com/CLUSTER_ID")
				Expect(err).NotTo(HaveOccurred())

				genPolicy := &irsaws.RoleDocument{}
				err = json.Unmarshal([]byte(roleJSON), genPolicy)
				Expect(*genPolicy).Should(Equal(expectedRoleDoc))
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})
})
