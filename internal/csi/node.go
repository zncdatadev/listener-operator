package csi

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	znclistenersv1alpha1 "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/mount"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	listenersv1alpha1 "github.com/zncdatadev/listener-operator/api/v1alpha1"
	"github.com/zncdatadev/listener-operator/pkg/util"
)

// volumeContext is the struct for create Volume ctx from PVC annotations
type volumeContext struct {
	// Default values for volume context
	Pod                *string `json:"csi.storage.k8s.io/pod.name"`
	PodNamespace       *string `json:"csi.storage.k8s.io/pod.namespace"`
	PodUID             *string `json:"csi.storage.k8s.io/pod.uid"`
	ServiceAccountName *string `json:"csi.storage.k8s.io/serviceAccount.name"`
	Ephemeral          *string `json:"csi.storage.k8s.io/ephemeral"`
	Provisioner        *string `json:"storage.kubernetes.io/csiProvisionerIdentity"`

	// User defined annotations for PVC
	ListenerClassName *string `json:"listeners.zncdata.dev/listener-class"` // required
	ListenerName      *string `json:"listeners.zncdata.dev/listener-name"`  // optional
}

func newVolumeContextFromMap(parameters map[string]string) *volumeContext {
	v := &volumeContext{}
	if val, ok := parameters[CSI_STORAGE_POD_NAME]; ok {
		v.Pod = &val
	}
	if val, ok := parameters[CSI_STORAGE_POD_NAMESPACE]; ok {
		v.PodNamespace = &val
	}
	if val, ok := parameters[CSI_STORAGE_POD_UID]; ok {
		v.PodUID = &val
	}
	if val, ok := parameters[CSI_STORAGE_SERVICE_ACCOUNT_NAME]; ok {
		v.ServiceAccountName = &val
	}
	if val, ok := parameters[CSI_STORAGE_EPHEMERAL]; ok {
		v.Ephemeral = &val
	}
	if val, ok := parameters[STORAGE_KUBERNETES_CSI_PROVISIONER_IDENTITY]; ok {
		v.Provisioner = &val
	}
	if val, ok := parameters[util.ListenersZncdataListenerClass]; ok {
		v.ListenerClassName = &val
	}
	if val, ok := parameters[util.ListenersZncdataListenerName]; ok {
		v.ListenerName = &val
	}

	return v
}

var _ csi.NodeServer = &NodeServer{}

type NodeServer struct {
	csi.UnimplementedNodeServer
	mounter mount.Interface
	nodeID  string
	client  client.Client
}

func NewNodeServer(
	nodeId string,
	mounter mount.Interface,
	client client.Client,
) *NodeServer {
	return &NodeServer{
		nodeID:  nodeId,
		mounter: mounter,
		client:  client,
	}
}

