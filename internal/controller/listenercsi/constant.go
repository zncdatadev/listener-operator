package listenercsi

import "path/filepath"

const (
	CSI_SERVICEACCOUNT_NAME     = "listeners-csi-zncdata-labs"
	CSI_CLUSTERROLE_NAME        = "listeners-csi-zncdata-labs"
	CSI_CLUSTERROLEBINDING_NAME = "listeners-csi-zncdata-labs"

	VOLUMES_MOUNTPOINT_DIR_NAME   = "mountpoint-dir"
	VOLUMES_PLUGIN_DIR_NAME       = "plugin-dir"
	VOLUMES_REGISTRATION_DIR_NAME = "registration-dir"
)

var (
	PROJECT_ROOT_DIR = filepath.Join(
		"..",
		"..",
		"..",
	)
	CRD_DIRECTORIES = filepath.Join(
		PROJECT_ROOT_DIR,
		"config",
		"crd",
		"bases",
	)
	LOCAL_BIN = filepath.Join(
		PROJECT_ROOT_DIR,
		"bin",
	)
)
