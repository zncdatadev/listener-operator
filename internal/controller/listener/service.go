package listener

import (
	"context"
	"errors"

	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
	"github.com/zncdata-labs/listener-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceReconciler struct {
	client          client.Client
	cr              *listenersv1alpha1.Listener
	name, namespace string
}

func NewServiceReconciler(
	client client.Client,
	cr *listenersv1alpha1.Listener,
) *ServiceReconciler {
	return &ServiceReconciler{
		client:    client,
		cr:        cr,
		name:      cr.Status.ServiceName,
		namespace: cr.Namespace,
	}
}

func (s *ServiceReconciler) createService(
	ctx context.Context,
	labels map[string]string,
	serviceType corev1.ServiceType,
) (ctrl.Result, error) {

	ports := []corev1.ServicePort{}

	for _, port := range s.cr.Spec.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:     port.Name,
			Protocol: port.Protocol,
			Port:     port.Port,
		})
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.getName(),
			Namespace: s.getNamespace(),
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: labels,
			Type:     serviceType,
		},
	}

	if err := ctrl.SetControllerReference(s.cr, service, s.client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	if munant, err := util.CreateOrUpdate(ctx, s.client, service); err != nil {
		return ctrl.Result{}, err
	} else if munant {
		// we need to requeue the request to update the service immediately!
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (s *ServiceReconciler) getName() string {
	return s.name
}

func (s *ServiceReconciler) getNamespace() string {
	return s.namespace
}

func (s *ServiceReconciler) describe(
	ctx context.Context,
) (*corev1.Service, error) {
	service := &corev1.Service{}
	key := client.ObjectKey{
		Namespace: s.getNamespace(),
		Name:      s.getName(),
	}

	if err := s.client.Get(ctx, key, service); err != nil {
		return nil, err
	}

	return service, nil
}

func (s *ServiceReconciler) getNodePorts(service *corev1.Service) ([]listenersv1alpha1.PortSpec, error) {

	if service.Spec.Type != corev1.ServiceTypeNodePort {
		return nil, errors.New("service is not of type NodePort")
	}

	ports := []listenersv1alpha1.PortSpec{}
	for _, port := range service.Spec.Ports {
		ports = append(ports, listenersv1alpha1.PortSpec{
			Name:     port.Name,
			Protocol: port.Protocol,
			Port:     port.NodePort,
		})
	}
	return ports, nil
}

func (s *ServiceReconciler) getLbIngressAddress(
	service *corev1.Service,
) (string, error) {
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return "", errors.New("service is not of type LoadBalancer")
	}

	if len(service.Status.LoadBalancer.Ingress) == 0 {
		return "", errors.New("service has no LoadBalancer Ingress")
	}

	return service.Status.LoadBalancer.Ingress[0].IP, nil
}

func (s *ServiceReconciler) getClusterIp(service *corev1.Service) (string, error) {
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		return "", errors.New("service is not of type ClusterIP")
	}

	return service.Spec.ClusterIP, nil
}

func (s *ServiceReconciler) getServiceType(service *corev1.Service) listenersv1alpha1.ServiceType {
	return listenersv1alpha1.ServiceType(service.Spec.Type)
}

func (s *ServiceReconciler) getNodesAddress(ctx context.Context) ([]util.AddressInfo, error) {

	endpoints := &corev1.Endpoints{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.getNamespace(), Name: s.getName()}, endpoints); err != nil {
		return nil, err
	}

	nodeNames := []string{}

	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			nodeNames = append(nodeNames, *address.NodeName)
		}
	}

	addresses := []util.AddressInfo{}

	for _, nodeName := range nodeNames {
		node := &corev1.Node{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: "", Name: nodeName}, node); err != nil {
			return nil, err
		}

		address, err := util.GetPriorNodeAddress(node)

		if err != nil {
			return nil, err
		}

		addresses = append(addresses, *address)

	}
	return addresses, nil
}
