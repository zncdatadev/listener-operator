/*
Copyright 2024 zncdata-labs.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package listener

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
)

var (
	logger = ctrl.Log.WithName("listener")
)

// ListenerReconciler reconciles a Listener object
type ListenerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listeners,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listeners/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listeners/finalizers,verbs=update
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listenerclasses,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Listener object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *ListenerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	instance := &listenersv1alpha1.Listener{}

	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.V(5).Info("Listener resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Listener")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Reconciling Listener", "Name", instance.Name)

	serviceType, err := r.getServiceTypeFromListenerClass(ctx, instance.Spec.ClassName, instance.Namespace)
	if err != nil {
		logger.Error(err, "Failed to get service type")
		return ctrl.Result{}, err
	}

	labels, err := r.getServiceMatchLabeles(instance)
	if err != nil {
		logger.Error(err, "Failed to get service match labels")
		return ctrl.Result{}, err
	}

	svcReconciler := &ServiceReconciler{
		client: r.Client,
		cr:     instance,
	}

	if result, err := svcReconciler.createService(
		ctx,
		labels,
		corev1.ServiceType(*serviceType),
	); err != nil {
		logger.Error(err, "Failed to create service")
		return ctrl.Result{}, err
	} else if result.Requeue {
		return result, nil
	}

	status, err := r.buildListenerStatus(ctx, r.Client, instance)
	if err != nil {
		logger.Error(err, "Failed to build listener status")
		return ctrl.Result{}, err
	}

	instance.Status = *status

	if result, err := r.updateListener(ctx, instance); err != nil {
		logger.Error(err, "Failed to update listener")
		return ctrl.Result{}, err
	} else if result.Requeue {
		return result, nil
	}

	return ctrl.Result{}, nil
}

func (r *ListenerReconciler) getListenerClass(
	ctx context.Context,
	name, namespace string,
) (*listenersv1alpha1.ListenerClass, error) {
	listenerClass := &listenersv1alpha1.ListenerClass{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, listenerClass); err != nil {
		return nil, err
	}
	return listenerClass, nil
}

func (r *ListenerReconciler) getServiceTypeFromListenerClass(
	ctx context.Context,
	name, namespace string,
) (*listenersv1alpha1.ServiceType, error) {
	listenerClass, err := r.getListenerClass(ctx, name, namespace)

	if err != nil {
		return nil, err
	}

	switch listenerClass.Spec.ServiceType {
	case listenersv1alpha1.ServiceTypeNodePort:
		return &listenerClass.Spec.ServiceType, nil
	case listenersv1alpha1.ServiceTypeLoadBalancer:
		return &listenerClass.Spec.ServiceType, nil
	case listenersv1alpha1.ServiceTypeClusterIP:
		return &listenerClass.Spec.ServiceType, nil
	default:
		return nil, errors.New("unknown service type: " + string(listenerClass.Spec.ServiceType))
	}
}

func (r *ListenerReconciler) getServiceMatchLabeles(listener *listenersv1alpha1.Listener) (map[string]string, error) {
	labels := map[string]string{}
	for key, value := range listener.Spec.ExtraPodMatchLabels {
		labels[key] = value
	}
	return labels, nil
}

func (r *ListenerReconciler) buildListenerStatus(
	ctx context.Context,
	client client.Client,
	listener *listenersv1alpha1.Listener,
) (*listenersv1alpha1.ListenerStatus, error) {
	status := &listenersv1alpha1.ListenerStatus{
		ServiceName: listener.Name,
	}

	ports := listener.Spec.Ports

	svcReconciler := &ServiceReconciler{
		client: client,
		cr:     listener,
	}

	service, err := svcReconciler.describe(ctx)
	if err != nil {
		return nil, err
	}

	serviceType := svcReconciler.getServiceType(service)

	switch serviceType {
	case listenersv1alpha1.ServiceTypeNodePort:
		status.NodePorts, err = svcReconciler.getNodePorts(service)
		if err != nil {
			return nil, err
		}
		addresses, err := svcReconciler.getNodesAddress(ctx)

		if err != nil {
			return nil, err
		}

		for _, address := range addresses {
			status.IngressAddress = append(status.IngressAddress, listenersv1alpha1.IngressAddressSpec{
				Address:     address.Address,
				AddressType: address.AddressType,
				Ports:       &ports,
			})
		}
	case listenersv1alpha1.ServiceTypeLoadBalancer:
		address, err := svcReconciler.getLbIngressAddress(service)
		if err != nil {
			return nil, err
		}
		status.IngressAddress = append(status.IngressAddress, listenersv1alpha1.IngressAddressSpec{
			Address:     address,
			AddressType: listenersv1alpha1.AddressTypeIP,
			Ports:       &ports,
		})
	case listenersv1alpha1.ServiceTypeClusterIP:
		address, err := svcReconciler.getClusterIp(service)
		if err != nil {
			return nil, err
		}
		status.IngressAddress = append(status.IngressAddress, listenersv1alpha1.IngressAddressSpec{
			Address:     address,
			AddressType: listenersv1alpha1.AddressTypeIP,
			Ports:       &ports,
		})
	default:
		return nil, errors.New("unknown service type: " + string(serviceType))

	}
	return status, nil
}

func (r *ListenerReconciler) updateListener(ctx context.Context, listener *listenersv1alpha1.Listener) (ctrl.Result, error) {
	err := r.Status().Update(ctx, listener)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&listenersv1alpha1.Listener{}).
		Complete(r)
}
