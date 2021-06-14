package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8s "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

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
}

// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts/status,verbs=get;update
// +kubebuilder:rbac:groups=irsa.voodoo.io,resources=iamroleserviceaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;delete

// Reconcile is called each time an event occurs on an api.IamRoleServiceAccount resource
func (r *IamRoleServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var irsa *api.IamRoleServiceAccount
	{ // extract role from the request
		var ok bool
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
	{
		if err := irsa.Validate(); err != nil { // the iamroleserviceaccount spec is invalid
			ok := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaFailed, Reason: err.Error()})
			return ctrl.Result{Requeue: !ok}, nil
		}
	}

	{
		if r.saWithNameExistsInNs(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) { // serviceAccountName conflicts with an existing one
			ok := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaFailed, Reason: "serviceAccountName conflict"})
			return ctrl.Result{Requeue: !ok}, nil
		}
	}

	ok := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaProgressing, Reason: "passed validation"})
	return ctrl.Result{Requeue: !ok}, nil
}

// reconcilerRoutine is an infinite loop attempting to make the policy, role, service_account converge to the irsa.Spec
func (r *IamRoleServiceAccountReconciler) reconcilerRoutine(ctx context.Context, irsa *api.IamRoleServiceAccount) (ctrl.Result, error) {
	var policyAlreadyExists, roleAlreadyExists, saAlreadyExists bool

	{ // Policy creation
		var ok bool
		policyAlreadyExists, ok = r.policyAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}

		if !policyAlreadyExists {
			ok := r.createPolicy(ctx, irsa)
			return ctrl.Result{Requeue: !ok}, nil
		} else {
			ok := r.updatePolicyIfNeeded(ctx, irsa)
			if !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	{ // Role creation
		var ok bool
		roleAlreadyExists, ok = r.roleAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}

		if !roleAlreadyExists {
			ok := r.createRole(ctx, irsa)
			return ctrl.Result{Requeue: !ok}, nil
		}
	}

	{ // service_account creation
		var ok bool
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
			ok := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaOK, Reason: "all resources successfully created"})
			return ctrl.Result{Requeue: !ok}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *IamRoleServiceAccountReconciler) executeFinalizerIfPresent(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
	if !containsString(irsa.ObjectMeta.Finalizers, r.finalizerID) {
		// no finalizer to execute
		return true
	}
	resourceLogId := irsa.ObjectMeta.Namespace + "/" + irsa.ObjectMeta.Name

	{ // we delete the service account we created, we first need to ensure it is not owned by another operator (since it's a serviceaccount)
		sa := &corev1.ServiceAccount{}
		step := "serviceAccount deletion : " + resourceLogId
		if err := r.Get(ctx, types.NamespacedName{Namespace: irsa.ObjectMeta.Namespace, Name: irsa.ObjectMeta.Name}, sa); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.logExtErr(err, step)
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
					r.logExtErr(err, step)
					return false
				}
			}
		}
	}

	{ // we delete the iamroleserviceaccount CR we may have already created
		if err := r.Delete(ctx, irsa); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.logExtErr(err, "iamroleserviceaccount deletion : "+resourceLogId)
				return false
			}
		}
	}

	{ // we remove our finalizer from the list and update it.
		irsa.ObjectMeta.Finalizers = removeString(irsa.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), irsa); err != nil {
			r.logExtErr(err, "failed to remove the finalizer : "+resourceLogId)
			return false
		}
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) getIrsaFromReq(ctx context.Context, req ctrl.Request) (*api.IamRoleServiceAccount, bool) {
	irsa := &api.IamRoleServiceAccount{}
	if err := r.Get(ctx, req.NamespacedName, irsa); err != nil {
		return nil, k8serrors.IsNotFound(err)
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

func (r *IamRoleServiceAccountReconciler) policyAlreadyExists(ctx context.Context, name, ns string) (bool, bool) {
	return r.resourceExists(ctx, name, ns, &api.Policy{})
}

func (r *IamRoleServiceAccountReconciler) roleAlreadyExists(ctx context.Context, name, ns string) (bool, bool) {
	return r.resourceExists(ctx, name, ns, &api.Role{})
}

func (r *IamRoleServiceAccountReconciler) saAlreadyExists(ctx context.Context, name, ns string) (bool, bool) {
	return r.resourceExists(ctx, name, ns, &corev1.ServiceAccount{})
}

func (r *IamRoleServiceAccountReconciler) resourceExists(ctx context.Context, name, ns string, obj client.Object) (bool, bool) {
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

func (r *IamRoleServiceAccountReconciler) createPolicy(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
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

func (r *IamRoleServiceAccountReconciler) updatePolicyIfNeeded(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
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

func (r *IamRoleServiceAccountReconciler) createRole(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
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

func (r *IamRoleServiceAccountReconciler) createServiceAccount(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
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

func (r *IamRoleServiceAccountReconciler) updateStatus(ctx context.Context, obj *api.IamRoleServiceAccount, status api.IamRoleServiceAccountStatus) bool {
	obj.Status = status
	return r.Status().Update(ctx, obj) == nil
}

func (r *IamRoleServiceAccountReconciler) registerFinalizerIfNeeded(role *api.IamRoleServiceAccount) bool {
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
