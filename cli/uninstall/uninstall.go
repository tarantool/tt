package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/install"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	progRegexp = "(?P<prog>.+)"

	verRegexp = "(?P<ver>.+)"

	MajorMinorPatchRegexp = `^[0-9]+\.[0-9]+\.[0-9]+`
)

var errNotInstalled = errors.New("program is not installed")

// remove removes binary/directory and symlinks from directory.
// It returns true if symlink was removed, error.
func remove(program search.Program, programVersion string, directory string,
	cmdCtx *cmdcontext.CmdCtx,
) (bool, error) {
	var linkPath string
	var err error

	if linkPath, err = util.JoinAbspath(directory, program.Exec()); err != nil {
		return false, err
	}

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return false, fmt.Errorf("couldn't find %s directory", directory)
	} else if err != nil {
		return false, fmt.Errorf("there was some problem with %s directory", directory)
	}

	fileName := program.String() + version.FsSeparator + programVersion
	path := filepath.Join(directory, fileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, errNotInstalled
	} else if err != nil {
		return false, fmt.Errorf("there was some problem locating %s", path)
	}

	var isSymlinkRemoved bool

	_, err = os.Lstat(linkPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("failed to access %q: %w", linkPath, err)
		}
		isSymlinkRemoved = false
	} else {
		// Get path where symlink point.
		resolvedPath, err := util.ResolveSymlink(linkPath)
		if err != nil {
			return false, fmt.Errorf("failed to resolve symlink %q: %w", linkPath, err)
		}

		// Remove symlink if it points to program.
		if strings.Contains(resolvedPath, fileName) {
			if err = os.Remove(linkPath); err != nil {
				return false, err
			}
			isSymlinkRemoved = true
		}
	}
	err = os.RemoveAll(path)
	if err != nil {
		return isSymlinkRemoved, err
	}

	return isSymlinkRemoved, err
}

// UninstallProgram uninstalls program and symlinks.
func UninstallProgram(
	program search.Program,
	programVersion string,
	binDst string,
	headerDst string,
	cmdCtx *cmdcontext.CmdCtx,
) error {
	log.Infof("Removing binary...")
	var err error

	if program == search.ProgramDev {
		tarantoolBinarySymlink := filepath.Join(binDst, "tarantool")
		_, isTarantoolDevInstalled, err := install.IsTarantoolDev(tarantoolBinarySymlink, binDst)
		if err != nil {
			return err
		}
		if !isTarantoolDevInstalled {
			return fmt.Errorf("%s is not installed", program)
		}
		if err := os.Remove(tarantoolBinarySymlink); err != nil {
			return err
		}
		headerDir := filepath.Join(headerDst, "tarantool")
		log.Infof("Removing headers...")
		// There can be no headers when `tarantool-dev` is installed.
		if err := os.Remove(headerDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		err = switchProgramToLatestVersion(program, binDst, headerDst)
		return err
	}

	if programVersion == "" {
		if programVersion, err = getDefault(program, binDst); err != nil {
			return err
		}
	}

	versionsToDelete, err := getAllTtVersionFormats(program, programVersion)
	if err != nil {
		return err
	}

	var isSymlinkRemoved bool
	for _, verToDel := range versionsToDelete {
		isSymlinkRemoved, err = remove(program, verToDel, binDst, cmdCtx)
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
		_, err = remove(program, programVersion, headerDst, cmdCtx)
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
func getAllTtVersionFormats(program search.Program, ttVersion string) ([]string, error) {
	versionsToDelete := []string{ttVersion}

	if program == search.ProgramTt {
		// Need to determine if we have x.y.z format in tt uninstall argument
		// to make sure we add version prefix.
		versionMatches, err := regexp.Match(MajorMinorPatchRegexp, []byte(ttVersion))
		if err != nil {
			return versionsToDelete, err
		}
		if versionMatches {
			versionsToDelete = append(versionsToDelete, "v"+ttVersion)
		}
	}

	return versionsToDelete, nil
}

// getDefault returns a default version of an installed program.
func getDefault(program search.Program, dir string) (string, error) {
	var ver string

	re := regexp.MustCompile(
		"^" + program.String() + version.FsSeparator + verRegexp + "$",
	)

	installedPrograms, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, file := range installedPrograms {
		matches := util.FindNamedMatches(re, file.Name())
		if ver != "" {
			return "", fmt.Errorf("%s has more than one installed version, "+
				"please specify the version to uninstall", program)
		} else {
			ver = matches["ver"]
		}
	}

	if ver == "" {
		return "", fmt.Errorf("%s has no installed version", program)
	}
	return ver, nil
}

// GetList generates a list of options to uninstall.
func GetList(cliOpts *config.CliOpts, program string) []string {
	list := []string{}
	re := regexp.MustCompile(
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
		matches := util.FindNamedMatches(re, file.Name())
		if len(matches) != 0 && matches["prog"] == program {
			list = append(list, matches["ver"])
		}
	}

	return list
}

// searchLatestVersion searches for the latest installed version of the program.
//
// Note: Searching `tarantool` EE or Dev versions may lead to found the latest CE version.
func searchLatestVersion(program search.Program, binDst, headerDst string) (string, error) {
	programsToSearch := []string{program.Exec()}
	if program.IsTarantool() && program.Exec() != program.String() {
		programsToSearch = append(programsToSearch, program.String())
	}

	programRegex := regexp.MustCompile(
		"^" + progRegexp + version.FsSeparator + verRegexp + "$",
	)

	binaries, err := os.ReadDir(binDst)
	if err != nil {
		return "", err
	}

	latestVersionInfo := version.Version{}
	latestVersion := ""
	latestHash := ""

	for _, binary := range binaries {
		if binary.IsDir() {
			continue
		}
		binaryName := binary.Name()
		matches := util.FindNamedMatches(programRegex, binaryName)

		// Need to match for the program and version.
		if len(matches) != 2 {
			log.Debugf("%q skipped: unexpected format", binaryName)
			continue
		}

		programName := matches["prog"]

		// Need to find the program in the list of suitable.
		if !slices.Contains(programsToSearch, programName) {
			continue
		}
		if latestHash == "" {
			isHash, _ := util.IsValidCommitHash(matches["ver"])
			if isHash {
				if program.IsTarantool() {
					// Same version of headers is required to activate the Tarantool binary.
					if _, err := os.Stat(filepath.Join(headerDst, binaryName)); os.IsNotExist(err) {
						continue
					}
				}
				latestHash = binaryName
				continue
			}
		}

		ver, err := version.Parse(matches["ver"])
		if err != nil {
			log.Debugf("%q skipped: wrong version format", binaryName)
			continue
		}
		if program.IsTarantool() {
			// Check for headers.
			if _, err := os.Stat(filepath.Join(headerDst, binaryName)); os.IsNotExist(err) {
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

// switchProgramToLatestVersion switches the active version of the program to the latest installed.
func switchProgramToLatestVersion(program search.Program, binDst, headerDst string) error {
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
	err = util.CreateSymlink(filepath.Join(binDst, progToSwitch), binaryPath, true)
	if err != nil {
		return err
	}

	if linkName == "tarantool" {
		headerPath := filepath.Join(headerDst, linkName)
		err = util.CreateSymlink(filepath.Join(headerDst, progToSwitch), headerPath, true)
		if err != nil {
			return err
		}
	}

	log.Infof("Current %q is set to %q.", linkName, progToSwitch)
	return nil
}
