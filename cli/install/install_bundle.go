package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// bundleParams holds the parameters required for the bundle installation process.
type bundleParams struct {
	// bundleInfo is the structure that holds information about the bundle.
	// It is filled in during the installation process in getBundleInfoForInstall.
	bundleInfo search.BundleInfo

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
		return fmt.Errorf("bin_dir is not set, check %s", configure.ConfigName)
	}

	if includeDir == "" {
		// For bundle installs, includeDir is usually required.
		return fmt.Errorf("include_dir is not set, check %s", configure.ConfigName)
	}

	// Reuse the writability checks from the main Install function.
	for _, dir := range []string{binDir, includeDir} {
		if _, err := os.Stat(dir); os.IsNotExist(err) && subDirIsWritable(dir) {
			continue // Directory doesn't exist but can be created.
		}

		if !dirIsWritable(dir) {
			return fmt.Errorf("the directory %s is not writeable for the current user", dir)
		}
	}
	return nil
}

// checkExistingInstallation checks if the specific version of the program is already installed.
// Returns true if the installation exists, false otherwise.
func checkExistingInstallation(versionStr, binDir, includeDir string) (bool, error) {
	binPath := filepath.Join(binDir, versionStr)
	incPath := filepath.Join(includeDir, versionStr)

	binExists := util.IsRegularFile(binPath)
	incExists := util.IsDir(incPath)

	log.Debugf("Checking existence: bin=%s (%t), inc=%s (%t)",
		binPath, binExists, incPath, incExists)
	return binExists && incExists, nil
}

// prepareTemporaryDirs creates temporary directories for installation and logging.
func prepareTemporaryDirs(bp *bundleParams) error {
	var err error

	bp.tmpDir, err = os.MkdirTemp("", bp.inst.Program.String()+"_install_*")
	if err != nil {
		return fmt.Errorf("failed to create temporary install directory: %w", err)
	}
	os.Chmod(bp.tmpDir, defaultDirPermissions)

	bp.logFile, err = os.CreateTemp("", bp.inst.Program.String()+"_install_log_*")
	if err != nil {
		os.RemoveAll(bp.tmpDir)
		return fmt.Errorf("failed to create temporary log file: %w", err)
	}

	log.Debugf("Created temporary install directory: %s", bp.tmpDir)
	log.Debugf("Created temporary log file: %s", bp.logFile.Name())
	return nil
}

// checkDependencies checks if the required system dependencies are installed.
func checkDependencies(program search.ProgramType, force bool) error {
	if force {
		log.Debugf("Skipping dependency check due to --force flag.")
		return nil
	}

	if err := programDependenciesInstalled(program); err != nil {
		return err
	}
	return nil
}

// copyBundle copies the bundle from a local cache.
func copyBundle(bp *bundleParams) error {
	distfiles := bp.opts.Repo.Install
	if distfiles == "" {
		return fmt.Errorf("cannot install from local repository: " +
			"distribution files directory (repo.install) is not set")
	}

	log.Infof("Checking local files...")
	bundleName := bp.bundleInfo.Version.Tarball
	localBundlePath := filepath.Join(distfiles, bundleName)
	if !util.IsRegularFile(localBundlePath) {
		return fmt.Errorf("local bundle file not found: %s", localBundlePath)
	}

	log.Infof("Local files found, installing from %s...", bundleName)
	err := util.CopyFilePreserve(localBundlePath, filepath.Join(bp.tmpDir, bundleName))
	if err != nil {
		fmt.Fprintf(bp.logFile, "Error copying local bundle: %v\n", err)
		return fmt.Errorf("failed to copy local bundle: %w", err)
	}
	return nil
}

// downloadBundle downloads the bundle from a remote source.
func downloadBundle(bp *bundleParams) error {
	bundleName := bp.bundleInfo.Version.Tarball

	// FIXME: Call with [search.SearchCtx] and the correct [search.PlatformInformer] is provided.
	bundleSource, err := search.TntIoMakePkgURI(
		search.GetApiPackage(bp.inst.Program),
		bp.bundleInfo.Release,
		bundleName,
		bp.inst.DevBuild,
	)
	if err != nil {
		return fmt.Errorf("failed to construct bundle download URI: %w", err)
	}

	log.Infof("Downloading %s... (%s)", bp.inst.Program, bundleSource)
	err = install_ee.DownloadBundle(
		bp.opts, bundleName, bundleSource, bp.bundleInfo.Token, bp.tmpDir)
	if err != nil {
		fmt.Fprintf(bp.logFile, "Error downloading bundle: %v\n", err)
		return fmt.Errorf("failed to download bundle: %w", err)
	}
	return nil
}

// obtainBundle downloads the bundle from a remote source or copies it from the local cache.
func obtainBundle(bp *bundleParams) error {
	if bp.inst.Local {
		return copyBundle(bp)
	}
	return downloadBundle(bp)
}

