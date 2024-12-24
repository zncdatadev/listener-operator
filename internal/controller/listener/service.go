package listener

import (
	"context"
	"errors"
	"maps"

	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	operatorclient "github.com/zncdatadev/operator-go/pkg/client"
	corev1 "k8s.io/api/core/v1"
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

func (s *ServiceReconciler) getNodePorts(service *corev1.Service) (map[string]int32, error) {
	if service.Spec.Type != corev1.ServiceTypeNodePort {
		return nil, errors.New("service is not of type NodePort")
	}

	ports := map[string]int32{}
	for _, port := range service.Spec.Ports {
		if port.Name == "" {
			logger.V(1).Info("port name is empty, so ignore it", "port", port, "service", service.Name, "namespace", service.Namespace)
			continue
		}
		ports[port.Name] = port.NodePort
	}
	logger.Info("get node ports", "ports", ports, "service", service.Name, "namespace", service.Namespace)
	return ports, nil
}

func (s *ServiceReconciler) getPorts(service *corev1.Service) map[string]int32 {
	ports := map[string]int32{}
	for _, port := range service.Spec.Ports {
		if port.Name == "" {
			logger.V(1).Info("port name is empty, so ignore it", "port", port, "service", service.Name, "namespace", service.Namespace)
			continue
		}
		ports[port.Name] = port.Port
	}
	logger.Info("get ports", "ports", ports, "service", service.Name, "namespace", service.Namespace)
	return ports
}

func (s *ServiceReconciler) getLbIngressAddress(
	service *corev1.Service,
) ([]util.AddressInfo, error) {
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil, errors.New("service is not of type LoadBalancer")
	}

	if len(service.Status.LoadBalancer.Ingress) == 0 {
		return nil, errors.New("service has no LoadBalancer Ingress")
	}

	addresses := make([]util.AddressInfo, 0, len(service.Status.LoadBalancer.Ingress))
	for _, ingress := range service.Status.LoadBalancer.Ingress {
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
	logger.Info("get lb ingress address", "addresses", addresses, "service", service.Name, "namespace", service.Namespace)
	return addresses, nil
}

func (s *ServiceReconciler) getClusterIp(service *corev1.Service) (string, error) {
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		return "", errors.New("service is not of type ClusterIP")
	}

	return service.Spec.ClusterIP, nil
}

func (s *ServiceReconciler) getServiceType(service *corev1.Service) corev1.ServiceType {
	return service.Spec.Type
}

func (s *ServiceReconciler) getNodesAddress(ctx context.Context) ([]util.AddressInfo, error) {
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

	logger.Info("get nodes address", "addresses", addresses, "service", s.getName(), "namespace", s.getNamespace())
	return addresses, nil
}

func (s *ServiceReconciler) getNodeNames(ctx context.Context) ([]string, error) {
	ns := s.getNamespace()
	name := s.getName()
	nodeNames := make([]string, 0)

	endpoints := &corev1.Endpoints{}
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, endpoints); err != nil {
		return nodeNames, err
	}

	// Only when the pods associated with the service are available, endpoints will have a value, otherwise it will be empty.
	// Return an empty address when endpoints are not ready.
	// When endpoints are ready, this method will be called again to retrieve the addresses.
	if len(endpoints.Subsets) == 0 {
		logger.V(1).Info("endpoints is not ready", "service", name, "namespace", ns)
		return nodeNames, nil
	}

	for _, subset := range endpoints.Subsets {
		for _, address := range subset.Addresses {
			nodeNames = append(nodeNames, *address.NodeName)
		}
	}

	logger.Info("get node names", "nodeNames", nodeNames, "service", name, "namespace", ns)
	return nodeNames, nil
}
