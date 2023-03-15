package pack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	dataDirName    = "data"
	controlDirName = "control_dir"

	dataArchiveName    = "data.tar.gz"
	controlArchiveName = "control.tar.gz"

	debianBinaryFileName = "debian-binary"

	debianBinaryFileContent = "2.0\n"

	PreInstScriptName  = "preinst"
	PostInstScriptName = "postinst"
)

// defaultEnvPrefix is a path there applications will be stored after install
// from RPM and Deb packages.
var defaultEnvPrefix = filepath.Join("usr", "share", "tarantool")

// debPacker is a structure that implements Packer interface
// with specific deb packing behavior.
type debPacker struct {
}

// DEB package is an ar archive that contains debian-binary, control.tar.gz and data.tar.gz files

// debian-binary  : contains format version string (2.0)
// data.tar.xz    : package files
// control.tar.xz : control files (control, preinst etc.)

// Run packs a bundle into deb package.
func (packer *debPacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts *config.CliOpts) error {
	var err error

	// If ar is not installed on the system (e.g. MacOS by default)
	// we don't build anything.
	if err := util.CheckRequiredBinaries("ar"); err != nil {
		return err
	}

	// Create a package directory, where it will be built.
	packageDir, err := os.MkdirTemp("", "tt_pack")
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
	bundlePath, err := prepareBundle(cmdCtx, *packCtx, opts, true)
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

	log.Info("Creating a data directory")

	packageName, err := getPackageName(packCtx, opts, "", false)
	if err != nil {
		return err
	}

	rootPrefix := filepath.Join(dataDirName, defaultEnvPrefix, packageName)
	packageDataDir := filepath.Join(packageDir, dataDirName)

	log.Debugf("Initialize the app directory for prefix: %s", rootPrefix)

	packagePrefixedPath := filepath.Join(packageDir, rootPrefix)
	err = os.MkdirAll(filepath.Join(packageDir, rootPrefix), dirPermissions)
	if err != nil {
		return err
	}

	envSystemPath := filepath.Join("/", defaultEnvPrefix, packageName)
	err = initSystemdDir(packCtx, opts, packageDataDir, envSystemPath)
	if err != nil {
		return err
	}

	// App directory.
	if err = copy.Copy(bundlePath, packagePrefixedPath); err != nil {
		return err
	}

	// Create data.tar.gz.
	dataArchivePath := filepath.Join(packageDir, dataArchiveName)
	err = WriteTgzArchive(packageDataDir, dataArchivePath)
	if err != nil {
		return err
	}

	log.Info("Creating a control directory")

	// Create a control directory with control file and postinst, preinst scripts inside.
	controlDirPath := filepath.Join(packageDir, controlDirName)
	err = createControlDir(cmdCtx, *packCtx, opts, controlDirPath)
	if err != nil {
		return err
	}

	// Create control.tar.gz.
	controlArchivePath := filepath.Join(packageDir, controlArchiveName)
	err = WriteTgzArchive(controlDirPath, controlArchivePath)
	if err != nil {
		return err
	}

	log.Info("Creating a debian-binary file")

	// Create debian-binary.
	err = createDebianBinary(packageDir)
	if err != nil {
		return err
	}

	debSuffix, err := getDebSuffix()
	if err != nil {
		return err
	}
	packageName, err = getPackageName(packCtx, opts, debSuffix, true)
	if err != nil {
		return err
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
		return fmt.Errorf("failed to pack DEB: %s", err)
	}

	log.Infof("Created result DEB package: %s", packageName)

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

// getTntTTAsDeps returns tarantool and tt cli from bin_dir as dependencies.
func getTntTTAsDeps(cmdCtx *cmdcontext.CmdCtx) (PackDependencies, error) {
	tntVerRaw, err := util.GetTarantoolVersion(&cmdCtx.Cli)
	if err != nil {
		return nil, err
	}
	tntVerParsed, err := version.Parse(tntVerRaw)
	if err != nil {
		return nil, err
	}

	tntVer := strings.Join([]string{
		strconv.FormatUint(tntVerParsed.Major, 10),
		strconv.FormatUint(tntVerParsed.Minor, 10),
		strconv.FormatUint(tntVerParsed.Patch, 10),
	}, ".")

	ttVer := version.GetVersion(true, false)

	return PackDependencies{
		PackDependency{
			Name:      "tarantool",
			Relations: []DepRelation{{Relation: "==", Version: tntVer}}},
		PackDependency{
			Name:      "tt",
			Relations: []DepRelation{{Relation: "==", Version: ttVer}}},
	}, nil
}

// getDebSuffix returns suffix for a Deb package.
func getDebSuffix() (string, error) {
	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}
	debSuffix := "-1" + "_" + arch + ".deb"
	return debSuffix, nil
}
