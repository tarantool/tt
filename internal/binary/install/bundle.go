package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/customerzone"
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
	errNoBundles           = errors.New("no packages found for this OS or release version")
	errNoAvailableVersions = errors.New("no available versions")
	errVersionNotFound     = errors.New(" version doesn't found")
)

// bundleParams holds the parameters required for the bundle installation process.
type bundleParams struct {
	// bundleInfo is the build to install and the source of its archive.
	// Its full SDK version is preserved when the installed product uses a shorter version.
	bundleInfo binary.Bundle

	// inst is the install context for the installation process.
	inst *InstallCtx

	// artifactRoot is the resolved installation scope.
	artifactRoot string

	// tmpDir is the temporary directory used for extraction.
	tmpDir string

	// logFile is the file where installation logs are written for debugging fails.
	logFile *os.File
}

// selection keeps the installed identity independent of the archive used to obtain it.
func (params *bundleParams) selection() binary.Selection {
	return binary.Selection{
		Program: params.inst.Program,
		ID: binary.BundleInstallationID(params.inst.Program,
			params.bundleInfo.Version, params.inst.DevBuild),
	}
}

// validateBundleInstallation checks the selected installation once. For SDKs it
// also returns the decoded ownership record for the following activation step.
func validateBundleInstallation(params *bundleParams) (binary.SDKInstallation, error) {
	var sdk binary.SDKInstallation

	if params.inst.Program == binary.ProgramSDK {
		selected := params.selection().ID

		sdk, err := binary.ReadSDK(params.artifactRoot, selected)
		if err != nil {
			return sdk, err
		}

		err = binary.ValidateSDKManifest(sdk, selected)
		if err != nil {
			return sdk, err
		}

		err = binary.ValidateSDKFiles(params.artifactRoot, sdk)
		if err != nil {
			return sdk, err
		}

		return sdk, nil
	}

	err := binary.ValidateInstallation(params.artifactRoot, params.selection())
	if err != nil {
		return sdk, err
	}

	return sdk, nil
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

// obtainBundle writes the archive of the build into the temporary directory,
// from the source that listed it: the customer zone, a local directory.
func obtainBundle(params *bundleParams) error {
	found := params.bundleInfo

	log.Infof("Obtaining %s from %s...", found.Artifact.Name, found.Source.Name())

	err := fetchArtifact(context.Background(), found.Source, found.Artifact,
		filepath.Join(params.tmpDir, found.Version.Tarball))
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error obtaining bundle: %v\n", err)

		return fmt.Errorf("failed to obtain bundle from %s: %w", found.Source.Name(), err)
	}

	return nil
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
	case binary.ProgramTcm, binary.ProgramTtEe:
		// Standalone archives contain their executable at the archive root.
	default:
		return "", "", fmt.Errorf("%w: %s", errUnsupportedBundleProgram, program)
	}

	binPath := filepath.Join(baseDir, program.Exec())
	if program == binary.ProgramTcm && !binary.IsRegularFile(binPath) {
		// Older TCM development bundles use the full product name.
		legacyPath := filepath.Join(baseDir, "tarantool-cluster-manager")
		if binary.IsRegularFile(legacyPath) {
			binPath = legacyPath
		}
	}

	if !binary.IsRegularFile(binPath) {
		return "", "", fmt.Errorf("%w%q", errCouldNotFindBinaryAt, binPath)
	}

	if incPath != "" && !binary.IsDir(incPath) {
		return "", "", fmt.Errorf("%w at %q", errMissingBundleHeaders, incPath)
	}

	return binPath, incPath, nil
}

// produceBundle validates the archive layout before placing any files.
func produceBundle(directory string, program binary.Program) ([]programArtifact, error) {
	source := program.BundleSource()
	switch {
	case source == binary.ProgramSDK:
		artifacts, err := produceSDK(directory, program)

		return artifacts, err
	case source.IsStandaloneArchive():
		executable, _, err := findBundlePathsInDir(directory, program)
		if err != nil {
			return nil, err
		}

		var artifact programArtifact

		artifact.program, artifact.executable = program, executable

		return []programArtifact{artifact}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedBundleProgram, program)
	}
}

