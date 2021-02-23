package controllers_test

import (
	"log"
	"math/rand"
	"sync"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	"github.com/VoodooTeam/irsa-operator/aws"
	"github.com/davecgh/go-spew/spew"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("cluster state", func() {
	// generate n awsState and clusterStates ?
	// define the converged state (everything created & k8s resources at ok)
	// expect the converged state to be reached before timeout
	var names []string

	It("must converge", func() {
		count := 100
		log.Println("will run against ", count, "different envs")
		for i := 0; i < count; i++ {
			names = append(names, validName())
		}

		var wg sync.WaitGroup
		for _, n := range names {
			wg.Add(1)
			go run(n, &wg)
		}
		wg.Wait()
	})
})

func run(irsaName string, wg *sync.WaitGroup) {
	defer wg.Done()
	var stack awsStack
	initialErrors := getInitialErrs()

	// will log in case of failure the final state and the events that lead there
	defer func(iE map[awsMethod]struct{}) {
		if recover() != nil {
			log.Println(">>> inital errors :")
			spew.Dump(iE)
			log.Println(">>> ended up with stack state :")
			spew.Dump(stack)

			GinkgoT().FailNow()
		}
	}(copyErrs(initialErrors))

	// ensure the stack doesnt exists yet
	raw, ok := st.stacks.Load(irsaName)
	Expect(raw).To(BeNil())
	Expect(ok).To(BeFalse())

	// initialize the awsStack for the current cluster
	st.stacks.Store(irsaName, awsStack{
		policy: aws.AwsPolicy{},
		role:   awsRole{},
		errors: initialErrors,
		events: []string{},
	})

	submittedPolicy := api.PolicySpec{
		Statement: []api.StatementSpec{
			{Resource: "arn:aws:s3:::my_corporate_bucket/exampleobject.png", Action: []string{"act1"}},
		},
	}

	{ // k8s
		{
			// we submit the iamroleserviceaccount Spec to k8s
			createResource(
				api.NewIamRoleServiceAccount(irsaName, testns, submittedPolicy),
			).Should(Succeed())
		}
		{ // every CR must eventually reach an OK status & serviceAccount has been created
			foundPolicyInCondition(irsaName, testns, api.CrOK).Should(BeTrue())
			foundRoleInCondition(irsaName, testns, api.CrOK).Should(BeTrue())
			foundIrsaInCondition(irsaName, testns, api.IrsaOK).Should(BeTrue())
			findSa(irsaName, testns).Should(BeTrue())
		}
	}

	{ // aws resources checks
		// once the cluster is ok, we check what it did to the aws stack
		raw, ok := st.stacks.Load(irsaName)
		Expect(raw).ShouldNot(BeNil())
		Expect(ok).Should(BeTrue())
		stack = raw.(awsStack)

		// policy
		Expect(stack.policy).ShouldNot(BeNil())
		Expect(stack.policy.Statement).To(Equal(submittedPolicy.Statement))

		// role
		Expect(stack.role).ShouldNot(BeNil())
		Expect(len(stack.role.attachedPolicies)).To(Equal(1))
		Expect(stack.role.attachedPolicies[0]).To(Equal(stack.policy.ARN))

		{
			iamrsa := getIrsa(irsaName, testns)
			Expect(iamrsa.Spec.Policy.Statement).To(Equal(stack.policy.Statement))
		}
		{
			role := getRole(irsaName, testns)
			Expect(role.Spec.PolicyARN).To(Equal(stack.policy.ARN))
			Expect(role.Spec.RoleARN).To(Equal(stack.role.arn))
		}
		{
			policy := getPolicy(irsaName, testns)
			Expect(policy.Spec.ARN).To(Equal(stack.policy.ARN))
		}
	}
}

func getInitialErrs() map[awsMethod]struct{} {
	methods := []awsMethod{
		policyExists,
		getStatement,
		updatePolicy,
		createPolicy,
		deletePolicy,
		getPolicyARN,
		createRole,
		attachRolePolicy,
		deleteRole,
		roleExists,
		getRoleARN,
		getAttachedRolePoliciesARNs}

	errs := make(map[awsMethod]struct{})
	for _, m := range methods {
		if rand.Float32() < 0.5 {
			errs[m] = struct{}{}
		}
	}
	return errs
}

func copyErrs(in map[awsMethod]struct{}) map[awsMethod]struct{} {
	out := make(map[awsMethod]struct{})
	for k, v := range in {
		out[k] = v
	}
	return out
}
