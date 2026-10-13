package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

var (
	errCouldNotFindBinaryAt     = errors.New("could not find binary at ")
	errMissingBundleHeaders     = errors.New("could not find headers")
	errIncompleteBundle         = errors.New("cannot activate incomplete installation")
	errBundlePathExists         = errors.New("installation path already exists")
	errUnsupportedBundleProgram = errors.New("unsupported bundle program")

	errInstallationFailedDuringExecutionPhaseSeeLog = errors.New(
		"installation failed during execution phase (see log: ",
	)
	errInstallationFailedDuringFinaleUpdateSymlinks = errors.New(
		"installation failed during finale update symlinks",
	)
	errLocalBundleFileNotFound = errors.New("local bundle file not found: ")
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

	// artifactRoot is the resolved installation scope.
	artifactRoot string

	// Destination paths are resolved after selecting the bundle version.
	versionDir string
	binPath    string
	incPath    string

	// tmpDir is the temporary directory used for extraction.
	tmpDir string

	// logFile is the file where installation logs are written for debugging fails.
	logFile *os.File
}

// checkExistingInstallation checks if the specific version of the program is already installed.
// Returns true if the installation exists, false otherwise.
func checkExistingInstallation(params *bundleParams) bool {
	binExists := binary.IsRegularFile(params.binPath)

	if !params.inst.Program.IsTarantool() {
		log.Debugf("Checking existence: bin=%s (%t)", params.binPath, binExists)

		return binExists
	}

	incExists := binary.IsDir(params.incPath)

	log.Debugf("Checking existence: bin=%s (%t), inc=%s (%t)",
		params.binPath, binExists, params.incPath, incExists)

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
func checkDependencies(program binary.Program, force bool) error {
	if force {
		log.Debugf("Skipping dependency check due to --force flag.")

		return nil
	}

	return programDependenciesInstalled(program)
}

// copyBundle copies the bundle from a local cache.
func copyBundle(params *bundleParams) error {
	log.Infof("Checking local files...")

	bundleName := params.bundleInfo.Version.Tarball
	localBundlePath := filepath.Join(params.inst.LocalRepo, bundleName)

	if !binary.IsRegularFile(localBundlePath) {
		return fmt.Errorf("%w%s", errLocalBundleFileNotFound, localBundlePath)
	}

	log.Infof("Local files found, installing from %s...", bundleName)

	err := binary.CopyFilePreserve(localBundlePath, filepath.Join(params.tmpDir, bundleName))
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error copying local bundle: %v\n", err)
		return fmt.Errorf("failed to copy local bundle: %w", err)
	}

	return nil
}

// downloadBundle downloads the bundle from a remote source.
func downloadBundle(params *bundleParams) error {
	bundleName := params.bundleInfo.Version.Tarball

	searchCtx := search.NewSearchCtx(
		search.NewPlatformInformer(),
		NewTntIoDownloader(params.bundleInfo.Token),
	)

	searchCtx.Program = params.inst.Program
	searchCtx.DevBuilds = params.inst.DevBuild
	searchCtx.ReleaseVersion = params.bundleInfo.Release

	bundleSource, err := search.TntIoMakePkgURI(&searchCtx, bundleName)
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
	if params.inst.LocalRepo != "" {
		return copyBundle(params)
	}

	return downloadBundle(params)
}

// unpackBundle extracts the contents of the bundle archive.
func unpackBundle(bundlePath string, logFile io.Writer) error {
	log.Infof("Unpacking archive %s...", filepath.Base(bundlePath))

	err := binary.ExtractTar(bundlePath)
	if err != nil {
		_, _ = fmt.Fprintf(logFile, "Error unpacking bundle: %v\n", err)
		return fmt.Errorf("failed to extract bundle %s: %w", filepath.Base(bundlePath), err)
	}

	log.Debugf("Bundle %s unpacked successfully.", filepath.Base(bundlePath))

	return nil
}

// findBundlePathsInDir makes path to binary and include directory.
func findBundlePathsInDir(baseDir string, program binary.Program) (
	string, string, error,
) {
	var incPath string

	switch program {
	case binary.ProgramEe:
		baseDir = filepath.Join(baseDir, "tarantool-enterprise")
		incPath = filepath.Join(baseDir, "include", program.Exec())
	case binary.ProgramTcm:
		// TCM archives contain only the executable at the archive root.
	default:
		return "", "", fmt.Errorf("%w: %s", errUnsupportedBundleProgram, program)
	}

	binPath := filepath.Join(baseDir, program.Exec())
	if !binary.IsRegularFile(binPath) {
		return "", "", fmt.Errorf("%w%q", errCouldNotFindBinaryAt, binPath)
	}

	if incPath != "" && !binary.IsDir(incPath) {
		return "", "", fmt.Errorf("%w at %q", errMissingBundleHeaders, incPath)
	}

	return binPath, incPath, nil
}

