package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
)

func NewRoleReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	awsrm AwsRoleManager,
	logger logr.Logger,
	clusterName,
	permissionsBoundariesPolicyARN string) *RoleReconciler {
	return &RoleReconciler{
		Client:                         client,
		scheme:                         scheme,
		awsRM:                          awsrm,
		log:                            logger,
		finalizerID:                    "role.irsa.voodoo.io",
		clusterName:                    clusterName,
		permissionsBoundariesPolicyARN: permissionsBoundariesPolicyARN,
	}
}

// RoleReconciler reconciles a Role object
type RoleReconciler struct {
	client.Client
	log                            logr.Logger
	scheme                         *runtime.Scheme
	awsRM                          AwsRoleManager
	finalizerID                    string
	clusterName                    string
	permissionsBoundariesPolicyARN string
}

// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=roles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=roles/finalizers,verbs=update

func (r *RoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var role *api.Role
	{ // extract role from the request
		var ok bool
		role, ok = r.getRoleFromReq(ctx, req)
		if !ok {
			// didn't complete, requeing
			return ctrl.Result{Requeue: true}, nil
		}
		if role == nil {
			// not found, has been deleted
			return ctrl.Result{}, nil
		}
	}

	{ // finalizer registration & execution
		if role.IsPendingDeletion() {
			// deletion requested, execute finalizer
			if ok := r.executeFinalizerIfPresent(role); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
			// all done, no requeue
			return ctrl.Result{}, nil
		} else {
			if ok := r.registerFinalizerIfNeeded(role); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	// handlers
	if role.Status.Condition == api.CrSubmitted {
		return r.admissionStep(ctx, role)
	}

	return r.reconcilerRoutine(ctx, role)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.Role{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		Complete(r)
}

//
// privates
//

// admissionStep does spec validation
func (r *RoleReconciler) admissionStep(ctx context.Context, role *api.Role) (ctrl.Result, error) {
	if err := role.Validate(r.clusterName); err != nil { // the role spec is invalid
		ok := r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, err.Error()))
		return ctrl.Result{Requeue: !ok}, nil
	}

	// update the role to "progressing"
	ok := r.updateStatus(ctx, role, api.NewRoleStatus(api.CrProgressing, "passed validation"))
	return ctrl.Result{Requeue: !ok}, nil
}

// reconcilerRoutine is an infinite loop attempting to make the aws IAM role, with it's attachment converge to the role.Spec
func (r *RoleReconciler) reconcilerRoutine(ctx context.Context, role *api.Role) (ctrl.Result, error) {
	if role.Spec.RoleARN == "" { // no arn in spec, if we find it on aws : we set the spec, otherwise : we create the AWS role
		roleExistsOnAws, err := r.awsRM.RoleExists(role.AwsName(r.clusterName))
		if err != nil {
			r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to check if role exists on AWS"))
			return ctrl.Result{Requeue: true}, nil
		}

		if roleExistsOnAws {
			if ok := r.setRoleArnField(ctx, role); !ok {
				r.updateStatus(ctx, role, api.NewRoleStatus(api.CrProgressing, "role found on AWS"))
				return ctrl.Result{}, nil // updating the role leads to an automatic requeue
			}
		} else {
			if ok := r.createRoleOnAws(ctx, role, r.permissionsBoundariesPolicyARN); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
			r.updateStatus(ctx, role, api.NewRoleStatus(api.CrProgressing, "role created on AWS"))
		}
	}

	if role.Spec.PolicyARN == "" { // the role doesn't have the policyARN set in Spec
		if ok := r.setPolicyArnFieldIfPossible(ctx, role); !ok { // we try to grab it from the policy resource and set it
			return ctrl.Result{Requeue: true}, nil
		}
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrProgressing, "policy found on AWS"))
		return ctrl.Result{Requeue: true}, nil
	} else { // the role already has a policyARN in Spec
		if ok := r.attachPolicyToRoleIfNeeded(ctx, role); !ok { // we attach the policy with the role on aws
			return ctrl.Result{Requeue: true}, nil
		}
	}

	if role.Status.Condition != api.CrOK {
		_ = r.updateStatus(ctx, role, api.NewRoleStatus(api.CrOK, "all done"))
	}

	return ctrl.Result{}, nil
}

func (r *RoleReconciler) setRoleArnField(ctx context.Context, role *api.Role) (completed bool) {
	// we get the role details from aws
	roleArn, err := r.awsRM.GetRoleARN(role.AwsName(r.clusterName))
	if err != nil {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to get role ARN on AWS : "+err.Error()))
		return false
	}

	// set the roleArn in spec
	role.Spec.RoleARN = roleArn
	if err := r.Update(context.Background(), role); err != nil {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to set roleArn field in role : "+err.Error()))
		return false
	}

	return true
}

func (r *RoleReconciler) createRoleOnAws(ctx context.Context, role *api.Role, permissionsBoundariesPolicyARN string) (completed bool) {
	if err := r.awsRM.CreateRole(*role, permissionsBoundariesPolicyARN); err != nil {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to create roleArn on aws : "+err.Error()))
		return false
	}
	return true
}

