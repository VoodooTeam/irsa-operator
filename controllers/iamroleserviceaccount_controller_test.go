package controllers_test

import (
	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("IamRoleServiceAccount validity check", func() {
	Context("if the spec.policy is empty", func() {
		invalidPolicySpec := api.PolicySpec{}

		It("fails at submission", func() {
			Expect(
				api.NewIamRoleServiceAccount(validName(), testns, invalidPolicySpec).Validate(),
			).ShouldNot(Succeed())
		})
	})

	Context("if the spec.policy is ok", func() {
		validPolicy := api.PolicySpec{
			Statement: []api.StatementSpec{
				{Resource: "arn:aws:s3:::my_corporate_bucket/exampleobject.png", Action: []string{"act1"}},
			},
		}

		Context("if everything else is also ok", func() {
			irsa := api.NewIamRoleServiceAccount(validName(), testns, validPolicy)
			It("it passes validation", func() {
				Expect(irsa.Validate()).Should(Succeed())
			})
		})
	})
})
