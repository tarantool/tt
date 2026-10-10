package binary

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	errASpecificVersionMustBeProvidedToInstall = errors.New(
		"a specific version must be provided to install ",
	)
	errCouldNotFindBinaryAt                         = errors.New("could not find binary at ")
	errIncludeDirIsNotSetCheck                      = errors.New("include_dir is not set, check ")
	errInstallationFailedDuringExecutionPhaseSeeLog = errors.New(
		"installation failed during execution phase (see log: ",
	)
	errInstallationFailedDuringFinaleUpdateSymlinks = errors.New(
		"installation failed during finale update symlinks",
	)
	errInstallationPathOrAlreadyExists       = errors.New("installation path ")
	errLocalBundleFileNotFound               = errors.New("local bundle file not found: ")
	errLocalRepositoryInstallDirectoryNotSet = errors.New(
		"cannot install from local repository: " +
			"distribution files directory (repo.install) is not set",
	)
	errTheDirectoryIsNotWriteableForTheCurrentUser = errors.New("the directory ")
)

// bundleParams holds the parameters required for the bundle installation process.
type bundleParams struct {
	// bundleInfo is the structure that holds information about the bundle.
	// It is filled in during the installation process in getBundleInfoForInstall.
	bundleInfo BundleInfo

	// prgVersion is the specific string is joined program with version.
	// It's filled in during the installation process in acquireBundleInfo.
	prgVersion string

	// inst is the install context for the installation process.
	inst *InstallCtx

	// opts contains command-line options and configurations.
	opts *config.CliOpts

	// tmpDir is the temporary directory used for extraction.
	tmpDir string

	// logFile is the file where installation logs are written for debugging fails.
	logFile *os.File
}

// checkInstallDirs validates that binary and include directories are configured and writable.
func checkInstallDirs(binDir, includeDir string) error {
	if binDir == "" {
		return fmt.Errorf("%w%s", errBinDirNotSet, configure.ConfigName)
	}

	if includeDir == "" {
		// For bundle installs, includeDir is usually required.
		return fmt.Errorf("%w%s", errIncludeDirIsNotSetCheck, configure.ConfigName)
	}

	// Reuse the writability checks from the main Install function.
	for _, dir := range []string{binDir, includeDir} {
		_, err := os.Stat(dir)
		if os.IsNotExist(err) && subDirIsWritable(dir) {
			continue // Directory doesn't exist but can be created.
		}

		if !dirIsWritable(dir) {
			return fmt.Errorf("%w%s is not writeable for the current user",
				errTheDirectoryIsNotWriteableForTheCurrentUser, dir)
		}
	}

	return nil
}

// checkExistingInstallation checks if the specific version of the program is already installed.
// Returns true if the installation exists, false otherwise.
func checkExistingInstallation(params *bundleParams) bool {
	binPath := filepath.Join(params.opts.Env.BinDir, params.prgVersion)
	incPath := filepath.Join(params.inst.IncDir, params.prgVersion)

	binExists := IsRegularFile(binPath)

	if !params.inst.Program.IsTarantool() {
		log.Debugf("Checking existence: bin=%s (%t)", binPath, binExists)

		return binExists
	}

	incExists := IsDir(incPath)

	log.Debugf("Checking existence: bin=%s (%t), inc=%s (%t)",
		binPath, binExists, incPath, incExists)

	return binExists && incExists
}

// prepareTemporaryDirs creates temporary directories for installation and logging.
func prepareTemporaryDirs(params *bundleParams) error {
	var err error

	params.tmpDir, err = os.MkdirTemp("", params.inst.Program.String()+"_install_*")
	if err != nil {
		return fmt.Errorf("failed to create temporary install directory: %w", err)
	}

	_ = os.Chmod(params.tmpDir, defaultDirPermissions)

	params.logFile, err = os.CreateTemp("", params.inst.Program.String()+"_install_log_*")
	if err != nil {
		_ = os.RemoveAll(params.tmpDir)
		return fmt.Errorf("failed to create temporary log file: %w", err)
	}

	log.Debugf("Created temporary install directory: %s", params.tmpDir)
	log.Debugf("Created temporary log file: %s", params.logFile.Name())

	return nil
}

