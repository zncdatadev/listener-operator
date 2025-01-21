package csi

import (
	"context"
	"errors"
	"fmt"
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

	listenerClass, err := c.getListenerClass(ctx, volumeCtx, params.pvcNamespace)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Get listenerClass error: %v", err)
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

// getAccessibleTopology determines the topology requirements for volume accessibility
//
// For NodePort services, the volume should preferably be accessible on nodes specified in
// the preferred topology requirements. For other service types (LoadBalancer, ClusterIP),
// the volume can be accessed from any node.
//
// Parameters:
//   - request: CreateVolumeRequest containing topology requirements
//   - listenerClass: ListenerClass containing service type configuration
//
// Returns:
//   - []*csi.Topology: List of topology requirements
//     -- For NodePort: returns the first preferred topology from the request
//     -- For other types: returns empty topology list (accessible from anywhere)
func (c *ControllerServer) getAccessibleTopology(request *csi.CreateVolumeRequest, listenerClass *listeners.ListenerClass) []*csi.Topology {
	// For NodePort services, we prefer specific node topology
	if listenerClass.Spec.ServiceType != nil && *listenerClass.Spec.ServiceType == corev1.ServiceTypeNodePort {
		if req := request.GetAccessibilityRequirements(); req != nil {
			preferred := req.GetPreferred()
			if len(preferred) > 0 {
				// Only return the first topology preference
				log.V(1).Info("using first topology preference for NodePort service",
					"topology", preferred[0])
				return []*csi.Topology{preferred[0]}
			}
		}
		log.V(1).Info("no accessibility preferences specified for NodePort service")
	}

	// For other service types or unspecified service type, no topology restrictions
	log.V(1).Info("no topology restrictions required", "serviceType", listenerClass.Spec.ServiceType)
	return []*csi.Topology{}
}

// getListenerClass attempts to get a ListenerClass object in two ways:
//  1. If volumeContext contains 'listeners.kubedoop.dev/name':
//     - First gets the Listener by this name
//     - Then gets the ListenerClass from the Listener's spec.className
//  2. If volumeContext contains 'listeners.kubedoop.dev/class':
//     - Directly gets the ListenerClass by this name
//
// Parameters:
//   - ctx: context.Context for the request
//   - volumeContext: map containing volume context parameters
//   - namespace: namespace where to look for the resources
//
// Returns:
//   - *listeners.ListenerClass: the found listener class
//   - error: any error encountered during the process
//
// Error cases:
//   - Neither listener name nor class name found in volume context
//   - Listener not found when looking up by name
//   - ListenerClass not found when looking up by name or from listener
func (c *ControllerServer) getListenerClass(ctx context.Context, volumeContext map[string]string, namespace string) (*listeners.ListenerClass, error) {
	// Try to get listener name from volume context first
	if listenerName, exists := volumeContext[constants.AnnotationListenerName]; exists {
		// Get listener by name
		listener := &listeners.Listener{}
		if err := c.client.Get(ctx, client.ObjectKey{Name: listenerName, Namespace: namespace}, listener); err != nil {
			return nil, fmt.Errorf("get listener error: %v", err)
		}

		// Get listener class from listener
		listenerClass := &listeners.ListenerClass{}
		if err := c.client.Get(ctx, client.ObjectKey{Name: listener.Spec.ClassName, Namespace: namespace}, listenerClass); err != nil {
			return nil, fmt.Errorf("get listener class from listener error: %v", err)
		}

		log.V(1).Info("got listener class from listener", "listener", listenerName, "namespace", namespace, "class", listenerClass.Name)
		return listenerClass, nil
	}

	// If no listener name found, get listener class directly
	className, exists := volumeContext[constants.AnnotationListenersClass]
	if exists {
		listenerClass := &listeners.ListenerClass{}
		if err := c.client.Get(ctx, client.ObjectKey{Name: className, Namespace: namespace}, listenerClass); err != nil {
			return nil, fmt.Errorf("get listener class error: %v", err)
		}

		log.V(1).Info("got listener class directly", "class", className, "namespace", namespace)
		return listenerClass, nil
	}

	return nil, fmt.Errorf("neither listener name nor listener class name found in volume context")
}

// getVolumeContext retrieves and validates the volume context from PVC annotations
//
// The volume context is derived from PVC annotations and contains listener configuration:
// Required (one of):
//   - listeners.kubedoop.dev/class: specifies the listener class name
//   - listeners.kubedoop.dev/name: references an existing listener
//
// Flow:
//  1. Retrieves PVC using provided name and namespace
//  2. Extracts annotations from the PVC
//  3. Validates that at least one required annotation exists
//  4. Returns the complete annotations map as volume context
//
// Parameters:
//   - ctx: context for the request
//   - params: contains PVC name and namespace
//
// Returns:
//   - map[string]string: PVC annotations as volume context
//   - error: in cases of:
//     -- PVC not found
//     -- Neither required annotation present
//     -- Other K8s API errors
func (c *ControllerServer) getVolumeContext(ctx context.Context, params *createVolumeRequestParams) (map[string]string, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: params.PVCName, Namespace: params.pvcNamespace}, pvc); err != nil {
		return nil, fmt.Errorf("get pvc error: %v", err)
	}

	annotations := pvc.GetAnnotations()
	log.V(1).Info("get annotations from PVC", "namespace", params.pvcNamespace, "name", params.PVCName, "annotations", annotations)

	// check only one of them exists
	_, classNameExists := annotations[constants.AnnotationListenersClass]
	_, listenerNameExist := annotations[constants.AnnotationListenerName]
	if !classNameExists && !listenerNameExist {
		return nil, fmt.Errorf("pvc annotations must have one of %q or %q", constants.AnnotationListenersClass, constants.AnnotationListenerName)
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
