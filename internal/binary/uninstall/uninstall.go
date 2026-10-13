package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tarantool/tt/sdk/log"
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
	activeDirectoryMode   = 0o755
)

// UninstallCtx contains the options for removing an installed program.
type UninstallCtx struct {
	binary.BinaryContext

	Version string
}

// validateProgram limits uninstall to products with scoped installations.
func validateProgram(program binary.Program) error {
	switch program {
	case binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm:
		return nil
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

// remove deletes one installation and only the active links selecting its artifacts.
func remove(program binary.Program, programVersion, root string) (bool, error) {
	installation := filepath.Join(root, binary.VersionsDirName, program.String(),
		program.String()+version.FsSeparator+programVersion)
	info, err := os.Lstat(installation)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("program is %w", errNotInstalled)
	case err != nil:
		return false, fmt.Errorf("failed to access %q: %w", installation, err)
	case !info.IsDir():
		return false, fmt.Errorf("program is %w", errNotInstalled)
	}

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

	err = os.RemoveAll(installation)
	if err != nil {
		return removed, fmt.Errorf("failed to remove %q: %w", installation, err)
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

// removeActiveSymlink compares complete targets before removing a selected link.
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
	if program.IsTarantool() {
		directories = append(directories, filepath.Join(root, binary.IncDirName))
	}

	if program == binary.ProgramEe {
		directories = append(directories,
			filepath.Join(root, binary.VersionsDirName, binary.ProgramCe.String()))
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

	if programVersion == "" {
		programVersion, err = getDefault(program, root)
		if err != nil {
			return err
		}
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

	entries, err := os.ReadDir(dir)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	var versions []string

	prefix := program.String() + version.FsSeparator
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && entry.Name() != prefix {
			versions = append(versions, strings.TrimPrefix(entry.Name(), prefix))
		}
	}

	return versions, nil
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
// EE considers both editions; lexical installation names put EE ahead of CE for ties.
func searchLatestVersion(program binary.Program, root string) (string, error) {
	programs := []binary.Program{program}
	if program == binary.ProgramEe {
		programs = append(programs, binary.ProgramCe)
	}

	var (
		latestInfo version.Version
		latest     string
		firstHash  string
	)

	for _, candidateProgram := range programs {
		versions, err := getVersions(candidateProgram, root)
		if err != nil {
			return "", err
		}

		for _, candidate := range versions {
			installation := filepath.Join(root, binary.VersionsDirName, candidateProgram.String(),
				candidateProgram.String()+version.FsSeparator+candidate)
			if !isUsableBinary(candidateProgram, installation) {
				continue
			}

			isHash, _ := binary.IsValidCommitHash(candidate)
			if isHash && firstHash == "" {
				firstHash = installation
			}

			if isHash {
				continue
			}

			parsed, err := version.Parse(candidate)
			if err != nil {
				log.Debugf("%q skipped: wrong version format", filepath.Base(installation))

				continue
			}

			if latest == "" || version.IsLess(latestInfo, parsed) {
				latestInfo = parsed
				latest = installation
			}
		}
	}

	if latest != "" {
		return latest, nil
	}

	return firstHash, nil
}

// isUsableBinary requires the executable and any matching Tarantool headers.
func isUsableBinary(program binary.Program, installation string) bool {
	if !binary.IsRegularFile(filepath.Join(installation, program.Exec())) {
		return false
	}

	return !program.IsTarantool() ||
		binary.IsDir(filepath.Join(installation, binary.IncDirName, program.Exec()))
}

// switchProgramToLatestVersion selects the replacement executable and matching headers.
func switchProgramToLatestVersion(program binary.Program, root string) error {
	installation, err := searchLatestVersion(program, root)
	if err != nil || installation == "" {
		return err
	}

	log.Infof("Changing symlinks...")

	binDir := filepath.Join(root, binary.BinDirName)

	err = binary.CreateDirectory(binDir, activeDirectoryMode)
	if err != nil {
		return err
	}

	err = binary.CreateSymlink(filepath.Join(installation, program.Exec()),
		filepath.Join(binDir, program.Exec()), true)
	if err != nil {
		return err
	}

	switch program {
	case binary.ProgramCe, binary.ProgramEe:
		incDir := filepath.Join(root, binary.IncDirName)

		err = binary.CreateDirectory(incDir, activeDirectoryMode)
		if err != nil {
			return err
		}

		err = binary.CreateSymlink(filepath.Join(installation, binary.IncDirName, program.Exec()),
			filepath.Join(incDir, program.Exec()), true)
		if err != nil {
			return err
		}
	default:
		// TT and TCM have no active headers.
	}

	log.Infof("Current %q is set to %q.", program.Exec(), filepath.Base(installation))

	return nil
}
