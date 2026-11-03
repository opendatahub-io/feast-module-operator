package k8s

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Object is a Kubernetes object that provides both runtime type information
// and metadata access. It is structurally identical to client.Object from
// controller-runtime, so any client.Object value satisfies this interface.
type Object interface {
	runtime.Object
	metav1.Object
}
