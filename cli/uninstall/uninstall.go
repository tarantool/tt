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
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	MajorMinorPatchRegexp = `^[0-9]+\.[0-9]+\.[0-9]+`
)

var errNotInstalled = errors.New("program is not installed")

// remove removes binary/directory and symlinks from directory.
// It returns true if symlink was removed, error.
func remove(program string, programVersion string, directory string) (bool, error) {
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
func UninstallProgram(program string, programVersion string,
	binDst string, headerDst string) error {
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

	if strings.Contains(program, "tarantool") {
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
func getAllTtVersionFormats(programName, ttVersion string) ([]string, error) {
	versionsToDelete := []string{ttVersion}

	if programName == search.ProgramTt {
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
func getDefault(program, dir string) (string, error) {
	versions, err := GetAvailableVersions(program, dir)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("%s has no installed version", program)
	}
	if len(versions) > 1 {
		return "", fmt.Errorf("%s has more than one installed version, "+
			"please specify the version to uninstall", program)
	}
	return versions[0], nil
}

// GetAvailableVersions returns a list of the program's versions installed into
// the 'dir' directory.
func GetAvailableVersions(program string, dir string) ([]string, error) {
	if !util.IsDir(dir) {
		return nil, fmt.Errorf("%q is missing or not a directory", dir)
	}

	versionPrefix := filepath.Join(dir, program+version.FsSeparator)

	programFiles, err := filepath.Glob(versionPrefix + "*")
	if err != nil {
		return nil, err
	}

	versions := []string{}
	for _, file := range programFiles {
		versions = append(versions, file[len(versionPrefix):])
	}

	return versions, nil
}

// searchLatestVersion searches for the latest installed version of the program.
func searchLatestVersion(program, binDst, headerDst string) (string, error) {
	binVersions, err := GetAvailableVersions(program, binDst)
	if err != nil {
		return "", err
	}

	headerVersions, err := GetAvailableVersions(program, headerDst)
	if err != nil {
		return "", err
	}

	binPrefix := filepath.Join(binDst, program+version.FsSeparator)

	// Find intersection and convert to version.Version
	versions := []version.Version{}
	for _, binVersion := range binVersions {
		if util.IsDir(binPrefix + binVersion) {
			continue
		}

		if slices.Contains(headerVersions, binVersion) {
			ver, err := version.Parse(binVersion)
			if err != nil {
				continue
			}
			versions = append(versions, ver)
		}
	}

	if len(versions) == 0 {
		return "", nil
	}

	latestVersion := slices.MaxFunc(versions, func(a, b version.Version) int {
		if a.Str == b.Str {
			return 0
		}
		isCommitHash, _ := util.IsValidCommitHash(a.Str)
		if isCommitHash || version.IsLess(a, b) {
			return -1
		}
		return 1
	})

	return program + version.FsSeparator + latestVersion.Str, nil
}

// switchProgramToLatestVersion switches the active version of the program to the latest installed.
func switchProgramToLatestVersion(program, binDst, headerDst string) error {
	linkName := program
	if program == search.ProgramCe || program == search.ProgramEe || program == search.ProgramDev {
		linkName = "tarantool"
	}

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
