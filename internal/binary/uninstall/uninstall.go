package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errHasNoInstalledVersion     = errors.New(" has no installed version")
	errMultipleInstalledVersions = errors.New(" has more than one installed version, ")
	errNotInstalled              = errors.New("not installed")
	errInvalidVersion            = errors.New("invalid version")
	errOutsideScope              = errors.New("directory points outside selected scope")
)

const (
	MajorMinorPatchRegexp = `^[0-9]+\.[0-9]+\.[0-9]+`
	productVersionTimeout = 5 * time.Second
)

// UninstallCtx contains the options for removing an installed program.
type UninstallCtx struct {
	binary.BinaryContext

	Version string
}

// validateProgram limits uninstall to products with scoped installations.
func validateProgram(program binary.Program) error {
	switch program {
	case binary.ProgramTt, binary.ProgramTtEe, binary.ProgramCe, binary.ProgramEe,
		binary.ProgramTcm, binary.ProgramSDK:
		return nil
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// remove deletes one installation and only the active links selecting its artifacts.
func remove(program binary.Program, programVersion, root string) (bool, error) {
	installation := binary.InstallationPath(root, program, programVersion)
	info, err := os.Lstat(installation)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("program is %w", errNotInstalled)
	case err != nil:
		return false, fmt.Errorf("failed to access %q: %w", installation, err)
	case !info.IsDir():
		return false, fmt.Errorf("program is %w", errNotInstalled)
	}

	removed, err := removeInstallationLinks(program, installation, root)
	if err != nil {
		return removed, err
	}

	err = os.RemoveAll(installation)
	if err != nil {
		return removed, fmt.Errorf("failed to remove %q: %w", installation, err)
	}

	return removed, nil
}

// removeInstallationLinks also cleans matching links to already missing SDK components.
func removeInstallationLinks(program binary.Program, installation, root string) (bool, error) {
	binLink := filepath.Join(root, binary.BinDirName, program.Exec())

	removed, err := removeActiveSymlink(binLink, filepath.Join(installation, program.Exec()))
	if err != nil {
		return false, err
	}

	switch program {
	case binary.ProgramCe, binary.ProgramEe:
		log.Infof("Removing headers...")

		incLink := filepath.Join(root, binary.IncDirName, program.Exec())
		incPath := filepath.Join(installation, binary.IncDirName, program.Exec())

		_, err = removeActiveSymlink(incLink, incPath)
		if err != nil {
			return removed, err
		}
	default:
		// TT and TCM have no active headers.
	}

	return removed, nil
}

// resolveArtifactPath resolves existing ancestors even if an incomplete artifact is absent.
func resolveArtifactPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to resolve %q: %w", path, err)
	}

	parent, err := resolveArtifactPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}

	return filepath.Join(parent, filepath.Base(path)), nil
}

// validateDirectories keeps directory traversal inside the resolved scope root.
func validateDirectories(root string, directories ...string) error {
	resolvedRoot, err := resolveArtifactPath(root)
	if err != nil {
		return err
	}

	for _, directory := range directories {
		resolved, err := resolveArtifactPath(directory)
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil {
			return fmt.Errorf("failed to locate directory %q within scope: %w", directory, err)
		}

		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: %q", errOutsideScope, directory)
		}
	}

	return nil
}

// removeActiveSymlink removes only a link selecting the requested artifact.
func removeActiveSymlink(linkPath, path string) (bool, error) {
	info, err := os.Lstat(linkPath)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to access %q: %w", linkPath, err)
	case info.Mode()&os.ModeSymlink == 0:
		return false, nil
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		return false, fmt.Errorf("failed to read symlink %q: %w", linkPath, err)
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}

	target, err = resolveArtifactPath(target)
	if err != nil {
		return false, err
	}

	installedPath, err := resolveArtifactPath(path)
	if err != nil {
		return false, err
	}

	if target != installedPath {
		return false, nil
	}

	err = os.Remove(linkPath)
	if err != nil {
		return false, fmt.Errorf("failed to remove symlink %q: %w", linkPath, err)
	}

	return true, nil
}

