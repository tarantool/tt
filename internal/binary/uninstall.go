package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	errDirectoryNotFound                = errors.New("couldn't find ")
	errHasNoInstalledVersion            = errors.New(" has no installed version")
	errMultipleInstalledVersions        = errors.New(" has more than one installed version, ")
	errThereWasSomeProblemLocating      = errors.New("there was some problem locating ")
	errThereWasSomeProblemWithDirectory = errors.New("there was some problem with ")
)

const binaryNameMatchGroups = 2

const (
	progRegexp = "(?P<prog>.+)"

	verRegexp = "(?P<ver>.+)"

	MajorMinorPatchRegexp = `^[0-9]+\.[0-9]+\.[0-9]+`
)

var errNotInstalled = errors.New("not installed")

// remove removes binary/directory and symlinks from directory.
// It returns true if symlink was removed, error.
func remove(program Program, programVersion, directory string) (bool, error) {
	linkPath, err := JoinAbspath(directory, program.Exec())
	if err != nil {
		return false, err
	}

	_, err = os.Stat(directory)
	if os.IsNotExist(err) {
		return false, fmt.Errorf("%w%s directory", errDirectoryNotFound, directory)
	} else if err != nil {
		return false, fmt.Errorf("%w%s directory", errThereWasSomeProblemWithDirectory, directory)
	}

	fileName := program.String() + version.FsSeparator + programVersion
	path := filepath.Join(directory, fileName)

	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, fmt.Errorf("program is %w", errNotInstalled)
	} else if err != nil {
		return false, fmt.Errorf("%w%s", errThereWasSomeProblemLocating, path)
	}

	var isSymlinkRemoved bool

	_, err = os.Lstat(linkPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to access %q: %w", linkPath, err)
	}

	if err == nil {
		// Get path where symlink point.
		resolvedPath, err := ResolveSymlink(linkPath)
		if err != nil {
			return false, fmt.Errorf("failed to resolve symlink %q: %w", linkPath, err)
		}

		// Remove symlink if it points to program.
		if strings.Contains(resolvedPath, fileName) {
			err = os.Remove(linkPath)
			if err != nil {
				return false, fmt.Errorf("failed to remove symlink %q: %w", linkPath, err)
			}

			isSymlinkRemoved = true
		}
	}

	err = os.RemoveAll(path)
	if err != nil {
		return isSymlinkRemoved, fmt.Errorf("failed to remove %q: %w", path, err)
	}

	return isSymlinkRemoved, nil
}

// UninstallProgram uninstalls program and symlinks.
func UninstallProgram(
	program Program,
	programVersion string,
	binDst string,
	headerDst string,
	cmdCtx *cmdcontext.CmdCtx,
) error {
	log.Infof("Removing binary...")

	var err error

	if program == ProgramDev {
		tarantoolBinarySymlink := filepath.Join(binDst, "tarantool")

		_, isTarantoolDevInstalled, err := IsTarantoolDev(tarantoolBinarySymlink, binDst)
		if err != nil {
			return err
		}

		if !isTarantoolDevInstalled {
			return fmt.Errorf("%s is %w", program, errNotInstalled)
		}

		err = os.Remove(tarantoolBinarySymlink)
		if err != nil {
			return fmt.Errorf("failed to remove symlink %q: %w", tarantoolBinarySymlink, err)
		}

		headerDir := filepath.Join(headerDst, "tarantool")

		log.Infof("Removing headers...")

		// There can be no headers when `tarantool-dev` is installed.
		err = os.Remove(headerDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove headers %q: %w", headerDir, err)
		}

		err = switchProgramToLatestVersion(program, binDst, headerDst)

		return err
	}

	if programVersion == "" {
		programVersion, err = getDefault(program, binDst)
		if err != nil {
			return err
		}
	}

	versionsToDelete, err := getAllTtVersionFormats(program, programVersion)
	if err != nil {
		return err
	}

	var isSymlinkRemoved bool

	for _, verToDel := range versionsToDelete {
		isSymlinkRemoved, err = remove(program, verToDel, binDst)
		if err != nil && !errors.Is(err, errNotInstalled) {
			return err
		}

		if err == nil {
			break
		}
	}

	if err != nil {
		return err
	}

	if program.IsTarantool() {
		log.Infof("Removing headers...")

		_, err = remove(program, programVersion, headerDst)
		if err != nil {
			return err
		}
	}

	log.Infof("%s%s%s is uninstalled.", program, version.CliSeparator, programVersion)

	if isSymlinkRemoved {
		err = switchProgramToLatestVersion(program, binDst, headerDst)
	}

	return err
}

