package listenercsi

import "path/filepath"

const (
	CSIServiceAccountName     = "listeners-csi-zncdata-labs"
	CSIClusterRoleName        = "listeners-csi-zncdata-labs"
	CSIClusterRoleBindingName = "listeners-csi-zncdata-labs"

	VolumesMountpointDirName   = "mountpoint-dir"
	VolumesPluginDirName       = "plugin-dir"
	VolumesRegistrationDirName = "registration-dir"
)

var (
	ProjectRootDir = filepath.Join(
		"..",
		"..",
		"..",
	)
	CRDDirectories = filepath.Join(
		ProjectRootDir,
		"config",
		"crd",
		"bases",
	)
	LocalBin = filepath.Join(
		ProjectRootDir,
		"bin",
	)
)