func (n *NodeServer) NodePublishVolume(ctx context.Context, request *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if err := n.validateNodePublishVolumeRequest(request); err != nil {
		return nil, err
	}

	targetPath := request.GetTargetPath()

	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	// get the volume context
	// Default, volume context contains data:
	//   - csi.storage.k8s.io/pod.name: <pod-name>
	//   - csi.storage.k8s.io/pod.namespace: <pod-namespace>
	//   - csi.storage.k8s.io/pod.uid: <pod-uid>
	//   - csi.storage.k8s.io/serviceAccount.name: <service-account-name>
	//   - csi.storage.k8s.io/ephemeral: <true|false>
	//   - storage.kubernetes.io/csiProvisionerIdentity: <provisioner-identity>
	//   - volume.kubernetes.io/storage-provisioner: <provisioner-name>
	//   - volume.beta.kubernetes.io/storage-provisioner: <provisioner-name>
	// If need more information about PVC, you should pass it to CreateVolumeResponse.Volume.VolumeContext
	// when called CreateVolume response in the controller side. Then use them here.
	// In this csi, we can get PVC annotations from volume context,
	// because we delivery it from controller to node already.
	// The following PVC annotations is required:
	//   - listeners.zncdata.dev/class: <listener-class-name>
	volumeContext := newVolumeContextFromMap(request.GetVolumeContext())

	if volumeContext.ListenerClassName == nil {
		return nil, status.Error(codes.InvalidArgument, "listener class name missing in request")
	}

	listenerClass := &listenersv1alpha1.ListenerClass{}

	// get the listener class
	if err := n.client.Get(ctx, client.ObjectKey{
		Name:      *volumeContext.ListenerClassName,
		Namespace: *volumeContext.PodNamespace,
	}, listenerClass); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pod, err := n.getPod(ctx, *volumeContext.Pod, *volumeContext.PodNamespace)

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// get the listener if listener name already exist volume context,
	// else create or update by listener class and pod info.
	listener, err := n.getListener(ctx, request.GetVolumeId(), *volumeContext)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// update pod label with listener name
	if err := n.patchPodLabelWithListener(ctx, pod, listener); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	data, err := n.getAddresses(ctx, listener, pod)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// mount the volume to the target path
	if err := n.mount(targetPath); err != nil {
		return nil, err
	}

	// write the listener data to the target path
	if err := n.writeData(targetPath, data); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// writeData writes the data to the target path.
func (n *NodeServer) writeData(targetPath string, data []util.IngressAddress) error {

	if data == nil {
		log.V(1).Info("Listener data is nil, skip write data")
		return nil
	}

	log.V(5).Info("Writing data to target path", "targetPath", targetPath, "data", data)

	// mkdir addresses path
	addressesPath := filepath.Join(targetPath, "addresses")
	if err := os.MkdirAll(addressesPath, 0755); err != nil {
		log.Error(err, "Mkdir addresses path error", "path", addressesPath)
		return err
	}
	log.V(5).Info("Mkdir addresses path", "path", addressesPath)

	var defaultAddressPath string

	for _, listenerData := range data {
		// mkdir address path
		listenerAddressPath := filepath.Join(addressesPath, listenerData.Address)
		if err := os.MkdirAll(listenerAddressPath, 0755); err != nil {
			log.Error(err, "Mkdir listener address path error", "path", listenerAddressPath)
			return err
		}
		log.V(5).Info("Mkdir listener address path", "address", listenerData.Address, "path", listenerAddressPath)
		// write address and ports
		if err := n.writeAddress(listenerAddressPath, listenerData); err != nil {
			log.Error(err, "Write address to listener address path error", "path", listenerAddressPath)
			return err
		}
		log.V(5).Info("Write address to listener address path", "address", listenerData.Address, "path", listenerAddressPath)
		defaultAddressPath = listenerAddressPath
	}

	if err := n.symlinkToDefaultAddress(defaultAddressPath, targetPath); err != nil {
		return err
	}

	return nil

}

func (n *NodeServer) writeAddress(targetPath string, data util.IngressAddress) error {
	if err := os.WriteFile(filepath.Join(targetPath, "address"), []byte(data.Address), fs.FileMode(0644)); err != nil {
		log.Error(err, "Write address to target path error", "path", targetPath)
		return err
	}

	listenerAddressPortPath := filepath.Join(targetPath, "ports")
	if err := os.MkdirAll(listenerAddressPortPath, 0755); err != nil {
		log.Error(err, "Mkdir listener address port path error", "path", listenerAddressPortPath)
		return err
	}

	for name, port := range data.Ports {
		portStr := strconv.Itoa(int(port))
		if err := os.WriteFile(filepath.Join(listenerAddressPortPath, name), []byte(portStr), fs.FileMode(0644)); err != nil {
			return err
		}
		log.V(5).Info("Write port to target path", "port", port, "address", data.Address)
	}
	return nil
}

func (n *NodeServer) symlinkToDefaultAddress(defaultAddressPath, targetPath string) error {
	sourcePath := strings.TrimPrefix(defaultAddressPath, targetPath)
	sourcePath = strings.TrimPrefix(sourcePath, "/")
	destPath := filepath.Join(targetPath, "default-address")
	if err := os.Symlink(sourcePath, destPath); err != nil {
		log.Error(err, "Symlink to default address error", "sourcePath", sourcePath, "destPath", destPath)
		return err
	}
	log.V(5).Info("Symlink to default address", "sourcePath", sourcePath, "destPath", destPath)
	return nil
}

func (n *NodeServer) patchPodLabelWithListener(
	ctx context.Context,
	pod *corev1.Pod,
	listener *znclistenersv1alpha1.Listener,
) error {
	// patch pod label with listener name
	copyedPod := pod.DeepCopy()
	if copyedPod.Labels == nil {
		copyedPod.Labels = map[string]string{}
	}

	maps.Copy(copyedPod.Labels, util.ListenerLabelsForPod(listener.Spec.ClassName, listener.Name))

	if err := n.client.Patch(ctx, copyedPod, client.MergeFrom(pod)); err != nil {
		log.Error(err, "Patch pod label error", "pod", pod.Name, "namespace", pod.Namespace)
		return err
	}
	log.V(5).Info("Pod label patched with listener name", "pod", pod.Name, "namespace", pod.Namespace, "patchedLabels", copyedPod.Labels)
	return nil
}

// getAddresses gets the listener address and ports from the listener status.
// When get address from listener status, if listener status is not ready,
// an error will raise. NodeController will retry to get address from listener status.
func (n *NodeServer) getAddresses(
	ctx context.Context,
	listener *znclistenersv1alpha1.Listener,
	pod *corev1.Pod,
) ([]util.IngressAddress, error) {
	if len(listener.Status.NodePorts) != 0 {
		address, err := n.getNodeAddressByPod(ctx, pod)
		if err != nil {
			return nil, err
		}
		log.V(5).Info("get address from listener status", "address", address, "listener", listener.Name, "namespace", listener.Namespace)
		return []util.IngressAddress{
			{
				AddressInfo: *address,
				Ports:       listener.Status.NodePorts,
			},
		}, nil

	} else if len(listener.Status.IngressAddresses) != 0 {
		var addresses []util.IngressAddress
		for _, ingressAddress := range listener.Status.IngressAddresses {
			log.V(5).Info("get address from listener status", "address", ingressAddress, "listener", listener.Name, "namespace", listener.Namespace)
			addresses = append(addresses, util.IngressAddress{
				AddressInfo: util.AddressInfo{
					Address:     ingressAddress.Address,
					AddressType: ingressAddress.AddressType,
				},
				Ports: ingressAddress.Ports,
			})
		}
		return addresses, nil
	}
	log.V(5).Info("can not found address from listener status", "listener", listener.Name, "namespace", listener.Namespace)
	return nil, status.Error(codes.Internal, "can not found address from listener status")
}

func (n *NodeServer) getNodeAddressByPod(ctx context.Context, pod *corev1.Pod) (*util.AddressInfo, error) {
	node := &corev1.Node{}

	if err := n.client.Get(ctx, client.ObjectKey{
		Name: pod.Spec.NodeName,
	}, node); err != nil {
		return nil, err
	}

	address, err := util.GetPriorNodeAddress(node)
	if err != nil {
		return nil, err
	}
	log.Info("get address from node", "address", address, "node", node.Name)
	return address, nil

}

func (n *NodeServer) getPod(ctx context.Context, podName, podNamespace string) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	if err := n.client.Get(ctx, client.ObjectKey{
		Name:      podName,
		Namespace: podNamespace,
	}, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func (*NodeServer) getPodPorts(pod *corev1.Pod) ([]znclistenersv1alpha1.PortSpec, error) {
	ports := []znclistenersv1alpha1.PortSpec{}
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name != "" {
				ports = append(ports, znclistenersv1alpha1.PortSpec{
					Name:     port.Name,
					Protocol: port.Protocol,
					Port:     port.ContainerPort,
				})
				log.V(8).Info("get pod port", "port", port, "container", container.Name, "pod", pod.Name, "namespace", pod.Namespace)
			} else {
				log.Info("port name is empty, so ignore to add listener", "port", port, "container", container.Name, "pod", pod.Name, "namespace", pod.Namespace)
			}
		}
	}

	if len(ports) == 0 {
		log.Info("pod has no vaild ports, please ensure all port has name or pod has at least one valid port", "pod", pod.Name, "namespace", pod.Namespace)
		return nil, status.Error(codes.Internal, "pod has no vaild ports, please ensure all port has name")
	}
	log.V(5).Info("get pod ports", "ports", ports, "pod", pod.Name, "namespace", pod.Namespace)
	return ports, nil
}

