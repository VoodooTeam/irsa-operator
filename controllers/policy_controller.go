package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
)

func NewPolicyReconciler(client client.Client, scheme *runtime.Scheme, awspm AwsPolicyManager, logger logr.Logger, cN string) *PolicyReconciler {
	return &PolicyReconciler{
		Client:      client,
		log:         logger,
		scheme:      scheme,
		awsPM:       awspm,
		finalizerID: "policy.irsa.voodoo.io",
		clusterName: cN,
	}
}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	client.Client
	scheme *runtime.Scheme
	awsPM  AwsPolicyManager
	log    logr.Logger

	finalizerID string
	clusterName string
}

// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=policies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=policies/finalizers,verbs=update

// Reconcile is called each time an event occurs on an api.Policy resource
func (r *PolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var policy *api.Policy
	{ // extract policy from the request
		var ok bool
		policy, ok = r.getPolicyFromReq(ctx, req)
		if !ok {
			// didn't complete, requeing
			return ctrl.Result{Requeue: true}, nil
		}
		if policy == nil {
			// not found, has been deleted
			return ctrl.Result{}, nil
		}
	}

	{ // finalizer registration & execution
		if policy.IsPendingDeletion() {
			if ok := r.executeFinalizerIfPresent(ctx, policy); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
			// ok, no requeue
			return ctrl.Result{}, nil
		} else {
			if ok := r.registerFinalizerIfNeeded(policy); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	// the resource has just been created
	if policy.Status.Condition == api.CrSubmitted {
		return r.admissionStep(ctx, policy)
	}

	// for whatever condition we'll try to check the aws policy needs to be created or updated
	return r.reconcilerRoutine(ctx, policy)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.Policy{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		Complete(r)
}

//
// privates
//

// admissionStep does spec validation
func (r *PolicyReconciler) admissionStep(ctx context.Context, policy *api.Policy) (ctrl.Result, error) {
	if err := policy.Validate(r.clusterName); err != nil { // the policy spec is not valid
		ok := r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, err.Error()))
		return ctrl.Result{Requeue: !ok}, nil
	}

	// update the role to "progressing"
	ok := r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrProgressing, "passed validation"))
	return ctrl.Result{Requeue: !ok}, nil
}

// reconcilerRoutine is an infinite loop attempting to make the aws IAM policy converge to the policy.Spec
func (r *PolicyReconciler) reconcilerRoutine(ctx context.Context, policy *api.Policy) (ctrl.Result, error) {
	if policy.Spec.ARN == "" { // no arn in spec, if we find it on aws : we set the spec, otherwise : we create the AWS policy
		foundARN, err := r.awsPM.GetPolicyARN(policy.PathPrefix(r.clusterName), policy.AwsName(r.clusterName))
		if err != nil {
			r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, err.Error()))
			return ctrl.Result{Requeue: true}, nil
		}

		if foundARN == "" { // no policy on aws, we create it
			if err := r.awsPM.CreatePolicy(*policy); err != nil { // creation failed
				r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, "failed to create policy on AWS : "+err.Error()))
			} else { // creation succeeded
				r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrProgressing, "policy created on AWS"))
			}
			return ctrl.Result{Requeue: true}, nil
		} else { // a policy already exists on aws
			r.setPolicyArnField(ctx, foundARN, policy) // we set the policyARN field
			return ctrl.Result{}, nil                  // modifying the policyARN field will generate a new event
		}
	} else { // policy ARN in spec, we may have to update it on aws
		policyStatement, err := r.awsPM.GetStatement(policy.Spec.ARN)
		if err != nil {
			r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, "get policyStatement on AWS failed : "+err.Error()))
			return ctrl.Result{Requeue: true}, nil
		}

		if !api.StatementEquals(policy.Spec.Statement, policyStatement) { // policy on aws doesn't correspond to the one in Spec
			// we update the aws policy
			if err := r.awsPM.UpdatePolicy(*policy); err != nil {
				r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, "update policyStatement on AWS failed : "+err.Error()))
				return ctrl.Result{Requeue: true}, nil
			} else {
				r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrProgressing, "update policyStatement on AWS succeeded"))
			}
		}
	}

	if policy.Status.Condition != api.CrOK {
		r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrOK, "all done"))
	}

	return ctrl.Result{}, nil
}

func (r *PolicyReconciler) executeFinalizerIfPresent(ctx context.Context, policy *api.Policy) (completed bool) {
	if !containsString(policy.ObjectMeta.Finalizers, r.finalizerID) { // no finalizer to execute
		return true
	}

	if policy.Spec.ARN == "" { // the operator hasn't created the policy yet, all done
		return true
	}

	if exists, err := r.awsPM.PolicyExists(policy.Spec.ARN); !exists && err == nil { // policy already deleted, all done
		return true
	}

	// delete the policy on AWS
	if err := r.awsPM.DeletePolicy(policy.Spec.ARN); err != nil { // deletion failed
		r.updateStatus(ctx, policy, api.NewPolicyStatus(api.CrError, "delete Policy on AWS failed : "+err.Error()))
		return false
	}

	{ // let's delete the policy (k8s resource) itself
		if err := r.Delete(context.TODO(), policy); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.controllerErrLog(policy, "delete policy", err)
				return false
			}
		}
	}

	// remove the finalizer from the list and update it.
	policy.ObjectMeta.Finalizers = removeString(policy.ObjectMeta.Finalizers, r.finalizerID)
	return r.Update(context.Background(), policy) == nil
}

// helper function to update a Policy status
func (r *PolicyReconciler) updateStatus(ctx context.Context, Policy *api.Policy, status api.PolicyStatus) bool {
	Policy.Status = status
	return r.Status().Update(ctx, Policy) == nil
}

func (r *PolicyReconciler) registerFinalizerIfNeeded(role *api.Policy) (completed bool) {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) { // the finalizer isn't registered yet
		// we add it to the role.
		role.ObjectMeta.Finalizers = append(role.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), role); err != nil {
			r.controllerErrLog(role, "setting finalizer", err)
			return false
		}
	}
	return true
}

func (r *PolicyReconciler) controllerErrLog(resource fullNamer, msg string, err error) {
	r.log.Info(fmt.Sprintf("[%s] : Failed to %s : %s", resource.FullName(), msg, err))
}

func (r *PolicyReconciler) getPolicyFromReq(ctx context.Context, req ctrl.Request) (policy *api.Policy, completed bool) {
	p := &api.Policy{}
	if err := r.Get(ctx, req.NamespacedName, p); err != nil {
		if errors.IsNotFound(err) {
			return nil, true
		}

		r.controllerErrLog(p, "get resource failed", err)
		return nil, false
	}

	return p, true
}

func (r *PolicyReconciler) setPolicyArnField(ctx context.Context, arn string, policy *api.Policy) (completed bool) {
	policy.Spec.ARN = arn
	if err := r.Update(ctx, policy); err != nil {
		r.controllerErrLog(policy, "set policy.Spec.ARN", err)
		return false
	}
	return true
}
