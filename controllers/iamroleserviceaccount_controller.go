package controllers

import (
	"context"
	"fmt"

	"errors"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8s "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"time"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewIrsaReconciler(client client.Client, scheme *runtime.Scheme, logger logr.Logger) *IamRoleServiceAccountReconciler {
	return &IamRoleServiceAccountReconciler{
		Client:      client,
		scheme:      scheme,
		log:         logger,
		finalizerID: "irsa.irsa.voodoo.io",
	}
}

// IamRoleServiceAccountReconciler reconciles a IamRoleServiceAccount object
type IamRoleServiceAccountReconciler struct {
	client.Client
	log         logr.Logger
	scheme      *runtime.Scheme
	finalizerID string

	TestingDelay *time.Duration
}

// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts/status,verbs=get;update
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;delete

// Reconcile is called each time an event occurs on an api.IamRoleServiceAccount resource
func (r *IamRoleServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.log.WithValues("iamroleserviceaccount", req.NamespacedName)

	{ // a processing delay can be set to ensure the testing framework sees every transitionnal state
		r.waitIfTesting()
	}

	var irsa *api.IamRoleServiceAccount
	{ // extract role from the request
		var ok completed
		irsa, ok = r.getIrsaFromReq(ctx, req)
		if !ok { // didn't complete, requeing
			return ctrl.Result{Requeue: true}, nil
		}
		if irsa == nil { // not found, has been deleted
			return ctrl.Result{}, nil
		}
	}

	{ // finalizer registration & execution
		if irsa.IsPendingDeletion() {
			if ok := r.executeFinalizerIfPresent(ctx, irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
			// ok, no requeue
			return ctrl.Result{}, nil
		} else {
			if ok := r.registerFinalizerIfNeeded(irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	if irsa.Status.Condition == api.IrsaSubmitted { // the resource has just been created
		return r.admissionStep(ctx, irsa)
	}

	// for whatever condition we'll try to check the role and policy actually exists
	return r.reconcilerRoutine(ctx, irsa)
}

// SetupWithManager sets up the controller with the Manager.
func (r *IamRoleServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.IamRoleServiceAccount{}).
		Owns(&api.Role{}).
		Owns(&api.Policy{}).
		Owns(&corev1.ServiceAccount{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 10,
		}).
		Complete(r)
}

//
// privates
//

// admissionStep does spec validation
func (r *IamRoleServiceAccountReconciler) admissionStep(ctx context.Context, irsa *api.IamRoleServiceAccount) (ctrl.Result, error) {
	r.log.Info("handling submitted IRSA (checking values, setting defaults)")

	{ // we check submitted spec validity
		if err := irsa.Validate(); err != nil {
			r.logExtErr(err, "invalid spec, passing status to failed")

			// we set the status.condition to failed
			if err := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaFailed, Reason: err.Error()}); err != nil {
				r.logExtErr(err, "failed to set iamroleserviceaccount status to failed")
				return ctrl.Result{Requeue: true}, nil
			}

			// and stop here
			return ctrl.Result{}, nil
		}
	}

	{ // we check the serviceAccountName doesn't conflict with an existing one
		if r.saWithNameExistsInNs(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) {
			e := errors.New("service_account already exists")
			r.log.Info(e.Error())

			// if it's the case, we set the status.condition to saNameConflict
			if err := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaSaNameConflict, Reason: e.Error()}); err != nil {
				r.logExtErr(err, "failed to set iamroleserviceaccount status to saNameConflict")
				return ctrl.Result{Requeue: true}, nil
			}

			// and stop here
			return ctrl.Result{}, nil
		}
	}

	{ // we update the status to pending
		if err := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaPending, Reason: "passed validation"}); err != nil {
			r.log.Info("requeing, failed to set iamroleserviceaccount status to pending", err)
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// and stop here
	return ctrl.Result{}, nil
}