// prepareForReinstall removes only the selected version after bundle validation.
func prepareForReinstall(params *bundleParams) error {
	_, err := os.Lstat(params.versionDir)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to inspect version directory: %w", err)
	}

	if !params.inst.Reinstall {
		return fmt.Errorf("%w: %s; use --reinstall to replace an incomplete installation",
			errBundlePathExists, params.versionDir)
	}

	err = os.RemoveAll(params.versionDir)
	if err != nil {
		return fmt.Errorf("failed to remove version directory %s: %w", params.versionDir, err)
	}

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

	switch params.inst.Program {
	case binary.ProgramEe:
		err = copyBuiltTarantool(srcBinPath, srcIncPath, params.binPath, params.incPath)
	case binary.ProgramTcm:
		err = copyExecutable(srcBinPath, params.binPath)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedBundleProgram, params.inst.Program)
	}

	if err != nil {
		return fmt.Errorf("failed to copy artifacts: %w", err)
	}

	log.Debugf("Artifacts copied successfully.")

	return nil
}

// changeActiveBundleVersion changes symlinks to the specified bundle executable version.
func changeActiveBundleVersion(params *bundleParams) error {
	if !checkExistingInstallation(params) {
		return fmt.Errorf("%w %s", errIncompleteBundle, params.prgVersion)
	}

	switch params.inst.Program {
	case binary.ProgramEe:
		return changeActiveTarantoolVersion(params.binPath, params.incPath, params.artifactRoot)
	case binary.ProgramTcm:
		return activateBundleExecutable(params.binPath, params.artifactRoot,
			params.inst.Program.Exec())
	default:
		return fmt.Errorf("%w: %s", errUnsupportedBundleProgram, params.inst.Program)
	}
}

// activateBundleExecutable creates the active executable link for a validated installation.
func activateBundleExecutable(binPath, artifactRoot, executable string) error {
	binDir := filepath.Join(artifactRoot, binary.BinDirName)

	err := os.MkdirAll(binDir, defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("failed to create binary directory: %w", err)
	}

	return binary.CreateSymlink(binPath, filepath.Join(binDir, executable), true)
}

// updateSymlinks updates the default symlinks to point to the newly installed version.
func updateSymlinks(params *bundleParams) error {
	log.Infof("Updating symlinks to point to %s...", params.prgVersion)

	err := changeActiveBundleVersion(params)
	if err != nil {
		log.Errorf("Failed to update symlinks: %v", err)

		return fmt.Errorf("failed to update symlinks: %w", err)
	}

	log.Infof("Symlinks updated successfully.")
	log.Infof("Add %q to PATH to use this as default.",
		filepath.Join(params.artifactRoot, binary.BinDirName))
	log.Infof("Active executable: %q",
		filepath.Join(params.artifactRoot, binary.BinDirName, params.inst.Program.Exec()))

	if params.inst.Program.IsTarantool() {
		log.Infof("Active headers: %q",
			filepath.Join(params.artifactRoot, binary.IncDirName, params.inst.Program.Exec()))
	}

	return nil
}

// acquireBundleInfoToInstall finds local candidates and fetches bundle information
// using the search package.
func acquireBundleInfoToInstall(params *bundleParams) error {
	if params.inst.requestedVersion == "" {
		log.Infof("Search for the latest stable %s version...", params.inst.Program)
	} else {
		log.Infof("Search for the requested %q version...", params.inst.requestedVersion)
	}

	bundles, err := fetchInstallBundles(params)
	if err != nil {
		return err
	}

	params.bundleInfo, err = selectBundleVersion(bundles,
		params.inst.requestedVersion, params.inst.Program)
	if err != nil {
		return err
	}

	params.prgVersion = params.inst.Program.String() + version.FsSeparator +
		params.bundleInfo.Version.Str
	log.Infof("Found bundle: %s", params.bundleInfo.Version.Tarball)
	log.Infof("Version: %s", params.bundleInfo.Version.Str)

	return nil
}

