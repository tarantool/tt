package pack

import (
	"fmt"
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
	defer os.RemoveAll(packageDir)

	log.Debugf("A root for package is located in: %s", packageDir)

	// Prepare a bundle.
	bundlePath, err := prepareBundle(cmdCtx, packCtx, opts)
	if err != nil {
		return err
	}
	defer os.RemoveAll(bundlePath)

	bundleName, err := getPackageName(packCtx, opts, "", false)
	if err != nil {
		return err
	}

	if err := copy.Copy(bundlePath, filepath.Join(packageDir, "usr", "share", "tarantool",
		bundleName)); err != nil {
		return err
	}

	resPackagePath, err := getPackageName(packCtx, opts, ".rpm", true)
	if err != nil {
		return err
	}

	err = packRpm(cmdCtx, packCtx, opts, packageDir, resPackagePath)

	if err != nil {
		return fmt.Errorf("Failed to create RPM package: %s", err)
	}

	log.Infof("Created result RPM package: %s", resPackagePath)

	return nil
}
