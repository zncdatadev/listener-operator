package listener

import (
	"context"
	"testing"

	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testClientPortName    = "client"
	testMetricsPortName   = "metrics"
	testExternalClassName = "external"
)

func TestServiceUpdatePreservesAllocationsAndConverges(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := listeners.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &listeners.Listener{
		ObjectMeta: metav1.ObjectMeta{Name: testClientPortName, Namespace: "default", UID: types.UID("listener-uid")},
		Spec: listeners.ListenerSpec{
			ClassName: testExternalClassName,
			Ports: []listeners.PortSpec{
				{Name: testMetricsPortName, Protocol: corev1.ProtocolTCP, Port: 9090},
				{Name: testClientPortName, Protocol: corev1.ProtocolTCP, Port: 8081},
				{Name: "new", Port: 8082},
			},
		},
	}
	class := &listeners.ListenerClass{
		ObjectMeta: metav1.ObjectMeta{Name: testExternalClassName},
		Spec: listeners.ListenerClassSpec{
			ServiceType:                  ptr.To(corev1.ServiceTypeNodePort),
			ServiceExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyCluster,
		},
	}
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name, Namespace: instance.Namespace,
			Annotations: map[string]string{"external.example/managed": "keep"},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.0.0.10", ClusterIPs: []string{"10.0.0.10", "fd00::10"},
			IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol},
			IPFamilyPolicy: ptr.To(corev1.IPFamilyPolicyPreferDualStack),
			Ports: []corev1.ServicePort{
				{Name: testClientPortName, Protocol: corev1.ProtocolTCP, Port: 8080, NodePort: 30080},
				{Name: testMetricsPortName, Protocol: corev1.ProtocolTCP, Port: 9090, NodePort: 30090},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, class, existing).Build()
	reconciler := NewServiceReconciler(c, instance)
	ctx := context.Background()
	result, err := reconciler.createService(ctx)
	if err != nil || result.IsZero() {
		t.Fatalf("expected changed service to requeue, got %v, %v", result, err)
	}
	actual := &corev1.Service{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(existing), actual); err != nil {
		t.Fatal(err)
	}
	if actual.Spec.ClusterIP != existing.Spec.ClusterIP || len(actual.Spec.ClusterIPs) != 2 ||
		len(actual.Spec.IPFamilies) != 2 || *actual.Spec.IPFamilyPolicy != *existing.Spec.IPFamilyPolicy {
		t.Fatalf("lost IP allocations: %+v", actual.Spec)
	}
	if actual.Spec.Ports[0].NodePort != 30090 || actual.Spec.Ports[1].NodePort != 30080 ||
		actual.Spec.Ports[2].NodePort != 0 {
		t.Fatalf("node ports must follow port identity: %+v", actual.Spec.Ports)
	}
	if actual.Spec.Ports[1].Port != 8081 || actual.Spec.Ports[1].TargetPort != intstr.FromInt32(8081) {
		t.Fatalf("port change was not applied: %+v", actual.Spec.Ports[1])
	}
	if actual.Spec.Ports[2].Protocol != corev1.ProtocolTCP {
		t.Fatal("an omitted protocol must default to TCP")
	}
	if !actual.Spec.PublishNotReadyAddresses || actual.Annotations["external.example/managed"] != "keep" ||
		metav1.GetControllerOf(actual) == nil {
		t.Fatalf("lost listener defaults or metadata: %+v", actual)
	}
	result, err = reconciler.createService(ctx)
	if err != nil || !result.IsZero() {
		t.Fatalf("unchanged service must converge, got %v, %v", result, err)
	}

	class.Spec.ServiceType = ptr.To(corev1.ServiceTypeClusterIP)
	if err := c.Update(ctx, class); err != nil {
		t.Fatal(err)
	}
	instance.Spec.PublishNotReadyAddresses = ptr.To(false)
	if _, err := reconciler.createService(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(existing), actual); err != nil {
		t.Fatal(err)
	}
	for _, port := range actual.Spec.Ports {
		if port.NodePort != 0 {
			t.Fatalf("ClusterIP service retained a node port: %+v", port)
		}
	}
	if actual.Spec.PublishNotReadyAddresses {
		t.Fatal("explicit false must override the listener default")
	}
}