func (n *NodeServer) getPVC(ctx context.Context, pvcName, pvcNamespace string) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := n.client.Get(ctx, client.ObjectKey{
		Name:      pvcName,
		Namespace: pvcNamespace,
	}, pvc); err != nil {
		return nil, err
	}
	return pvc, nil
}

func (n *NodeServer) getPV(ctx context.Context, pvName string) (*corev1.PersistentVolume, error) {
	pv := &corev1.PersistentVolume{}
	if err := n.client.Get(ctx, client.ObjectKey{
		Name: pvName,
	}, pv); err != nil {
		return nil, err
	}
	return pv, nil
}

// getListener get listener if listener name already exist volume context,
// else create or update by listener class and pod info.
// !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!! NOTE !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
// If use listener status immediately after tihs method called, when the
// listener is createOrUpdate with listener class,
// listener status my not updated, then you will get error.
// Do not warry, we can get listener status in the next time.
func (n *NodeServer) getListener(ctx context.Context, pvName string, volumeContext volumeContext) (*znclistenersv1alpha1.Listener, error) {

	if volumeContext.ListenerName != nil {
		listener := &znclistenersv1alpha1.Listener{}
		if err := n.client.Get(ctx, client.ObjectKey{
			Name:      *volumeContext.ListenerName,
			Namespace: *volumeContext.PodNamespace,
		}, listener); err != nil {
			return nil, err
		}
		return listener, nil
	}

	// get pod
	pod, err := n.getPod(ctx, *volumeContext.Pod, *volumeContext.PodNamespace)
	if err != nil {
		return nil, err
	}

	// get the pv
	pv, err := n.getPV(ctx, pvName)
	if err != nil {
		return nil, err
	}

	// get pvc
	pvc, err := n.getPVC(ctx, pv.Spec.ClaimRef.Name, pv.Spec.ClaimRef.Namespace)
	if err != nil {
		return nil, err
	}

	// Note: all port name must be set, otherwise it will raise error
	ports, err := n.getPodPorts(pod)
	if err != nil {
		return nil, err
	}

	// get listener when listener name exist in volume context,·
	// else create or update a listener by listener class and pod info.
	listener, err := n.createOrUpdateListener(
		ctx,
		volumeContext,
		pv,
		pvc.GetObjectMeta().GetLabels(),
		ports,
	)
	if err != nil {
		return nil, err
	}

	return listener, nil
}