// UninstallProgram removes one installation within the selected scope.
func UninstallProgram(uninstallCtx UninstallCtx) error {
	program, programVersion := uninstallCtx.Program, uninstallCtx.Version

	err := validateProgram(program)
	if err != nil {
		return err
	}

	if programVersion == "." || programVersion == ".." || filepath.IsAbs(programVersion) ||
		strings.ContainsAny(programVersion, "/\\\x00") {
		return fmt.Errorf("%w: %q", errInvalidVersion, programVersion)
	}

	root, err := uninstallCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	directories := []string{
		filepath.Join(root, binary.VersionsDirName),
		filepath.Join(root, binary.VersionsDirName, program.String()),
		filepath.Join(root, binary.BinDirName),
	}
	switch program {
	case binary.ProgramCe, binary.ProgramEe, binary.ProgramSDK:
		directories = append(directories, filepath.Join(root, binary.IncDirName))
	default:
		// Other products have no headers.
	}

	if fallback := program.FallbackProgram(); fallback != binary.ProgramUnknown {
		directories = append(directories,
			filepath.Join(root, binary.VersionsDirName, fallback.String()))
	}

	err = validateDirectories(root, directories...)
	if err != nil {
		return err
	}

	log.Infof("Removing binary...")

	if program == binary.ProgramCe && programVersion == "" {
		buildPath, err := binary.TarantoolBuildPath(filepath.Join(root, binary.BinDirName))
		if err != nil {
			return err
		}

		if buildPath != "" {
			return uninstallTarantoolFromBuild(root)
		}
	}

	_, fromSDK := binary.SDKComponentVersion(program, programVersion)
	matchBundleVersion := fromSDK || program.BundleSource() != binary.ProgramUnknown

	switch {
	case programVersion == "":
		programVersion, err = getDefault(program, root)
	case matchBundleVersion:
		installed, readErr := getVersions(program, root)
		if readErr != nil {
			return readErr
		}

		programVersion, err = binary.MatchInstalledVersion(program, programVersion, installed)
	}

	if errors.Is(err, version.ErrInstalledVersionNotFound) {
		return fmt.Errorf("%w: %w", errNotInstalled, err)
	}

	if err != nil {
		return err
	}

	if program == binary.ProgramSDK {
		err = uninstallSDK(root, programVersion)

		return err
	}

	versionsToDelete, err := getAllTtVersionFormats(program, programVersion)
	if err != nil {
		return err
	}

	var removed bool

	for _, candidate := range versionsToDelete {
		removed, err = remove(program, candidate, root)
		if !errors.Is(err, errNotInstalled) {
			break
		}
	}

	if err != nil {
		return err
	}

	log.Infof("%s%s%s is uninstalled.", program, version.CliSeparator, programVersion)

	if removed {
		return switchProgramToLatestVersion(program, root)
	}

	return nil
}

// uninstallTarantoolFromBuild removes scoped active links, preserving the source build.
func uninstallTarantoolFromBuild(root string) error {
	binaryLink := filepath.Join(root, binary.BinDirName, binary.ProgramCe.Exec())

	err := os.Remove(binaryLink)
	if err != nil {
		return fmt.Errorf("failed to remove symlink %q: %w", binaryLink, err)
	}

	log.Infof("Removing headers...")

	headerLink := filepath.Join(root, binary.IncDirName, binary.ProgramCe.Exec())
	info, err := os.Lstat(headerLink)

	switch {
	case errors.Is(err, os.ErrNotExist):
		// A linked build need not have headers.
	case err != nil:
		return fmt.Errorf("failed to access headers %q: %w", headerLink, err)
	case info.Mode()&os.ModeSymlink != 0:
		err = os.Remove(headerLink)
		if err != nil {
			return fmt.Errorf("failed to remove headers %q: %w", headerLink, err)
		}
	}

	return switchProgramToLatestVersion(binary.ProgramCe, root)
}

