package listenercsi

import "path/filepath"

const (
	CSIServiceAccountName     = "listeners-csi-zncdatadev"
	CSIClusterRoleName        = "listeners-csi-zncdatadev"
	CSIClusterRoleBindingName = "listeners-csi-zncdatadev"

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