func (n *NodeServer) createOrUpdateListener(
	ctx context.Context,
	volumeContext volumeContext,
	pv *corev1.PersistentVolume,
	labels map[string]string,
	ports []znclistenersv1alpha1.PortSpec,
) (*znclistenersv1alpha1.Listener, error) {

	listener, err := n.buildListener(
		*volumeContext.Pod,
		*volumeContext.PodNamespace,
		pv,
		labels,
		*volumeContext.ListenerClassName,
		ports,
	)

	if err != nil {
		return nil, err
	}

	if err := n.client.Get(ctx, client.ObjectKey{
		Name:      listener.Name,
		Namespace: listener.Namespace,
	}, listener); errors.IsNotFound(err) {
		log.V(5).Info("Listener not found, create listener", "listener", listener.Name, "namespace", listener.Namespace)
		if err := n.client.Create(ctx, listener); err != nil {
			return nil, err
		}
		log.V(5).Info("A new listener created, we retry to get listener", "listener", listener.Name, "namespace", listener.Namespace)
		// wait for listener status ready, then retry to get listener
		// to avoid get listener status error
		time.Sleep(200 * time.Millisecond)
		if err := n.client.Get(ctx, client.ObjectKey{
			Name:      listener.Name,
			Namespace: listener.Namespace,
		}, listener); err != nil {
			return nil, err
		}
		log.V(5).Info("Found this listener just created. But this listener status may not be ready.", "listener", listener.Name, "namespace", listener.Namespace)

	} else if err == nil {
		log.V(5).Info("Listener found, update listener", "listener", listener.Name, "namespace", listener.Namespace)
		if err := n.client.Update(ctx, listener); err != nil {
			return nil, err
		}
	} else {
		log.Error(err, "Get listener error")
		return nil, err
	}

	return listener, nil
}

