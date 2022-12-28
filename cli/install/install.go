package install

import (
	"bufio"
	_ "embed"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/docker"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/templates/engines"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// Backported cmake rules for static build.
// Static build has appeared since version 2.6.1.
//
//go:embed extra/tarantool-static-build.patch
var staticBuildPatch []byte

// Fix missing OpenSSL symbols.
//
//go:embed extra/openssl-symbols.patch
var opensslSymbolsPatch []byte

//go:embed extra/openssl-symbols-1.10.14.patch
var opensslSymbolsPatch14 []byte

// Necessary for building with >= glibc-2.34.
// Not actual for >= (1.10.11, 2.8.3).
//
//go:embed extra/gh-6686-fix-build-with-glibc-2-34.patch
var glibcPatch []byte

// zlib version 1.2.11 is no longer available for download.
// Not actual for >= 2.10.0-rc1, 2.8.4.
//
//go:embed extra/zlib-backup-old.patch
var zlibPatchOld []byte

//go:embed extra/zlib-backup.patch
var zlibPatch []byte

// Old version of the libunwind doesn't compile under GCC 10.
// Not actual for >= 2.10.0-rc1.
//
//go:embed extra/bump-libunwind-old.patch
var unwindPatchOld []byte

//go:embed extra/bump-libunwind.patch
var unwindPatch []byte

//go:embed extra/bump-libunwind-new.patch
var unwindPatchNew []byte

// defaultDirPermissions is rights used to create folders.
// 0755 - drwxr-xr-x
// We need to give permission for all to execute
// read,write for user and only read for others.
const defaultDirPermissions = 0755

// InstallCtx contains information for program installation.
type InstallCtx struct {
	// Reinstall is a flag. If it is set,
	// target application will be reinstalled if already exists.
	Reinstall bool
	// Force is a flag. If it is set, install will force
	// requirements errors.
	Force bool
	// Noclean is a flag. If it is set,
	// install will don't remove tmp files.
	Noclean bool
	// Local is a flag. If it is set,
	// install will do local installation.
	Local bool
	// BuildInDocker is set if tarantool must be built in docker container.
	BuildInDocker bool
	// programName is a program name to install.
	programName string
	// verbose flag enables verbose logging.
	verbose bool
}

// Package is a struct containing sys and install name of the package.
type Package struct {
	// sysName is a string containing system name of package.
	sysName string
	// installName is a string containing install name of package.
	installName string
}

// DistroInfo is a struct containing info about linux distro.
type DistroInfo struct {
	Name         string
	Vendor       string
	Version      string
	Architecture string
}

var (
	// PrettyNameRe is a regexp for PrettyName in os-release file.
	PrettyNameRe = regexp.MustCompile(`^PRETTY_NAME=(.*)$`)
	// IDRe is a regexp for ID in os-release file.
	IDRe = regexp.MustCompile(`^ID=(.*)$`)
	// VersionIDRe is a regexp for VersionID in os-release file.
	VersionIDRe = regexp.MustCompile(`^VERSION_ID=(.*)$`)
)

// getDistroInfo collects info about linux distro.
func getDistroInfo() (DistroInfo, error) {
	var distroInfo DistroInfo
	var err error

	// Get architecture.
	if distroInfo.Architecture, err = util.GetArch(); err != nil {
		return distroInfo, err
	}

	// Get distribution info.
	releaseFile, err := os.Open("/etc/os-release")
	if err != nil {
		return distroInfo, err
	}
	defer releaseFile.Close()

	scanner := bufio.NewScanner(releaseFile)
	for scanner.Scan() {
		if m := PrettyNameRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Name = strings.Trim(m[1], `"`)
		} else if m := IDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Vendor = strings.Trim(m[1], `"`)
		} else if m := VersionIDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Version = strings.Trim(m[1], `"`)
		}
	}
	return distroInfo, nil
}

// detectOsName returns name of the OS.
func detectOsName() (string, error) {
	if runtime.GOOS == "darwin" {
		return "darwin", nil
	}
	if runtime.GOOS == "windows" {
		return "windows", nil
	}
	if runtime.GOOS == "linux" {
		distroInfo, err := getDistroInfo()
		return distroInfo.Name, err
	}
	return "", fmt.Errorf("unknown OS")
}

