package pack

import (
	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"os"
	"path/filepath"
)

// archivePacker is a structure that implements Packer interface
// with specific archive packing behavior.
type archivePacker struct {
}

// Run of ArchivePacker packs the bundle into tarball.
func (packer *archivePacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *cmdcontext.PackCtx) error {
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

	tarName, err := getTarPackageName(packCtx)
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

// getTarPackageName returns the result name of the tarball.
func getTarPackageName(packCtx *cmdcontext.PackCtx) (string, error) {
	var tarName string
	packageVersion := getVersion(packCtx)

	if packCtx.FileName != "" {
		tarName = packCtx.FileName
	} else if packCtx.Name != "" {
		tarName = packCtx.Name + "_" + packageVersion + ".tar.gz"
	} else {
		absPath, err := filepath.Abs(".")
		if err != nil {
			return "", err
		}
		tarName = filepath.Base(absPath) + "_" + packageVersion + ".tar.gz"
	}

	return tarName, nil
}
