package k8s

import (
	"k8s.io/apimachinery/pkg/types"
)

// ObjectKey is an alias for types.NamespacedName with additional helper methods.
type ObjectKey types.NamespacedName

// Named creates an ObjectKey with just a name (for cluster-scoped resources).
func Named(name string) ObjectKey {
	return ObjectKey{Name: name}
}

// NamespacedNamed creates an ObjectKey with both namespace and name.
func NamespacedNamed(namespace string, name string) ObjectKey {
	return ObjectKey{Namespace: namespace, Name: name}
}

// InNamespace sets the namespace for the ObjectKey, enabling fluent API like Named("foo").InNamespace("bar").
func (k ObjectKey) InNamespace(namespace string) ObjectKey {
	k.Namespace = namespace

	return k
}

// ToNamespacedName converts ObjectKey back to types.NamespacedName.
func (k ObjectKey) ToNamespacedName() types.NamespacedName {
	return types.NamespacedName(k)
}