// getTarantoolVersions returns all available versions from tarantool repository.
func getTarantoolVersions(local bool, distfiles string) ([]version.Version, error) {
	versions := []version.Version{}
	var err error

	if local {
		versions, err = search.GetVersionsFromGitLocal(distfiles + "/tarantool")
	} else {
		versions, err = search.GetVersionsFromGitRemote(search.GitRepoTarantool)
	}

	if err != nil {
		return nil, err
	}

	return versions, nil
}

// getTTVersions returns all available versions from tt repository.
func getTTVersions(local bool, distfiles string) ([]version.Version, error) {
	versions := []version.Version{}
	var err error

	if local {
		versions, err = search.GetVersionsFromGitLocal(distfiles + "/tt")
	} else {
		versions, err = search.GetVersionsFromGitRemote(search.GitRepoTT)
	}

	if err != nil {
		return nil, err
	}

	return versions, nil
}

// isProgramInstalled checks if program is installed.
func isProgramInstalled(program string) bool {
	if _, err := exec.LookPath(program); err != nil {
		return false
	}
	return true
}

// isPackageInstalledDebian checks if package is installed on Debian/Ubuntu.
func isPackageInstalledDebian(packageName string) bool {
	cmd := exec.Command("dpkg", "-L", packageName)
	cmd.Start()
	if cmd.Wait() == nil {
		return true
	} else {
		return false
	}
}

// printLog prints logfile to stdout.
func printLog(logName string) error {
	logs, err := os.ReadFile(logName)
	if err != nil {
		return err
	}
	os.Stdout.Write(logs)
	return nil
}

// isPackageInstalled checks if package is installed.
func isPackageInstalled(packageName string) bool {
	osName, _ := detectOsName()
	if strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian") {
		return isPackageInstalledDebian(packageName)
	}
	if strings.Contains(osName, "darwin") {
		packageList, _ := util.RunCommandAndGetOutput("brew", "list")
		return strings.Contains(packageList, packageName)
	}
	if strings.Contains(osName, "CentOS") {
		packageList, _ := util.RunCommandAndGetOutput("yum", "list", "--installed")
		return strings.Contains(packageList, packageName)
	}
	return false
}

// programDependenciesInstalled checks if dependencies are installed.
func programDependenciesInstalled(program string) bool {
	programs := []Package{}
	packages := []string{}
	osName, _ := detectOsName()
	if program == "tt" {
		programs = []Package{{"mage", "mage"}, {"git", "git"}}
	} else if program == "tarantool" {
		if osName == "darwin" {
			programs = []Package{{"cmake", "cmake"}, {"git", "git"},
				{"make", "make"}, {"clang", "clang"}}
		} else if strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian") {
			programs = []Package{{"cmake", "cmake"}, {"git", "git"}, {"make", "make"},
				{"gcc", " build-essential"}}
			packages = []string{"coreutils", "sed"}
		} else if strings.Contains(osName, "CentOs") {
			programs = []Package{{"cmake", "cmake"}, {"git", "git"}, {"make", "make"},
				{"gcc", "gcc"}, {"g++", "gcc-c++ "}}
			packages = []string{"libstdc++-static", "perl"}
		} else {
			answer, err := util.AskConfirm(os.Stdin, "Unknown OS, can't check if dependencies"+
				" are installed.\n"+
				"Procced, without checking?")
			if !answer || err != nil {
				return false
			}
			if answer {
				return true
			}
		}
	}
	missing_pack := []string{}
	// Programs that are installed from source.
	missing_pack_src := []string{}
	for _, program := range programs {
		if !isProgramInstalled(program.sysName) {
			// Mage is installed from source instead of package manager.
			if program.sysName == "mage" {
				missing_pack_src = append(missing_pack_src, program.installName)
			} else {
				missing_pack = append(missing_pack, program.installName)
			}
		}
	}

	for _, packageName := range packages {
		if !isPackageInstalled(packageName) {
			missing_pack = append(missing_pack, packageName)
		}
	}

	if len(missing_pack) != 0 || len(missing_pack_src) != 0 {
		log.Error("The operation requires some dependencies.")
		fmt.Println("Missing packages: " + strings.Join(missing_pack, " ") + " " +
			strings.Join(missing_pack_src, " "))
		if osName == "darwin" {
			fmt.Println("You can install them by running commands:")
			fmt.Println("brew install " + strings.Join(missing_pack, " ") +
				strings.Join(missing_pack_src, " "))
		} else if strings.Contains(osName, "CentOs") {
			fmt.Println("You can install them by running command:")
			if len(missing_pack) != 0 {
				fmt.Println(" sudo yum install " + strings.Join(missing_pack, " "))
			}
			if len(missing_pack_src) != 0 {
				fmt.Println("install from sources: " +
					strings.Join(missing_pack_src, " "))
			}
		} else if strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian") {
			fmt.Println("You can install them by running command:")
			if len(missing_pack) != 0 {
				fmt.Println(" sudo apt install " + strings.Join(missing_pack, " "))
			}
			if len(missing_pack_src) != 0 {
				fmt.Println("install from sources: " +
					strings.Join(missing_pack_src, " "))
			}
		}
		fmt.Println("Usage: tt install -f if you already have those packages installed")
		return false
	}
	return true
}

