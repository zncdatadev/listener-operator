package listener

import (
	"context"
	"errors"
	"fmt"
	"maps"

	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	operatorclient "github.com/zncdatadev/operator-go/pkg/client"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	util "github.com/zncdatadev/listener-operator/pkg/util"
)

type ServiceReconciler struct {
	client   client.Client
	listener *listeners.Listener
}

func NewServiceReconciler(
	client client.Client,
	listener *listeners.Listener,
) *ServiceReconciler {
	return &ServiceReconciler{
		client:   client,
		listener: listener,
	}
}

func (s *ServiceReconciler) createService(
	ctx context.Context,
) (ctrl.Result, error) {
	listenerClass, err := s.getListenerClass(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	ports := []corev1.ServicePort{}

	for _, port := range s.listener.Spec.Ports {
		if port.Name != "" {
			ports = append(ports, corev1.ServicePort{
				Name:     port.Name,
				Protocol: port.Protocol,
				Port:     port.Port,
			})
		} else {
			logger.V(1).Info("port name is empty, so ignore it", "port", port)
		}
	}

	if len(ports) == 0 {
		return ctrl.Result{}, errors.New("could not find any valid ports in listener: " + s.listener.Name + " namespace: " + s.listener.Namespace)
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.getName(),
			Namespace: s.getNamespace(),
		},
		Spec: corev1.ServiceSpec{
			Ports:                    ports,
			Selector:                 s.getPodSelectorLabels(),
			Type:                     *listenerClass.Spec.ServiceType,
			PublishNotReadyAddresses: s.listener.Spec.PublishNotReadyAddresses,
		},
	}

	if externalTrafficPolicy := s.getExternalTrafficPolicyFromListenerClass(listenerClass); externalTrafficPolicy != nil {
		logger.V(1).Info("set external traffic policy", "externalTrafficPolicy", *externalTrafficPolicy)
		service.Spec.ExternalTrafficPolicy = *externalTrafficPolicy
	}

	if err := ctrl.SetControllerReference(s.listener, service, s.client.Scheme()); err != nil {
		return ctrl.Result{}, err
	}

	if mutant, err := operatorclient.CreateOrUpdate(ctx, s.client, service); err != nil {
		return ctrl.Result{}, err
	} else if mutant {
		// we need to requeue the request to update the service immediately!
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (s *ServiceReconciler) getName() string {
	return s.listener.Name
}

func (s *ServiceReconciler) getNamespace() string {
	return s.listener.Namespace
}

func (s *ServiceReconciler) getListenerClass(ctx context.Context) (*listeners.ListenerClass, error) {
	listenerClass := &listeners.ListenerClass{}
	if err := s.client.Get(ctx, client.ObjectKey{Name: s.listener.Spec.ClassName}, listenerClass); err != nil {
		return nil, err
	}
	logger.V(1).Info("get listener class", "listenerClass", listenerClass.Name)
	return listenerClass, nil
}

func (s *ServiceReconciler) getPodSelectorLabels() map[string]string {
	labels := util.ListenerMountPodLabels(s.listener)

	podSelectorLabels := s.listener.Spec.ExtraPodSelectorLabels

	maps.Copy(labels, podSelectorLabels)

	logger.V(1).Info("get pod selector labels for service", "namespace", s.getNamespace(), "service", s.getName(), "selectorLabels", labels)
	return labels
}

func (s *ServiceReconciler) getExternalTrafficPolicyFromListenerClass(listenerClass *listeners.ListenerClass) *corev1.ServiceExternalTrafficPolicyType {
	serviceType := *listenerClass.Spec.ServiceType

	if serviceType == corev1.ServiceTypeNodePort || serviceType == corev1.ServiceTypeLoadBalancer {
		return &listenerClass.Spec.ServiceExternalTrafficPolicy
	}
	return nil
}

func (s *ServiceReconciler) describe(ctx context.Context) (*corev1.Service, error) {
	service := &corev1.Service{}
	key := client.ObjectKey{
		Namespace: s.listener.Namespace,
		Name:      s.getName(),
	}

	if err := s.client.Get(ctx, key, service); err != nil {
		return nil, err
	}
	logger.V(1).Info("describe service", "service", service.Name, "namespace", service.Namespace)
	return service, nil
}

type ServiceAddress struct {
	client  client.Client
	service *corev1.Service
}

func NewServiceAddress(client client.Client, service *corev1.Service) *ServiceAddress {
	return &ServiceAddress{
		client:  client,
		service: service,
	}
}

func (s *ServiceAddress) Name() string {
	return s.service.Name
}

func (s *ServiceAddress) Namespace() string {
	return s.service.Namespace
}

func (s *ServiceAddress) getLbIngressAddresses() ([]util.AddressInfo, error) {
	if s.service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil, errors.New("service is not of type LoadBalancer")
	}

	if len(s.service.Status.LoadBalancer.Ingress) == 0 {
		return nil, errors.New("service has no LoadBalancer Ingress")
	}

	addresses := make([]util.AddressInfo, 0, len(s.service.Status.LoadBalancer.Ingress))
	for _, ingress := range s.service.Status.LoadBalancer.Ingress {
		if ingress.Hostname != "" {
			addresses = append(addresses, util.AddressInfo{
				Address:     ingress.Hostname,
				AddressType: listeners.AddressTypeHostname,
			})
		}
		if ingress.IP != "" {
			addresses = append(addresses, util.AddressInfo{
				Address:     ingress.IP,
				AddressType: listeners.AddressTypeIP,
			})
		}
	}
	logger.Info("get lb ingress address", "addresses", addresses, "service", s.Name(), "namespace", s.Namespace())
	return addresses, nil
}

func (s *ServiceAddress) getClusterIpAddresses() ([]util.AddressInfo, error) {
	if s.service.Spec.Type != corev1.ServiceTypeClusterIP {
		return nil, fmt.Errorf("service.Spec.Type is not of type ClusterIP, but %s", s.service.Spec.Type)
	}

	addresses := make([]util.AddressInfo, 0, len(s.service.Spec.ClusterIPs))
	for _, clusterIP := range s.service.Spec.ClusterIPs {
		addresses = append(addresses, util.AddressInfo{
			Address:     clusterIP,
			AddressType: listeners.AddressTypeIP,
		})
	}

	logger.Info("get cluster ip", "addresses", addresses, "service", s.Name(), "namespace", s.Namespace())
	return addresses, nil
}

func (s *ServiceAddress) getServiceType() corev1.ServiceType {
	return s.service.Spec.Type
}

func (s *ServiceAddress) getNodesAddresses(ctx context.Context) ([]util.AddressInfo, error) {
	addresses := make([]util.AddressInfo, 0)
	nodeNames, err := s.getNodeNames(ctx)
	if err != nil {
		return addresses, err
	}

	for _, nodeName := range nodeNames {
		node := &corev1.Node{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: "", Name: nodeName}, node); err != nil {
			return nil, err
		}
		address, err := util.GetPriorNodeAddress(node)
		if err != nil {
			return nil, err
		}
		logger.V(1).Info("get node address", "address", address.Address, "node", node.Name)
		addresses = append(addresses, *address)
	}

	logger.Info("get nodes address", "addresses", addresses, "service", s.Name(), "namespace", s.Namespace())
	return addresses, nil
}