func (r *RoleReconciler) attachPolicyToRoleIfNeeded(ctx context.Context, role *api.Role) (completed bool) {
	awsRoleName := role.AwsName(r.clusterName)
	roleAlreadyCreatedOnAws, err := r.awsRM.RoleExists(awsRoleName)
	if err != nil {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to check if the role exists : "+err.Error()))
		return false
	}

	if !roleAlreadyCreatedOnAws {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "role not created on AWS yet : "+err.Error()))
		return false
	}

	// maybe the policy is already attached to it ?
	policiesARNs, err := r.awsRM.GetAttachedRolePoliciesARNs(awsRoleName)
	if err != nil {
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to retrieve attached role policies : "+err.Error()))
		return false
	}

	for _, pARN := range policiesARNs { // iterate over found policies
		if pARN == role.Spec.PolicyARN {
			return true
		}
	}

	// the policy is not attached yet
	if err := r.awsRM.AttachRolePolicy(awsRoleName, role.Spec.PolicyARN); err != nil { // we attach the policy
		r.updateStatus(ctx, role, api.NewRoleStatus(api.CrError, "failed to attach policy to role : "+err.Error()))
		return false
	}

	r.updateStatus(ctx, role, api.NewRoleStatus(api.CrProgressing, "policy attached to role"))
	return true
}

func (r *RoleReconciler) setPolicyArnFieldIfPossible(ctx context.Context, role *api.Role) (completed bool) {
	// we'll try to get it from the policy resource
	policy, ok := r.getPolicy(ctx, role.Name, role.Namespace)
	if !ok || policy == nil {
		// not found
		return false
	}

	// if its arn field is not set
	if policy.Spec.ARN == "" {
		return false
	}

	role.Spec.PolicyARN = policy.Spec.ARN
	if err := r.Update(ctx, role); err != nil {
		r.controllerErrLog(policy, "set policyARN in role spec", err)
		return false
	}

	return true
}

func (r *RoleReconciler) registerFinalizerIfNeeded(role *api.Role) (completed bool) {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) {
		// the finalizer isn't registered yet
		// we add it to the role.
		role.ObjectMeta.Finalizers = append(role.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), role); err != nil {
			r.controllerErrLog(role, "setting finalizer", err)
			return false
		}
	}
	return true
}

func (r *RoleReconciler) executeFinalizerIfPresent(role *api.Role) (completed bool) {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) { // no finalizer to execute
		return true
	}

	for { // if some policies are attached to the role, wait till they're detached
		attachedPoliciesARNs, err := r.awsRM.GetAttachedRolePoliciesARNs(role.AwsName(r.clusterName))
		if err != nil {
			r.controllerErrLog(role, "list attached policies", err)
			return false
		}

		if len(attachedPoliciesARNs) == 0 { // no policy attached, exit the loop
			r.updateStatus(context.TODO(), role, api.NewRoleStatus(api.CrDeleting, "no policy attached"))
			break
		}

		// we found some policies attached
		// policy should also try to detach policies on its side
		r.updateStatus(context.TODO(), role, api.NewRoleStatus(api.CrDeleting, fmt.Sprintf("%d policies still attached, waiting for them to be detached", len(attachedPoliciesARNs))))
		for _, attachedPolicyARN := range attachedPoliciesARNs {
			r.awsRM.DetachRolePolicy(role.AwsName(r.clusterName), attachedPolicyARN)
		}
		time.Sleep(time.Second * 5)
	}

	{ // delete the role on AWS
		if err := r.awsRM.DeleteRole(role.AwsName(r.clusterName)); err != nil {
			r.controllerErrLog(role, "aws role deletion", err)
			return false
		}
		r.updateStatus(context.TODO(), role, api.NewRoleStatus(api.CrDeleting, "role deleted on AWS"))
	}

	{ // delete the role CR
		if err := r.Delete(context.TODO(), role); err != nil && !k8serrors.IsNotFound(err) {
			r.controllerErrLog(role, "deletion", err)
			return false
		}
	}

	// remove the finalizer
	role.ObjectMeta.Finalizers = removeString(role.ObjectMeta.Finalizers, r.finalizerID)
	return r.Update(context.Background(), role) == nil
}

func (r *RoleReconciler) getPolicy(ctx context.Context, name, ns string) (_ *api.Policy, completed bool) {
	policy := &api.Policy{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, policy); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, true
		}

		r.controllerErrLog(policy, "get policy", err)
		return nil, false
	}

	return policy, true
}

func (r *RoleReconciler) getRoleFromReq(ctx context.Context, req ctrl.Request) (_ *api.Role, completed bool) {
	role := &api.Role{}
	if err := r.Get(ctx, req.NamespacedName, role); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, true
		}

		r.controllerErrLog(role, "get resource", err)
		return nil, false
	}

	return role, true
}

func (r *RoleReconciler) updateStatus(ctx context.Context, role *api.Role, status api.RoleStatus) bool {
	role.Status = status
	return r.Status().Update(ctx, role) == nil
}

func (r *RoleReconciler) controllerErrLog(resource fullNamer, msg string, err error) {
	r.log.Info(fmt.Sprintf("[%s] : Failed to %s : %s", resource.FullName(), msg, err))
}