// unpackBundle extracts the contents of the bundle archive.
func unpackBundle(bundlePath string, logFile io.Writer) error {
	log.Infof("Unpacking archive %s...", filepath.Base(bundlePath))
	err := util.ExtractTar(bundlePath)
	if err != nil {
		fmt.Fprintf(logFile, "Error unpacking bundle: %v\n", err)
		return fmt.Errorf("failed to extract bundle %s: %w", filepath.Base(bundlePath), err)
	}

	log.Debugf("Bundle %s unpacked successfully.", filepath.Base(bundlePath))
	return nil
}

// getSubDirForProgram returns the subdirectory inside archive for the specified program type.
func getSubDirForProgram(program search.ProgramType) string {
	switch program {
	case search.ProgramEe:
		return "tarantool-enterprise"
	case search.ProgramTcm:
		return "" // The TCM bundle is flat (no subdirectories).
	default:
		return ""
	}
}

// findBundlePathsInDir makes path to binary and include directory.
func findBundlePathsInDir(baseDir string, program search.ProgramType) (
	string, string, error,
) {
	subDir := getSubDirForProgram(program)

	binPath := filepath.Join(baseDir, subDir, program.Exec())
	if !util.IsRegularFile(binPath) {
		return "", "", fmt.Errorf("could not find binary at %q", binPath)
	}

	incPath := filepath.Join(baseDir, subDir, "include", program.Exec())
	if !util.IsDir(incPath) {
		incPath = "" // No include directory found in bundle.
	}
	return binPath, incPath, nil
}

// prepareForReinstall remove existing destination directories/files if needed.
func prepareForReinstall(bp *bundleParams) error {
	destBinPath := filepath.Join(bp.opts.Env.BinDir, bp.prgVersion)
	destIncPath := filepath.Join(bp.inst.IncDir, bp.prgVersion)

	if bp.inst.Reinstall {
		if util.IsRegularFile(destBinPath) {
			log.Infof("%s version of %q already exists, removing...",
				bp.prgVersion, bp.inst.Program)

			if err := os.RemoveAll(destBinPath); err != nil {
				fmt.Fprintf(bp.logFile, "Error removing binary: %v\n", err)
				return fmt.Errorf("failed to remove binary %s: %w", destBinPath, err)
			}
		}

		if util.IsDir(destIncPath) {
			log.Infof("Include directory for %s version already exists, removing...",
				bp.prgVersion)
			if err := os.RemoveAll(destIncPath); err != nil {
				fmt.Fprintf(bp.logFile, "Error removing include dir: %v\n", err)
				return fmt.Errorf("failed to remove include directory %s: %w",
					destIncPath, err)
			}
		}

		log.Debugf("Existing files removed to reinstall version %q for program %q",
			bp.prgVersion, bp.inst.Program)

	} else {
		// If no Reinstall option, ensure that we don't have already installed version.
		if util.IsRegularFile(destBinPath) || util.IsDir(destIncPath) {
			return fmt.Errorf("installation path %s or %s already exists",
				destBinPath, destIncPath)
		}
	}

	return nil
}

// copyNewArtifacts locates the binary and include files in the unpacked directory
// and copies them to the final destination.
func copyNewArtifacts(bp *bundleParams) error {
	srcBinPath, srcIncPath, err := findBundlePathsInDir(bp.tmpDir, bp.inst.Program)
	if err != nil {
		fmt.Fprintf(bp.logFile, "Error finding artifacts: %v\n", err)
		return fmt.Errorf("failed to locate artifacts after extraction: %w", err)
	}

	err = prepareForReinstall(bp)
	if err != nil {
		fmt.Fprintf(bp.logFile, "Error preparing for reinstall: %v\n", err)
		return fmt.Errorf("failed to prepare for reinstall: %w", err)
	}

	err = copyBuildedTarantool(
		srcBinPath,
		srcIncPath,
		bp.opts.Env.BinDir,
		bp.inst.IncDir,
		bp.prgVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to copy artifacts: %w", err)
	}

	log.Debugf("Artifacts copied successfully.")
	return nil
}

// updateSymlinks updates the default symlinks to point to the newly installed version.
// Uses the existing changeActiveTarantoolVersion function.
func updateSymlinks(bp *bundleParams) error {
	log.Infof("Updating symlinks to point to %s...", bp.prgVersion)
	err := changeActiveTarantoolVersion(bp.prgVersion, bp.opts.Env.BinDir, bp.inst.IncDir)
	if err != nil {
		log.Errorf("Failed to update symlinks: %v", err)
		return fmt.Errorf("failed to update symlinks: %w", err)
	}

	log.Infof("Symlinks updated successfully.")
	// Log the final symlink paths for clarity
	log.Infof("Active version set by symlinks: %q and %q",
		filepath.Join(bp.opts.Env.BinDir, bp.inst.Program.Exec()),
		filepath.Join(bp.inst.IncDir, bp.inst.Program.Exec()))
	return nil
}

// performInitialChecks performs initial validation before starting the installation.
func performInitialChecks(bp *bundleParams) error {
	if bp.inst.version == "" {
		return fmt.Errorf("a specific version must be provided to install %s", bp.inst.Program)
	}

	if err := checkInstallDirs(bp.opts.Env.BinDir, bp.inst.IncDir); err != nil {
		return err
	}

	log.Infof("Requested version: %s", bp.inst.version)
	return nil
}

