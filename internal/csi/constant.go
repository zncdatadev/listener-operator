package csi

const (
	// Default values for volume context
	CSI_STORAGE_POD_NAME                        string = "csi.storage.k8s.io/pod.name"
	CSI_STORAGE_POD_NAMESPACE                   string = "csi.storage.k8s.io/pod.namespace"
	CSI_STORAGE_POD_UID                         string = "csi.storage.k8s.io/pod.uid"
	CSI_STORAGE_SERVICE_ACCOUNT_NAME            string = "csi.storage.k8s.io/serviceAccount.name"
	CSI_STORAGE_EPHEMERAL                       string = "csi.storage.k8s.io/ephemeral"
	STORAGE_KUBERNETES_CSI_PROVISIONER_IDENTITY string = "storage.kubernetes.io/csiProvisionerIdentity"
	VOLUME_KUBERNETES_STORAGE_PROVISIONER       string = "volume.kubernetes.io/storage-provisioner"
)

const (
	// User defined annotations for PVC
	LISTENERS_ZNCDATA_LISTENER_CLASS string = "listeners.zncdata.dev/class-name"
	LISTENERS_ZNCDATA_LISTENER_NAME  string = "listeners.zncdata.dev/listener-name"
)
