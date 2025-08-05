package csi

import (
	"context"
	"errors"

	"k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/zncdatadev/listener-operator/internal/util/version"
	"github.com/zncdatadev/listener-operator/pkg/server"
)

const (
	DefaultDriverName = "listeners.kubedoop.dev"
)

var (
	logger = ctrl.Log.WithName("csi-driver")
)

type Driver struct {
	name     string
	nodeID   string
	endpoint string

	server server.NonBlockingServer

	client client.Client
}

// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listeners,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=podlisteners,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=listeners.kubedoop.dev,resources=listenerclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func NewDriver(
	nodeID string,
	endpoint string,
	client client.Client,
) *Driver {
	srv := server.NewNonBlockingServer(endpoint)

	return &Driver{
		name:     DefaultDriverName,
		nodeID:   nodeID,
		endpoint: endpoint,
		server:   srv,
		client:   client,
	}
}

func (d *Driver) Run(ctx context.Context) error {

	logger.V(1).Info("csi node driver information", "versionInfo", version.NewAppInfo(d.name).String())

	// check node id
	if d.nodeID == "" {
		return errors.New("NodeID is not provided")
	}

	cs := NewControllerServer(d.client)
	ns := NewNodeServer(d.nodeID, mount.New("listener-csi"), d.client)
	is := NewIdentityServer(d.name, version.BuildVersion)

	// Register the services with the gRPC server
	d.server.RegisterService(ns, is, cs)

	if err := d.server.Start(ctx); err != nil {
		return err
	}

	// Gracefully stop the server when the context is done
	go func() {
		<-ctx.Done()
		d.server.Stop()
	}()

	if err := d.server.Wait(); err != nil {
		logger.Error(err, "error while waiting for server to finish")
		return err
	}
	logger.Info("csi driver stopped")
	return nil
}

func (d *Driver) Stop() {
	d.server.Stop()
}
