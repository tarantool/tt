package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	progRegexp = "(?P<prog>" +
		search.ProgramTt + "|" +
		search.ProgramCe + "|" +
		search.ProgramEe + ")"
	verRegexp = "(?P<ver>.*)"
)

// remove removes binary/directory and symlinks from directory.
// It returns true if symlink was removed, error.
func remove(program string, programVersion string, directory string,
	cmdCtx *cmdcontext.CmdCtx) (bool, error) {
	var linkPath string
	var err error

	if program == search.ProgramCe || program == search.ProgramEe {
		if linkPath, err = util.JoinAbspath(directory, "tarantool"); err != nil {
			return false, err
		}
	} else {
		if linkPath, err = util.JoinAbspath(directory, program); err != nil {
			return false, err
		}
	}

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return false, fmt.Errorf("couldn't find %s directory", directory)
	} else if err != nil {
		return false, fmt.Errorf("there was some problem with %s directory", directory)
	}

	fileName := program + version.FsSeparator + programVersion
	path := filepath.Join(directory, fileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, fmt.Errorf("there is no %s installed", program)
	} else if err != nil {
		return false, fmt.Errorf("there was some problem locating %s", path)
	}
	// Get path where symlink point.
	resolvedPath, err := util.ResolveSymlink(linkPath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve symlink %s: %s", linkPath, err)
	}
	var isSymlinkRemoved bool
	// Remove symlink if it points to program.
	if strings.Contains(resolvedPath, fileName) {
		err = os.Remove(linkPath)
		if err != nil {
			return false, err
		}
		isSymlinkRemoved = true
	}
	err = os.RemoveAll(path)
	if err != nil {
		return isSymlinkRemoved, err
	}

	return isSymlinkRemoved, err
}

// UninstallProgram uninstalls program and symlinks.
func UninstallProgram(program string, programVersion string, binDst string, headerDst string,
	cmdCtx *cmdcontext.CmdCtx) error {
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

	var isSymlinkRemoved bool
	isSymlinkRemoved, err = remove(program, programVersion, binDst, cmdCtx)
	if err != nil {
		return err
	}
	if strings.Contains(program, "tarantool") {
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

// getDefault returns a default version of an installed program.
func getDefault(program, dir string) (string, error) {
	var ver string

	re := regexp.MustCompile(
		"^" + program + version.FsSeparator + verRegexp + "$",
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
func searchLatestVersion(linkName, binDst, headerDst string) (string, error) {
	var programsToSearch []string
	if linkName == "tarantool" {
		programsToSearch = []string{search.ProgramCe, search.ProgramEe}
	} else {
		programsToSearch = []string{linkName}
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
	hashFound := false
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
		if util.Find(programsToSearch, programName) == -1 {
			continue
		}
		isRightFormat, _ := util.IsValidCommitHash(matches["ver"])

		if isRightFormat {
			if hashFound {
				continue
			}
			if strings.Contains(programName, "tarantool") {
				// Check for headers.
				if _, err := os.Stat(filepath.Join(headerDst, binaryName)); os.IsNotExist(err) {
					continue
				}
			}
			hashFound = true
			latestHash = binaryName
			continue
		}
		ver, err := version.Parse(matches["ver"])
		if err != nil {
			continue
		}
		if strings.Contains(programName, "tarantool") {
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
func switchProgramToLatestVersion(program, binDst, headerDst string) error {
	linkName := program
	if program == search.ProgramCe || program == search.ProgramEe || program == search.ProgramDev {
		linkName = "tarantool"
	}

	progToSwitch, err := searchLatestVersion(linkName, binDst, headerDst)
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