// getAllTtVersionFormats returns all version formats with 'v' prefix and
// without it before x.y.z version.
func getAllTtVersionFormats(program binary.Program, ttVersion string) ([]string, error) {
	versionsToDelete := []string{ttVersion}

	if program == binary.ProgramTt {
		// Need to determine if we have x.y.z format in tt binary uninstall argument
		// to make sure we add version prefix.
		versionMatches, err := regexp.MatchString(MajorMinorPatchRegexp, ttVersion)
		if err != nil {
			return versionsToDelete, fmt.Errorf("failed to match version %q: %w", ttVersion, err)
		}

		if versionMatches {
			versionsToDelete = append(versionsToDelete, "v"+ttVersion)
		}
	}

	return versionsToDelete, nil
}

// getVersions lists only installation directories belonging to the selected product.
func getVersions(program binary.Program, root string) ([]string, error) {
	dir := filepath.Join(root, binary.VersionsDirName, program.String())

	err := validateDirectories(root, filepath.Join(root, binary.VersionsDirName), dir)
	if err != nil {
		return nil, err
	}

	versions, err := binary.InstalledVersions(root, program)

	return versions, err
}

// getDefault returns the sole installed version, including incomplete installations.
func getDefault(program binary.Program, root string) (string, error) {
	versions, err := getVersions(program, root)
	if err != nil {
		return "", err
	}

	switch len(versions) {
	case 0:
		return "", fmt.Errorf("%s%w", program, errHasNoInstalledVersion)
	case 1:
		return versions[0], nil
	default:
		return "", fmt.Errorf("%s%wplease specify the version to uninstall",
			program, errMultipleInstalledVersions)
	}
}

// GetList generates version completions from exactly the selected scope.
func GetList(scope binary.Scope, program string) []string {
	parsed, err := binary.ParseProgram(program)
	if err != nil {
		return nil
	}

	err = validateProgram(parsed)
	if err != nil {
		return nil
	}

	root, err := scope.ArtifactPath()
	if err != nil {
		return nil
	}

	versions, err := getVersions(parsed, root)
	if err != nil {
		return nil
	}

	return versions
}

// searchLatestVersion returns a replacement installation within the selected root.
// Enterprise products also consider the community product sharing their executable.
func searchLatestVersion(program binary.Program, root string) (binary.Selection, error) {
	programs := []binary.Program{program}
	if fallback := program.FallbackProgram(); fallback != binary.ProgramUnknown {
		programs = append(programs, fallback)
	}

	var (
		latestInfo version.Version
		latest     binary.Selection
		firstHash  binary.Selection
	)

	for _, candidateProgram := range programs {
		versions, err := getVersions(candidateProgram, root)
		if err != nil {
			return latest, err
		}

		for _, candidate := range versions {
			selection := binary.Selection{Program: candidateProgram, ID: candidate}

			err = binary.ValidateInstallation(root, selection)
			if err != nil {
				continue
			}

			isHash, _ := binary.IsValidCommitHash(candidate)
			if isHash {
				if firstHash.ID == "" {
					firstHash = selection
				}

				continue
			}

			parsed, err := replacementVersion(root, candidateProgram, candidate)
			if err != nil {
				log.Debugf("%s %q skipped: %v", candidateProgram, candidate, err)

				continue
			}

			if latest.ID == "" || version.IsLess(latestInfo, parsed) {
				latestInfo = parsed
				latest = selection
			}
		}
	}

	if latest.ID != "" {
		return latest, nil
	}

	return firstHash, nil
}

// replacementVersion compares components by their product release. TCM and
// tt-ee have their own versions, while the SDK version follows Tarantool.
func replacementVersion(root string, program binary.Program,
	selected string,
) (version.Version, error) {
	productVersion := selected
	parseVersion := version.Parse
	sdkVersion, fromSDK := binary.SDKComponentVersion(program, selected)

	if fromSDK {
		productVersion = sdkVersion
	}

	productVersion = strings.TrimPrefix(productVersion, binary.DevVersionPrefix)

	if program == binary.ProgramTtEe || (fromSDK && program == binary.ProgramTcm) {
		ctx, cancel := context.WithTimeout(context.Background(), productVersionTimeout)
		defer cancel()

		executable := filepath.Join(binary.InstallationPath(root, program, selected),
			program.Exec())

		var (
			output []byte
			err    error
		)

		if program == binary.ProgramTtEe {
			output, err = cmdcontext.TtVersionOutput(ctx, executable)
			parseVersion = version.ParseTt
		} else {
			output, err = exec.CommandContext(ctx, executable, "version").Output()
		}

		if err != nil {
			return version.Version{}, fmt.Errorf("failed to get %s version: %w", program, err)
		}

		productVersion = strings.TrimSpace(string(output))
	}

	parsed, err := parseVersion(productVersion)
	if err != nil {
		return parsed, fmt.Errorf("failed to read product version for %q: %w", selected, err)
	}

	return parsed, nil
}

