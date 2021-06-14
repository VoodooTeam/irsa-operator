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

type fullNamer interface {
	FullName() string
}

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
	{ //validation
		if err := irsa.Validate(); err != nil { // the iamroleserviceaccount spec is invalid
			ok := r.updateStatus(ctx, irsa, api.IamRoleServiceAccountStatus{Condition: api.IrsaFailed, Reason: err.Error()})
			return ctrl.Result{Requeue: !ok}, nil
		}
	}

	{ //conflict check
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

	{ // policy creation
		var ok bool
		policyAlreadyExists, ok = r.policyAlreadyExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace)
		if !ok {
			return ctrl.Result{Requeue: true}, nil
		}

		if !policyAlreadyExists { // create policy
			ok := r.createPolicy(ctx, irsa)
			return ctrl.Result{Requeue: !ok}, nil
		} else { // update policy
			if ok := r.updatePolicyIfNeeded(ctx, irsa); !ok {
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	{ // role creation
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
			if r.roleIsOk(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) && r.policyIsOK(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace) { // wait for role & policy to be successfully created
				if ok := r.createServiceAccount(ctx, irsa); !ok {
					return ctrl.Result{Requeue: true}, nil
				}
			}
		} // todo : update the already existing serviceAccount
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

	{ // we delete the sa we created
		sa := &corev1.ServiceAccount{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: irsa.ObjectMeta.Namespace, Name: irsa.ObjectMeta.Name}, sa); err != nil {
			if !k8serrors.IsNotFound(err) {
				r.logExtErr(irsa, "get sa", err)
				return false
			}
		}

		{ // ensure it is not owned by another operator
			owned := false
			for _, or := range sa.GetOwnerReferences() {
				if or.UID == irsa.UID {
					owned = true
					break
				}
			}

			if owned { // we delete the service account
				if err := r.Delete(ctx, sa); err != nil && !k8serrors.IsNotFound(err) {
					r.logExtErr(irsa, "delete sa", err)
					return false
				}
			}
		}
	}

	{ // delete the irsa CR
		if err := r.Delete(ctx, irsa); err != nil && !k8serrors.IsNotFound(err) {
			r.logExtErr(irsa, "delete irsa", err)
			return false
		}
	}

	{ // we remove our finalizer from the list and update it.
		irsa.ObjectMeta.Finalizers = removeString(irsa.ObjectMeta.Finalizers, r.finalizerID)
		if err := r.Update(context.Background(), irsa); err != nil {
			r.logExtErr(irsa, "remove the finalizer", err)
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

func (r *IamRoleServiceAccountReconciler) resourceExists(ctx context.Context, name, ns string, obj client.Object) (exists bool, ok bool) {
	if err := r.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, obj); err != nil {
		return false, k8s.IsNotFound(err)
	}

	return true, true
}

func (r *IamRoleServiceAccountReconciler) createPolicy(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
	newPolicy := api.NewPolicy(irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace, irsa.Spec.Policy.Statement)

	{ // set this irsa instance as the owner of this role
		if err := ctrl.SetControllerReference(irsa, newPolicy, r.scheme); err != nil { // another resource is already the owner...
			r.logExtErr(irsa, "set the controller reference", err)
			return false
		}
	}

	{ // create the policy resource
		if err := r.Client.Create(ctx, newPolicy); err != nil { // we create it, requeue
			r.logExtErr(irsa, "create policy", err)
			return false
		}
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) updatePolicyIfNeeded(ctx context.Context, irsa *api.IamRoleServiceAccount) (ok bool) {
	policy := &api.Policy{}
	exists, ok := r.resourceExists(ctx, irsa.ObjectMeta.Name, irsa.ObjectMeta.Namespace, policy)
	if !ok || !exists {
		return false
	}

	policy.Spec.Statement = irsa.Spec.Policy.Statement
	if err := r.Client.Update(ctx, policy); err != nil { // we update it
		r.logExtErr(irsa, "create policy", err)
		return false
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) createRole(ctx context.Context, irsa *api.IamRoleServiceAccount) bool {
	// we initialize a new role
	role := api.NewRole(
		irsa.ObjectMeta.Name,
		irsa.ObjectMeta.Namespace,
	)

	// set this irsa instance as the owner of this role
	if err := ctrl.SetControllerReference(irsa, role, r.scheme); err != nil { // another resource is already the owner...
		r.logExtErr(irsa, "set controller reference", err)
		return false
	}

	// then we create the role on k8s
	if err := r.Client.Create(ctx, role); err != nil {
		r.logExtErr(irsa, "create role", err)
		return false
	}

	return true
}

func (r *IamRoleServiceAccountReconciler) createServiceAccount(ctx context.Context, irsa *api.IamRoleServiceAccount) (ok bool) {
	role := &api.Role{}
	{ // get role details
		if err := r.Client.Get(ctx, types.NamespacedName{Name: irsa.ObjectMeta.Name, Namespace: irsa.ObjectMeta.Namespace}, role); err != nil {
			if k8s.IsNotFound(err) {
				return false
			} else { // something went wrong, requeue
				r.logExtErr(irsa, "get resource", err)
				return false
			}
		}
	}

	if role.Spec.RoleARN != "" { // initialize a new serviceAccount
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
		if err := ctrl.SetControllerReference(irsa, newServiceAccount, r.scheme); err != nil { // another resource is already the owner...
			r.logExtErr(irsa, "set controller reference", err)
			return false
		}

		// then actually create the serviceAccount
		if err := r.Client.Create(ctx, newServiceAccount); err != nil { // we create it, requeue
			r.logExtErr(irsa, "create sa", err)
			return false
		}
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
			r.logExtErr(role, "set finalizer", err)
			return false
		}
	}
	return true
}

func (r *IamRoleServiceAccountReconciler) logExtErr(resource fullNamer, msg string, err error) {
	r.log.Info(fmt.Sprintf("[%s] : Failed to %s : %s", resource.FullName(), msg, err))
}
