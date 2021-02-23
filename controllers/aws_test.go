package controllers_test

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	"github.com/VoodooTeam/irsa-operator/aws"
)

func newAwsFake() *awsFake {
	return &awsFake{
		stacks: &sync.Map{},
	}
}

type awsFake struct {
	stacks *sync.Map // used as : map[resourceName(string)]stack(awsStack)
}

type awsStack struct {
	policy aws.AwsPolicy
	role   awsRole
	errors map[awsMethod]struct{}
	events []string
}

type awsRole struct {
	name             string
	arn              string
	attachedPolicies []string
}

type awsMethod string

const (
	policyExists                awsMethod = "policyExists"
	getStatement                awsMethod = "getStatement"
	updatePolicy                awsMethod = "updatePolicy"
	createPolicy                awsMethod = "createPolicy"
	deletePolicy                awsMethod = "deletePolicy"
	getPolicyARN                awsMethod = "getPolicyARN"
	createRole                  awsMethod = "createRole"
	attachRolePolicy            awsMethod = "attachRolePolicy"
	deleteRole                  awsMethod = "deleteRole"
	roleExists                  awsMethod = "roleExists"
	getRoleARN                  awsMethod = "getRoleARN"
	getAttachedRolePoliciesARNs awsMethod = "getAttachedRolePoliciesARNs"
)

func (s *awsFake) PolicyExists(arn string) (bool, error) {
	cN := getResourceName(arn)
	if err := s.shouldFailAt(cN, policyExists); err != nil {
		return false, err
	}

	stack, ok := s.stacks.Load(cN)
	if !ok {
		return false, nil
	}

	return stack.(awsStack).policy.ARN != "", nil
}

func (s *awsFake) CreatePolicy(policy api.Policy) error {
	n := policy.ObjectMeta.Name
	if err := s.shouldFailAt(n, createPolicy); err != nil {
		return err
	}

	// todo abstract away this step (same code in all methods)
	raw, ok := s.stacks.Load(n)
	if !ok {
		return errors.New("policy doesn't exists")
	}
	stack := raw.(awsStack)

	stack.policy = aws.AwsPolicy{ARN: policyARN(policy), Statement: policy.Spec.Statement}
	s.stacks.Store(n, stack)
	return nil
}

func (s *awsFake) UpdatePolicy(policy api.Policy) error {
	n := policy.ObjectMeta.Name
	if err := s.shouldFailAt(n, updatePolicy); err != nil {
		return err
	}
	raw, ok := s.stacks.Load(n)
	if !ok {
		return errors.New("policy doesn't exists")
	}

	stack := raw.(awsStack)
	stack.policy.Statement = policy.Spec.Statement
	s.stacks.Store(n, stack)
	return nil
}

