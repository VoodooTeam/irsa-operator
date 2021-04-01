package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
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
	_ = r.log.WithValues("role", req.NamespacedName)

	var role *api.Role
	{ // extract role from the request
		var ok completed
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
	r.log.Info("admissionStep")

	if err := role.Validate(r.clusterName); err != nil { // the role spec is invalid
		r.log.Info("invalid spec, passing status to failed")
		if err := r.updateStatus(ctx, role, api.RoleStatus{Condition: api.CrFailed, Reason: err.Error()}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// update the role to "pending"
	if err := r.updateStatus(ctx, role, api.RoleStatus{Condition: api.CrPending, Reason: "passed validation"}); err != nil {
		return ctrl.Result{}, err
	}

	r.log.Info("successfully set role status to pending")
	return ctrl.Result{}, nil
}

// reconcilerRoutine is an infinite loop attempting to make the aws IAM role, with it's attachment converge to the role.Spec
func (r *RoleReconciler) reconcilerRoutine(ctx context.Context, role *api.Role) (ctrl.Result, error) {
	r.log.Info("reconciler routine")

	if role.Spec.RoleARN == "" { // no arn in spec, if we find it on aws : we set the spec, otherwise : we create the AWS role
		roleExistsOnAws, err := r.awsRM.RoleExists(role.AwsName(r.clusterName))
		if err != nil {
			_ = r.updateStatus(ctx, role, api.RoleStatus{Condition: role.Status.Condition, Reason: "failed to check if role exists on AWS"})
			return ctrl.Result{Requeue: true}, nil
		}

		if roleExistsOnAws {
			if ok := r.setRoleArnField(ctx, role); !ok {
				return ctrl.Result{}, nil // updating the role leads to an automatic requeue
			}
		} else {
			if ok := r.createRoleOnAws(ctx, role, r.permissionsBoundariesPolicyARN); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	if role.Spec.PolicyARN == "" { // the role doesn't have the policyARN set in Spec
		if ok := r.setPolicyArnFieldIfPossible(ctx, role); !ok { // we try to grab it from the policy resource and set it
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if role.Spec.PolicyARN != "" { // the role already has a policyARN in Spec
		if ok := r.attachPolicyToRoleIfNeeded(ctx, role); !ok { // we attach the policy with the role on aws
			return ctrl.Result{Requeue: true}, nil
		}
	}

	//if role.Spec.PermissionsBoundariesPolicyArn != r.permissionsBoundariesPolicyARN {
	//	// todo : add permissionsBoundariesPolicyARN equality check
	//	r.log.Info("permissionsBoundariesPolicyARN changed")
	//}

	if role.Status.Condition != api.CrOK {
		_ = r.updateStatus(ctx, role, api.RoleStatus{Condition: api.CrOK})
	}

	return ctrl.Result{}, nil
}

func (r *RoleReconciler) setRoleArnField(ctx context.Context, role *api.Role) completed {
	withScope := scope("setRoleArnField")
	// we get the role details from aws
	roleArn, err := r.awsRM.GetRoleARN(role.AwsName(r.clusterName))
	if err != nil {
		r.addEvent(role, newMsg(withScope("failed to get role arn on aws")))
		return false
	}

	// set the roleArn in spec
	role.Spec.RoleARN = roleArn
	if err := r.Update(context.Background(), role); err != nil {
		r.addEvent(role, newMsg(withScope("failed to set roleArn field in role")))
		return false
	}

	return true
}

func (r *RoleReconciler) createRoleOnAws(ctx context.Context, role *api.Role, permissionsBoundariesPolicyARN string) completed {
	if err := r.awsRM.CreateRole(*role, permissionsBoundariesPolicyARN); err != nil {
		r.addEvent(role, newErr("failed to create roleArn on aws", err))
		return false
	}
	return true
}

func (r *RoleReconciler) attachPolicyToRoleIfNeeded(ctx context.Context, role *api.Role) completed {
	withScope := scope("attachPolicyToRoleIfNeeded")

	awsRoleName := role.AwsName(r.clusterName)
	roleAlreadyCreatedOnAws, err := r.awsRM.RoleExists(awsRoleName)
	if err != nil {
		r.addEvent(role, newErr(withScope("failed to check if the role exists"), err))
		return false
	}

	if !roleAlreadyCreatedOnAws {
		r.addEvent(role, newMsg(withScope("role not created on AWS yet")))
		return false
	}

	// maybe the policy is already attached to it ?
	policiesARNs, err := r.awsRM.GetAttachedRolePoliciesARNs(awsRoleName)
	if err != nil {
		r.addEvent(role, newErr(withScope("failed to retrieve attached role policies"), err))
		return false
	}

	for _, pARN := range policiesARNs { // iterate over found policies
		if pARN == role.Spec.PolicyARN {
			r.addEvent(role, newMsg(withScope("policy already attached")))
			return true
		}
	}

	// the policy is not attached yet
	if err := r.awsRM.AttachRolePolicy(awsRoleName, role.Spec.PolicyARN); err != nil { // we attach the policy
		r.addEvent(role, newErr(withScope("failed to attach policy to role"), err))
		return false
	}

	r.addEvent(role, newMsg(withScope("attached policy to role")))
	return true
}

func (r *RoleReconciler) setPolicyArnFieldIfPossible(ctx context.Context, role *api.Role) completed {
	r.log.Info("setPolicyArnFieldIfPossible")

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
		r.logExtErr(err, "failed to set policyARN in role spec")
		return false
	}

	return true
}

func (r *RoleReconciler) registerFinalizerIfNeeded(role *api.Role) completed {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) {
		// the finalizer isn't registered yet
		// we add it to the role.
		role.ObjectMeta.Finalizers = append(role.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), role); err != nil {
			r.logExtErr(err, "setting finalizer failed")
			return false
		}
	}
	return true
}

func (r *RoleReconciler) executeFinalizerIfPresent(role *api.Role) completed {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) {
		// no finalizer to execute
		return true
	}
	r.log.Info("executing finalizer : deleting role on aws")

	// if some policies are attached to the role
	// we'll wait till they're detached
	waitForPolicies := true
	for waitForPolicies {
		attachedPolicies, err := r.awsRM.GetAttachedRolePoliciesARNs(role.AwsName(r.clusterName))
		if err != nil {
			r.logExtErr(err, "failed to list attached policies")
			return false
		}

		// we found some policies attached
		// we loop
		if len(attachedPolicies) > 0 {
			r.log.Info(fmt.Sprintf("%d policies still attached, waiting for them to be detached", len(attachedPolicies)))
			time.Sleep(time.Second * 5)
		} else {
			// no policy attach, we exit the loop
			waitForPolicies = false
		}
	}

	// we delete the role
	if err := r.awsRM.DeleteRole(role.AwsName(r.clusterName)); err != nil {
		r.logExtErr(err, "failed delete the role")
		return false
	}

	r.log.Info("deleting role")
	// let's delete the role itself
	if err := r.Delete(context.TODO(), role); err != nil {
		if !k8serrors.IsNotFound(err) {
			r.logExtErr(err, "role resource failed")
			return false
		}
	}

	// it succeeded
	// we remove our finalizer from the list and update it.
	role.ObjectMeta.Finalizers = removeString(role.ObjectMeta.Finalizers, r.finalizerID)
	if err := r.Update(context.Background(), role); err != nil {
		r.logExtErr(err, "failed to remove the finalizer")
		return false
	}

	return true
}

func (r *RoleReconciler) getPolicy(ctx context.Context, name, ns string) (*api.Policy, completed) {
	policy := &api.Policy{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, policy); err != nil {
		if errors.IsNotFound(err) {
			return nil, true
		}

		r.logExtErr(err, "get policy failed")
		return nil, false
	}

	return policy, true
}

func (r *RoleReconciler) getRoleFromReq(ctx context.Context, req ctrl.Request) (*api.Role, completed) {
	role := &api.Role{}
	if err := r.Get(ctx, req.NamespacedName, role); err != nil {
		if errors.IsNotFound(err) {
			return nil, true
		}

		r.logExtErr(err, "get resource failed")
		return nil, false
	}

	return role, true
}

// helper function to update a Role status
func (r *RoleReconciler) updateStatus(ctx context.Context, role *api.Role, status api.RoleStatus) error {
	role.Status = status
	return r.Status().Update(ctx, role)
}

func (r *RoleReconciler) addEvent(role *api.Role, e Event) {
	_ = r.updateStatus(context.Background(), role, api.RoleStatus{Condition: role.Status.Condition, Reason: e.String()})
}

func (r *RoleReconciler) logExtErr(err error, msg string) {
	r.log.Info(fmt.Sprintf("%s : %s", msg, err))
}
