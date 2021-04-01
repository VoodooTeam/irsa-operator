package controllers_test

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	api "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	testns               = "default"
	resourcePollTimeout  = time.Second * 50
	resourcePollInterval = time.Millisecond * 500
)

// generates a 20 letters string (~5.0e-29 collision probability)
func randString() string {
	letterBytes := "abcdefghijklmnopqrstuvwxyz" // must be valid DNS
	b := make([]byte, 20)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}

// validName is just a more user-friendly name
var validName = randString

// ObjTester is used to find a k8s resource with a given Status
type ObjTester interface {
	client.Object
	HasStatus(st fmt.Stringer) bool
}

// createResource is used to create any k8s resource and expect a result afterwards
func createResource(obj client.Object) GomegaAsyncAssertion {
	return Expect(k8sClient.Create(context.Background(), obj))
}

func find(name, ns string, status fmt.Stringer, obj ObjTester) GomegaAsyncAssertion {
	return Eventually(func() bool {
		err := k8sClient.Get(
			context.Background(),
			types.NamespacedName{Name: name, Namespace: ns},
			obj,
		)
		if err != nil {
			return false
		}

		return obj.HasStatus(status)
	}, resourcePollTimeout, resourcePollInterval)
}

func findSa(name, ns string) GomegaAsyncAssertion {
	return Eventually(func() bool {
		err := k8sClient.Get(
			context.Background(),
			types.NamespacedName{Name: name, Namespace: ns},
			&corev1.ServiceAccount{},
		)
		return err == nil
	}, resourcePollTimeout, resourcePollInterval)
}

func foundIrsaInCondition(name, ns string, cond api.IrsaCondition) GomegaAsyncAssertion {
	return find(name, ns, cond, &api.IamRoleServiceAccount{})
}

func foundPolicyInCondition(name, ns string, cond api.CrCondition) GomegaAsyncAssertion {
	return find(name, ns, cond, &api.Policy{})
}

func foundRoleInCondition(name, ns string, cond api.CrCondition) GomegaAsyncAssertion {
	return find(name, ns, cond, &api.Role{})
}

func getRole(name, ns string) api.Role {
	obj := &api.Role{}
	getOnK8s(name, ns, obj)
	return *obj
}

func getIrsa(name, ns string) api.IamRoleServiceAccount {
	obj := &api.IamRoleServiceAccount{}
	getOnK8s(name, ns, obj)
	return *obj
}
func getPolicy(name, ns string) api.Policy {
	obj := &api.Policy{}
	getOnK8s(name, ns, obj)
	return *obj
}

func getOnK8s(name, ns string, o client.Object) {
	if err := k8sClient.Get(
		context.Background(),
		types.NamespacedName{Name: name, Namespace: ns},
		o,
	); err != nil {
		log.Fatal("failed to get object")
	}
}