// reconcilerRoutine is an infinite loop attempting to make the policy, role, service_account converge to the irsa.Spec
func (r *IamRoleServiceAccountReconciler) reconcilerRoutine(ctx context.Context, irsa *api.IamRoleServiceAccount) (ctrl.Result, error) {
	r.log.Info("reconciler routine")

	var policyAlreadyExists, roleAlreadyExists, saAlreadyExists bool

	{ // Policy creation
		var ok completed
		policyAlreadyExists, ok = r.policyAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}

		if !policyAlreadyExists {
			if ok := r.createPolicy(ctx, irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		} else {
			if ok := r.updatePolicyIfNeeded(ctx, irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	{ // Role creation
		var ok completed
		roleAlreadyExists, ok = r.roleAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}

		if !roleAlreadyExists {
			if ok := r.createRole(ctx, irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	{ // service_account creation
		var ok completed
		saAlreadyExists, ok = r.saAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}
		if !saAlreadyExists {
			if r.roleIsOk(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) && r.policyIsOK(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) {
				if ok := r.createServiceAccount(ctx, irsa); !ok {
					return ctrl.Result{Requeue: true}, nil
				}
			}
		}
	}

	{ // set the status to ok
		if policyAlreadyExists && roleAlreadyExists && saAlreadyExists && irsa.Status.Condition != api.IrsaOK {
			if err := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaOK, Reason: "all resources successfully created"}); err != nil {
				r.logExtErr(err, "failed to set iamroleserviceaccount status to ok")
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	// all done, we'll keep watching after missing resources every 20"
	return ctrl.Result{RequeueAfter: time.Second * 20}, nil
}

func (r *IamRoleServiceAccountReconciler) executeFinalizerIfPresent(ctx context.Context, irsa *api.IamRoleServiceAccount) completed {
	if !containsString(irsa.ObjectMeta.Finalizers, r.finalizerID) {
		// no finalizer to execute
		return true
	}

	r.log.Info("executing finalizer")

	{ // we delete the service account we created, we first need to ensure it is not owned by another operator (since it's a serviceaccount)
		sa := &corev1.ServiceAccount{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: irsa.ObjectMeta.Namespace, Name: irsa.ObjectMeta.Name}, sa); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.logExtErr(err, "get resource failed")
				return false
			}
		}

		owned := false
		for _, or := range sa.GetOwnerReferences() {
			if or.UID == irsa.UID {
				owned = true
				break
			}
		}

		if owned { // we delete the service account if we own it
			if err := r.Delete(ctx, sa); err != nil {
				if !k8serrors.IsNotFound(err) {
					r.logExtErr(err, "get resource failed")
					return false
				}
			}
		}
	}

	{ // we delete the iamroleserviceaccount CR we may have already created
		r.log.Info("deleting irsa")
		if err := r.Delete(ctx, irsa); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.logExtErr(err, "delete resource failed")
				return false
			}
		}
	}

	{ // we remove our finalizer from the list and update it.
		irsa.ObjectMeta.Finalizers = removeString(irsa.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), irsa); err != nil {
			r.logExtErr(err, "failed to remove the finalizer")
			return false
		}
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) getIrsaFromReq(ctx context.Context, req ctrl.Request) (*api.IamRoleServiceAccount, completed) {
	irsa := &api.IamRoleServiceAccount{}
	if err := r.Get(ctx, req.NamespacedName, irsa); err != nil {
		if k8serrors.IsNotFound(err) {
			r.log.Info("IamRoleServiceAccount deleted")
			return nil, true
		}

		r.logExtErr(err, "get resource failed")
		return nil, false
	}

	return irsa, true
}

func (r IamRoleServiceAccountReconciler) roleIsOk(ctx context.Context, name, ns string) bool {
	role := &api.Role{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, role); err != nil {
		return false
	}
	return role.Status.Condition == api.CrOK
}

func (r IamRoleServiceAccountReconciler) policyIsOK(ctx context.Context, name, ns string) bool {
	policy := &api.Policy{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, policy); err != nil {
		return false
	}
	return policy.Status.Condition == api.CrOK
}

func (r *IamRoleServiceAccountReconciler) policyAlreadyExists(ctx context.Context, name, ns string) (bool, completed) {
	return r.resourceExists(ctx, name, ns, &api.Policy{})
}

func (r *IamRoleServiceAccountReconciler) roleAlreadyExists(ctx context.Context, name, ns string) (bool, completed) {
	return r.resourceExists(ctx, name, ns, &api.Role{})
}

func (r *IamRoleServiceAccountReconciler) saAlreadyExists(ctx context.Context, name, ns string) (bool, completed) {
	return r.resourceExists(ctx, name, ns, &corev1.ServiceAccount{})
}

func (r *IamRoleServiceAccountReconciler) resourceExists(ctx context.Context, name, ns string, obj client.Object) (bool, completed) {
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, obj); err != nil {
		if k8s.IsNotFound(err) {
			return false, true
		} else {
			// something went wrong, requeue
			r.logExtErr(err, "get resource failed")
			return false, false
		}
	}

	return true, true
}

