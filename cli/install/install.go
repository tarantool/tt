package install

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io"
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
	"github.com/tarantool/tt/cli/templates"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
	"golang.org/x/sys/unix"
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

const (
	// defaultDirPermissions is rights used to create folders.
	// 0755 - drwxr-xr-x
	// We need to give permission for all to execute
	// read,write for user and only read for others.
	defaultDirPermissions = 0755

	MajorMinorPatchRegexp = `^[0-9]+\.[0-9]+\.[0-9]+`
)

// programGitRepoUrls contains URLs of programs git repositories.
var programGitRepoUrls = map[string]string{
	"tarantool": search.GitRepoTarantool,
	"tt":        search.GitRepoTT,
}

// InstallCtx contains information for program installation.
type InstallCtx struct {
	// Reinstall is a flag. If it is set,
	// target application will be reinstalled if already exists.
	Reinstall bool
	// Force is a flag which disables dependency check if enabled.
	Force bool
	// Noclean is a flag. If it is set,
	// install will don't remove tmp files.
	Noclean bool
	// Local is a flag. If it is set,
	// install will do local installation.
	Local bool
	// BuildInDocker is set if tarantool must be built in docker container.
	BuildInDocker bool
	// ProgramName is a program name to install.
	ProgramName string
	// verbose flag enables verbose logging.
	verbose bool
	// Version of the program to install.
	version string
	// Dynamic flag enables dynamic linking.
	Dynamic bool
	// buildDir is the directory, where the tarantool executable is searched,
	// in case of installation from the local build directory.
	buildDir string
	// IncDir is the directory, where the tarantool headers are located.
	IncDir string
	// Install development build.
	DevBuild bool
	// skipMasterUpdate is set if user doesn't want to check for latest master
	// version and update for it if master version already exists. It inherits
	// the --no-prompt flag from global context.
	skipMasterUpdate bool
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

// IsTarantoolDev returns true if tarantoolBinarySymlink is `tarantool-dev` version.
func IsTarantoolDev(tarantoolBinarySymlink, binDir string) (string, bool, error) {
	bin, err := os.Readlink(tarantoolBinarySymlink)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(binDir, bin)
	}
	return bin, filepath.Dir(bin) != binDir, nil
}

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

// getVersionsFromRepo returns all available versions from github repository.
func getVersionsFromRepo(local bool, distfiles string, program string,
	repolink string) ([]version.Version, error) {
	versions := []version.Version{}
	var err error

	if local {
		versions, err = search.GetVersionsFromGitLocal(filepath.Join(distfiles, program))
	} else {
		versions, err = search.GetVersionsFromGitRemote(repolink)
	}

	if err != nil {
		return nil, err
	}

	return versions, nil
}

// getCommit returns all available commits from repository.
func getCommit(local bool, distfiles string, programName string,
	line string) (string, error) {
	commit := ""
	var err error

	if local {
		commit, err = search.GetCommitFromGitLocal(filepath.Join(distfiles, programName),
			line)
	} else {
		commit, err = search.GetCommitFromGitRemote(programGitRepoUrls[programName],
			line)
	}

	if err != nil {
		return "", err
	}

	return commit, nil
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
func programDependenciesInstalled(program string) error {
	programs := []Package{}
	packages := []string{}
	osName, _ := detectOsName()
	if program == search.ProgramTt {
		programs = []Package{{"mage", "mage"}, {"git", "git"}}
	} else if program == search.ProgramCe {
		if osName == "darwin" {
			programs = []Package{{"cmake", "cmake"}, {"git", "git"},
				{"make", "make"}, {"clang", "clang"}, {"openssl", "openssl"}}
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
				"Proceed without checking?")
			if err != nil {
				return err
			}
			if !answer {
				return util.ErrCmdAbort
			}
			return nil
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
		var errMsg strings.Builder
		errMsg.WriteString("Missing packages: " + strings.Join(missing_pack, " ") + " " +
			strings.Join(missing_pack_src, " ") + "\n")
		if osName == "darwin" {
			errMsg.WriteString(
				"You can install them by running commands:\nbrew install " + strings.Join(
					missing_pack,
					" ",
				) +
					strings.Join(
						missing_pack_src,
						" ",
					) + "\n",
			)
		} else if strings.Contains(osName, "CentOs") {
			errMsg.WriteString("You can install them by running command:\n")
			if len(missing_pack) != 0 {
				errMsg.WriteString(" sudo yum install " + strings.Join(missing_pack, " ") + "\n")
			}
			if len(missing_pack_src) != 0 {
				errMsg.WriteString("install from sources: " +
					strings.Join(missing_pack_src, " ") + "\n")
			}
		} else if strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian") {
			errMsg.WriteString("You can install them by running command:\n")
			if len(missing_pack) != 0 {
				errMsg.WriteString(" sudo apt install " + strings.Join(missing_pack, " ") + "\n")
			}
			if len(missing_pack_src) != 0 {
				errMsg.WriteString("install from sources: " +
					strings.Join(missing_pack_src, " ") + "\n")
			}
		}
		errMsg.WriteString("Usage: tt install -f if you already have those packages installed")
		return errors.New(errMsg.String())
	}
	return nil
}

