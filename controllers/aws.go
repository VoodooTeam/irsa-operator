package controllers

import (
	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
)

type AwsManager interface {
	AwsPolicyManager
	AwsRoleManager
}

type AwsPolicyManager interface {
	PolicyExists(arn string) (bool, error)
	GetStatement(arn string) ([]api.StatementSpec, error)
	GetPolicyARN(pathPrefix, uniqueName string) (string, error)
	CreatePolicy(api.Policy) error
	UpdatePolicy(api.Policy) error
	DeletePolicy(policyARN string) error
}

type AwsRoleManager interface {
	RoleExists(roleName string) (bool, error)
	CreateRole(role api.Role, permissionsBoundariesPolicyARN string) error
	DeleteRole(roleName string) error
	AttachRolePolicy(roleName, policyARN string) error
	GetAttachedRolePoliciesARNs(roleName string) ([]string, error)
	GetRoleARN(roleName string) (string, error)
	DetachRolePolicy(roleName, policyARN string) error
}
