package listener

import (
	"context"
	"path/filepath"
	"testing"

	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestServiceReconcileAgainstAPIServer(t *testing.T) {
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := listeners.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	class := &listeners.ListenerClass{
		ObjectMeta: metav1.ObjectMeta{Name: testExternalClassName},
		Spec:       listeners.ListenerClassSpec{ServiceType: ptr.To(corev1.ServiceTypeNodePort)},
	}
	instance := &listeners.Listener{
		ObjectMeta: metav1.ObjectMeta{Name: testClientPortName, Namespace: "service-test"},
		Spec: listeners.ListenerSpec{
			ClassName:                class.Name,
			Ports:                    []listeners.PortSpec{{Name: testClientPortName, Port: 8080}},
			PublishNotReadyAddresses: ptr.To(false),
		},
	}
	for _, obj := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: instance.Namespace}}, class, instance,
	} {
		if err := c.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	reconciler := NewServiceReconciler(c, instance)
	if _, err := reconciler.createService(ctx); err != nil {
		t.Fatal(err)
	}
	service := &corev1.Service{}
	key := client.ObjectKeyFromObject(instance)
	if err := c.Get(ctx, key, service); err != nil {
		t.Fatal(err)
	}
	if service.Spec.ClusterIP == "" || service.Spec.Ports[0].NodePort == 0 || service.Spec.PublishNotReadyAddresses {
		t.Fatalf("unexpected allocated service: %+v", service.Spec)
	}
	clusterIP, nodePort := service.Spec.ClusterIP, service.Spec.Ports[0].NodePort
	result, err := reconciler.createService(ctx)
	if err != nil || !result.IsZero() {
		t.Fatalf("server defaults must not trigger another update: %v, %v", result, err)
	}
	instance.Spec.Ports = []listeners.PortSpec{{Name: testMetricsPortName, Port: 9090}, {Name: testClientPortName, Port: 8081}}
	if _, err := reconciler.createService(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, key, service); err != nil {
		t.Fatal(err)
	}
	if service.Spec.ClusterIP != clusterIP || service.Spec.Ports[1].NodePort != nodePort {
		t.Fatalf("port changes lost existing allocations: %+v", service.Spec)
	}
	result, err = reconciler.createService(ctx)
	if err != nil || !result.IsZero() {
		t.Fatalf("updated service must converge: %v, %v", result, err)
	}
}
