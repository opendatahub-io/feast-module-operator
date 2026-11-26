package support

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AddFinalizerToCurrentResource returns the UID read by the update attempt,
// including on errors so cleanup can handle a lost update response.
func AddFinalizerToCurrentResource(ctx context.Context, cli client.Client, obj client.Object, finalizer string) (types.UID, error) {
	var heldUID types.UID
	key := client.ObjectKeyFromObject(obj)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := cli.Get(ctx, key, obj); err != nil {
			return err
		}
		heldUID = obj.GetUID()
		controllerutil.AddFinalizer(obj, finalizer)
		return cli.Update(ctx, obj)
	})
	return heldUID, err
}

// ReleaseFinalizer removes only the test finalizer from the original object,
// leaving a replacement object and any controller-owned finalizers untouched.
func ReleaseFinalizer(ctx context.Context, cli client.Client, obj client.Object, uid types.UID, finalizer string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := obj.DeepCopyObject().(client.Object)
		if err := cli.Get(ctx, client.ObjectKeyFromObject(obj), fresh); err != nil {
			return client.IgnoreNotFound(err)
		}
		if fresh.GetUID() != uid || !controllerutil.RemoveFinalizer(fresh, finalizer) {
			return nil
		}
		return cli.Update(ctx, fresh)
	})
}
