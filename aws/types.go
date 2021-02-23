package aws

import (
	"encoding/json"
	"fmt"
	"regexp"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
)

type PolicyDocument struct {
	Version   string
	Statement []Statement
}

type Statement struct {
	Effect   StatementEffect
	Action   []string
	Resource string
}

func (s Statement) ToSpec() api.StatementSpec {
	return api.StatementSpec{
		Resource: s.Resource,
		Action:   s.Action,
	}
}

type StatementEffect string

const (
	StatementAllow StatementEffect = "Allow"
	StatementDeny  StatementEffect = "Deny"
)

func NewPolicyDocumentString(p api.PolicySpec) (string, error) {
	stmt := []Statement{}

	for _, s := range p.Statement {
		stmt = append(stmt, Statement{
			Effect:   StatementAllow,
			Action:   s.Action,
			Resource: s.Resource,
		})
	}

	policy := PolicyDocument{
		Version:   "2012-10-17",
		Statement: stmt,
	}

	bytes, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

type RoleDocument struct {
	Version   string
	Statement []RoleStatement
}

type RoleStatement struct {
	Effect    StatementEffect
	Principal struct {
		Federated string
	} `json:"Principal"`
	Action    string
	Condition struct {
		StringEquals map[string]string
	}
}

func NewAssumeRolePolicyDoc(r api.Role, oidcProviderArn string) (string, error) {
	// resource : https://aws.amazon.com/blogs/opensource/introducing-fine-grained-iam-roles-service-accounts

	// we extract the issuerHostpath from the oidcProviderARN (needed in the condition field)
	issuerHostpath := oidcProviderArn
	submatches := regexp.MustCompile(`(?s)/(.*)`).FindStringSubmatch(issuerHostpath)
	if len(submatches) == 2 {
		issuerHostpath = submatches[1]
	}

	// then create the json formatted Trust policy
	bytes, err := json.Marshal(
		RoleDocument{
			Version: "2012-10-17",
			Statement: []RoleStatement{
				{
					Effect: StatementAllow,
					Principal: struct{ Federated string }{
						Federated: string(oidcProviderArn),
					},
					Action: "sts:AssumeRoleWithWebIdentity",
					Condition: struct {
						StringEquals map[string]string
					}{
						StringEquals: map[string]string{
							fmt.Sprintf("%s:sub", issuerHostpath): fmt.Sprintf("system:serviceaccount:%s:%s", r.ObjectMeta.Namespace, r.Spec.ServiceAccountName)},
					},
				},
			},
		},
	)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
