package listenercsi

import (
	"context"
	"time"

	listenersv1alpha1 "github.com/zncdatadev/listener-operator/api/v1alpha1"
	util "github.com/zncdatadev/listener-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PersetListenerClass struct {
	client client.Client
	cr     *listenersv1alpha1.ListenerCSI
}

func NewPersetListenerClass(client client.Client, cr *listenersv1alpha1.ListenerCSI) *PersetListenerClass {
	return &PersetListenerClass{
		client: client,
		cr:     cr,
	}
}

func (p *PersetListenerClass) Reconcile(ctx context.Context) (ctrl.Result, error) {
	return p.apply(ctx)
}

func (p *PersetListenerClass) apply(ctx context.Context) (ctrl.Result, error) {
	if result, err := NewListenerClassReconciler(
		p.client,
		p.cr,
		"cluster-internal",
		listenersv1alpha1.ServiceTypeClusterIP,
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewListenerClassReconciler(
		p.client,
		p.cr,
		"external-unstable",
		listenersv1alpha1.ServiceTypeNodePort,
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewListenerClassReconciler(
		p.client,
		p.cr,
		"external-stable",
		listenersv1alpha1.ServiceTypeNodePort,
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	return ctrl.Result{}, nil

}

type ListenerClassReconciler struct {
	client client.Client
	cr     *listenersv1alpha1.ListenerCSI

	name        string
	serviceType listenersv1alpha1.ServiceType
}

func NewListenerClassReconciler(
	client client.Client,
	cr *listenersv1alpha1.ListenerCSI,
	name string,
	serviceType listenersv1alpha1.ServiceType,
) *ListenerClassReconciler {
	return &ListenerClassReconciler{
		client:      client,
		cr:          cr,
		name:        name,
		serviceType: serviceType,
	}
}

func (r *ListenerClassReconciler) Reconcile(ctx context.Context) (ctrl.Result, error) {

	obj := r.build()

	return r.apply(ctx, obj)
}

func (r *ListenerClassReconciler) apply(ctx context.Context, obj *listenersv1alpha1.ListenerClass) (ctrl.Result, error) {
	if mutant, err := util.CreateOrUpdate(ctx, r.client, obj); err != nil {
		return ctrl.Result{}, err
	} else if mutant {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ListenerClassReconciler) build() *listenersv1alpha1.ListenerClass {

	obj := &listenersv1alpha1.ListenerClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: r.name,
			Labels: map[string]string{
				"app.kubernetes.io/created-by": "listener-operator",
			},
		},
		Spec: listenersv1alpha1.ListenerClassSpec{
			ServiceType: r.serviceType,
			ServiceAnnotations: map[string]string{
				"app.kubernetes.io/managed-by": "csi-driver-" + r.cr.GetName(),
				"app.kubernetes.io/created-by": "listener-operator",
			},
		},
	}
	return obj
}