// checkDependencies checks if the required system dependencies are installed.
func checkDependencies(program Program, force bool) error {
	if force {
		log.Debugf("Skipping dependency check due to --force flag.")

		return nil
	}

	return programDependenciesInstalled(program)
}

// copyBundle copies the bundle from a local cache.
func copyBundle(params *bundleParams) error {
	distfiles := params.opts.Repo.Install
	if distfiles == "" {
		return errLocalRepositoryInstallDirectoryNotSet
	}

	log.Infof("Checking local files...")

	bundleName := params.bundleInfo.Version.Tarball
	localBundlePath := filepath.Join(distfiles, bundleName)

	if !IsRegularFile(localBundlePath) {
		return fmt.Errorf("%w%s", errLocalBundleFileNotFound, localBundlePath)
	}

	log.Infof("Local files found, installing from %s...", bundleName)

	err := CopyFilePreserve(localBundlePath, filepath.Join(params.tmpDir, bundleName))
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error copying local bundle: %v\n", err)
		return fmt.Errorf("failed to copy local bundle: %w", err)
	}

	return nil
}

// downloadBundle downloads the bundle from a remote source.
func downloadBundle(params *bundleParams) error {
	bundleName := params.bundleInfo.Version.Tarball

	searchCtx := NewSearchCtx(
		NewPlatformInformer(),
		NewTntIoDownloader(params.bundleInfo.Token),
	)

	searchCtx.Program = params.inst.Program
	searchCtx.DevBuilds = params.inst.DevBuild
	searchCtx.ReleaseVersion = params.bundleInfo.Release

	bundleSource, err := TntIoMakePkgURI(&searchCtx, bundleName)
	if err != nil {
		return fmt.Errorf("failed to construct bundle download URI: %w", err)
	}

	log.Infof("Downloading %s... (%s)", params.inst.Program, bundleSource)

	err = DownloadBundle(searchCtx.TntIoDoer, bundleName, bundleSource, params.tmpDir)
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error downloading bundle: %v\n", err)
		return fmt.Errorf("failed to download bundle: %w", err)
	}

	return nil
}

// obtainBundle downloads the bundle from a remote source or copies it from the local cache.
func obtainBundle(params *bundleParams) error {
	if params.inst.Local {
		return copyBundle(params)
	}

	return downloadBundle(params)
}

// unpackBundle extracts the contents of the bundle archive.
func unpackBundle(bundlePath string, logFile io.Writer) error {
	log.Infof("Unpacking archive %s...", filepath.Base(bundlePath))

	err := ExtractTar(bundlePath)
	if err != nil {
		_, _ = fmt.Fprintf(logFile, "Error unpacking bundle: %v\n", err)
		return fmt.Errorf("failed to extract bundle %s: %w", filepath.Base(bundlePath), err)
	}

	log.Debugf("Bundle %s unpacked successfully.", filepath.Base(bundlePath))

	return nil
}

// getSubDirForProgram returns the subdirectory inside archive for the specified program type.
func getSubDirForProgram(program Program) string {
	switch program {
	case ProgramEe:
		return "tarantool-enterprise"
	case ProgramTcm:
		return "" // The TCM bundle is flat (no subdirectories).
	default:
		return ""
	}
}

// findBundlePathsInDir makes path to binary and include directory.
func findBundlePathsInDir(baseDir string, program Program) (
	string, string, error,
) {
	subDir := getSubDirForProgram(program)

	binPath := filepath.Join(baseDir, subDir, program.Exec())
	if !IsRegularFile(binPath) {
		return "", "", fmt.Errorf("%w%q", errCouldNotFindBinaryAt, binPath)
	}

	incPath := filepath.Join(baseDir, subDir, "include", program.Exec())
	if !IsDir(incPath) {
		incPath = "" // No include directory found in bundle.
	}

	return binPath, incPath, nil
}