// produceSDK prepares the Enterprise SDK once and selects the requested products.
// Installing an individual product selects a subset of the same prepared artifacts.
func produceSDK(directory string, requested binary.Program) ([]programArtifact, error) {
	products := []binary.Program{requested}
	if requested == binary.ProgramSDK {
		products = binary.SDKProducts()
	}

	artifacts := make([]programArtifact, 0, len(products))
	root := filepath.Join(directory, "tarantool-enterprise")

	for _, program := range products {
		var artifact programArtifact

		artifact.program = program
		artifact.executable = filepath.Join(root, program.Exec())

		if program == binary.ProgramEe {
			executable, headers, err := findBundlePathsInDir(directory, program)
			if err != nil {
				return nil, err
			}

			artifact.executable, artifact.headers = executable, headers
		} else if !binary.IsRegularFile(artifact.executable) {
			return nil, fmt.Errorf("%w%q", errCouldNotFindBinaryAt, artifact.executable)
		}

		artifacts = append(artifacts, artifact)
	}

	return artifacts, nil
}

// prepareBundlePlacements resolves ownership and destinations once, before the
// store stages any files. The manifest records the artifacts actually prepared.
func prepareBundlePlacements(params *bundleParams, artifacts []programArtifact) []bundlePlacement {
	selected := params.selection()

	var sdk binary.SDKInstallation

	selections := []binary.Selection{selected}
	if selected.Program == binary.ProgramSDK {
		sdk.Version = selected.ID
		for _, artifact := range artifacts {
			sdk.Products = append(sdk.Products, artifact.program)
		}

		selections = sdk.Selections()
	}

	placements := make([]bundlePlacement, 0, len(artifacts)+1)
	for index, artifact := range artifacts {
		selection := selections[index]

		placements = append(placements, bundlePlacement{
			destination: binary.InstallationPath(params.artifactRoot,
				selection.Program, selection.ID),
			prepare: func(directory string) error {
				err := copyProgramArtifact(directory, artifact)

				return err
			},
		})
	}

	if selected.Program == binary.ProgramSDK {
		placements = append(placements, bundlePlacement{
			destination: binary.InstallationPath(params.artifactRoot,
				selected.Program, selected.ID),
			prepare: func(directory string) error {
				err := binary.WriteSDK(directory, sdk)

				return err
			},
		})
	}

	return placements
}

// copyNewArtifacts prepares and places the selected products from the unpacked archive.
func copyNewArtifacts(params *bundleParams) error {
	artifacts, err := produceBundle(params.tmpDir, params.inst.Program)
	if err != nil {
		_, _ = fmt.Fprintf(params.logFile, "Error finding artifacts: %v\n", err)
		return fmt.Errorf("failed to locate artifacts after extraction: %w", err)
	}

	placements := prepareBundlePlacements(params, artifacts)

	err = placeBundleArtifacts(placements, params.inst.Reinstall)
	if err != nil {
		return fmt.Errorf("failed to copy artifacts: %w", err)
	}

	log.Debugf("Artifacts copied successfully.")

	return nil
}

// updateSymlinks validates the newly installed version before activating it.
func updateSymlinks(params *bundleParams) error {
	sdk, err := validateBundleInstallation(params)
	if err != nil {
		return fmt.Errorf("%w %s: %w", errIncompleteBundle, params.bundleInfo.Version.Str, err)
	}

	err = activateBundle(params, sdk)
	if err != nil {
		return err
	}

	return nil
}