func (r *IamRoleServiceAccountReconciler) createPolicy(ctx context.Context, irsa *api.IamRoleServiceAccount) completed {
	r.log.Info("creating missing policy")
	// we instantiate the policy
	newPolicy := api.NewPolicy(irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace, irsa.Spec.Policy.Statement)

	// set this irsa instance as the owner of this role
	if err := ctrl.SetControllerReference(irsa, newPolicy, r.scheme); err != nil {
		// another resource is already the owner...
		r.logExtErr(err, "failed to set the controller reference")
		return false
	}

	if err := r.Client.Create(ctx, newPolicy); err != nil {
		// we failed to create it, requeue
		r.logExtErr(err, "failed to create policy")
		return false
	}
	return true
}

func (r *IamRoleServiceAccountReconciler) updatePolicyIfNeeded(ctx context.Context, irsa *api.IamRoleServiceAccount) completed {
	policy := &api.Policy{}
	exists, ok := r.resourceExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace, policy)
	if !bool(ok) || !exists {
		return false
	}

	// todo, check if they are actually different

	// we instantiate the policy
	policy.Spec.Statement = irsa.Spec.Policy.Statement
	if err := r.Client.Update(ctx, policy); err != nil {
		// we failed to create it, requeue
		r.logExtErr(err, "failed to create policy")
		return false
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) createRole(ctx context.Context, irsa *api.IamRoleServiceAccount) completed {
	r.log.Info("creating role")

	// we initialize a new role
	role := api.NewRole(
		irsa.ObjectMeta.Name,
		irsa.ObjectMeta.Namespace,
	)

	// set this irsa instance as the owner of this role
	if err := ctrl.SetControllerReference(irsa, role, r.scheme); err != nil {
		// another resource is already the owner...
		r.logExtErr(err, "failed to set the controller reference")
		return false
	}

	// then we create the role on k8s
	if err := r.Client.Create(ctx, role); err != nil {
		r.logExtErr(err, "failed to create role")
		return false
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) createServiceAccount(ctx context.Context, irsa *api.IamRoleServiceAccount) completed {
	r.log.Info("creating service_account")

	role := &api.Role{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: irsa.ObjectMeta.Name, Namespace: irsa.ObjectMeta.Namespace}, role); err != nil {
		if k8s.IsNotFound(err) {
			return false
		} else {
			// something went wrong, requeue
			r.logExtErr(err, "get resource failed")
			return false
		}
	}

	if role.Spec.RoleARN != "" {
		r.log.Info("role has arn")

		// we initialize a new serviceAccount
		newServiceAccount := &corev1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ServiceAccount",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      irsa.ObjectMeta.Name,
				Namespace: irsa.ObjectMeta.Namespace,
				Annotations: map[string]string{
					"eks.amazonaws.com/role-arn": role.Spec.RoleARN,
				},
			},
		}

		// set the current iamroleserviceaccount as the owner
		if err := ctrl.SetControllerReference(irsa, newServiceAccount, r.scheme); err != nil {
			// another resource is already the owner...
			r.logExtErr(err, "failed to set the controller reference")
			return false
		}

		// then actually create the serviceAccount
		if err := r.Client.Create(ctx, newServiceAccount); err != nil {
			// we failed to create it, requeue
			r.logExtErr(err, "failed to create serviceaccount")
			return false
		}
	} else {
		r.log.Info("role has no RoleArn in spec, waiting")
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) saWithNameExistsInNs(ctx context.Context, name, ns string) bool {
	// a bit fragile, don't check errors other than api.NotFound
	return r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &corev1.ServiceAccount{}) == nil
}

func (r *IamRoleServiceAccountReconciler) updateStatus(ctx context.Context, obj *api.IamRoleServiceAccount, status api.IamRoleServiceAccountStatus) error {
	obj.Status = status
	return r.Status().Update(ctx, obj)
}

func (r *IamRoleServiceAccountReconciler) waitIfTesting() {
	if r.TestingDelay != nil {
		time.Sleep(*r.TestingDelay)
	}
}

func (r *IamRoleServiceAccountReconciler) registerFinalizerIfNeeded(role *api.IamRoleServiceAccount) completed {
	if !containsString(role.ObjectMeta.Finalizers, r.finalizerID) {
		// the finalizer isn't registered yet
		// we add it to the irsa
		role.ObjectMeta.Finalizers = append(role.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), role); err != nil {
			r.logExtErr(err, "setting finalizer failed")
			return false
		}
	}
	return true
}

func (r *IamRoleServiceAccountReconciler) logExtErr(err error, msg string) {
	r.log.Info(fmt.Sprintf("%s : %s", msg, err))
}