// switchProgramToLatestVersion selects the replacement executable and matching headers.
func switchProgramToLatestVersion(program binary.Program, root string) error {
	selection, err := searchLatestVersion(program, root)
	if err != nil {
		return err
	}

	if selection.ID == "" {
		return nil
	}

	log.Infof("Changing symlinks...")

	err = binary.ActivateSelections(root, []binary.Selection{selection})
	if err != nil {
		return err
	}

	log.Infof("Current %q is set to %q.", program.Exec(),
		binary.InstallationName(selection.Program, selection.ID))

	return nil
}

func uninstallSDK(root, selected string) error {
	sdk, err := binary.ReadSDK(root, selected)
	if err != nil {
		return err
	}

	err = binary.ValidateSDKManifest(sdk, selected)
	if err != nil {
		return err
	}

	selections := sdk.Selections()

	// Inspect every component directory before removing any of the SDK.
	for _, selection := range selections {
		err = validateDirectories(root,
			binary.InstallationPath(root, selection.Program, selection.ID))
		if err != nil {
			return err
		}
	}

	var activeProducts []binary.Program

	for _, selection := range selections {
		active, err := remove(selection.Program, selection.ID, root)
		if errors.Is(err, errNotInstalled) {
			active, err = removeInstallationLinks(selection.Program,
				binary.InstallationPath(root, selection.Program, selection.ID), root)
		}

		if err != nil {
			return err
		}

		if active {
			activeProducts = append(activeProducts, selection.Program)
		}
	}

	err = os.RemoveAll(binary.InstallationPath(root, binary.ProgramSDK, selected))
	if err != nil {
		return fmt.Errorf("failed to remove SDK installation: %w", err)
	}

	remaining, err := getVersions(binary.ProgramSDK, root)
	if err != nil {
		log.Warnf("Could not inspect remaining SDK installations: %v", err)
	}

	log.Infof("SDK %s is uninstalled.", selected)

	// Replacements are optional. A broken candidate or failed activation must
	// neither prevent removal nor turn a completed removal into an error.
	replacement := latestCompleteSDK(root, remaining)
	for _, product := range activeProducts {
		if slices.Contains(replacement.Products, product) {
			err = binary.ActivateSelections(root, []binary.Selection{
				{Program: product, ID: binary.SDKVersionPrefix + replacement.Version},
			})
		} else {
			err = switchProgramToLatestVersion(product, root)
		}

		if err != nil {
			log.Warnf("Could not activate a replacement for %s: %v", product, err)
		}
	}

	return nil
}

func latestCompleteSDK(root string, installed []string) binary.SDKInstallation {
	var (
		latest        binary.SDKInstallation
		latestVersion version.Version
	)

	for _, selected := range installed {
		sdk, err := binary.ReadSDK(root, selected)
		if err != nil {
			log.Debugf("SDK %q skipped as a replacement: %v", selected, err)

			continue
		}

		err = binary.ValidateSDKManifest(sdk, selected)
		if err != nil {
			log.Debugf("SDK %q skipped as a replacement: %v", selected, err)

			continue
		}

		err = binary.ValidateSDKFiles(root, sdk)
		if err != nil {
			continue
		}

		parsed, err := version.Parse(strings.TrimPrefix(selected, binary.DevVersionPrefix))
		if err != nil {
			continue
		}

		if latest.Version == "" || version.IsLess(latestVersion, parsed) {
			latest = sdk
			latestVersion = parsed
		}
	}

	return latest
}
