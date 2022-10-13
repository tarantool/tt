package pack

import (
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
)

// archivePacker is a structure that implements Packer interface
// with specific archive packing behavior.
type archivePacker struct {
}

// Run of ArchivePacker packs the bundle into tarball.
func (packer *archivePacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx) error {
	bundlePath, err := prepareBundle(cmdCtx, packCtx)
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

	log.Infof("The passed bundle packed into temporary directory: %s", bundlePath)

	tarName, err := getPackageName(packCtx, ".tar.gz", true)
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