// prepareForReinstall remove existing destination directories/files if needed.
func prepareForReinstall(params *bundleParams) error {
	destBinPath := filepath.Join(params.opts.Env.BinDir, params.prgVersion)
	destIncPath := filepath.Join(params.inst.IncDir, params.prgVersion)

	if !params.inst.Reinstall {
		if IsRegularFile(destBinPath) || IsDir(destIncPath) {
			return fmt.Errorf("%w%s or %s already exists",
				errInstallationPathOrAlreadyExists, destBinPath, destIncPath)
		}

		return nil
	}

	if IsRegularFile(destBinPath) {
		log.Infof("%s version of %q already exists, removing...",
			params.prgVersion, params.inst.Program)

		err := os.RemoveAll(destBinPath)
		if err != nil {
			_, _ = fmt.Fprintf(params.logFile, "Error removing binary: %v\n", err)
			return fmt.Errorf("failed to remove binary %s: %w", destBinPath, err)
		}
	}

	if IsDir(destIncPath) {
		log.Infof("Include directory for %s version already exists, removing...",
			params.prgVersion)

		err := os.RemoveAll(destIncPath)
		if err != nil {
			_, _ = fmt.Fprintf(params.logFile, "Error removing include dir: %v\n", err)

			return fmt.Errorf("failed to remove include directory %s: %w",
				destIncPath, err)
		}
	}

	log.Debugf("Existing files removed to reinstall version %q for program %q",
		params.prgVersion, params.inst.Program)

	return nil
}

// copyNewArtifacts locates the binary and include files in the unpacked directory
// and copies them to the final destination.
func copyNewArtifacts(params *bundleParams) error {
	srcBinPath, srcIncPath, err := findBundlePathsInDir(params.tmpDir, params.inst.Program)
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error finding artifacts: %v\n", err)
		return fmt.Errorf("failed to locate artifacts after extraction: %w", err)
	}

	err = prepareForReinstall(params)
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error preparing for reinstall: %v\n", err)
		return fmt.Errorf("failed to prepare for reinstall: %w", err)
	}

	err = copyBuildedTarantool(
		srcBinPath,
		srcIncPath,
		params.opts.Env.BinDir,
		params.inst.IncDir,
		params.prgVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to copy artifacts: %w", err)
	}

	log.Debugf("Artifacts copied successfully.")

	return nil
}

// changeActiveBundleVersion changes symlinks to the specified bundle executable version.
func changeActiveBundleVersion(params *bundleParams) error {
	execPath := filepath.Join(params.opts.Env.BinDir, params.inst.Program.Exec())

	err := CreateSymlink(params.prgVersion, execPath, true)
	if err != nil {
		return err
	}

	if IsDir(filepath.Join(params.inst.IncDir, params.prgVersion)) {
		incPath := filepath.Join(params.inst.IncDir, params.inst.Program.Exec())
		return CreateSymlink(params.prgVersion, incPath, true)
	}

	return nil
}

// updateSymlinks updates the default symlinks to point to the newly installed version.
// Uses the existing changeActiveTarantoolVersion function.
func updateSymlinks(params *bundleParams) error {
	log.Infof("Updating symlinks to point to %s...", params.prgVersion)

	err := changeActiveBundleVersion(params)
	if err != nil {
		log.Errorf("Failed to update symlinks: %v", err)

		return fmt.Errorf("failed to update symlinks: %w", err)
	}

	log.Infof("Symlinks updated successfully.")
	// Log the final symlink paths for clarity.
	log.Infof("Active version set by symlinks: %q and %q",
		filepath.Join(params.opts.Env.BinDir, params.inst.Program.Exec()),
		filepath.Join(params.inst.IncDir, params.inst.Program.Exec()))

	return nil
}

// performInitialChecks performs initial validation before starting the installation.
func performInitialChecks(params *bundleParams) error {
	if params.inst.version == "" {
		return fmt.Errorf("%w%s", errASpecificVersionMustBeProvidedToInstall, params.inst.Program)
	}

	err := checkInstallDirs(params.opts.Env.BinDir, params.inst.IncDir)
	if err != nil {
		return err
	}

	log.Infof("Requested version: %s", params.inst.version)

	return nil
}

