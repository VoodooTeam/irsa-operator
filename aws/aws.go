package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	"github.com/VoodooTeam/irsa-operator/controllers"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"

	"github.com/go-logr/logr"
)

type AwsPolicy struct {
	ARN       string
	Statement []api.StatementSpec
}

type RealAwsManager struct {
	Client          *iam.IAM
	log             logr.Logger
	clusterName     string
	oidcProviderArn string
}

func NewAwsManager(sess *session.Session, logger logr.Logger, cN, oidcProviderArn string) controllers.AwsManager {
	return &RealAwsManager{
		Client:          iam.New(sess),
		log:             logger,
		clusterName:     cN,
		oidcProviderArn: oidcProviderArn,
	}
}

var desc = "created by the irsa-operator"

func (m RealAwsManager) GetStatement(arn string) ([]api.StatementSpec, error) {
	// we retrieve the defaultVersionID by getting the policy
	res, err := m.Client.GetPolicy(&iam.GetPolicyInput{PolicyArn: &arn})
	if err != nil {
		return nil, err
	}

	// we get the url-encoded document of the default version of the policy
	resPV, err := m.Client.GetPolicyVersion(&iam.GetPolicyVersionInput{PolicyArn: &arn, VersionId: res.Policy.DefaultVersionId})
	if err != nil {
		return nil, err
	}

	// we decode the document
	decodedDoc, err := url.QueryUnescape(*resPV.PolicyVersion.Document)
	if err != nil {
		return nil, err
	}

	// unmarshal the document in our aws specific PolicyDocument struct
	doc := &PolicyDocument{}
	if err := json.Unmarshal([]byte(decodedDoc), doc); err != nil {
		return nil, err
	}

	// iterate over all the statements to convert them in statementSpec
	stmtSpecs := []api.StatementSpec{}
	for _, s := range doc.Statement {
		stmtSpecs = append(stmtSpecs, s.ToSpec())
	}

	return stmtSpecs, nil
}

func (m RealAwsManager) UpdatePolicy(policy api.Policy) error {
	policyDoc, err := NewPolicyDocumentString(policy.Spec)
	if err != nil {
		m.logExtErr(err, "failed at policy serialization")
		return err
	}

	_, err = m.Client.CreatePolicyVersion(&iam.CreatePolicyVersionInput{PolicyArn: &policy.Spec.ARN, PolicyDocument: &policyDoc, SetAsDefault: aws.Bool(true)})
	if err != nil {
		return err
	}

	return nil
}

func (m RealAwsManager) CreatePolicy(policy api.Policy) error {
	_ = m.log.WithName("aws").WithName("policy")

	policyDoc, err := NewPolicyDocumentString(policy.Spec)
	if err != nil {
		m.logExtErr(err, "failed at policy serialization")
		return err
	}

	pn := policy.AwsName(m.clusterName)
	pp := policy.Path(m.clusterName)
	input := &iam.CreatePolicyInput{
		PolicyName:     &pn,
		PolicyDocument: &policyDoc,
		Description:    &desc,
		Path:           &pp,
	}

	if _, err := m.Client.CreatePolicy(input); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusConflict {
				// already created, nothing to do
				m.log.Info("policy already created on aws")
				return nil
			}
		}

		// other error
		m.logExtErr(err, "failed at policy creation")
		return err
	}

	m.log.Info("policy created on aws")
	return nil
}

func (m RealAwsManager) PolicyExists(policyARN string) (bool, error) {
	if _, err := m.Client.GetPolicy(&iam.GetPolicyInput{PolicyArn: &policyARN}); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				m.log.Info("policy doesnt exists on aws")
				return false, nil
			}
		}
		return false, err
	}

	return true, nil
}

// Gets an aws policy on aws
func (m RealAwsManager) GetPolicyARN(pathPrefix, uniqueName string) (string, error) {
	_ = m.log.WithName("aws").WithName("policy")

	// we list the policies and try to find a match
	out, err := m.Client.ListPolicies(&iam.ListPoliciesInput{PathPrefix: &pathPrefix})
	if err != nil {
		m.logExtErr(err, "failed to list policies on aws")
		return "", nil
	}

	for _, p := range out.Policies {
		if *p.PolicyName == uniqueName {
			return *p.Arn, nil
		}
	}

	// return nothing
	return "", nil
}

