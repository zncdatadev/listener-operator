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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
	util "github.com/zncdata-labs/listener-operator/pkg/util"
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

func (r *ListenerReconciler) getServiceType(listenerClass *listenersv1alpha1.ListenerClass) listenersv1alpha1.ServiceType {
	if listenerClass.Spec.ServiceType == "" {
		return listenersv1alpha1.ServiceTypeClusterIP
	}
	return listenerClass.Spec.ServiceType
}

func (r *ListenerReconciler) getServiceMatchLabeles(listener *listenersv1alpha1.Listener) (map[string]string, error) {
	labels := map[string]string{}
	for key, value := range listener.Spec.ExtraPodMatchLabels {
		labels[key] = value
	}
	return labels, nil
}

func (r *ListenerReconciler) getPorts(listener *listenersv1alpha1.Listener) []corev1.ServicePort {
	ports := []corev1.ServicePort{}
	for _, port := range *listener.Spec.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:       port.Name,
			Protocol:   port.Protocol,
			Port:       port.Port,
			TargetPort: intstr.IntOrString{IntVal: port.Port},
		})
	}
	return ports
}

func (r *ListenerReconciler) createService(
	ctx context.Context,
	owner *listenersv1alpha1.Listener,
	name, namespace string,
	ports []corev1.ServicePort,
	labels map[string]string,
) (ctrl.Result, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: labels,
		},
	}

	if err := ctrl.SetControllerReference(owner, service, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	if munant, err := util.CreateOrUpdate(ctx, r.Client, service); err != nil {
		return ctrl.Result{}, err
	} else if munant {
		// we need to requeue the request to update the service immediately!
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ListenerReconciler) getNodes(ctx context.Context, serviceName, namespace string) ([]corev1.Node, error) {

	endpoints := &corev1.Endpoints{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, endpoints); err != nil {
		return nil, err
	}

	nodeNames := []string{}

	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			nodeNames = append(nodeNames, *address.NodeName)
		}
	}

	nodes := []corev1.Node{}

	for _, nodeName := range nodeNames {
		node := &corev1.Node{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: "", Name: nodeName}, node); err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}

	return nodes, nil

}

func (r *ListenerReconciler) getService(ctx context.Context, name, namespace string) (*corev1.Service, error) {
	service := &corev1.Service{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, service); err != nil {
		return nil, err
	}
	return service, nil
}

func (r *ListenerReconciler) updateListener(ctx context.Context, listener *listenersv1alpha1.Listener) error {
	return r.Update(ctx, listener)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&listenersv1alpha1.Listener{}).
		Complete(r)
}