// getAllTtVersionFormats returns all version formats with 'v' prefix and
// without it before x.y.z version.
func getAllTtVersionFormats(program Program, ttVersion string) ([]string, error) {
	versionsToDelete := []string{ttVersion}

	if program == ProgramTt {
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

// getDefault returns a default version of an installed program.
func getDefault(program Program, dir string) (string, error) {
	var ver string

	programRegex := regexp.MustCompile(
		"^" + program.String() + version.FsSeparator + verRegexp + "$",
	)

	installedPrograms, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %q: %w", dir, err)
	}

	for _, file := range installedPrograms {
		matches := FindNamedMatches(programRegex, file.Name())

		if ver != "" {
			return "", fmt.Errorf("%s%wplease specify the version to uninstall",
				program, errMultipleInstalledVersions)
		} else {
			ver = matches["ver"]
		}
	}

	if ver == "" {
		return "", fmt.Errorf("%s%w", program, errHasNoInstalledVersion)
	}

	return ver, nil
}

// GetList generates a list of options to uninstall.
func GetList(cliOpts *config.CliOpts, program string) []string {
	list := []string{}
	programRegex := regexp.MustCompile(
		"^" + progRegexp + version.FsSeparator + verRegexp + "$",
	)

	if cliOpts.Env.BinDir == "" {
		return nil
	}

	installedPrograms, err := os.ReadDir(cliOpts.Env.BinDir)
	if err != nil {
		return nil
	}

	for _, file := range installedPrograms {
		matches := FindNamedMatches(programRegex, file.Name())
		if len(matches) != 0 && matches["prog"] == program {
			list = append(list, matches["ver"])
		}
	}

	return list
}

// searchLatestVersion searches for the latest installed version of the program.
//
// Note: Searching `tarantool` EE or Dev versions may lead to found the latest CE version.
func searchLatestVersion(program Program, binDst, headerDst string) (string, error) {
	programsToSearch := []string{program.Exec()}
	if program.IsTarantool() && program.Exec() != program.String() {
		programsToSearch = append(programsToSearch, program.String())
	}

	programRegex := regexp.MustCompile(
		"^" + progRegexp + version.FsSeparator + verRegexp + "$",
	)

	binaries, err := os.ReadDir(binDst)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %q: %w", binDst, err)
	}

	var (
		latestVersionInfo version.Version
		latestVersion     string
		latestHash        string
	)

	for _, binary := range binaries {
		if binary.IsDir() {
			continue
		}

		binaryName := binary.Name()
		matches := FindNamedMatches(programRegex, binaryName)

		// Need to match for the program and version.
		if len(matches) != binaryNameMatchGroups {
			log.Debugf("%q skipped: unexpected format", binaryName)

			continue
		}

		programName := matches["prog"]

		// Need to find the program in the list of suitable.
		if !slices.Contains(programsToSearch, programName) {
			continue
		}

		if latestHash == "" && isUsableCommitBinary(program, headerDst, binaryName,
			matches["ver"]) {
			latestHash = binaryName
			continue
		}

		ver, err := version.Parse(matches["ver"])
		if err != nil {
			log.Debugf("%q skipped: wrong version format", binaryName)

			continue
		}

		if program.IsTarantool() {
			// Check for headers.
			_, err = os.Stat(filepath.Join(headerDst, binaryName))
			if os.IsNotExist(err) {
				continue
			}
		}

		// Update latest version.
		if latestVersion == "" || version.IsLess(latestVersionInfo, ver) {
			latestVersionInfo = ver
			latestVersion = binaryName
		}
	}

	if latestVersion != "" {
		return latestVersion, nil
	}

	return latestHash, nil
}

func isUsableCommitBinary(program Program, headerDst, binaryName, versionStr string) bool {
	isHash, _ := IsValidCommitHash(versionStr)
	if !isHash {
		return false
	}

	if !program.IsTarantool() {
		return true
	}

	// Same version of headers is required to activate the Tarantool binary.
	_, err := os.Stat(filepath.Join(headerDst, binaryName))

	return !os.IsNotExist(err)
}

// switchProgramToLatestVersion switches the active version of the program to the latest installed.
func switchProgramToLatestVersion(program Program, binDst, headerDst string) error {
	linkName := program.Exec()

	progToSwitch, err := searchLatestVersion(program, binDst, headerDst)
	if err != nil {
		return err
	}

	if progToSwitch == "" {
		return nil
	}

	log.Infof("Changing symlinks...")

	binaryPath := filepath.Join(binDst, linkName)

	err = CreateSymlink(filepath.Join(binDst, progToSwitch), binaryPath, true)
	if err != nil {
		return err
	}

	if linkName == "tarantool" {
		headerPath := filepath.Join(headerDst, linkName)

		err = CreateSymlink(filepath.Join(headerDst, progToSwitch), headerPath, true)
		if err != nil {
			return err
		}
	}

	log.Infof("Current %q is set to %q.", linkName, progToSwitch)

	return nil
}