func (s *awsFake) DeletePolicy(arn string) error {
	cN := getResourceName(arn)
	if err := s.shouldFailAt(cN, deletePolicy); err != nil {
		return err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	stack.policy = aws.AwsPolicy{}
	s.stacks.Store(cN, stack)
	return nil
}

func (s *awsFake) GetPolicyARN(pathPrefix, awsName string) (string, error) {
	cN := getPolicyNameFromAwsName(awsName)
	if err := s.shouldFailAt(cN, getPolicyARN); err != nil {
		return "", err
	}

	stack, ok := s.stacks.Load(cN)
	if !ok {
		return "", errors.New("stack doesn't exists")
	}

	return stack.(awsStack).policy.ARN, nil
}

func (s *awsFake) GetStatement(arn string) ([]api.StatementSpec, error) {
	n := getResourceName(arn)
	if err := s.shouldFailAt(n, getStatement); err != nil {
		return nil, err
	}

	stack, ok := s.stacks.Load(n)
	if !ok {
		return nil, errors.New("stack doesn't exists")
	}
	return stack.(awsStack).policy.Statement, nil
}

func (s *awsFake) CreateRole(r api.Role) error {
	n := r.ObjectMeta.Name
	if err := s.shouldFailAt(n, createRole); err != nil {
		return err
	}

	raw, ok := s.stacks.Load(n)
	if !ok {
		return errors.New("policy doesn't exists")
	}

	stack := raw.(awsStack)
	stack.role = awsRole{name: roleName(r), arn: roleArn(r), attachedPolicies: []string{}}
	s.stacks.Store(n, stack)
	return nil
}

func (s *awsFake) DeleteRole(roleName string) error {
	cN := getResourceName(roleName)
	if err := s.shouldFailAt(cN, deleteRole); err != nil {
		return err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	stack.role = awsRole{}
	s.stacks.Store(cN, stack)
	return nil
}

func (s *awsFake) RoleExists(roleName string) (bool, error) {
	cN := getClusterNameFromRoleName(roleName)
	if err := s.shouldFailAt(cN, roleExists); err != nil {
		return false, err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return false, errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	return stack.role.name != "", nil
}

func (s *awsFake) GetRoleARN(roleName string) (string, error) {
	cN := getClusterNameFromRoleName(roleName)
	if err := s.shouldFailAt(cN, getRoleARN); err != nil {
		return "", err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return "", errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	return stack.role.arn, nil
}

func (s *awsFake) GetAttachedRolePoliciesARNs(roleName string) ([]string, error) {
	cN := getClusterNameFromRoleName(roleName)
	if err := s.shouldFailAt(cN, getAttachedRolePoliciesARNs); err != nil {
		return nil, err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return nil, errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	return stack.role.attachedPolicies, nil
}

func (s *awsFake) AttachRolePolicy(roleName, policyARN string) error {
	cN := getClusterNameFromRoleName(roleName)
	if err := s.shouldFailAt(cN, attachRolePolicy); err != nil {
		return err
	}

	raw, ok := s.stacks.Load(cN)
	if !ok {
		return errors.New("stack doesn't exists")
	}

	stack := raw.(awsStack)
	// todo fix roleName inconcistencies (should be awsRoleName ?)
	//if stack.role.name != roleName {
	//	return errors.New("role not found")
	//}

	stack.role.attachedPolicies = append(stack.role.attachedPolicies, policyARN)
	s.stacks.Store(cN, stack)
	return nil
}

// shouldFailAt does 2 (!) things :
// - abstract the error mechanism
// - toggle the next result that will be returned
func (s *awsFake) shouldFailAt(n string, m awsMethod) error {
	raw, found := s.stacks.Load(n)
	if !found {
		log.Fatal("stack not found :", n, ",", string(m))
	}
	stack := raw.(awsStack)

	// an error exists for method key
	// delete the error
	// we add this event
	// store the new stack[clusterName]
	if _, found := stack.errors[m]; found {
		delete(stack.errors, m)
		stack.events = append(stack.events, fmt.Sprintf("failure : %s", string(m)))
		s.stacks.Store(n, stack)
		return errors.New(string(m))
	}

	// otherwise
	// we store anything at method key
	// add the event
	// store the new stack[clusterName]
	stack.events = append(stack.events, fmt.Sprintf("success : %s", string(m)))
	s.stacks.Store(n, stack)
	return nil
}

func policyARN(p api.Policy) string {
	arn := genUniqueName(p.Namespace, p.Name)
	return arn
}

func roleName(r api.Role) string {
	rN := genUniqueName(r.Namespace, r.Name)
	return rN
}

func roleArn(r api.Role) string {
	rN := genUniqueName(r.Namespace, r.Name)
	return "arn:" + rN
}

func genUniqueName(ns, n string) string {
	// we don't have to build something realistic, just something that is convenient for testing
	return fmt.Sprintf("%s-%s", ns, n)
}

func getResourceName(roleNameOrPolicyARN string) string {
	return strings.Split(roleNameOrPolicyARN, "-")[1]
}

func getPolicyNameFromAwsName(name string) string {
	return strings.Split(name, "-")[4]
}
func getClusterNameFromRoleName(name string) string {
	return strings.Split(name, "-")[4]
}
