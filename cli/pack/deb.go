package pack

import (
	"fmt"
	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	dataDirName    = "data"
	controlDirName = "control_dir"

	dataArchiveName    = "data.tar.gz"
	controlArchiveName = "control.tar.gz"

	debianBinaryFileName = "debian-binary"

	// This is a prefix where the bundle will be located after unpacking.
	debBundlePath = "usr/share/tarantool/%s"

	debianBinaryFileContent = "2.0\n"

	defaultPackageName = "bundle.deb"

	PreInstScriptName  = "preinst"
	PostInstScriptName = "postinst"
)

// debPacker is a structure that implements Packer interface
// with specific deb packing behavior.
type debPacker struct {
}

// DEB package is an ar archive that contains debian-binary, control.tar.gz and data.tar.gz files

// debian-binary  : contains format version string (2.0)
// data.tar.xz    : package files
// control.tar.xz : control files (control, preinst etc.)

// Run packs a bundle into deb package.
func (packer *debPacker) Run(cmdCtx *cmdcontext.CmdCtx) error {
	var err error
	packCtx := cmdCtx.Pack

	// If ar is not installed on the system (e.g. MacOS by default)
	// we don't build anything.
	if err := util.CheckRequiredBinaries("ar"); err != nil {
		return err
	}

	// Create a package directory, where it will be built.
	packageDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}

	log.Infof("A root for package is located in: %s", packageDir)

	// Prepare a bundle.
	bundlePath, err := prepareBundle(cmdCtx)
	if err != nil {
		return err
	}
	defer os.RemoveAll(bundlePath)

	// Create a data directory.
	rootPrefixDir := dataDirName
	rootPrefix := filepath.Join(rootPrefixDir, debBundlePath)
	if packCtx.Name != "" {
		rootPrefix = fmt.Sprintf(rootPrefix, packCtx.Name)
	} else {
		rootPrefix = fmt.Sprintf(rootPrefix, "bundle")
	}

	log.Infof("Initialize the app directory for prefix: %s", rootPrefix)

	packagePrefixedPath := filepath.Join(packageDir, rootPrefix)
	err = os.MkdirAll(filepath.Join(packageDir, rootPrefix), dirPermissions)
	if err != nil {
		return err
	}
	// App directory.
	if err = copyBundleDir(packagePrefixedPath, bundlePath); err != nil {
		return err
	}

	log.Infof("Create data tgz")

	// Create data.tar.gz.
	dataArchivePath := filepath.Join(packageDir, dataArchiveName)
	err = WriteTgzArchive(filepath.Join(packageDir, rootPrefixDir), dataArchivePath)
	if err != nil {
		return err
	}

	// Create a control directory with control file and postinst, preinst scripts inside.
	controlDirPath := filepath.Join(packageDir, controlDirName)
	err = createControlDir(&packCtx, controlDirPath)
	if err != nil {
		return err
	}

	log.Debugf("Create deb control directory tgz")

	// Create control.tar.gz.
	controlArchivePath := filepath.Join(packageDir, controlArchiveName)
	err = WriteTgzArchive(controlDirPath, controlArchivePath)
	if err != nil {
		return err
	}

	log.Debugf("Create debian-binary file")

	// Create debian-binary.
	err = createDebianBinary(packageDir)
	if err != nil {
		return err
	}

	packageName := defaultPackageName
	if packCtx.FileName != "" {
		packageName = packCtx.FileName
	} else if packCtx.Name != "" {
		packageName = packCtx.Name + ".deb"
	}

	// Create result archive.
	packDebCmd := exec.Command(
		"ar", "r",
		packageName,
		filepath.Join(packageDir, debianBinaryFileName),
		controlArchivePath,
		dataArchivePath,
	)

	err = packDebCmd.Run()
	if err != nil {
		return fmt.Errorf("Failed to pack DEB: %s", err)
	}

	log.Infof("Created result DEB package: %s", bundlePath)
	return err
}

// createDebianBinary creates a debian-binary file for deb package.
func createDebianBinary(packageDir string) error {
	debBin, err := os.Create(filepath.Join(packageDir, debianBinaryFileName))
	if err != nil {
		return err
	}
	_, err = debBin.Write([]byte(debianBinaryFileContent))
	if err != nil {
		return err
	}
	return nil
}

// getTntTTVersions checks the version of tarantool in bin_dir and returns it.
func getTntTTVersions(packCtx *cmdcontext.PackCtx) (PackDependencies, error) {
	tntVerBytes, err := exec.Command(filepath.Join(packCtx.App.BinDir, "tarantool"), "--version").
		Output()
	if err != nil {
		return nil, err
	}
	tntVer, err := util.NormalizeGitVersion(string(tntVerBytes))
	if err != nil {
		return nil, err
	}

	return PackDependencies{
		PackDependency{
			Name:      "tarantool",
			Relations: []DepRelation{{Relation: "==", Version: tntVer}}},
	}, nil
}