func (n *NodeServer) buildListener(
	podName string,
	podNamespace string,
	owner client.Object,
	labales map[string]string,
	listenerClassName string,
	ports []znclistenersv1alpha1.PortSpec,
) (*znclistenersv1alpha1.Listener, error) {

	obj := &znclistenersv1alpha1.Listener{
		Spec: znclistenersv1alpha1.ListenerSpec{
			ClassName: listenerClassName,
			Ports:     ports,
		},

		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: podNamespace,
			Labels:    labales,
		},
	}

	// set the owner reference
	if err := ctrl.SetControllerReference(owner, obj, n.client.Scheme()); err != nil {
		return nil, err
	}

	return obj, nil
}

// mount mounts the volume to the target path.
// Mount the volume to the target path with tmpfs.
// The target path is created if it does not exist.
// The volume is mounted with the following options:
//   - noexec (no execution)
//   - nosuid (no set user ID)
//   - nodev (no device)
func (n *NodeServer) mount(targetPath string) error {
	// check if the target path exists
	// if not, create the target path
	// if exists, return error
	if exist, err := mount.PathExists(targetPath); err != nil {
		return status.Error(codes.Internal, err.Error())
	} else if exist {
		return status.Error(codes.Internal, "target path "+targetPath+" already exists")
	} else {
		if err := os.MkdirAll(targetPath, 0750); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}

	opts := []string{
		"noexec",
		"nosuid",
		"nodev",
	}

	// mount the volume to the target path
	if err := n.mounter.Mount("tmpfs", targetPath, "tmpfs", opts); err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	return nil
}

// NodeUnpublishVolume unpublishes the volume from the node.
// unmount the volume from the target path, and remove the target path
func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, request *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	// check requests
	if request.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if request.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	targetPath := request.GetTargetPath()

	// unmount the volume from the target path
	if err := n.mounter.Unmount(targetPath); err != nil {
		// FIXME: use status.Error to return error
		// return nil, status.Error(codes.Internal, err.Error())
		log.V(1).Info("Volume not found, skip delete volume")
	}

	// remove the target path
	if err := os.RemoveAll(targetPath); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.V(5).Info("Volume unpublished", "volumeID", request.GetVolumeId(), "targetPath", targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeServer) validateNodePublishVolumeRequest(request *csi.NodePublishVolumeRequest) error {
	if request.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "volume ID missing in request")
	}
	if request.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	if request.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}

	if request.GetVolumeContext() == nil || len(request.GetVolumeContext()) == 0 {
		return status.Error(codes.InvalidArgument, "Volume context missing in request")
	}
	return nil
}

func (n *NodeServer) NodeStageVolume(ctx context.Context, request *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if len(request.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if len(request.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target path missing in request")

	}

	if request.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnstageVolume(ctx context.Context, request *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	if len(request.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if len(request.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target path missing in request")

	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (n *NodeServer) NodeGetCapabilities(ctx context.Context, request *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	newCapabilities := func(cap csi.NodeServiceCapability_RPC_Type) *csi.NodeServiceCapability {
		return &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		}
	}

	var capabilities []*csi.NodeServiceCapability

	for _, capability := range []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	} {
		capabilities = append(capabilities, newCapabilities(capability))
	}

	resp := &csi.NodeGetCapabilitiesResponse{
		Capabilities: capabilities,
	}

	return resp, nil

}

func (n *NodeServer) NodeGetInfo(ctx context.Context, request *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: n.nodeID,
	}, nil
}
