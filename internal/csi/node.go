package csi

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
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
	if val, ok := parameters[LISTENERS_ZNCDATA_LISTENER_CLASS]; ok {
		v.ListenerClassName = &val
	}
	if val, ok := parameters[LISTENERS_ZNCDATA_LISTENER_NAME]; ok {
		v.ListenerName = &val
	}

	return v
}

type AddressInfo struct {
	Address     string
	AddressType listenersv1alpha1.AddressType
}

type ListenerIngress struct {
	AddressInfo
	Ports []listenersv1alpha1.PortSpec
}

var _ csi.NodeServer = &NodeServer{}

type NodeServer struct {
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

	// get the listener
	listener, err := n.getListener(ctx, request.GetVolumeId(), *volumeContext)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	data, err := n.getAddressForPod(ctx, listener, pod)
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
func (n *NodeServer) writeData(targetPath string, data *ListenerIngress) error {

	if data == nil {
		log.V(1).Info("Listener data is nil, skip write data")
		return nil
	}

	// mkdir addresses path
	addressesPath := filepath.Join(targetPath, "addresses")
	if err := os.MkdirAll(addressesPath, 0750); err != nil {
		return err
	}

	// mkdir address path
	listenerAddressPath := filepath.Join(addressesPath, data.Address)
	if err := os.MkdirAll(listenerAddressPath, 0750); err != nil {
		return err
	}

	// write address
	if err := os.WriteFile(filepath.Join(listenerAddressPath, "address"), []byte(data.Address), fs.FileMode(0644)); err != nil {
		return err
	}

	// make ports path in address
	listenerAddressPortPath := filepath.Join(listenerAddressPath, "ports")

	// write ports
	for _, port := range data.Ports {
		portStr := strconv.Itoa(int(port.Port))
		if err := os.WriteFile(filepath.Join(listenerAddressPortPath, port.Name), []byte(portStr), fs.FileMode(0644)); err != nil {
			return err
		}
	}
	return nil
}

func (n *NodeServer) getAddressForPod(
	ctx context.Context,
	listener *listenersv1alpha1.Listener,
	pod *corev1.Pod,
) (*ListenerIngress, error) {
	if len(listener.Status.NodePorts) != 0 {

		address, err := n.getPriorNodeAddress(ctx, pod)

		if err != nil {
			return nil, err
		}

		return &ListenerIngress{
			AddressInfo: *address,
			Ports:       listener.Status.NodePorts,
		}, nil
	} else if len(listener.Status.IngressAddress) != 0 {
		for _, ingressAddress := range listener.Status.IngressAddress {
			return &ListenerIngress{
				AddressInfo: AddressInfo{
					Address:     ingressAddress.Address,
					AddressType: ingressAddress.AddressType,
				},
				Ports: *ingressAddress.Ports,
			}, nil
		}
	}

	return nil, status.Error(codes.Internal, "listener address not found")
}

func (n *NodeServer) getPriorNodeAddress(ctx context.Context, pod *corev1.Pod) (*AddressInfo, error) {
	node := &corev1.Node{}

	if err := n.client.Get(ctx, client.ObjectKey{
		Name: pod.Spec.NodeName,
	}, node); err != nil {
		return nil, err
	}

	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeExternalIP {
			return &AddressInfo{
				Address:     address.Address,
				AddressType: listenersv1alpha1.AddressTypeIP,
			}, nil
		} else if address.Type == corev1.NodeInternalIP {
			return &AddressInfo{
				Address:     address.Address,
				AddressType: listenersv1alpha1.AddressTypeIP,
			}, nil
		} else if address.Type == corev1.NodeHostName {
			return &AddressInfo{
				Address:     address.Address,
				AddressType: listenersv1alpha1.AddressTypeHostname,
			}, nil
		}
	}
	return nil, status.Error(codes.Internal, "node address not found")
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

func (*NodeServer) getPodPorts(pod *corev1.Pod) []listenersv1alpha1.PortSpec {
	ports := []listenersv1alpha1.PortSpec{}
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			ports = append(ports, listenersv1alpha1.PortSpec{
				Name:     port.Name,
				Protocol: port.Protocol,
				Port:     port.ContainerPort,
			})
		}
	}
	return ports
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
// listener status my not updated. You can got incorrect status data.
func (n *NodeServer) getListener(ctx context.Context, pvName string, volumeContext volumeContext) (*listenersv1alpha1.Listener, error) {

	if volumeContext.ListenerName != nil {
		listener := &listenersv1alpha1.Listener{}
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

	// apply the listener
	listener, err := n.applyListener(
		ctx,
		volumeContext,
		pv,
		pvc.GetObjectMeta().GetLabels(),
		n.getPodPorts(pod),
	)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func (n *NodeServer) applyListener(
	ctx context.Context,
	volumeContext volumeContext,
	pv *corev1.PersistentVolume,
	labels map[string]string,
	ports []listenersv1alpha1.PortSpec,
) (*listenersv1alpha1.Listener, error) {
	listener := n.buildListener(
		*volumeContext.Pod,
		*volumeContext.PodNamespace,
		labels,
		*volumeContext.ListenerClassName,
		ports,
	)

	// set the owner reference
	if err := ctrl.SetControllerReference(pv, listener, n.client.Scheme()); err != nil {
		return nil, err
	}

	if _, err := CreateOrUpdate(ctx, n.client, listener); err != nil {
		return nil, err
	}
	return listener, nil
}

func (n *NodeServer) buildListener(
	podName string,
	podNamespace string,
	labales map[string]string,
	listenerClassName string,
	ports []listenersv1alpha1.PortSpec,
) *listenersv1alpha1.Listener {

	obj := &listenersv1alpha1.Listener{
		Spec: listenersv1alpha1.ListenerSpec{
			ClassName: listenerClassName,
			Ports:     ports,
		},

		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: podNamespace,
			Labels:    labales,
		},
	}
	return obj
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
		return status.Error(codes.Internal, "target path already exists")
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

func (n *NodeServer) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (n NodeServer) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
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
