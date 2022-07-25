package install

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

// InstallationFlag is a struct that contains all install flags.
type InstallationFlag struct {
	// Reinstall is a flag. If it is set,
	// target application will be reinstalled if already exists.
	Reinstall bool
	// Force is a flag. If it is set, install will force
	// requirements errors.
	Force bool
	// Verbose is a flag.
	// If it is set, install will print log to stderr.
	Verbose bool
	// Noclean is a flag. If it is set,
	// install will don't remove tmp files.
	Noclean bool
	// Local is a flag. If it is set,
	// install will do local installation.
	Local bool
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

const (
	// TarantoolLink is a link to the git repository of tarantool.
	TarantoolLink = "https://github.com/tarantool/tarantool.git"
	// TTLink is a link to the git repository of tt.
	TTLink = "https://github.com/tarantool/tt"
	// defaultDirPermissions is rights used to create folders.
	// 0755 - drwxr-xr-x
	// We need to give permission for all to execute
	// read,write for user and only read for others.
	defaultDirPermissions = 0755
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
	return "", fmt.Errorf("Unknown OS")
}

// getLatestTarantoolTag returns latest tag from tarantool repository.
func getLatestTarantoolTag() (string, error) {
	// TODO Use version functions from util/version (will be implemented later)

	// Get all tags from tarantool git repo.
	versions, err := util.RunCommandAndGetOutput("git", "ls-remote", "--tags",
		"--sort="+"v:refname", TarantoolLink)
	if err != nil {
		return "", err
	}

	versionsArray := strings.Split(versions, "\n")
	if len(versionsArray) == 0 {
		return "", fmt.Errorf("Could not get latest version of tarantool.")
	}
	// Get last tag.
	latestTag := versionsArray[len(versionsArray)-2]
	latestTag = strings.TrimSuffix(latestTag, "^{}")
	trimpos := strings.LastIndex(latestTag, "/") + 1
	latestTag = latestTag[trimpos:]
	return latestTag, nil
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
			answer, err := util.AskConfirm("Unknown OS, can't check if dependencies" +
				" are installed.\n" +
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
		log.Infof("The operation requires some dependencies.")
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

// ExecuteCommand executes program with given args in verbose or quiet mode.
func ExecuteCommand(program string, isVerbose bool, logFile *os.File, workDir string,
	args ...string) error {
	cmd := exec.Command(program, args...)
	if isVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	cmd.Dir = workDir
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	return err
}

// downloadRepo downloads git repository.
func downloadRepo(repoLink string, tag string, dst string,
	logFile *os.File, verbose bool) error {
	var err error
	if tag == "master" {
		err = util.ExecuteCommand("git", verbose, logFile, dst, "clone",
			"-j", "18", repoLink,
			"--recursive", dst)
	} else {
		err = util.ExecuteCommand("git", verbose, logFile, dst, "clone", "-b", tag,
			"--depth=1", "-j", "18", repoLink,
			"--recursive", dst)
	}

	return err
}

// copyBuildedTT copies tt binary.
func copyBuildedTT(binDir, path, version string, flags InstallationFlag,
	logFile *os.File) error {
	var err error
	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		err = os.Mkdir(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("Unable to create %s\n Error: %s", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("Unable to create %s\n Error: %s", binDir, err)
	}
	if flags.Reinstall {
		err = os.Remove(filepath.Join(binDir, version))
		if err != nil {
			return err
		}
	}
	err = util.CopyFilePreserve(filepath.Join(path, "tt"), filepath.Join(binDir, version))
	return err
}

// installTt installs selected version of tt.
func installTt(version string, binDir string, flags InstallationFlag, distfiles string) error {

	// Check binary directory.
	if binDir == "" {
		return fmt.Errorf("BinDir is not set, check tarantool.yaml")
	}
	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())
	if version == "tt" {
		version = "tt=master"
	}
	// TODO After tt release remove.
	if version != "tt=master" {
		return fmt.Errorf("Currently tt has no versions, only one is master")
	}
	// Check tt dependencies.
	if !flags.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tt") {
			return nil
		}
	}

	// Check if that version is already installed.
	log.Infof("Checking existing...")
	if checkExisting(version, binDir) && !flags.Reinstall {
		log.Infof("%s version of tt already exists, updating symlink...", version)
		err := util.CreateSymLink(version, binDir, "tt", true)
		log.Infof("Done")
		return err
	}

	path, err := os.MkdirTemp("", "tt_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !flags.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tt.
	if flags.Local {
		if checkExisting("tt", distfiles) {
			log.Infof("Local files found, installing from them...")
			localPath, _ := util.JoinAbspath(distfiles, "tt")
			err = copy.Copy(localPath, path)
			if err != nil {
				return err
			}
			util.ExecuteCommand("git", flags.Verbose, logFile, path, "checkout", "master")
		} else {
			return fmt.Errorf("Can't find distfiles directory.")
		}
	} else {
		log.Infof("Downloading tt...")
		err = downloadRepo(TTLink, "master", path, logFile, flags.Verbose)
	}

	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Build tt.
	log.Infof("Building tt...")
	err = util.ExecuteCommand("mage", flags.Verbose, logFile, path,
		"build")
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Copy binary.
	log.Infof("Copying executable...")
	err = copyBuildedTT(binDir, path, version, flags, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlink.
	err = util.CreateSymLink(version, binDir, "tt", true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	log.Infof("Done.")
	if flags.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// checkExistingTarantool
func checkExistingTarantool(version, binDir, includeDir string,
	flags InstallationFlag) (bool, error) {
	var err error
	flag := false
	if checkExisting(version, binDir) {
		if !flags.Reinstall {
			log.Infof("%s version of tarantool already exists, updating symlink...", version)
			err = util.CreateSymLink(version, binDir, "tarantool", true)
			log.Infof("Done")
			flag = true
		}
	}
	return flag, err
}

// buildTarantool builds tarantool from source.
func buildTarantool(srcPath string, tarVersion string,
	flags InstallationFlag, logFile *os.File) error {
	err := util.ExecuteCommand("git", flags.Verbose, logFile, srcPath,
		"submodule", "update", "--init", "--recursive")
	if err != nil {
		return err
	}

	buildPath := filepath.Join(srcPath, "/static-build/build")
	err = os.MkdirAll(buildPath, defaultDirPermissions)
	if err != nil {
		return err
	}

	util.ExecuteCommand("git", flags.Verbose, logFile, srcPath,
		"tag "+tarVersion+" -m '"+tarVersion+"'")

	maxThreads := fmt.Sprint(runtime.NumCPU())
	err = util.ExecuteCommand("cmake", flags.Verbose, logFile, buildPath,
		"..", "-DCMAKE_BUILD_TYPE=RelWithDebInfo ",
		"-DCMAKE_INSTALL_PREFIX="+buildPath)
	if err != nil {
		return err
	}

	err = util.ExecuteCommand("make", flags.Verbose, logFile, buildPath,
		"-j"+maxThreads)
	return err
}

// copyLocalTarantool finds and copies local tarantool folder to tmp folder.
func copyLocalTarantool(distfiles string, path string, tarVersion string,
	flags InstallationFlag, logFile *os.File) error {
	var err error
	if checkExisting("tarantool", distfiles) {
		log.Infof("Local files found, installing from them...")
		localPath, _ := util.JoinAbspath(distfiles, "tarantool")
		err = copy.Copy(localPath, path)
		if err != nil {
			return err
		}
		err = util.ExecuteCommand("git", flags.Verbose, logFile, path, "checkout", tarVersion)
	} else {
		return fmt.Errorf("Can't find distfiles directory.")
	}
	return err
}

// copyBuildedTarantool copies binary and include dir.
func copyBuildedTarantool(binPath, incPath, binDir, includeDir, version string,
	flags InstallationFlag, logFile *os.File) error {
	var err error
	log.Infof("Copying executable...")
	if _, err := os.Stat(binDir); os.IsNotExist(err) {
		err = os.Mkdir(binDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("Unable to create %s\n Error: %s", binDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("Unable to create %s\n Error: %s", binDir, err)
	}

	err = util.CopyFileChangePerms(binPath, filepath.Join(binDir, version),
		defaultDirPermissions)
	if err != nil {
		return err
	}

	log.Infof("Copying headers...")
	if _, err := os.Stat(includeDir); os.IsNotExist(err) {
		err = os.Mkdir(includeDir, defaultDirPermissions)
		if err != nil {
			return fmt.Errorf("Unable to create %s\n Error: %s", includeDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("Unable to create %s\n Error: %s", includeDir, err)
	}
	err = copy.Copy(incPath, filepath.Join(includeDir, version)+"/")
	return err
}

// checkTarVersion returns true if version exists.
func checkTarVersion(version string) (bool, error) {
	versions, err := util.RunCommandAndGetOutput("git", "-c", "versionsort.suffix=-",
		"ls-remote", "--tags", "--sort="+"v:refname",
		"https://github.com/tarantool/tarantool.git")
	if err != nil {
		return false, err
	}
	versionsArray := strings.Split(versions, "\n")
	for _, v := range versionsArray {
		trimPos := strings.LastIndex(v, "/") + 1
		v = v[trimPos:]
		if strings.Contains(version, v) {
			return true, err
		}
	}
	return false, err
}

// checkTarVersionLocal returns true if version exists locally.
func checkTarVersionLocal(version string, distfiles string) (bool, error) {
	versions, err := search.RunCommandAndGetOutputInDir("git",
		distfiles+"/tarantool",
		"-c", "versionsort.suffix=-",
		"tag", "--sort="+"v:refname")
	if err != nil {
		return false, err
	}
	versionsArray := strings.Split(versions, "\n")
	for _, v := range versionsArray {
		if strings.Contains(version, v) {
			return true, err
		}
	}
	return false, err
}

// installTarantool installs selected version of tarantool.
func installTarantool(version string, binDir string, incDir string, flags InstallationFlag,
	distfiles string) error {
	var err error
	// Get latest tarantool tag if needed.
	_, tarVersion, _ := strings.Cut(version, "=")
	if tarVersion == "" {
		tarVersion, err = getLatestTarantoolTag()
		if err != nil {
			return err
		}
		version += "=" + tarVersion
	}
	if util.IsDeprecated(tarVersion) {
		return fmt.Errorf("Deprecated version of tarantool: %s", tarVersion)
	}

	// Check bin and header dirs.
	if binDir == "" {
		return fmt.Errorf("BinDir is not set, check tarantool.yaml ")
	}
	if incDir == "" {
		return fmt.Errorf("IncludeDir is not set, check tarantool.yaml")
	}
	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool=" + tarVersion)

	// Check dependencies.
	if !flags.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tarantool") {
			return nil
		}
	}

	// Check if program is already installed.
	if !flags.Reinstall {
		log.Infof("Checking existing...")
		versionExists, err := checkExistingTarantool(version,
			binDir, incDir, flags)
		if err != nil || versionExists {
			return err
		}
	}

	path, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !flags.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tarantool.
	if flags.Local {
		log.Infof("Checking local files...")
		if tarVersion != "master" {
			verExists, err := checkTarVersionLocal(tarVersion, distfiles)
			if !verExists {
				return fmt.Errorf("%s version of tarantool doesn't exist", tarVersion)
			}
			if err != nil {
				return err
			}
		}
		err = copyLocalTarantool(distfiles, path, tarVersion, flags,
			logFile)
	} else {
		log.Infof("Downloading tarantool...")
		if tarVersion != "master" {
			verExists, err := checkTarVersion(tarVersion)
			if !verExists {
				return fmt.Errorf("%s version of tarantool doesn't exist", tarVersion)
			}
			if err != nil {
				return err
			}
		}
		err = downloadRepo(TarantoolLink, tarVersion, path, logFile, flags.Verbose)
	}
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Build tarantool.
	log.Infof("Building tarantool...")
	err = buildTarantool(path, tarVersion, flags, logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Copy binary and headers.
	if flags.Reinstall {
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
	err = copyBuildedTarantool(binPath, incPath, binDir, incDir, version, flags,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = util.CreateSymLink(version, binDir, "tarantool", true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	err = util.CreateSymLink(version, incDir, "tarantool", true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}
	log.Infof("Done.")
	if flags.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// getLatestTarantoolEETag returns latest version of tarantool-ee for user's OS.
func getLatestTarantoolEETag(cliOpts *config.CliOpts) (string, error) {
	versions, err := install_ee.FetchVersions(cliOpts)
	if err != nil {
		return "", err
	}
	return versions[len(versions)-1].Str, err
}

// installTarantoolEE installs selected version of tarantool-ee.
func installTarantoolEE(version string, binDir string, includeDir string, flags InstallationFlag,
	distfiles string, cliOpts *config.CliOpts) error {
	_, tarVersion, _ := strings.Cut(version, "=")
	var err error

	// Get latest version if it was not specified.
	if tarVersion == "" {
		log.Infof("Getting latest tarantool-ee version..")
		tarVersion, err = getLatestTarantoolEETag(cliOpts)
	}
	if err != nil {
		return err
	}

	// Check bin and header dirs.
	if binDir == "" {
		return fmt.Errorf("BinDir is not set, check tarantool.yaml")
	}
	if includeDir == "" {
		return fmt.Errorf("IncludeDir is not set, check tarantool.yaml")
	}
	logFile, err := ioutil.TempFile("", "tarantool_install")
	if err != nil {
		return err
	}
	defer os.Remove(logFile.Name())

	log.Infof("Installing tarantool-ee=" + tarVersion)

	// Check dependencies.
	if !flags.Force {
		log.Infof("Checking dependencies...")
		if !programDependenciesInstalled("tarantool") {
			return nil
		}
	}

	// Check if program is already installed.
	log.Infof("Checking existing...")
	log.Infof("Getting bundle name for %s", tarVersion)
	bundleName, err := install_ee.GetVersionName(cliOpts, tarVersion)
	if err != nil {
		return err
	}
	version = "tarantool-ee=" + tarVersion
	if !flags.Reinstall {
		log.Infof("Checking existing...")
		versionExists, err := checkExistingTarantool(version,
			binDir, includeDir, flags)
		if err != nil || versionExists {
			return err
		}
	}

	path, err := os.MkdirTemp("", "tarantool_install")
	if err != nil {
		return err
	}
	os.Chmod(path, defaultDirPermissions)

	if !flags.Noclean {
		defer os.RemoveAll(path)
	}

	// Download tarantool.
	if flags.Local {
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
			return fmt.Errorf("Can't find distfiles directory.")
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
	if flags.Reinstall {
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
	err = copyBuildedTarantool(binPath, incPath, binDir, includeDir, version, flags,
		logFile)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	// Set symlinks.
	log.Infof("Changing symlinks...")
	err = util.CreateSymLink(version, binDir, "tarantool", true)
	if err != nil {
		return err
	}
	err = util.CreateSymLink(version, includeDir, "tarantool", true)
	if err != nil {
		printLog(logFile.Name())
		return err
	}

	log.Infof("Done.")
	if flags.Noclean {
		log.Infof("Artifacts can be found at: %s", path)
	}
	return nil
}

// Install installs program.
func Install(args []string, binDir string, includeDir string, flags InstallationFlag,
	local string, cliOpts *config.CliOpts) error {
	var err error
	if len(args) != 1 {
		return fmt.Errorf("Invalid number of parameters")
	}
	if strings.Contains(args[0], "tt") {
		log.Infof("Installing tt...")
		err = installTt(args[0], binDir, flags, local)
	} else if strings.Contains(args[0], "tarantool-ee") {
		err = installTarantoolEE(args[0], binDir, includeDir, flags, local, cliOpts)
	} else if strings.Contains(args[0], "tarantool") {
		err = installTarantool(args[0], binDir, includeDir, flags, local)
	} else {
		log.Errorf("Unknown application: " + args[0])
	}
	return err
}