// downloadRepo downloads git repository.
func downloadRepo(repoLink string, tag string, dst string, logFile *os.File, verbose bool) error {
	gitCloneArgs := make([]string, 0, 10)
	if tag == "master" {
		gitCloneArgs = append(gitCloneArgs, "clone", repoLink,
			"--recursive", dst)
	} else {
		gitCloneArgs = append(gitCloneArgs, "clone", "-b", tag, "--depth=1", repoLink,
			"--recursive", dst)
	}

	if util.IsGitFetchJobsSupported() {
		gitCloneArgs = append(gitCloneArgs, "-j", "19") // 19 - Tarantool submodules count.
	}

	return util.ExecuteCommand("git", verbose, logFile, dst, gitCloneArgs...)
}

// copyBuildedTT copies tt binary.
func copyBuildedTT(binDir, path, version string, installCtx InstallCtx,
	logFile *os.File) error {
	var err error
	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		err = os.MkdirAll(binDir, defaultDirPermissions)
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

// checkCommit checks the existence of a commit by hash.
func checkCommit(input string, programName string, installCtx InstallCtx,
	distfiles string) (string, string, error) {
	pullRequestHash := ""
	isPullRequest, _ := util.IsPullRequest(input)
	if isPullRequest {
		pullRequestHash, err := getCommit(installCtx.Local, distfiles,
			programName, input)
		if err != nil {
			return input, pullRequestHash,
				fmt.Errorf("%s: unable to get pull-request info", input)
		}
		pullRequestHash = pullRequestHash[0:util.MinCommitHashLength]
		return input, pullRequestHash, nil
	}

	isRightFormat, err := util.IsValidCommitHash(input)
	if err != nil {
		return input, pullRequestHash, err
	}

	if !isRightFormat {
		return input, pullRequestHash, fmt.Errorf("hash has a wrong format")
	}

	returnedHash, err := getCommit(installCtx.Local, distfiles, programName, input)
	if err != nil {
		return input, pullRequestHash, fmt.Errorf("%s: unable to get hash info", input)
	}

	return returnedHash, pullRequestHash, nil
}

// gitCheckout switches to commit/tag, initializes and updates submodules.
func gitCheckout(repoDir string, checkout string, verbose bool, logWriter io.Writer) error {
	err := util.ExecuteCommand("git", verbose, logWriter, repoDir, "checkout", checkout)
	if err != nil {
		return fmt.Errorf("failed to checkout: %s", err)
	}
	err = util.ExecuteCommand("git", verbose, logWriter, repoDir, "submodule", "update",
		"--init", "--recursive")
	if err != nil {
		return fmt.Errorf("failed to update submodules: %s", err)
	}
	return nil
}

// installTt installs selected version of tt.
func installTt(binDir string, installCtx InstallCtx, distfiles string) error {
	versionFound := false
	pullRequestHash := ""
	isPullRequest := false
	pullRequestID := ""

	// Get latest version if it was not specified.
	ttVersion := installCtx.version
	if ttVersion == "" {
		log.Infof("Getting latest tt version...")
		versions, err := getVersionsFromRepo(installCtx.Local, distfiles, "tt", search.GitRepoTT)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return fmt.Errorf("no versions were fetched")
		} else {
			ttVersion = versions[len(versions)-1].Str
		}
	}

	// Check that the version exists.
	// The tag format in tt is vX.Y.Z, but the user can use the X.Y.Z format
	// and this option needs to be supported.
	if ttVersion != "master" {
		_, err := version.Parse(ttVersion)
		if err == nil {
			log.Infof("Searching in versions...")
			versions, err := getVersionsFromRepo(installCtx.Local, distfiles,
				"tt", search.GitRepoTT)
			if err != nil {
				return err
			}

			versionMatches, err := regexp.Match(MajorMinorPatchRegexp, []byte(ttVersion))
			if err != nil {
				return err
			}
			for _, ver := range versions {
				if ttVersion == ver.Str || (versionMatches && "v"+ttVersion == ver.Str) {
					versionFound = true
					ttVersion = ver.Str
					break
				}
			}
			if !versionFound {
				return fmt.Errorf("%s version of tt doesn't exist", ttVersion)
			}
		} else {
			isPullRequest, pullRequestID = util.IsPullRequest(ttVersion)

			if isPullRequest {
				log.Infof("Searching in pull-requests...")
			} else {
				log.Infof("Searching in commits...")
			}

			ttVersion, pullRequestHash, err = checkCommit(
				ttVersion, "tt", installCtx, distfiles)
			if err != nil {
				return err
			}
		}

	}

	versionStr := ""

	if versionFound {
		versionStr = search.ProgramTt + version.FsSeparator + ttVersion
	} else {
		if isPullRequest {
			versionStr = search.ProgramTt + version.FsSeparator + pullRequestHash
		} else {
			versionStr = search.ProgramTt + version.FsSeparator +
				ttVersion[0:util.Min(len(ttVersion), util.MinCommitHashLength)]
		}
	}

	// Check if that version is already installed.
	// If it is installed, check if the newest version exists.
	if !installCtx.Reinstall {
		log.Infof("Checking existing...")
		pathToBin := filepath.Join(binDir, versionStr)
		if util.IsRegularFile(pathToBin) {
			isBinExecutable, err := util.IsExecOwner(pathToBin)
			if err != nil {
				return err
			}

			isUpdatePossible, err := isUpdatePossible(installCtx,
				pathToBin,
				search.ProgramTt,
				ttVersion,
				distfiles,
				isBinExecutable)
			if err != nil {
				return err
			}

			if !isUpdatePossible {
				log.Infof("%s version of tt already exists, updating symlink...", versionStr)
				err := util.CreateSymlink(versionStr, filepath.Join(binDir, search.ProgramTt), true)
				if err != nil {
					return err
				}
				log.Infof("Done")
				return nil
			}
			log.Info("Found newest commit of tt in master")
			// Reduce the case to a reinstallation.
			installCtx.Reinstall = true
		}
	}

	// Check binary directory.
	if binDir == "" {
		return fmt.Errorf("bin_dir is not set, check %s", configure.ConfigName)
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return err
	}

	defer os.Remove(logFile.Name())
	log.Infof("Installing tt=" + ttVersion)

	// Check tt dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if err := programDependenciesInstalled(search.ProgramTt); err != nil {
			return err
		}
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
		if util.IsDir(filepath.Join(distfiles, "tt")) {
			log.Infof("Local files found, installing from them...")
			localPath, _ := util.JoinAbspath(distfiles, "tt")
			err = copy.Copy(localPath, path)
			if err != nil {
				return err
			}
			err = gitCheckout(path, ttVersion, installCtx.verbose, logFile)
		} else {
			return fmt.Errorf("can't find distfiles directory")
		}
	} else {
		log.Infof("Downloading tt...")
		if versionFound {
			err = downloadRepo(search.GitRepoTT, ttVersion, path, logFile, installCtx.verbose)
		} else {
			err = downloadRepo(search.GitRepoTT, "master", path, logFile, installCtx.verbose)
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			if isPullRequest {
				pullRequestCommand := "pull/" + pullRequestID +
					"/head:" + ttVersion
				err = util.ExecuteCommand("git", installCtx.verbose, logFile, path,
					"fetch", "origin", pullRequestCommand)
				if err != nil {
					printLog(logFile.Name())
					return err
				}
			}
			err = gitCheckout(path, ttVersion, installCtx.verbose, logFile)
		}
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

	if isPullRequest {
		log.Infof("Binary name is %s", pullRequestHash)
	}

	// Copy binary.
	log.Infof("Copying executable...")
	err = copyBuildedTT(binDir, path, versionStr, installCtx, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlink.
	err = util.CreateSymlink(versionStr, filepath.Join(binDir, "tt"), true)
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

// patchTarantool applies patches to specific versions of tarantool.
func patchTarantool(srcPath string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) error {
	log.Infof("Patching tarantool...")

	if tarVersion == "master" {
		return nil
	}

	ver, err := version.Parse(tarVersion)
	if err != nil {
		return err
	}

	patches := []patcher{
		patchRange_1_to_2_6_1{defaultPatchApplier{staticBuildPatch}},
		patchRange_1_to_1_10_14{defaultPatchApplier{opensslSymbolsPatch}},
		patchRange_1_10_14_to_1_10_16{defaultPatchApplier{opensslSymbolsPatch14}},
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

// prepareCmakeOpts prepares cmake command line options for tarantool building.
func prepareCmakeOpts(buildPath string, tntVersion string,
	installCtx InstallCtx) ([]string, error) {
	cmakeOpts := []string{".."}

	// Disable backtrace feature for versions 1.10.X.
	// This feature is not supported by a backported static build.
	btFlag := "ON"
	if tntVersion != "master" {
		version, err := version.Parse(tntVersion)
		if err == nil {
			if version.Major == 1 {
				btFlag = "OFF"
			}
		}
	}

	cmakeOpts = append(cmakeOpts, `-DCMAKE_TARANTOOL_ARGS=-DCMAKE_BUILD_TYPE=RelWithDebInfo;`+
		`-DENABLE_WERROR=OFF;-DENABLE_BACKTRACE=`+btFlag)

	if installCtx.Dynamic {
		cmakeOpts = append(cmakeOpts, "-DCMAKE_INSTALL_PREFIX="+filepath.Join(buildPath,
			"tarantool-prefix"))
	} else {
		cmakeOpts = append(cmakeOpts, "-DCMAKE_INSTALL_PREFIX="+buildPath)
	}

	return cmakeOpts, nil
}

// prepareMakeOpts prepares make command line options for tarantool building.
func prepareMakeOpts(installCtx InstallCtx) []string {
	makeOpts := []string{}
	if installCtx.Dynamic {
		makeOpts = append(makeOpts, "install")
	}
	if _, isMakeFlagsSet := os.LookupEnv("MAKEFLAGS"); !isMakeFlagsSet {
		maxThreads := fmt.Sprint(runtime.NumCPU())
		makeOpts = append(makeOpts, "-j", maxThreads)
	}
	return makeOpts
}

// buildTarantool builds tarantool from source. Returns a path, where build artifacts are placed.
func buildTarantool(srcPath string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) (string, error) {

	buildPath := filepath.Join(srcPath, "/static-build/build")
	if installCtx.Dynamic {
		buildPath = filepath.Join(srcPath, "/dynamic-build")
	}
	err := os.MkdirAll(buildPath, defaultDirPermissions)
	if err != nil {
		return "", err
	}

	cmakeOpts, err := prepareCmakeOpts(buildPath, tarVersion, installCtx)
	if err != nil {
		return "", err
	}

	err = util.ExecuteCommand("cmake", installCtx.verbose, logFile, buildPath, cmakeOpts...)
	if err != nil {
		return "", err
	}

	makeOpts := prepareMakeOpts(installCtx)

	return buildPath, util.ExecuteCommand("make", installCtx.verbose, logFile, buildPath,
		makeOpts...)
}

// copyLocalTarantool finds and copies local tarantool folder to tmp folder.
func copyLocalTarantool(distfiles string, path string, tarVersion string,
	installCtx InstallCtx, logFile *os.File) error {
	var err error
	if util.IsDir(filepath.Join(distfiles, "tarantool")) {
		log.Infof("Local files found, installing from them...")
		localPath, _ := util.JoinAbspath(distfiles, "tarantool")
		err = copy.Copy(localPath, path)
		if err != nil {
			return err
		}
		err = gitCheckout(path, tarVersion, installCtx.verbose, logFile)
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
		err = os.MkdirAll(binDir, defaultDirPermissions)
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

func installTarantoolInDocker(tntVersion, binDir, incDir string, installCtx InstallCtx,
	distfiles string) error {
	tmpDir, err := os.MkdirTemp("", "docker_build_ctx")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	goTextEngine := templates.NewDefaultEngine()
	dockerfileText, err := goTextEngine.RenderText(string(tarantoolBuildDockerfile),
		map[string]string{"uid": currentUser.Uid})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	if err = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfileText),
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
	ttCfg := configure.GetDefaultCliOpts()
	ttCfg.Env.BinDir = "/tt_bin"
	ttCfg.Env.IncludeDir = "/tt_include"
	ttCfg.Repo.Install = "/tt_distfiles"
	if err = util.WriteYaml(filepath.Join(tmpDir, configure.ConfigName), ttCfg); err != nil {
		return err
	}

	// Generate install command line for tt in container.
	tntInstallCommandLine := []string{"./tt"}
	if installCtx.verbose {
		tntInstallCommandLine = append(tntInstallCommandLine, "-V")
	}
	tntInstallCommandLine = append(tntInstallCommandLine, "install", "-f", search.ProgramCe,
		tntVersion)
	if installCtx.Reinstall {
		tntInstallCommandLine = append(tntInstallCommandLine, "--reinstall")
	}
	if installCtx.Local {
		tntInstallCommandLine = append(tntInstallCommandLine, "--local-repo")
	}
	if installCtx.Dynamic {
		tntInstallCommandLine = append(tntInstallCommandLine, "--dynamic")
	}

	// Exclude last element from incDir path, because it already has "include" subdir appended.
	// So we get the parent of incDir to get original include path.
	dockerRunOptions := docker.RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_tarantool_build",
		Command:     tntInstallCommandLine,
		Binds: []string{
			fmt.Sprintf("%s:%s", binDir, ttCfg.Env.BinDir),
			fmt.Sprintf("%s:%s", filepath.Dir(incDir), ttCfg.Env.IncludeDir),
			fmt.Sprintf("%s:%s", distfiles, ttCfg.Repo.Install),
		},
		Verbose: installCtx.verbose,
	}
	if err = docker.RunContainer(dockerRunOptions, os.Stdout); err != nil {
		return err
	}

	return nil
}

func getLatestRelease(versions []version.Version) string {
	latestRelease := ""

	for n := len(versions) - 1; n >= 0; n-- {
		if versions[n].Release.Type == version.TypeRelease {
			latestRelease = versions[n].Str
			break
		}
	}

	return latestRelease
}

// changeActiveTarantoolVersion changes symlinks to the specified tarantool version.
func changeActiveTarantoolVersion(versionStr, binDir, incDir string) error {
	err := util.CreateSymlink(versionStr, filepath.Join(binDir, "tarantool"), true)
	if err != nil {
		return err
	}
	err = util.CreateSymlink(versionStr, filepath.Join(incDir, "tarantool"), true)
	return err
}

// installTarantool installs selected version of tarantool.
func installTarantool(binDir string, incDir string, installCtx InstallCtx,
	distfiles string) error {
	// Check bin and header dirs.
	if binDir == "" {
		return fmt.Errorf("bin_dir is not set, check %s", configure.ConfigName)
	}
	if incDir == "" {
		return fmt.Errorf("inc_dir is not set, check %s", configure.ConfigName)
	}

	versionFound := false
	pullRequestHash := ""
	isPullRequest := false
	pullRequestID := ""

	// Get latest version if it was not specified.
	tarVersion := installCtx.version
	if tarVersion == "" {
		log.Infof("Getting latest tarantool version...")

		versions, err := getVersionsFromRepo(installCtx.Local, distfiles, "tarantool",
			search.GitRepoTarantool)
		if err != nil {
			return err
		}

		tarVersion = getLatestRelease(versions)
		if tarVersion == "" {
			return fmt.Errorf("no version found")
		}
	}

	// Check that the version exists.
	if tarVersion != "master" {
		_, err := version.Parse(tarVersion)
		if err == nil {
			log.Infof("Searching in versions...")
			versions, err := getVersionsFromRepo(installCtx.Local, distfiles, "tarantool",
				search.GitRepoTarantool)
			if err != nil {
				return err
			}
			for _, ver := range versions {
				if tarVersion == ver.Str {
					versionFound = true
					break
				}
			}
			if !versionFound {
				return fmt.Errorf("%s version of tarantool doesn't exist", tarVersion)
			}
		} else {
			isPullRequest, pullRequestID = util.IsPullRequest(tarVersion)

			if isPullRequest {
				log.Infof("Searching in pull-requests...")
			} else {
				log.Infof("Searching in commits...")
			}

			tarVersion, pullRequestHash, err = checkCommit(
				tarVersion, "tarantool", installCtx, distfiles)
			if err != nil {
				return err
			}
		}
	}

	versionStr := ""

	if versionFound {
		versionStr = search.ProgramCe + version.FsSeparator + tarVersion
	} else {
		if isPullRequest {
			versionStr = search.ProgramCe + version.FsSeparator + pullRequestHash
		} else {
			versionStr = search.ProgramCe + version.FsSeparator +
				tarVersion[0:util.Min(len(tarVersion), util.MinCommitHashLength)]
		}
	}

	// Check if program is already installed.
	// If it is installed, check if newest version exists.
	if !installCtx.Reinstall {
		log.Infof("Checking existing...")
		pathToBin := filepath.Join(binDir, versionStr)
		if util.IsRegularFile(pathToBin) &&
			util.IsDir(filepath.Join(incDir, versionStr)) {

			isBinExecutable, err := util.IsExecOwner(pathToBin)
			if err != nil {
				return err
			}

			isUpdatePossible, err := isUpdatePossible(installCtx,
				pathToBin,
				search.ProgramCe,
				tarVersion,
				distfiles,
				isBinExecutable)
			if err != nil {
				return err
			}

			if !isUpdatePossible {
				log.Infof("%s version of tarantool already exists, updating symlinks...",
					versionStr)
				err = changeActiveTarantoolVersion(versionStr, binDir, incDir)
				if err != nil {
					return err
				}
				log.Infof("Done")
				return nil
			}

			log.Infof("Found newest commit of tarantool in master")
		}
	}

	if installCtx.BuildInDocker {
		return installTarantoolInDocker(tarVersion, binDir, incDir, installCtx, distfiles)
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool=" + tarVersion)
	// Check dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if err := programDependenciesInstalled(search.ProgramCe); err != nil {
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
		err = copyLocalTarantool(distfiles, path, tarVersion, installCtx,
			logFile)
	} else {
		log.Infof("Downloading tarantool...")
		if versionFound {
			err = downloadRepo(search.GitRepoTarantool, tarVersion, path, logFile,
				installCtx.verbose)
		} else {
			err = downloadRepo(search.GitRepoTarantool, "master", path, logFile, installCtx.verbose)
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			if isPullRequest {
				pullRequestCommand := "pull/" + pullRequestID +
					"/head:" + tarVersion
				err = util.ExecuteCommand("git", installCtx.verbose, logFile, path,
					"fetch", "origin", pullRequestCommand)
				if err != nil {
					printLog(logFile.Name())
					return err
				}
			}
			err = gitCheckout(path, tarVersion, installCtx.verbose, logFile)
		}
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Patch tarantool.
	if versionFound {
		err = patchTarantool(path, tarVersion, installCtx, logFile)
		if err != nil {
			printLog(logFile.Name())
			return err
		}
	}

	// Build tarantool.
	log.Infof("Building tarantool...")
	buildPath, err := buildTarantool(path, tarVersion, installCtx, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	if isPullRequest {
		log.Infof("Binary name is %s", pullRequestHash)
	}
	// Copy binary and headers.
	if installCtx.Reinstall {
		if util.IsRegularFile(filepath.Join(binDir, versionStr)) {
			log.Infof("%s version of tarantool already exists, removing files...",
				versionStr)
			err = os.RemoveAll(filepath.Join(binDir, versionStr))
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			err = os.RemoveAll(filepath.Join(incDir, versionStr))
		}
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	binPath := filepath.Join(buildPath, "tarantool-prefix", "bin", "tarantool")
	incPath := filepath.Join(buildPath, "tarantool-prefix", "include", "tarantool") + "/"
	err = copyBuildedTarantool(binPath, incPath, binDir, incDir, versionStr, installCtx,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = changeActiveTarantoolVersion(versionStr, binDir, incDir)
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

// isUpdatePossible func checks if Tt or Tarantool are 'master' version
// and if so, asks user about checking for update to latest commit.
// Function returns boolean variable if update of master is possible or not.
func isUpdatePossible(installCtx InstallCtx,
	pathToBin,
	program,
	progVer,
	distfiles string,
	isBinExecutable bool) (bool, error) {
	var curBinHash, lastCommitHash string
	// We need to make sure that we check newest commits only for
	// production 'master' branch. Also we want to ask if user wants
	// to check for updates.
	if isBinExecutable && progVer == "master" {
		var err error
		answer := false
		if !installCtx.skipMasterUpdate {
			answer, err = util.AskConfirm(os.Stdin, "The 'master' version of the "+program+
				" has already been installed.\n"+
				"Would you like to update it if there are newer commits available?")
			if err != nil {
				return false, err
			}
		}

		if answer {
			lastCommitHash, err = getCommit(installCtx.Local, distfiles,
				program, progVer)
			if err != nil {
				return false, err
			}

			if program == search.ProgramCe {
				tarantoolBin := cmdcontext.TarantoolCli{
					Executable: pathToBin,
				}
				binVersion, err := tarantoolBin.GetVersion()
				if err != nil {
					return false, err
				}
				// We need to trim first rune to get commit hash
				// from string structure 'g<commitHash>'.
				if len(binVersion.Hash) < 1 {
					return false, fmt.Errorf("could not get commit hash of the version"+
						"of an installed %s", program)
				}
				curBinHash = binVersion.Hash[1:]
			} else if program == search.ProgramTt {
				ttVer, err := cmdcontext.GetTtVersion(pathToBin)
				if err != nil {
					return false, err
				}
				curBinHash = ttVer.Hash
			}
		}
	}

	if strings.HasPrefix(lastCommitHash, curBinHash) {
		return false, nil
	}
	return true, nil
}

// installTarantoolEE installs selected version of tarantool-ee.
func installTarantoolEE(binDir string, includeDir string, installCtx InstallCtx,
	distfiles string, cliOpts *config.CliOpts) error {
	var err error

	// Check bin and header directories.
	if binDir == "" {
		return fmt.Errorf("bin_dir is not set, check %s", configure.ConfigName)
	}
	if includeDir == "" {
		return fmt.Errorf("inc_dir is not set, check %s", configure.ConfigName)
	}

	files := []string{}
	if installCtx.Local {
		localFiles, err := os.ReadDir(cliOpts.Repo.Install)
		if err != nil {
			return err
		}

		for _, file := range localFiles {
			if strings.Contains(file.Name(), "tarantool-enterprise-sdk") && !file.IsDir() {
				files = append(files, file.Name())
			}
		}
	}

	tarVersion := installCtx.version
	if tarVersion == "" {
		return fmt.Errorf("to install tarantool-ee, you need to specify the version")
	}

	// Check if program is already installed.
	versionStr := search.ProgramEe + version.FsSeparator + tarVersion
	if !installCtx.Reinstall {
		log.Infof("Checking existing...")
		if util.IsRegularFile(filepath.Join(binDir, versionStr)) &&
			util.IsDir(filepath.Join(includeDir, versionStr)) {
			log.Infof("%s version of tarantool already exists, updating symlinks...", versionStr)
			err = changeActiveTarantoolVersion(versionStr, binDir, includeDir)
			if err != nil {
				return err
			}
			log.Infof("Done")
			return err
		}
	}

	ver, err := search.GetTarantoolBundleInfo(cliOpts, installCtx.Local,
		installCtx.DevBuild, files, tarVersion)
	if err != nil {
		return err
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool-ee=" + tarVersion)

	// Check dependencies.
	if !installCtx.Force {
		log.Infof("Checking dependencies...")
		if err := programDependenciesInstalled(search.ProgramCe); err != nil {
			return err
		}
	}

	log.Infof("Getting bundle name for %s", tarVersion)
	bundleName := ver.Version.Tarball
	bundleSource, err := search.TntIoMakePkgURI(ver.Package, ver.Release,
		bundleName, installCtx.DevBuild)
	if err != nil {
		return err
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
		if util.IsRegularFile(filepath.Join(distfiles, bundleName)) {
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
		err := install_ee.GetTarantoolEE(cliOpts, bundleName, bundleSource, ver.Token, path)
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
		if util.IsRegularFile(filepath.Join(binDir, versionStr)) {
			log.Infof("%s version of tarantool-ee already exists, removing files...",
				versionStr)
			err = os.RemoveAll(filepath.Join(binDir, versionStr))
			if err != nil {
				printLog(logFile.Name())
				return err
			}
			err = os.RemoveAll(filepath.Join(includeDir, versionStr))
		}
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	binPath := filepath.Join(path, "/tarantool-enterprise/tarantool")
	incPath := filepath.Join(path, "/tarantool-enterprise/include/tarantool") + "/"
	err = copyBuildedTarantool(binPath, incPath, binDir, includeDir, versionStr, installCtx,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = changeActiveTarantoolVersion(versionStr, binDir, includeDir)
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

// dirIsWritable checks if the current user has the write access to the passed directory.
func dirIsWritable(dir string) bool {
	return unix.Access(dir, unix.W_OK) == nil
}

// searchTarantoolHeaders searches tarantool headers.
// First, it checks the specified includeDir,
// in case of failure, it checks the default one.
func searchTarantoolHeaders(buildDir, includeDir string) (string, error) {
	var err error
	if includeDir != "" {
		includeDir, err = filepath.Abs(includeDir)
		if err != nil {
			return "", err
		}
		if !util.IsDir(includeDir) {
			return "", fmt.Errorf("directory %v doesn't exist, "+
				"or isn't a directory", includeDir)
		}
		return includeDir, nil
	}
	// Check the default path.
	defaultIncPath := filepath.Join(buildDir, "tarantool-prefix", "include", "tarantool")
	if util.IsDir(defaultIncPath) {
		return defaultIncPath, nil
	}
	return "", nil
}

// installTarantoolDev installs tarantool from the local build directory.
func installTarantoolDev(ttBinDir string, ttIncludeDir, buildDir,
	includeDir string) error {
	var err error

	// Validate build directory.
	if buildDir, err = filepath.Abs(buildDir); err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}
	if !util.IsDir(buildDir) {
		return fmt.Errorf("directory %v doesn't exist, or isn't directory", buildDir)
	}

	checkedBinaryPaths := make([]string, 0)

	// Searching for tarantool binary.
	for _, binaryRelPath := range [...]string{
		"src/tarantool",
		"tarantool/src/tarantool",
		"tarantool-prefix/bin/tarantool",
	} {
		binaryPath := filepath.Join(buildDir, binaryRelPath)

		var isExecOwner bool
		isExecOwner, err = util.IsExecOwner(binaryPath)
		if err == nil && isExecOwner && !util.IsDir(binaryPath) {
			// Check that tt directories exist.
			if err = util.CreateDirectory(ttBinDir, defaultDirPermissions); err != nil {
				return err
			}
			if err = util.CreateDirectory(ttIncludeDir, defaultDirPermissions); err != nil {
				return err
			}

			log.Infof("Changing symlinks...")
			err = util.CreateSymlink(binaryPath, filepath.Join(ttBinDir, "tarantool"), true)
			if err != nil {
				return err
			}

			includeDir, err = searchTarantoolHeaders(buildDir, includeDir)
			if err != nil {
				return err
			}
			tarantoolIncludeSymlink := filepath.Join(ttIncludeDir, "tarantool")
			// Remove old symlink to the tarantool headers.
			// RemoveAll is used to perform deletion even if the file is not a symlink.
			err = os.RemoveAll(tarantoolIncludeSymlink)
			if err != nil {
				return err
			}
			if includeDir == "" {
				log.Warn("Tarantool headers location was not specified. " +
					"`tt build`, `tt rocks` may not work properly.\n" +
					"  To specify include files location use --include-dir option.")
			} else {
				err = util.CreateSymlink(includeDir, tarantoolIncludeSymlink, true)
				if err != nil {
					return err
				}
				log.Infof("tarantool headers directory set as %v.", includeDir)
			}
			log.Infof("Done.")
			return nil
		}
		checkedBinaryPaths = append(checkedBinaryPaths, binaryPath)
	}

	return fmt.Errorf("tarantool binary was not found in the paths:\n%s",
		strings.Join(checkedBinaryPaths, "\n"))
}

// subDirIsWritable checks if the passed dir doesn't exist but can be created.
func subDirIsWritable(dir string) bool {
	var err error
	for {
		_, err = os.Stat(dir)
		if os.IsNotExist(err) {
			dir = filepath.Dir(dir)
			continue
		}
		return dirIsWritable(dir)
	}
}

// Install installs program.
func Install(binDir string, includeDir string, installCtx InstallCtx,
	local string, cliOpts *config.CliOpts) error {
	var err error

	// This check is needed for knowing that we will be able to copy
	// recently built binaries to the corresponding bin and include directories.
	for _, dir := range []string{binDir, includeDir} {
		if _, err := os.Stat(dir); os.IsNotExist(err) && subDirIsWritable(dir) {
			continue
		}
		if !dirIsWritable(dir) {
			return fmt.Errorf("the directory %s is not writeable for the current user.\n"+
				"     Please, update rights to the directory or use 'sudo' for successful install",
				dir)
		}
	}
	includeDir = filepath.Join(includeDir, "include")

	switch installCtx.ProgramName {
	case search.ProgramTt:
		err = installTt(binDir, installCtx, local)
	case search.ProgramCe:
		err = installTarantool(binDir, includeDir, installCtx, local)
	case search.ProgramEe:
		err = installTarantoolEE(binDir, includeDir, installCtx, local, cliOpts)
	case search.ProgramDev:
		err = installTarantoolDev(binDir, includeDir, installCtx.buildDir,
			installCtx.IncDir)
	default:
		return fmt.Errorf("unknown application: %s", installCtx.ProgramName)
	}

	return err
}

func FillCtx(cmdCtx *cmdcontext.CmdCtx, installCtx *InstallCtx, args []string) error {
	installCtx.verbose = cmdCtx.Cli.Verbose
	installCtx.skipMasterUpdate = cmdCtx.Cli.NoPrompt

	if cmdCtx.CommandName == search.ProgramDev {
		if len(args) != 1 {
			return fmt.Errorf("exactly one build directory must be specified")
		}
		installCtx.buildDir = args[0]
		return nil
	}

	if len(args) == 1 {
		installCtx.version = args[0]
	} else if len(args) > 1 {
		return fmt.Errorf("invalid number of parameters")
	}

	return nil
}
