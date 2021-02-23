package aws_test

import (
	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var validPolicy = api.NewPolicy("name", "testns", []api.StatementSpec{
	{Resource: "arn:aws:s3:::my_corporate_bucket/exampleobject.png", Action: []string{"an:action"}},
})
var _ = Describe("policy", func() {
	It("given a valid policy", func() {

		By("creating the policy it without error")
		err := awsmngr.CreatePolicy(*validPolicy)
		Expect(err).NotTo(HaveOccurred())

		By("ensuring the creation is idempotent")
		err = awsmngr.CreatePolicy(*validPolicy)
		Expect(err).NotTo(HaveOccurred())

		By("retrieving the policy ARN")
		policyARN, err := awsmngr.GetPolicyARN(validPolicy.PathPrefix(clusterName), validPolicy.AwsName(clusterName))
		Expect(err).NotTo(HaveOccurred())
		Expect(policyARN).NotTo(BeEmpty())

		By("deleting it")
		Expect(policyARN).NotTo(BeEmpty())
		err = awsmngr.DeletePolicy(policyARN)
		Expect(err).NotTo(HaveOccurred())

		By("ensuring deletion is also idempotent")
		Expect(policyARN).NotTo(BeEmpty())
		err = awsmngr.DeletePolicy(policyARN)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("role", func() {
	role := api.NewRole("name", "testns", "serviceaccountname")
	Context("given a valid role", func() {
		It("doesn't exist yet", func() {
			exists, err := awsmngr.RoleExists(role.AwsName(clusterName))
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})

		Context("creation", func() {
			It("can create it without error", func() {
				err := awsmngr.CreateRole(*role)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("idempotency", func() {
				It("creation is idempotent", func() {
					err := awsmngr.CreateRole(*role)
					Expect(err).NotTo(HaveOccurred())
				})

				Context("exists check", func() {
					It("can be checked for existing", func() {
						exists, err := awsmngr.RoleExists(role.AwsName(clusterName))
						Expect(err).NotTo(HaveOccurred())
						Expect(exists).To(BeTrue())
					})

					Context("policies can be attached", func() {
						policyARN := ""
						It("the policy must exist first", func() {
							var err error
							err = awsmngr.CreatePolicy(*validPolicy)
							Expect(err).NotTo(HaveOccurred())

							policyARN, err = awsmngr.GetPolicyARN(validPolicy.PathPrefix(clusterName), validPolicy.AwsName(clusterName))
							Expect(err).NotTo(HaveOccurred())
							Expect(policyARN).NotTo(BeEmpty())
						})

						Context("when done", func() {
							It("actually can be attached", func() {
								err := awsmngr.AttachRolePolicy(role.AwsName(clusterName), policyARN)
								Expect(err).NotTo(HaveOccurred())
							})

							It("and retrieved", func() {
								attached, err := awsmngr.GetAttachedRolePoliciesARNs(role.AwsName(clusterName))
								Expect(err).NotTo(HaveOccurred())
								Expect(len(attached)).To(Equal(1))
								Expect(attached[0]).To(Equal(policyARN))
							})

							Context("delete attached policy", func() {
								It("the role can be deleted without error", func() {
									err := awsmngr.DeleteRole(role.AwsName(clusterName))
									Expect(err).NotTo(HaveOccurred())
								})
							})

							Context("delete attached policy", func() {
								It("can be done without error", func() {
									err := awsmngr.DeletePolicy(policyARN)
									Expect(err).NotTo(HaveOccurred())
								})

								// this doesn't seem to work with localstack
								// todo reproduce with the aws cli :
								// nothing attached returned when calling  ListEntitiesForPolicy against localstack
								// I guess something is missing
								//
								//Context("the policy now should be detached", func() {
								//	It("and doesn't cause error to attempt to retrieve it", func() {
								//		attached, err := awsmngr.GetAttachedRolePoliciesARNs(role.AwsName())
								//		Expect(err).NotTo(HaveOccurred())
								//		Expect(attached).To(BeEmpty())
								//	})
								//})
							})
						})
					})

					Context("deletion", func() {
						It("can be deleted without error", func() {
							err := awsmngr.DeleteRole(role.AwsName(clusterName))
							Expect(err).NotTo(HaveOccurred())
						})

						Context("idempotency", func() {
							It("deletion is idempotent", func() {
								err := awsmngr.DeleteRole(role.AwsName(clusterName))
								Expect(err).NotTo(HaveOccurred())
							})
						})
					})
				})
			})
		})
	})
})