func (m RealAwsManager) DeletePolicy(policyARN string) error {

	// we first ensure the policy isn't already deleted
	if _, err := m.Client.GetPolicy(&iam.GetPolicyInput{PolicyArn: &policyARN}); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// already created, nothing to do
				m.log.Info("policy doesnt exists on aws")
				return nil
			}
		}
	}

	m.log.Info("found policy")

	// list what the policy is attached to
	listRes, err := m.Client.ListEntitiesForPolicy(&iam.ListEntitiesForPolicyInput{PolicyArn: &policyARN})
	if err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// already deleted, nothing to do
				m.log.Info("policy already deleted on aws")
				return nil
			}
		}
		return err
	}
	m.log.Info("policy found")

	// detach the policy from the role
	if len(listRes.PolicyRoles) > 1 {
		// should be attached to a single role
		// we're conservative and return an error
		return errors.New("policy attached to several roles, not supposed to happen")
	}

	m.log.Info(fmt.Sprintf("policy attached to %d roles", len(listRes.PolicyRoles)))
	for _, r := range listRes.PolicyRoles {
		// we ignore the detach errors
		_, err := m.Client.DetachRolePolicy(&iam.DetachRolePolicyInput{RoleName: r.RoleName, PolicyArn: &policyARN})
		if err != nil {
			m.logExtErr(err, "failed to detach policy from role")
			return err
		}
	}

	m.log.Info("policy will be deleted")
	pvv, err := m.Client.ListPolicyVersions(&iam.ListPolicyVersionsInput{PolicyArn: &policyARN})
	if err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// already deleted, nothing to do
				m.log.Info("policy already deleted on aws")
				return nil
			}
		} else {
			return err
		}
	}

	for _, pv := range pvv.Versions {
		if *pv.IsDefaultVersion {
			continue
		}
		// we ignore potential errors, if we didn't manage to delete all non-default versions, the DeletePolicy below will fail
		_, _ = m.Client.DeletePolicyVersion(&iam.DeletePolicyVersionInput{
			PolicyArn: &policyARN,
			VersionId: pv.VersionId,
		})
	}

	// actually delete policy
	if _, err := m.Client.DeletePolicy(&iam.DeletePolicyInput{PolicyArn: &policyARN}); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// already deleted, nothing to do
				m.log.Info("policy already deleted on aws")
				return nil
			}
		}
		return err
	}

	return nil
}

func (m RealAwsManager) RoleExists(roleName string) (bool, error) {
	_ = m.log.WithName("aws").WithName("role")

	res, err := m.Client.GetRole(&iam.GetRoleInput{RoleName: &roleName})
	if err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// if it failed because it didn't find it
				// perfect, it just doesn't exists, not an error
				m.log.Info("role not found  on aws")
				return false, nil
			}
		}

		// for any other error, let's return it
		return false, err
	}

	// the role field is supposed to be mandatory, but we just ensure it found something
	return res.Role != nil, nil
}

func (m RealAwsManager) GetRoleARN(roleName string) (string, error) {
	_ = m.log.WithName("aws").WithName("role")

	res, err := m.Client.GetRole(&iam.GetRoleInput{RoleName: &roleName})
	if err != nil {
		return "", err
	}

	// the role field is supposed to be mandatory, but we just ensure it found something
	if res.Role == nil {
		return "", errors.New("aws returned an empty role without error (they said it shouldn't happen)")
	}
	return *res.Role.Arn, nil
}

func (m RealAwsManager) GetAttachedRolePoliciesARNs(roleName string) ([]string, error) {
	_ = m.log.WithName("aws").WithName("role")

	// let's get all the policies attached to the given role
	res, err := m.Client.ListAttachedRolePolicies(&iam.ListAttachedRolePoliciesInput{RoleName: &roleName})
	if err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound {
				// if it failed because it didn't find it
				// perfect, it just doesn't exists, not an error
				m.log.Info("role not found  on aws")
				return nil, nil
			}
		}

		// for any error, let's return it
		m.logExtErr(err, "failed to get the role to find its attached policies on aws")
		return nil, err
	}

	// otherwise, we aggregate the policies ARNs
	arns := []string{}
	for _, p := range res.AttachedPolicies {
		arns = append(arns, *p.PolicyArn)
	}

	return arns, nil
}

func (m RealAwsManager) CreateRole(role api.Role) error {
	_ = m.log.WithName("aws").WithName("role")

	roleDoc, err := NewAssumeRolePolicyDoc(role, m.oidcProviderArn)
	if err != nil {
		m.logExtErr(err, "failed at trust policy serialization")
		return err
	}

	rn := role.AwsName(m.clusterName)
	if _, err := m.Client.CreateRole(&iam.CreateRoleInput{
		RoleName:                 &rn,
		AssumeRolePolicyDocument: &roleDoc,
		Description:              &desc,
	}); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusConflict {
				// the role already exists, we return without error
				m.log.Info("role already created on aws")
				return nil
			}
		}

		m.logExtErr(err, "failed to create trust role policy")
		return err
	}

	m.log.Info(fmt.Sprintf("successfully created trust role policy (%s) on aws", rn))
	return nil
}

func (m RealAwsManager) AttachRolePolicy(roleName, policyARN string) error {
	_ = m.log.WithName("aws").WithName("role")

	if _, err := m.Client.AttachRolePolicy(&iam.AttachRolePolicyInput{RoleName: &roleName, PolicyArn: &policyARN}); err != nil {
		m.logExtErr(err, "failed to attach role policy on aws")
		return err
	}

	m.log.Info(fmt.Sprintf("successfully attached role (%s) & policy (%s) on aws", roleName, policyARN))
	return nil
}

func (m RealAwsManager) DeleteRole(roleName string) error {
	if _, err := m.Client.DeleteRole(&iam.DeleteRoleInput{RoleName: &roleName}); err != nil {
		if reqErr, ok := err.(awserr.RequestFailure); ok {
			if reqErr.StatusCode() == http.StatusNotFound || reqErr.StatusCode() == http.StatusConflict {
				// already deleted, nothing to do
				return nil
			}
		}
		m.logExtErr(err, "role already deleted on aws")
		return err
	}

	return nil
}

func (m RealAwsManager) logExtErr(err error, msg string) {
	m.log.Info(fmt.Sprintf("%s : %s", msg, err))
}
