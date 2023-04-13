package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
func remove(program string, programVersion string, directory string,
	cmdCtx *cmdcontext.CmdCtx) error {
	var linkPath string
	var err error

	if program == search.ProgramCe || program == search.ProgramEe {
		if linkPath, err = util.JoinAbspath(directory, "tarantool"); err != nil {
			return err
		}
	} else {
		if linkPath, err = util.JoinAbspath(directory, program); err != nil {
			return err
		}
	}

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return fmt.Errorf("couldn't find %s directory", directory)
	} else if err != nil {
		return fmt.Errorf("there was some problem with %s directory", directory)
	}

	fileName := program + version.FsSeparator + programVersion
	path := filepath.Join(directory, fileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("there is no %s installed", program)
	} else if err != nil {
		return fmt.Errorf("there was some problem locating %s", path)
	}
	// Get path where symlink point.
	resolvedPath, err := util.ResolveSymlink(linkPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlink %s: %s", linkPath, err)
	}
	// Remove symlink if it points to program.
	if strings.Contains(resolvedPath, fileName) {
		err = os.Remove(linkPath)
		if err != nil {
			return err
		}
	}
	err = os.RemoveAll(path)
	if err != nil {
		return err
	}

	return err
}

// UninstallProgram uninstalls program and symlinks.
func UninstallProgram(program string, programVersion string, binDst string, headerDst string,
	cmdCtx *cmdcontext.CmdCtx) error {
	log.Infof("Removing binary...")
	var err error

	if programVersion == "" {
		if programVersion, err = getDefault(program, binDst); err != nil {
			return err
		}
	}

	if err = remove(program, programVersion, binDst, cmdCtx); err != nil {
		return err
	}
	if strings.Contains(program, "tarantool") {
		log.Infof("Removing headers...")
		err = remove(program, programVersion, headerDst, cmdCtx)
	}
	log.Infof("%s%s%s is uninstalled.", program, version.CliSeparator, programVersion)
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
func GetList(cliOpts *config.CliOpts) []string {
	list := []string{}
	re := regexp.MustCompile(
		"^" + progRegexp + version.FsSeparator + verRegexp + "$",
	)

	if cliOpts.App.BinDir == "" {
		return nil
	}

	installedPrograms, err := os.ReadDir(cliOpts.App.BinDir)
	if err != nil {
		return nil
	}

	for _, file := range installedPrograms {
		matches := util.FindNamedMatches(re, file.Name())
		if len(matches) != 0 {
			list = append(list, matches["prog"]+version.CliSeparator+matches["ver"])
		}
	}

	return list
}