// acquireBundleInfoToInstall finds local candidates and fetches bundle information
// using the search package.
func acquireBundleInfoToInstall(params *bundleParams) error {
	var (
		bundles BundleInfoSlice
		err     error
	)

	log.Infof("Search for the requested %q version...", params.inst.version)

	if params.inst.Local {
		bundles, err = FindLocalBundles(params.inst.Program,
			os.DirFS(params.opts.Repo.Install))
		if err != nil {
			return err
		}
	} else {
		searchCtx := NewSearchCtx(NewPlatformInformer(), NewTntIoDoer())

		searchCtx.Program = params.inst.Program
		searchCtx.Filter = SearchAll
		searchCtx.Package = GetAPIPackage(params.inst.Program)
		searchCtx.DevBuilds = params.inst.DevBuild

		bundles, err = FetchBundlesInfo(&searchCtx, params.opts)
		if err != nil {
			return err
		}
	}

	params.bundleInfo, err = SelectVersion(bundles, params.inst.version)
	if err != nil {
		return err
	}

	params.prgVersion = params.inst.Program.String() + version.FsSeparator +
		params.bundleInfo.Version.Str
	log.Infof("Found bundle: %s", params.bundleInfo.Version.Tarball)
	log.Infof("Version: %s", params.bundleInfo.Version.Str)

	return nil
}

// executeBundleInstallation performs the core installation steps:
// dependency check, download/copy, unpack, copy artifacts.
// It manages temporary directories and log files.
func executeBundleInstallation(params *bundleParams) (string, error) {
	err := prepareTemporaryDirs(params)
	if err != nil {
		return "", err
	}

	logFilePath := params.logFile.Name()

	if !params.inst.KeepTemp {
		defer func() {
			_ = params.logFile.Close()
			_ = os.Remove(params.logFile.Name())
			_ = os.RemoveAll(params.tmpDir)
		}()
	}

	defer func() {
		// Note: capture the error, if any, and dump the saved log on the screen.
		if err != nil {
			log.Errorf("Installation failed: %v", err)
			log.Infof("See log for details: %s", logFilePath)

			_ = printLog(logFilePath) // Attempt to print log content.
		}
	}()

	err = executeBundleInstallationSteps(params)

	return logFilePath, err
}

// executeBundleInstallationSteps installs the bundle using the prepared temporary files.
func executeBundleInstallationSteps(params *bundleParams) error {
	log.Infof("Starting installation steps in %s...", params.tmpDir)

	_, _ = fmt.Fprintf(params.logFile, "Installation started for %s version %s\n",
		params.inst.Program, params.bundleInfo.Version.Str)

	err := checkDependencies(params.inst.Program, params.inst.Force)
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Dependency check failed: %v\n", err)
		return err
	}

	_, _ = fmt.Fprintf(params.logFile, "Dependency check passed.\n")

	err = obtainBundle(params)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(params.logFile, "Bundle obtained successfully.\n")

	bundlePath := filepath.Join(params.tmpDir, params.bundleInfo.Version.Tarball)

	err = unpackBundle(bundlePath, params.logFile)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(params.logFile, "Bundle unpacked successfully.\n")

	err = copyNewArtifacts(params)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(params.logFile, "Artifacts copied successfully.\n")

	log.Infof("Core installation steps completed successfully.")

	_, _ = fmt.Fprintf(params.logFile, "Core installation steps completed successfully.\n")

	return nil
}

// installBundleProgram orchestrates the installation process for programs distributed as bundles.
func installBundleProgram(installCtx *InstallCtx, cliOpts *config.CliOpts) error {
	// The remaining fields are filled in by the installation steps.
	var params bundleParams

	params.inst = installCtx
	params.opts = cliOpts

	err := performInitialChecks(&params)
	if err != nil {
		return err
	}

	err = acquireBundleInfoToInstall(&params)
	if err != nil {
		log.Errorf("Failed to find bundles to install: %v", err)

		return err
	}

	if !params.inst.Reinstall {
		log.Infof("Checking existing installation...")

		exists := checkExistingInstallation(&params)

		if exists {
			log.Infof("%s version %s already exists.", params.inst.Program, params.prgVersion)

			return updateSymlinks(&params)
		}

		log.Debugf("No existing installation found for %s.", params.prgVersion)
	}

	log.Infof("Installing %s=%s", params.inst.Program, params.bundleInfo.Version.Str)

	logFilePath, err := executeBundleInstallation(&params)
	if err != nil {
		return fmt.Errorf("%w%s)", errInstallationFailedDuringExecutionPhaseSeeLog, logFilePath)
	}

	err = updateSymlinks(&params)
	if err != nil {
		return errInstallationFailedDuringFinaleUpdateSymlinks
	}

	log.Infof("Successfully installed %s version %s",
		params.inst.Program, params.bundleInfo.Version.Str)
	log.Info("Done.")

	return nil
}
