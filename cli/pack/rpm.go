package pack

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// rpmPacker is a structure that implements Packer interface
// with specific rpm packing behavior.
type rpmPacker struct {
}

// Run packs a bundle into rpm package.
func (packer *rpmPacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts *config.CliOpts) error {
	var err error

	if err := util.CheckRequiredBinaries("cpio"); err != nil {
		return err
	}

	// Create a package directory, where it will be built.
	packageDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(packageDir)
		if err != nil {
			log.Warnf("Failed to remove a temporary directory %s: %s",
				packageDir, err.Error())
		}
	}()

	log.Debugf("A root for package is located in: %s", packageDir)

	// Prepare a bundle.
	bundlePath, err := prepareBundle(cmdCtx, packCtx, opts, true)
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(bundlePath)
		if err != nil {
			log.Warnf("Failed to remove a temporary directory %s: %s",
				bundlePath, err.Error())
		}
	}()

	bundleName := packCtx.Name
	if bundleName == "" {
		if bundleName, err = getPackageName(*cmdCtx); err != nil {
			return fmt.Errorf("cannot generate package name: %s", bundleName)
		}
	}

	packagingEnvInstallPath := filepath.Join(packageDir, "usr", "share", "tarantool",
		bundleName)
	if err := copy.Copy(bundlePath, packagingEnvInstallPath); err != nil {
		return err
	}

	// Temporary workaround until copy package version upgrade, so PermissionControl can be
	// customized.
	fileSystem := os.DirFS(packagingEnvInstallPath)
	fs.WalkDir(fileSystem, ".", updatePermissions(packagingEnvInstallPath))

	rpmSuffix, err := getRPMSuffix()
	if err != nil {
		return err
	}
	resPackagePath, err := getPackageFileName(packCtx, opts, rpmSuffix, true)
	if err != nil {
		return err
	}

	envSystemPath := filepath.Join("/", defaultEnvPrefix, bundleName)
	err = initSystemdDir(packCtx, opts, packageDir, envSystemPath)
	if err != nil {
		return err
	}

	if err = createArtifactsDirs(packageDir, packCtx); err != nil {
		return err
	}

	err = packRpm(cmdCtx, packCtx, opts, packageDir, resPackagePath)

	if err != nil {
		return fmt.Errorf("failed to create RPM package: %s", err)
	}

	log.Infof("Created result RPM package: %s", resPackagePath)

	return nil
}

// getRPMSuffix returns suffix for an RPM package.
func getRPMSuffix() (string, error) {
	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}
	debSuffix := "-1" + "." + arch + ".rpm"
	return debSuffix, nil
}