// checkExisting checks if program is already installed in binary directory.
func checkExisting(version string, dst string) bool {
	if _, err := os.Stat(filepath.Join(dst, version)); os.IsNotExist(err) {
		return false
	} else {
		return true
	}
}

// downloadRepo downloads git repository.
func downloadRepo(repoLink string, tag string, dst string,
	logFile *os.File, verbose bool) error {
	var err error
	if tag == "master" {
		err = util.ExecuteCommand("git", verbose, logFile, dst, "clone", repoLink,
			"--recursive", dst)
	} else {
		err = util.ExecuteCommand("git", verbose, logFile, dst, "clone", "-b", tag,
			"--depth=1", repoLink,
			"--recursive", dst)
	}

	return err
}

// copyBuildedTT copies tt binary.
func copyBuildedTT(binDir, path, version string, installCtx InstallCtx,
	logFile *os.File) error {
	var err error
	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		err = os.Mkdir(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %s", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %s", binDir, err)
	}
	if installCtx.Reinstall {
		err = os.Remove(filepath.Join(binDir, version))
		if err != nil {
			return err
		}
	}
	err = util.CopyFilePreserve(filepath.Join(path, "tt"), filepath.Join(binDir, version))
	return err
}

// installTt installs selected version of tt.
func installTt(version string, binDir string, installCtx InstallCtx, distfiles string) error {
	versions, err := getTTVersions(installCtx.Local, distfiles)
	if err != nil {
		return err
	}

	// Get latest version if it was not specified.
	_, ttVersion, _ := strings.Cut(version, search.VersionCliSeparator)
	if ttVersion == "" {
		log.Infof("Getting latest tt version..")
		if len(versions) == 0 {
			// TODO Remove after first tt release (must return error: no versions).
			ttVersion = "master"
		} else {
			ttVersion = versions[len(versions)-1].Str
		}
	}

	// Check that the version exists.
	// The tag format in tt is vX.Y.Z, but the user can use the X.Y.Z format
	// and this option needs to be supported.
	if ttVersion != "master" {
		versionFound := false
		for _, ver := range versions {
			if ttVersion == ver.Str || (ttVersion[0:1] != "v" && "v"+ttVersion == ver.Str) {
				versionFound = true
				ttVersion = ver.Str
				break
			}
		}

		if !versionFound {
			return fmt.Errorf("%s version of tt doesn't exist", ttVersion)
		}
	}

	// Check binary directory.
	if binDir == "" {
		return fmt.Errorf("binDir is not set, check tarantool.yaml")
	}
	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())
	log.Infof("Installing tt=" + ttVersion)

	// Check tt dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tt") {
			return nil
		}
	}

	version = "tt" + search.VersionFsSeparator + ttVersion
	// Check if that version is already installed.
	log.Infof("Checking existing...")
	if checkExisting(version, binDir) && !installCtx.Reinstall {
		log.Infof("%s version of tt already exists, updating symlink...", version)
		err := util.CreateSymlink(version, filepath.Join(binDir, "tt"), true)
		log.Infof("Done")
		return err
	}

	path, err := os.MkdirTemp("", "tt_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !installCtx.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tt.
	if installCtx.Local {
		if checkExisting("tt", distfiles) {
			log.Infof("Local files found, installing from them...")
			localPath, _ := util.JoinAbspath(distfiles, "tt")
			err = copy.Copy(localPath, path)
			if err != nil {
				return err
			}
			util.ExecuteCommand("git", installCtx.verbose, logFile, path, "checkout", ttVersion)
		} else {
			return fmt.Errorf("can't find distfiles directory")
		}
	} else {
		log.Infof("Downloading tt...")
		err = downloadRepo(search.GitRepoTT, ttVersion, path, logFile, installCtx.verbose)
	}

	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Build tt.
	log.Infof("Building tt...")
	err = util.ExecuteCommand("mage", installCtx.verbose, logFile, path,
		"build")
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Copy binary.
	log.Infof("Copying executable...")
	err = copyBuildedTT(binDir, path, version, installCtx, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlink.
	err = util.CreateSymlink(version, filepath.Join(binDir, "tt"), true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	log.Infof("Done.")
	if installCtx.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// checkExistingTarantool
func checkExistingTarantool(version, binDir, includeDir string,
	installCtx InstallCtx) (bool, error) {
	var err error
	flag := false
	if checkExisting(version, binDir) {
		if !installCtx.Reinstall {
			log.Infof("%s version of tarantool already exists, updating symlink...", version)
			err = util.CreateSymlink(version, filepath.Join(binDir, "tarantool"), true)
			log.Infof("Done")
			flag = true
		}
	}
	return flag, err
}

func patchTarantool(srcPath string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) error {
	log.Infof("Patching tarantool...")

	if tarVersion == "master" {
		return nil
	}

	ver, err := version.GetVersionDetails(tarVersion)
	if err != nil {
		return err
	}

	patches := []patcher{
		patchRange_1_to_2_6_1{defaultPatchApplier{staticBuildPatch}},
		patchRange_1_to_1_10_14{defaultPatchApplier{opensslSymbolsPatch}},
		patch_1_10_14{defaultPatchApplier{opensslSymbolsPatch14}},
		patchRange_1_to_1_10_12{defaultPatchApplier{glibcPatch}},
		patchRange_2_to_2_8{defaultPatchApplier{glibcPatch}},
		patchRange_2_8_to_2_8_3{defaultPatchApplier{glibcPatch}},
		patch_2_10_0_rc1{defaultPatchApplier{glibcPatch}},
		patchRange_2_7_to_2_7_2{defaultPatchApplier{zlibPatchOld}},
		patchRange_2_7_2_to_2_7_4{defaultPatchApplier{zlibPatch}},
		patchRange_2_8_1_to_2_8_4{defaultPatchApplier{zlibPatch}},
		patch_2_10_beta{defaultPatchApplier{zlibPatch}},
		patchRange_2_7_to_2_7_2{defaultPatchApplier{unwindPatchOld}},
		patch_2_8_4{defaultPatchApplier{unwindPatchNew}},
		patchRange_2_7_2_to_2_7_4{defaultPatchApplier{unwindPatch}},
		patchRange_2_8_1_to_2_8_4{defaultPatchApplier{unwindPatch}},
		patch_2_10_beta{defaultPatchApplier{unwindPatch}},
	}

	for _, patch := range patches {
		if patch.isApplicable(ver) {
			err = patch.apply(srcPath, installCtx.verbose, logFile)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// buildTarantool builds tarantool from source.
func buildTarantool(srcPath string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) error {

	buildPath := filepath.Join(srcPath, "/static-build/build")
	err := os.MkdirAll(buildPath, defaultDirPermissions)
	if err != nil {
		return err
	}

	// Disable backtrace feature for versions 1.10.X.
	// This feature is not supported by a backported static build.
	btFlag := "ON"
	if tarVersion != "master" {
		version, err := version.GetVersionDetails(tarVersion)
		if err != nil {
			return err
		}
		if version.Major == 1 {
			btFlag = "OFF"
		}
	}

	maxThreads := fmt.Sprint(runtime.NumCPU())
	err = util.ExecuteCommand("cmake", installCtx.verbose, logFile, buildPath,
		"..", `-DCMAKE_TARANTOOL_ARGS="-DCMAKE_BUILD_TYPE=RelWithDebInfo;`+
			`-DENABLE_WERROR=OFF;-DENABLE_BACKTRACE=`+btFlag,
		"-DCMAKE_INSTALL_PREFIX="+buildPath)
	if err != nil {
		return err
	}

	err = util.ExecuteCommand("make", installCtx.verbose, logFile, buildPath,
		"-j"+maxThreads)
	return err
}

// copyLocalTarantool finds and copies local tarantool folder to tmp folder.
func copyLocalTarantool(distfiles string, path string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) error {
	var err error
	if checkExisting("tarantool", distfiles) {
		log.Infof("Local files found, installing from them...")
		localPath, _ := util.JoinAbspath(distfiles, "tarantool")
		err = copy.Copy(localPath, path)
		if err != nil {
			return err
		}
		err = util.ExecuteCommand("git", installCtx.verbose, logFile, path, "checkout", tarVersion)
	} else {
		return fmt.Errorf("can't find distfiles directory")
	}
	return err
}

// copyBuildedTarantool copies binary and include dir.
func copyBuildedTarantool(binPath, incPath, binDir, includeDir, version string,
	installCtx InstallCtx, logFile *os.File) error {
	var err error
	log.Infof("Copying executable...")
	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		err = os.Mkdir(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %s", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %s", binDir, err)
	}

	err = util.CopyFileChangePerms(binPath, filepath.Join(binDir, version),
		defaultDirPermissions)
	if err != nil {
		return err
	}

	log.Infof("Copying headers...")
	if _, err := os.Stat(includeDir); os.IsNotExist(err) {
		err = os.MkdirAll(includeDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("unable to create %s\n Error: %s", includeDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("unable to create %s\n Error: %s", includeDir, err)
	}
	err = copy.Copy(incPath, filepath.Join(includeDir, version)+"/")
	return err
}

//go:embed Dockerfile.tnt.build
var tarantoolBuildDockerfile []byte

func installTarantoolInDocker(binDir string, incDir string, installCtx InstallCtx,
	distfiles string) error {
	tmpDir, err := ioutil.TempDir("", "docker_build_ctx")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	goTextEngine := engines.NewDefaultEngine()
	dockerfileText, err := goTextEngine.RenderText(string(tarantoolBuildDockerfile),
		map[string]string{"uid": currentUser.Uid})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	if err = ioutil.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfileText),
		0664); err != nil {
		return err
	}

	// Copy tt executable.
	if currentExecutable, err := os.Executable(); err != nil {
		return err
	} else {
		if err = copy.Copy(currentExecutable, filepath.Join(tmpDir, "tt")); err != nil {
			return nil
		}
	}

	// Generate tt config for tt process in the container.
	ttCfg := config.Config{
		CliConfig: configure.GetDefaultCliOpts(),
	}
	ttCfg.CliConfig.App.BinDir = "/tt_bin"
	ttCfg.CliConfig.App.IncludeDir = "/tt_include"
	ttCfg.CliConfig.Repo.Install = "/tt_distfiles"
	if err = util.WriteYaml(filepath.Join(tmpDir, configure.ConfigName), ttCfg); err != nil {
		return err
	}

	// Generate install command line for tt in container.
	tntInstallCommandLine := []string{"./tt"}
	if installCtx.verbose {
		tntInstallCommandLine = append(tntInstallCommandLine, "-V")
	}
	tntInstallCommandLine = append(tntInstallCommandLine, "install", "tarantool", "-f")
	if installCtx.Reinstall {
		tntInstallCommandLine = append(tntInstallCommandLine, "--reinstall")
	}
	if installCtx.Local {
		tntInstallCommandLine = append(tntInstallCommandLine, "--local-repo")
	}

	// Exclude last element from incDir path, because it already has "include" subdir appended.
	// So we get the parent of incDir to get original include path.
	dockerRunOptions := docker.RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_tarantool_build",
		Command:     tntInstallCommandLine,
		Binds: []string{
			fmt.Sprintf("%s:%s", binDir, ttCfg.CliConfig.App.BinDir),
			fmt.Sprintf("%s:%s", filepath.Dir(incDir), ttCfg.CliConfig.App.IncludeDir),
			fmt.Sprintf("%s:%s", distfiles, ttCfg.CliConfig.Repo.Install),
		},
		Verbose: installCtx.verbose,
	}
	if err = docker.RunContainer(dockerRunOptions, os.Stdout); err != nil {
		return err
	}

	return nil
}

// installTarantool installs selected version of tarantool.
func installTarantool(version string, binDir string, incDir string,
	installCtx InstallCtx, distfiles string) error {
	// Check bin and header dirs.
	if binDir == "" {
		return fmt.Errorf("BinDir is not set, check tarantool.yaml ")
	}
	if incDir == "" {
		return fmt.Errorf("IncludeDir is not set, check tarantool.yaml")
	}

	versions, err := getTarantoolVersions(installCtx.Local, distfiles)
	if err != nil {
		return err
	}

	// Get latest version if it was not specified.
	_, tarVersion, _ := strings.Cut(version, search.VersionCliSeparator)
	if tarVersion == "" {
		log.Infof("Getting latest tarantool version..")
		if len(versions) == 0 {
			return fmt.Errorf("no version found")
		}

		tarVersion = versions[len(versions)-1].Str
	}

	// Check that the version exists.
	if tarVersion != "master" {
		versionFound := false
		for _, ver := range versions {
			if tarVersion == ver.Str {
				versionFound = true
				break
			}
		}

		if !versionFound {
			return fmt.Errorf("%s version of tarantool doesn't exist", tarVersion)
		}
	}

	version = "tarantool" + search.VersionFsSeparator + tarVersion
	// Check if program is already installed.
	if !installCtx.Reinstall {
		log.Infof("Checking existing...")
		versionExists, err := checkExistingTarantool(version,
			binDir, incDir, installCtx)
		if err != nil || versionExists {
			return err
		}
	}

	if installCtx.BuildInDocker {
		return installTarantoolInDocker(binDir, incDir, installCtx, distfiles)
	}

	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool=" + tarVersion)

	// Check dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tarantool") {
			return nil
		}
	}

	path, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !installCtx.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tarantool.
	if installCtx.Local {
		log.Infof("Checking local files...")
		err = copyLocalTarantool(distfiles, path, tarVersion, installCtx,
			logFile)
	} else {
		log.Infof("Downloading tarantool...")
		err = downloadRepo(search.GitRepoTarantool, tarVersion, path, logFile, installCtx.verbose)
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Patch tarantool.
	err = patchTarantool(path, tarVersion, installCtx, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Build tarantool.
	log.Infof("Building tarantool...")
	err = buildTarantool(path, tarVersion, installCtx, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Copy binary and headers.
	if installCtx.Reinstall {
		if checkExisting(version, binDir) {
			log.Infof("%s version of tarantool already exists, removing files...",
				version)
			err = os.RemoveAll(filepath.Join(binDir, version))
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			err = os.RemoveAll(filepath.Join(incDir, version))
		}
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	buildPath := filepath.Join(path, "/static-build/build")
	binPath := filepath.Join(buildPath, "/tarantool-prefix/bin/tarantool")
	incPath := filepath.Join(buildPath, "/tarantool-prefix/include/tarantool") + "/"
	err = copyBuildedTarantool(binPath, incPath, binDir, incDir, version, installCtx,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = util.CreateSymlink(version, filepath.Join(binDir, "tarantool"), true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	err = util.CreateSymlink(version, filepath.Join(incDir, "tarantool"), true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	log.Infof("Done.")
	if installCtx.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// getTarantoolEEVersions returns all available versions of tarantool-ee for user's OS.
func getTarantoolEEVersions(cliOpts *config.CliOpts, local bool,
	files []string) ([]version.Version, error) {
	versions := []version.Version{}
	var err error

	if local {
		versions, err = install_ee.FetchVersionsLocal(files)
	} else {
		versions, err = install_ee.FetchVersions(cliOpts)
	}

	if err != nil {
		return nil, err
	}

	return versions, nil
}

// installTarantoolEE installs selected version of tarantool-ee.
func installTarantoolEE(version string, binDir string, includeDir string, installCtx InstallCtx,
	distfiles string, cliOpts *config.CliOpts) error {
	var err error

	files := []string{}
	if installCtx.Local {
		localFiles, err := os.ReadDir(cliOpts.Repo.Install)
		if err != nil {
			return err
		}

		for _, file := range localFiles {
			if strings.Contains(file.Name(), "tarantool-enterprise-bundle") && !file.IsDir() {
				files = append(files, file.Name())
			}
		}
	}
	versions, err := getTarantoolEEVersions(cliOpts, installCtx.Local, files)
	if err != nil {
		return err
	}

	// Get latest version if it was not specified.
	_, tarVersion, _ := strings.Cut(version, search.VersionCliSeparator)
	if tarVersion == "" {
		log.Infof("Getting latest tarantool-ee version..")
		if len(versions) == 0 {
			return fmt.Errorf("no version found")
		}

		tarVersion = versions[len(versions)-1].Str
	}

	// Check that the version exists.
	versionFound := false
	for _, ver := range versions {
		if tarVersion == ver.Str {
			versionFound = true
			break
		}
	}
	if !versionFound {
		return fmt.Errorf("%s version of tarantool-ee doesn't exist", tarVersion)
	}

	// Check bin and header dirs.
	if binDir == "" {
		return fmt.Errorf("binDir is not set, check tarantool.yaml")
	}
	if includeDir == "" {
		return fmt.Errorf("includeDir is not set, check tarantool.yaml")
	}
	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool-ee=" + tarVersion)

	// Check dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tarantool") {
			return nil
		}
	}

	// Check if program is already installed.
	log.Infof("Checking existing...")
	log.Infof("Getting bundle name for %s", tarVersion)
	bundleName := ""
	for _, ver := range versions {
		if ver.Str == tarVersion {
			bundleName = ver.Tarball
		}
	}

	version = "tarantool-ee" + search.VersionFsSeparator + tarVersion
	if !installCtx.Reinstall {
		log.Infof("Checking existing...")
		versionExists, err := checkExistingTarantool(version,
			binDir, includeDir, installCtx)
		if err != nil || versionExists {
			return err
		}
	}

	path, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !installCtx.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tarantool.
	if installCtx.Local {
		log.Infof("Checking local files...")
		if checkExisting(bundleName, distfiles) {
			log.Infof("Local files found, installing from them...")
			localPath, _ := util.JoinAbspath(distfiles,
				bundleName)
			err = util.CopyFilePreserve(localPath,
				filepath.Join(path, bundleName))
			if err != nil {
				printLog(logFile.Name())
				return err
			}
		} else {
			return fmt.Errorf("can't find distfiles directory")
		}
	} else {
		log.Infof("Downloading tarantool-ee...")
		err := install_ee.GetTarantoolEE(cliOpts, bundleName, path)
		if err != nil {
			printLog(logFile.Name())
			return err
		}
	}

	// Unpack archive.
	log.Infof("Unpacking archive...")
	err = util.ExtractTar(filepath.Join(path,
		bundleName))
	if err != nil {
		return err
	}

	// Copy binary and headers.
	if installCtx.Reinstall {
		if checkExisting(version, binDir) {
			log.Infof("%s version of tarantool-ee already exists, removing files...",
				version)
			err = os.RemoveAll(filepath.Join(binDir, version))
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			err = os.RemoveAll(filepath.Join(includeDir, version))
		}
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	binPath := filepath.Join(path, "/tarantool-enterprise/tarantool")
	incPath := filepath.Join(path, "/tarantool-enterprise/include/tarantool") + "/"
	err = copyBuildedTarantool(binPath, incPath, binDir, includeDir, version, installCtx,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = util.CreateSymlink(version, filepath.Join(binDir, "tarantool"), true)
	if err != nil {
		return err
	}
	err = util.CreateSymlink(version, filepath.Join(includeDir, "tarantool"), true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	log.Infof("Done.")
	if installCtx.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// Install installs program.
func Install(args []string, binDir string, includeDir string, installCtx InstallCtx,
	local string, cliOpts *config.CliOpts) error {
	var err error

	switch installCtx.programName {
	case "tt":
		err = installTt(args[0], binDir, installCtx, local)
	case "tarantool":
		err = installTarantool(args[0], binDir, includeDir, installCtx, local)
	case "tarantool-ee":
		err = installTarantoolEE(args[0], binDir, includeDir, installCtx, local, cliOpts)
	default:
		return fmt.Errorf("unknown application: %s", installCtx.programName)
	}

	return err
}

func FillCtx(cmdCtx *cmdcontext.CmdCtx, installCtx *InstallCtx, args []string) error {
	installCtx.verbose = cmdCtx.Cli.Verbose

	if len(args) != 1 {
		return fmt.Errorf("invalid number of parameters")
	}

	re := regexp.MustCompile(
		"^(?P<prog>tt|tarantool|tarantool-ee)(?:" + search.VersionCliSeparator + ".*)?$",
	)
	matches := util.FindNamedMatches(re, args[0])
	if len(matches) == 0 {
		return fmt.Errorf("unknown application: %s", args[0])
	}
	installCtx.programName = matches["prog"]

	if installCtx.BuildInDocker && installCtx.programName != "tarantool" {
		return fmt.Errorf("--use-docker can be used only for 'tarantool' program")
	}

	return nil
}