// activateBundle applies a selection already checked by validateBundleInstallation.
func activateBundle(params *bundleParams, sdk binary.SDKInstallation) error {
	log.Debugf("Updating symlinks to point to %s...", params.selection().ID)

	var err error

	if params.inst.Program == binary.ProgramSDK {
		err = binary.ActivateSDK(params.artifactRoot, sdk)
	} else {
		err = binary.ActivateSelections(params.artifactRoot,
			[]binary.Selection{params.selection()})
	}

	if err != nil {
		log.Errorf("Failed to update symlinks: %v", err)

		return fmt.Errorf("failed to update symlinks: %w", err)
	}

	log.Infof("Symlinks updated successfully.")

	products := []binary.Program{params.inst.Program}
	if params.inst.Program == binary.ProgramSDK {
		products = sdk.Products
	}

	for _, product := range products {
		log.Infof("Active executable: %q",
			filepath.Join(params.artifactRoot, binary.BinDirName, product.Exec()))

		if product.IsTarantool() {
			log.Infof("Active headers: %q",
				filepath.Join(params.artifactRoot, binary.IncDirName, product.Exec()))
		}
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

	log.Infof("Found bundle: %s", params.bundleInfo.Version.Tarball)
	log.Infof("Version: %s", params.bundleInfo.Version.Str)

	return nil
}

// selectBundleVersion resolves empty or numeric requests using the archive source.
// SDK archives default to gc64. Standalone EE also accepts its installed identities.
// Other requests match complete archive identifiers exactly.
// Bundles must be sorted in ascending version order.
func selectBundleVersion(bundles binary.Bundles, requested string, program binary.Program) (
	binary.Bundle, error,
) {
	if program == binary.ProgramSDK {
		requested = strings.TrimPrefix(requested, binary.SDKVersionPrefix)
	}

	source := program.BundleSource()
	if (source == binary.ProgramSDK || source.IsStandaloneArchive()) &&
		(requested == "" || version.IsNumericVersion(requested)) {
		versions := make([]version.Version, 0, len(bundles))

		for _, bundle := range bundles {
			if bundle.Version.Release.Type != version.TypeRelease {
				continue
			}

			if source == binary.ProgramSDK && bundle.Version.BuildName != "gc64" {
				continue
			}

			versions = append(versions, bundle.Version)
		}

		matched, err := version.MatchVersion(requested, versions)
		if err != nil {
			return binary.Bundle{}, err
		}

		requested = matched
	}

	if program == binary.ProgramEe {
		for _, bundle := range slices.Backward(bundles) {
			if binary.BundleInstallationID(program, bundle.Version, false) == requested {
				return bundle, nil
			}
		}
	}

	return selectBundle(bundles, requested)
}

// selectBundle selects the build named ver exactly, or the latest one for an
// empty ver.
func selectBundle(bundles binary.Bundles, ver string) (binary.Bundle, error) {
	if len(bundles) == 0 {
		return binary.Bundle{}, errNoAvailableVersions
	}

	if ver == "" {
		return bundles[len(bundles)-1], nil
	}

	for _, found := range bundles {
		if found.Version.Str == ver {
			return found, nil
		}
	}

	return binary.Bundle{}, fmt.Errorf("%q%w", ver, errVersionNotFound)
}

// fetchInstallBundles obtains candidates without selecting the requested version:
// from the directory of --local-dir, or from the source of this build of tt
// with the archives of the product.
func fetchInstallBundles(params *bundleParams) (binary.Bundles, error) {
	program := params.inst.Program
	query := binary.NewBundleQuery(program, params.inst.DevBuild, "")

	if params.inst.LocalRepo != "" {
		bundles, err := binary.ListBundles(context.Background(),
			customerzone.NewDir(params.inst.LocalRepo), program, query)

		return bundles, err
	}

	source, err := binary.BundleSource(program)
	if err != nil {
		return nil, err
	}

	bundles, err := binary.ListBundles(context.Background(), source, program, query)
	if err != nil {
		return nil, err
	}

	if len(bundles) == 0 {
		return nil, errNoBundles
	}

	return bundles, nil
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
		log.Infof("Artifacts can be found at: %s", params.tmpDir)

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

// prepareBundleProgram installs SDK and standalone products without activating them.
func prepareBundleProgram(installCtx InstallCtx) (installationActivation, error) {
	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return nil, err
	}

	var params bundleParams

	params.inst = &installCtx
	params.artifactRoot = artifactRoot

	err = acquireBundleInfoToInstall(&params)
	if err != nil {
		log.Errorf("Failed to find bundles to install: %v", err)

		return nil, err
	}

	if prepareExistingBundle(&params) {
		return func() error { return updateSymlinks(&params) }, nil
	}

	log.Infof("Installing %s=%s", params.inst.Program, params.selection().ID)

	logFilePath, err := executeBundleInstallation(&params)
	if err != nil {
		return nil, fmt.Errorf("%w%s): %w",
			errInstallationFailedDuringExecutionPhaseSeeLog, logFilePath, err)
	}

	return func() error {
		err := updateSymlinks(&params)
		if err != nil {
			return fmt.Errorf("%w: %w", errInstallationFailedDuringFinaleUpdateSymlinks, err)
		}

		log.Infof("Successfully installed %s version %s",
			params.inst.Program, params.selection().ID)
		log.Info("Done.")

		return nil
	}, nil
}

// prepareExistingBundle checks for a complete version without changing active links.
func prepareExistingBundle(params *bundleParams) bool {
	if params.inst.Reinstall {
		return false
	}

	log.Infof("Checking existing installation...")

	_, err := validateBundleInstallation(params)
	if err != nil {
		log.Debugf("No complete installation found for %s: %v", params.selection().ID, err)

		return false
	}

	log.Infof("%s version %s already exists.", params.inst.Program, params.selection().ID)

	return true
}
