package csi

import (
	"context"
	"errors"
	"regexp"

	"github.com/container-storage-interface/spec/lib/go/csi"
	listeners "github.com/zncdatadev/operator-go/pkg/apis/listeners/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zncdatadev/operator-go/pkg/constants"
)

var (
	volumeCaps = []*csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		},
	}
)

type createVolumeRequestParams struct {
	PVCName      string
	pvcNamespace string
}

func newCreateVolumeRequestParamsFromMap(params map[string]string) (*createVolumeRequestParams, error) {
	pvcName, pvcNameExists := params[CSI_STORAGE_PVC_NAME]
	pvcNamespace, pvcNamespaceExists := params[CSI_STORAGE_PVC_NAMESPACE]

	if !pvcNameExists || !pvcNamespaceExists {
		return nil, status.Error(codes.InvalidArgument, "ensure '--extra-create-metadata' args are added in the sidecar of the csi-provisioner container.")
	}

	return &createVolumeRequestParams{
		PVCName:      pvcName,
		pvcNamespace: pvcNamespace,
	}, nil
}

type ControllerServer struct {
	csi.UnimplementedControllerServer
	client client.Client
}

var _ csi.ControllerServer = &ControllerServer{}

func NewControllerServer(client client.Client) *ControllerServer {
	return &ControllerServer{
		client: client,
	}
}

func (c *ControllerServer) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := validateCreateVolumeRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	requiredCap := request.CapacityRange.GetRequiredBytes()

	if request.Parameters["listenerFinalizer"] == "true" {
		log.V(1).Info("volume is listener finalizer", "volume", request.Name)

	}

	// requests.parameters is StorageClass.Parameters, which is set by user when creating PVC.
	// When adding '--extra-create-metadata' args in sidecar of registry.k8s.io/sig-storage/csi-provisioner container, we can get
	// 'csi.storage.k8s.io/pvc/name' and 'csi.storage.k8s.io/pvc/namespace' from params.
	// ref: https://github.com/kubernetes-csi/external-provisioner?tab=readme-ov-file#command-line-options
	params, err := newCreateVolumeRequestParamsFromMap(request.Parameters)

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Get createVolumeRequestParams error: %v", err)
	}

	volumeCtx, err := c.getVolumeContext(ctx, params)

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Get listener Volume refer error: %v", err)
	}

	listenerClassName, exist := volumeCtx[constants.AnnotationListenersClass]

	if !exist {
		return nil, status.Errorf(codes.InvalidArgument, "Get listener class name error: %v", err)
	}

	listenerClass, err := c.getListenerClass(ctx, listenerClassName, params.pvcNamespace)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "ListenerClass: %q. Detail: %v", listenerClassName, err)
	}

	accessibleTopology := c.getAccessibleTopology(request, listenerClass)
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           request.GetName(),
			CapacityBytes:      requiredCap,
			VolumeContext:      volumeCtx,
			AccessibleTopology: accessibleTopology,
		},
	}, nil
}

func (c *ControllerServer) getAccessibleTopology(request *csi.CreateVolumeRequest, listenerClass *listeners.ListenerClass) []*csi.Topology {
	if *listenerClass.Spec.ServiceType == corev1.ServiceTypeNodePort {
		return request.GetAccessibilityRequirements().GetRequisite()
	} else {
		return []*csi.Topology{}
	}
}

func (c *ControllerServer) getListenerClass(ctx context.Context, name string, namespace string) (*listeners.ListenerClass, error) {
	listenerClass := &listeners.ListenerClass{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, listenerClass); err != nil {
		return nil, err
	}

	return listenerClass, nil
}

func (c *ControllerServer) getPvc(ctx context.Context, name, namespace string) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, pvc); err != nil {
		return nil, err
	}

	return pvc, nil
}

// getVolumeContext gets volume ctx from PVC annotations

//   - get PVC by k8s client with PVC name and namespace, then get annotations from PVC.
//   - get 'listeners.kubedoop.dev/class' from PVC annotations, and check.
//   - return annotations.
//
// You can use custom annotations:
//   - listeners.kubedoop.dev/class: <class-name>	# required
//   - listeners.kubedoop.dev/name: <name>	# optional
func (c *ControllerServer) getVolumeContext(ctx context.Context, params *createVolumeRequestParams) (map[string]string, error) {
	pvc, err := c.getPvc(ctx, params.PVCName, params.pvcNamespace)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "PVC: %q, Namespace: %q. Detail: %v", params.PVCName, params.pvcNamespace, err)
	}

	annotations := pvc.GetAnnotations()
	log.V(1).Info("get annotations from PVC", "namespace", params.pvcNamespace, "name", params.PVCName, "annotations", annotations)

	_, classNameExists := annotations[constants.AnnotationListenersClass]
	if !classNameExists {
		return nil, errors.New("required annotations '" + constants.AnnotationListenersClass + "' not found in PVC")
	}

	return annotations, nil
}

func (c *ControllerServer) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	if err := c.validateDeleteVolumeRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// check pv if dynamic
	dynamic, err := CheckDynamicPV(request.GetVolumeId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Check Volume ID error: %v", err)
	}

	if !dynamic {
		log.V(1).Info("Volume is not dynamic, skip delete volume")
		return &csi.DeleteVolumeResponse{}, nil
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *ControllerServer) validateDeleteVolumeRequest(request *csi.DeleteVolumeRequest) error {
	if request.VolumeId == "" {
		return errors.New("volume ID is required")
	}

	return nil
}

func validateCreateVolumeRequest(request *csi.CreateVolumeRequest) error {
	if request.GetName() == "" {
		return errors.New("volume Name is required")
	}

	if request.GetCapacityRange() == nil {
		return errors.New("capacityRange is required")
	}

	if request.GetVolumeCapabilities() == nil {
		return errors.New("volumeCapabilities is required")
	}

	if !isValidVolumeCapabilities(request.GetVolumeCapabilities()) {
		return errors.New("volumeCapabilities is not supported")
	}

	return nil
}

func (c *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID is required")
	}

	vcs := request.GetVolumeCapabilities()

	if len(vcs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapabilities is required")
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: request.VolumeCapabilities,
		},
	}, nil
}

func (c *ControllerServer) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (c *ControllerServer) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (c *ControllerServer) ControllerGetCapabilities(ctx context.Context, request *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
		},
	}, nil
}

func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) bool {
	foundAll := true
	for _, c := range volCaps {
		if !isSupportVolumeCapabilities(c) {
			foundAll = false
		}
	}
	return foundAll
}

// isSupportVolumeCapabilities checks if the volume capabilities are supported by the driver
func isSupportVolumeCapabilities(cap *csi.VolumeCapability) bool {
	switch cap.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		return false
	case *csi.VolumeCapability_Mount:
		break
	default:
		return false
	}
	for _, volumeCap := range volumeCaps {
		if volumeCap.GetMode() == cap.AccessMode.GetMode() {
			return true
		}
	}
	return false
}

func CheckDynamicPV(name string) (bool, error) {
	return regexp.Match("pvc-\\w{8}(-\\w{4}){3}-\\w{12}", []byte(name))
}