// acquireBundleInfoToInstall finds local candidates and fetches bundle information
// using the search package.
func acquireBundleInfoToInstall(bp *bundleParams) error {
	var bundles search.BundleInfoSlice
	var err error

	log.Infof("Search for the requested %q version...", bp.inst.version)
	if bp.inst.Local {
		bundles, err = search.FindLocalBundles(bp.inst.Program, os.DirFS(bp.opts.Repo.Install))
		if err != nil {
			return err
		}

	} else {
		searchCtx := search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())
		searchCtx.Program = bp.inst.Program
		searchCtx.Filter = search.SearchAll
		searchCtx.Package = search.GetApiPackage(bp.inst.Program)
		searchCtx.DevBuilds = bp.inst.DevBuild

		bundles, err = search.FetchBundlesInfo(&searchCtx, bp.opts)
		if err != nil {
			return err
		}
	}

	bp.bundleInfo, err = search.SelectVersion(bundles, bp.inst.version)
	if err != nil {
		return err
	}

	bp.prgVersion = bp.inst.Program.String() + version.FsSeparator + bp.bundleInfo.Version.Str
	log.Infof("Found bundle: %s", bp.bundleInfo.Version.Tarball)
	log.Infof("Version: %s", bp.bundleInfo.Version.Str)
	return nil
}

// executeBundleInstallation performs the core installation steps:
// dependency check, download/copy, unpack, copy artifacts.
// It manages temporary directories and log files.
func executeBundleInstallation(bp *bundleParams) (logFilePath string, errRet error) {
	err := prepareTemporaryDirs(bp)
	if err != nil {
		return "", err
	}
	logFilePath = bp.logFile.Name()

	if !bp.inst.KeepTemp {
		defer func() {
			bp.logFile.Close()
			os.Remove(bp.logFile.Name())
			os.RemoveAll(bp.tmpDir)
		}()
	}

	defer func() {
		// Note: capture the error, if any, and dump the saved log on the screen.
		if errRet != nil {
			log.Errorf("Installation failed: %v", errRet)
			log.Infof("See log for details: %s", logFilePath)
			printLog(logFilePath) // Attempt to print log content
		}
	}()

	log.Infof("Starting installation steps in %s...", bp.tmpDir)
	fmt.Fprintf(bp.logFile, "Installation started for %s version %s\n",
		bp.inst.Program, bp.bundleInfo.Version.Str)

	if err = checkDependencies(bp.inst.Program, bp.inst.Force); err != nil {
		fmt.Fprintf(bp.logFile, "Dependency check failed: %v\n", err)
		return logFilePath, err
	}
	fmt.Fprintf(bp.logFile, "Dependency check passed.\n")

	err = obtainBundle(bp)
	if err != nil {
		return logFilePath, err
	}
	fmt.Fprintf(bp.logFile, "Bundle obtained successfully.\n")
	bundlePath := filepath.Join(bp.tmpDir, bp.bundleInfo.Version.Tarball)

	if err = unpackBundle(bundlePath, bp.logFile); err != nil {
		return logFilePath, err
	}
	fmt.Fprintf(bp.logFile, "Bundle unpacked successfully.\n")

	err = copyNewArtifacts(bp)
	if err != nil {
		return logFilePath, err
	}
	fmt.Fprintf(bp.logFile, "Artifacts copied successfully.\n")

	log.Infof("Core installation steps completed successfully.")
	fmt.Fprintf(bp.logFile, "Core installation steps completed successfully.\n")
	return logFilePath, nil
}

// installBundleProgram orchestrates the installation process for programs distributed as bundles.
func installBundleProgram(installCtx *InstallCtx, cliOpts *config.CliOpts) error {
	bp := bundleParams{
		inst: installCtx,
		opts: cliOpts,
	}

	if err := performInitialChecks(&bp); err != nil {
		return err
	}

	err := acquireBundleInfoToInstall(&bp)
	if err != nil {
		log.Errorf("Failed to find bundles to install: %v", err)
		return err
	}

	if !bp.inst.Reinstall {
		log.Infof("Checking existing installation...")
		exists, err := checkExistingInstallation(bp.prgVersion,
			bp.opts.Env.BinDir, bp.inst.IncDir)
		if err != nil {
			return fmt.Errorf("failed to check existing installation: %w", err)
		}
		if exists {
			log.Infof("%s version %s already exists.", bp.inst.Program, bp.prgVersion)
			return updateSymlinks(&bp)
		}
		log.Debugf("No existing installation found for %s.", bp.prgVersion)
	}

	log.Infof("Installing %s=%s", bp.inst.Program, bp.bundleInfo.Version.Str)
	logFilePath, err := executeBundleInstallation(&bp)
	if err != nil {
		return fmt.Errorf("installation failed during execution phase (see log: %s)", logFilePath)
	}

	err = updateSymlinks(&bp)
	if err != nil {
		return fmt.Errorf("installation failed during finale update symlinks")
	}

	log.Infof("Successfully installed %s version %s", bp.inst.Program, bp.bundleInfo.Version.Str)
	log.Info("Done.")
	return nil
}
