package pack

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// archivePacker is a structure that implements Packer interface
// with specific archive packing behavior.
type archivePacker struct {
}

// Run of ArchivePacker packs the bundle into tarball.
func (packer *archivePacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts *config.CliOpts) error {
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

	log.Debugf("The package structure is created in: %s", bundlePath)

	tgzSuffix, err := getTgzSuffix()
	if err != nil {
		return err
	}
	tarName, err := getPackageName(packCtx, opts, tgzSuffix, true)
	if err != nil {
		return err
	}

	log.Infof("Creating tarball.")

	currentDir, err := os.Getwd()
	if err != nil {
		return err
	}
	tarName = filepath.Join(currentDir, tarName)

	err = WriteTgzArchive(bundlePath, tarName)
	if err != nil {
		if err := os.Remove(tarName); err != nil {
			log.Warnf("Failed to remove a tarball file %s: %s", tarName, err)
		}
		return err
	}
	log.Infof("Bundle is packed successfully to %s.", tarName)
	return nil
}

// getTgzSuffix returns suffix for a tarball.
func getTgzSuffix() (string, error) {
	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}
	tgzSuffix := strings.Join([]string{"", arch, "tar", "gz"}, ".")
	return tgzSuffix, nil
}