// selectBundleVersion resolves empty or numeric requests to the latest stable EE or TCM build.
// EE defaults to gc64. Other requests retain exact matching of complete identifiers.
// Bundles must be sorted in ascending version order.
func selectBundleVersion(bundles search.BundleInfoSlice, requested string, program binary.Program) (
	search.BundleInfo, error,
) {
	if (program != binary.ProgramEe && program != binary.ProgramTcm) ||
		(requested != "" && !version.IsNumericVersion(requested)) {
		return search.SelectVersion(bundles, requested)
	}

	versions := make([]version.Version, 0, len(bundles))

	for _, bundle := range bundles {
		if bundle.Version.Release.Type != version.TypeRelease {
			continue
		}

		if program == binary.ProgramEe && bundle.Version.BuildName != "gc64" {
			continue
		}

		versions = append(versions, bundle.Version)
	}

	matched, err := version.MatchVersion(requested, versions)
	if err != nil {
		return search.BundleInfo{}, err
	}

	return search.SelectVersion(bundles, matched)
}

// fetchInstallBundles obtains candidates without selecting the requested version.
func fetchInstallBundles(params *bundleParams) (search.BundleInfoSlice, error) {
	if params.inst.LocalRepo != "" {
		return search.FindLocalBundles(params.inst.Program, os.DirFS(params.inst.LocalRepo))
	}

	searchCtx := search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())

	searchCtx.BinaryContext = params.inst.BinaryContext
	searchCtx.CredentialsFile = params.inst.CredentialsFile
	searchCtx.Filter = search.SearchAll
	searchCtx.Package = search.GetAPIPackage(params.inst.Program)
	searchCtx.DevBuilds = params.inst.DevBuild

	return search.FetchBundlesInfo(&searchCtx)
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

	defer func() { finishBundleInstallation(params, err) }()

	err = executeBundleInstallationSteps(params)

	return logFilePath, err
}

// finishBundleInstallation reports failures before closing the log and applying retention policy.
func finishBundleInstallation(params *bundleParams, installErr error) {
	if installErr != nil {
		log.Errorf("Installation failed: %v", installErr)
		log.Infof("See log for details: %s", params.logFile.Name())

		_ = printLog(params.logFile.Name())
	}

	_ = params.logFile.Close()
	if params.inst.KeepTemp {
		return
	}

	_ = os.RemoveAll(params.tmpDir)

	if installErr != nil {
		return
	}

	_ = os.Remove(params.logFile.Name())
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

// installBundleProgram orchestrates the installation of EE and TCM bundles.
func installBundleProgram(installCtx InstallCtx) error {
	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	// The remaining fields are filled in by the installation steps.
	var params bundleParams

	params.inst = &installCtx
	params.artifactRoot = artifactRoot

	err = acquireBundleInfoToInstall(&params)
	if err != nil {
		log.Errorf("Failed to find bundles to install: %v", err)

		return err
	}

	params.versionDir = filepath.Join(artifactRoot, binary.VersionsDirName,
		installCtx.Program.String(), params.prgVersion)
	params.binPath = filepath.Join(params.versionDir, installCtx.Program.Exec())
	params.incPath = filepath.Join(params.versionDir, binary.IncDirName, installCtx.Program.Exec())

	existing, err := prepareExistingBundle(&params)
	if err != nil {
		return err
	}

	if existing {
		return nil
	}

	log.Infof("Installing %s=%s", params.inst.Program, params.bundleInfo.Version.Str)

	logFilePath, err := executeBundleInstallation(&params)
	if err != nil {
		return fmt.Errorf("%w%s): %w",
			errInstallationFailedDuringExecutionPhaseSeeLog, logFilePath, err)
	}

	err = updateSymlinks(&params)
	if err != nil {
		return fmt.Errorf("%w: %w", errInstallationFailedDuringFinaleUpdateSymlinks, err)
	}

	log.Infof("Successfully installed %s version %s",
		params.inst.Program, params.bundleInfo.Version.Str)
	log.Info("Done.")

	return nil
}

// prepareExistingBundle reactivates a complete version unless replacement was requested.
func prepareExistingBundle(params *bundleParams) (bool, error) {
	if params.inst.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing installation...")

	if !checkExistingInstallation(params) {
		log.Debugf("No existing installation found for %s.", params.prgVersion)

		return false, nil
	}

	log.Infof("%s version %s already exists.", params.inst.Program, params.prgVersion)

	err := updateSymlinks(params)
	if err != nil {
		return false, err
	}

	return true, nil
}
