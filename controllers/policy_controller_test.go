package controllers_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
)

var _ = Describe("Awspolicy validity check", func() {
	Context("When creating an Awspolicy", func() {
		clusterName := randString()
		Context("if the spec.statement is nil", func() {
			It("fails at submission", func() {
				Expect(
					api.NewPolicy(validName(), testns, nil).Validate(clusterName),
				).ShouldNot(Succeed())
			})
		})

		Context("if the spec.statement is an empty array", func() {
			name := validName()
			It("fails at validation", func() {
				Expect(
					api.NewPolicy(name, testns, []api.StatementSpec{}).Validate(clusterName),
				).ShouldNot(Succeed())
			})
		})

		Context("if the spec.statement[*].resource is not a valid ARN", func() {
			name := validName()

			It("fails at validation", func() {
				Expect(
					api.NewPolicy(name, testns, []api.StatementSpec{
						{Resource: "not an arn", Action: []string{"do something"}},
					}).Validate(clusterName),
				).ShouldNot(Succeed())
			})
		})

		Context("if the spec.statement[*].action is an empty array", func() {
			name := validName()
			validARN := "arn:aws:s3:::my_corporate_bucket/exampleobject.png"

			It("fails at validation", func() {
				Expect(
					api.NewPolicy(name, testns, []api.StatementSpec{
						{Resource: validARN, Action: []string{}},
					}).Validate(clusterName),
				).ShouldNot(Succeed())
			})
		})

		Context("if everything is ok", func() {
			name := validName()
			validARN := "arn:aws:s3:::my_corporate_bucket/exampleobject.png"

			It("passes the api submission", func() {
				Expect(
					api.NewPolicy(name, testns, []api.StatementSpec{
						{Resource: validARN, Action: []string{"an:action"}},
					}).Validate(clusterName),
				).Should(Succeed())
			})
		})
	})
})
