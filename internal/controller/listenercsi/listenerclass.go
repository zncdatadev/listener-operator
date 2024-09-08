package listenercsi

import (
	"context"
	"time"

	operatorlistener "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	operatorclient "github.com/zncdatadev/operator-go/pkg/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	listenersv1alpha1 "github.com/zncdatadev/listener-operator/api/v1alpha1"
)

type PersetListenerClass struct {
	client ctrlclient.Client
	cr     *listenersv1alpha1.ListenerCSI
}

func NewPersetListenerClass(client ctrlclient.Client, cr *listenersv1alpha1.ListenerCSI) *PersetListenerClass {
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
		corev1.ServiceTypeClusterIP,
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewListenerClassReconciler(
		p.client,
		p.cr,
		"external-unstable",
		corev1.ServiceTypeNodePort,
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewListenerClassReconciler(
		p.client,
		p.cr,
		"external-stable",
		corev1.ServiceTypeNodePort, // This should be LoadBalancer, but it's NodePort for the sake of the example
	).Reconcile(ctx); err != nil {
		return ctrl.Result{}, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	return ctrl.Result{}, nil

}

type ListenerClassReconciler struct {
	client ctrlclient.Client
	cr     *listenersv1alpha1.ListenerCSI

	name        string
	serviceType corev1.ServiceType
}

func NewListenerClassReconciler(
	client ctrlclient.Client,
	cr *listenersv1alpha1.ListenerCSI,
	name string,
	serviceType corev1.ServiceType,
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

func (r *ListenerClassReconciler) apply(ctx context.Context, obj *operatorlistener.ListenerClass) (ctrl.Result, error) {
	if mutant, err := operatorclient.CreateOrUpdate(ctx, r.client, obj); err != nil {
		return ctrl.Result{}, err
	} else if mutant {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ListenerClassReconciler) build() *operatorlistener.ListenerClass {

	obj := &operatorlistener.ListenerClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: r.name,
			Labels: map[string]string{
				"app.kubernetes.io/created-by": "listener-operator",
			},
		},
		Spec: operatorlistener.ListenerClassSpec{
			ServiceType: &[]corev1.ServiceType{r.serviceType}[0],
			ServiceAnnotations: map[string]string{
				"app.kubernetes.io/managed-by": "csi-driver-" + r.cr.GetName(),
				"app.kubernetes.io/created-by": "listener-operator",
			},
		},
	}
	return obj
}