func (s *ServiceAddress) getNodeNames(ctx context.Context) ([]string, error) {
	endpointSliceList := &discoveryv1.EndpointSliceList{}
	if err := s.client.List(ctx, endpointSliceList, client.InNamespace(s.service.Namespace), client.MatchingLabels{"kubernetes.io/service-name": s.service.Name}); err != nil {
		return nil, fmt.Errorf("failed to list endpoints for service %s/%s: %w", s.service.Namespace, s.service.Name, err)
	}

	// Only when the pods associated with the service are available, endpoints will have a value, otherwise it will be empty.
	// Return an empty address when endpoints are not ready.
	// When endpoints are ready, this method will be called again to retrieve the addresses.
	if len(endpointSliceList.Items) == 0 {
		logger.V(1).Info("no endpoints found for service", "service", s.Name(), "namespace", s.Namespace())
		return nil, nil
	}

	nodeNames := make([]string, 0)
	for _, endpointSlice := range endpointSliceList.Items {
		for _, endpoint := range endpointSlice.Endpoints {
			if endpoint.NodeName != nil {
				nodeNames = append(nodeNames, *endpoint.NodeName)
			} else {
				logger.V(1).Info("endpoint has no node name", "endpoint", endpoint, "service", s.Name(), "namespace", s.Namespace())
			}
		}
	}

	logger.Info("get node names", "nodeNames", nodeNames, "service", s.Name(), "namespace", s.Namespace())
	return nodeNames, nil
}

func (s *ServiceAddress) getNodePorts() (map[string]int32, error) {
	if s.service.Spec.Type != corev1.ServiceTypeNodePort {
		return nil, errors.New("service is not of type NodePort")
	}

	ports := map[string]int32{}
	for _, port := range s.service.Spec.Ports {
		if port.Name == "" {
			logger.V(1).Info("port name is empty, so ignore it", "port", port, "service", s.Name(), "namespace", s.Namespace())
			continue
		}
		ports[port.Name] = port.NodePort
	}
	logger.Info("get node ports", "ports", ports, "service", s.Name(), "namespace", s.Namespace())
	return ports, nil
}

func (s *ServiceAddress) getPorts() map[string]int32 {
	ports := map[string]int32{}
	for _, port := range s.service.Spec.Ports {
		if port.Name == "" {
			logger.V(1).Info("port name is empty, so ignore it", "port", port, "service", s.Name(), "namespace", s.Namespace())
			continue
		}
		ports[port.Name] = port.Port
	}
	logger.Info("get ports", "ports", ports, "service", s.Name(), "namespace", s.Namespace())
	return ports
}
