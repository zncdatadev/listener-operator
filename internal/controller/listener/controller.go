/*
Copyright 2024 zncdatadev.

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

	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	"github.com/zncdatadev/operator-go/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	util "github.com/zncdatadev/listener-operator/pkg/util"
)

var (
	logger = ctrl.Log.WithName("listener")
)

// ListenerReconciler reconciles a Listener object
type ListenerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listeners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listeners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listeners/finalizers,verbs=update
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listenerclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listenerclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=endpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete

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

	existInstance := &listeners.Listener{}

	if err := r.Get(ctx, req.NamespacedName, existInstance); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.V(1).Info("Listener resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Listener")
		return ctrl.Result{}, err
	}
	instance := existInstance.DeepCopy()

	logger.V(1).Info("Reconciling Listener", "Name", instance.Name)

	// Create a service for the listener
	svcReconciler := &ServiceReconciler{client: r.Client, listener: instance}
	if result, err := svcReconciler.createService(ctx); err != nil {
		logger.Error(err, "Failed to create service")
		return ctrl.Result{}, err
	} else if !result.IsZero() {
		return result, nil
	}

	// Update listener status with service information
	if err := r.updateListenerStatus(ctx, instance, svcReconciler); err != nil {
		logger.Error(err, "Failed to update listener status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&listeners.Listener{}).
		Watches(&corev1.Endpoints{}, handler.TypedEnqueueRequestsFromMapFunc(r.handleEndpointsChanges)).
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(r.handlePVChanges)).
		Watches(&listeners.ListenerClass{}, handler.EnqueueRequestsFromMapFunc(r.handleListenerClassChanges)).
		Complete(r)
}

func (r *ListenerReconciler) handleListenerClassChanges(ctx context.Context, obj client.Object) []ctrl.Request {
	listenerClass := obj.(*listeners.ListenerClass)
	list := &listeners.ListenerList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}

	requests := make([]ctrl.Request, 0)
	for _, listener := range list.Items {
		if listener.Spec.ClassName == listenerClass.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      listener.Name,
					Namespace: listener.Namespace,
				},
			})
		}
	}

	return requests
}

func (r *ListenerReconciler) handleEndpointsChanges(ctx context.Context, obj client.Object) []ctrl.Request {
	endpoints := obj.(*corev1.Endpoints)
	list := &listeners.ListenerList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}

	requests := make([]ctrl.Request, 0)
	for _, listener := range list.Items {
		if listener.Status.ServiceName == endpoints.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      listener.Name,
					Namespace: listener.Namespace,
				},
			})
		}
	}

	return requests
}

func (r *ListenerReconciler) handlePVChanges(ctx context.Context, obj client.Object) []ctrl.Request {
	pv := obj.(*corev1.PersistentVolume)

	if ns, ok := pv.Labels[util.LabelListenerNamespace]; ok {
		if name, ok := pv.Labels[constants.AnnotationListenerName]; ok {
			return []ctrl.Request{{
				NamespacedName: types.NamespacedName{
					Name:      name,
					Namespace: ns,
				},
			}}
		}
	}
	return nil
}

func (r *ListenerReconciler) preferredAddressType(ctx context.Context, listener *listeners.Listener) (listeners.AddressType, error) {
	listenerClass := &listeners.ListenerClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: listener.Spec.ClassName}, listenerClass); err != nil {
		logger.Error(err, "Failed to get ListenerClass")
		return "", err
	}

	if listenerClass.Spec.PreferredAddressType == listeners.AddressTypeHostnameConservative {
		if *listenerClass.Spec.ServiceType == corev1.ServiceTypeNodePort {
			return listeners.AddressTypeIP, nil
		}
		return listeners.AddressTypeHostname, nil
	}
	return listenerClass.Spec.PreferredAddressType, nil
}

func (r *ListenerReconciler) updateListenerStatus(ctx context.Context, listener *listeners.Listener, svcReconciler *ServiceReconciler) error {
	status := &listeners.ListenerStatus{ServiceName: listener.Name}
	service, err := svcReconciler.describe(ctx)
	if err != nil {
		return err
	}

	preferredAddressType, err := r.preferredAddressType(ctx, listener)
	if err != nil {
		return err
	}

	serviceType := svcReconciler.getServiceType(service)
	servicePorts := svcReconciler.getPorts(service)

	// update service NodePorts to status when service type is NodePort
	switch serviceType {
	case corev1.ServiceTypeNodePort:
		ports, err := svcReconciler.getNodePorts(service)
		if err != nil {
			return err
		}

		addresses, err := svcReconciler.getNodesAddress(ctx)
		if err != nil {
			return err
		}

		for _, address := range addresses {
			pickAddress := address.Pick(preferredAddressType)
			status.IngressAddresses = append(status.IngressAddresses, listeners.IngressAddressSpec{
				Ports:       ports,
				Address:     pickAddress.Address,
				AddressType: pickAddress.AddressType,
			})
		}
		status.NodePorts = ports
	case corev1.ServiceTypeLoadBalancer:
		address, err := svcReconciler.getLbIngressAddress(service)
		if err != nil {
			return err
		}
		for _, addr := range address {
			pickAddress := addr.Pick(preferredAddressType)
			status.IngressAddresses = append(status.IngressAddresses, listeners.IngressAddressSpec{
				Address:     pickAddress.Address,
				AddressType: pickAddress.AddressType,
				Ports:       servicePorts,
			})
		}
	case corev1.ServiceTypeClusterIP:
		if serviceType == corev1.ServiceTypeClusterIP {
			address, err := svcReconciler.getClusterIp(service)
			if err != nil {
				return err
			}
			status.IngressAddresses = append(status.IngressAddresses, listeners.IngressAddressSpec{
				Address:     address,
				AddressType: listeners.AddressTypeIP,
				Ports:       servicePorts,
			})
		} else {
			address := r.getServiceFQDN(service)
			status.IngressAddresses = append(status.IngressAddresses, listeners.IngressAddressSpec{
				Address:     address,
				AddressType: listeners.AddressTypeHostname,
				Ports:       servicePorts,
			})
		}

	default:
		return errors.New("unknown service type: " + string(serviceType))

	}

	logger.V(1).Info("Listener status", "serviceType", serviceType, "listener", listener.Name, "namespace", listener.Namespace, "status", status)

	listener.Status = *status

	return r.Status().Update(ctx, listener)
}

func (r *ListenerReconciler) getServiceFQDN(service *corev1.Service) string {
	return service.Name + "." + service.Namespace + ".svc." + r.getClusterDomain()
}

func (r *ListenerReconciler) getClusterDomain() string {
	// TODO: get cluster domain from cluster configuration
	return "cluster.local"
}
