package listenercsi

import (
	"context"
	"time"

	listenersv1alpha1 "github.com/zncdatadev/listener-operator/api/v1alpha1"
	util "github.com/zncdatadev/listener-operator/pkg/util"
	storage "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StorageClass struct {
	client client.Client

	cr *listenersv1alpha1.ListenerCSI
}

func NewStorageClass(client client.Client, cr *listenersv1alpha1.ListenerCSI) *StorageClass {
	return &StorageClass{
		client: client,
		cr:     cr,
	}
}

func (r *StorageClass) Reconcile(ctx context.Context) (ctrl.Result, error) {

	obj := r.build()

	return r.apply(ctx, obj)
}

func (r *StorageClass) build() *storage.StorageClass {

	obj := &storage.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "listeners.zncdata.dev",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "listener-operator",
			},
		},
		Provisioner: "listeners.zncdata.dev",
	}

	return obj
}

func (r *StorageClass) apply(ctx context.Context, obj *storage.StorageClass) (ctrl.Result, error) {
	mutant, err := util.CreateOrUpdate(ctx, r.client, obj)
	if err != nil {
		return ctrl.Result{}, err
	} else if mutant {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return ctrl.Result{}, nil
}
