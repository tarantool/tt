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
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// remove removes binary/directory and symlinks from directory.
func remove(program string, directory string, cmdCtx *cmdcontext.CmdCtx) error {
	var linkPath string
	var err error

	re := regexp.MustCompile(
		"^(?P<prog>tt|tarantool|tarantool-ee)(?:" + version.CliSeparator + "(?P<ver>.*))?$",
	)

	matches := util.FindNamedMatches(re, program)
	if len(matches) == 0 {
		return fmt.Errorf("unknown program: %s", program)
	}

	if matches["prog"] == "tarantool" || matches["prog"] == "tarantool-ee" {
		linkPath, err = util.JoinAbspath(directory, "tarantool")
	} else {
		linkPath, err = util.JoinAbspath(directory, matches["prog"])
	}

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return fmt.Errorf("couldn't find %s directory", directory)
	} else if err != nil {
		return fmt.Errorf("there was some problem with %s directory", directory)
	}

	fileName := matches["prog"] + version.FsSeparator + matches["ver"]
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
func UninstallProgram(program string, binDst string, headerDst string,
	cmdCtx *cmdcontext.CmdCtx) error {
	log.Infof("Removing binary...")
	err := remove(program, binDst, cmdCtx)
	if err != nil {
		return err
	}
	if strings.Contains(program, "tarantool") {
		log.Infof("Removing headers...")
		err = remove(program, headerDst, cmdCtx)
	}
	log.Infof("%s is uninstalled.", program)
	return err
}

// GetList generates a list of options to uninstall.
func GetList(cliOpts *config.CliOpts) []string {
	list := []string{}
	re := regexp.MustCompile(
		"^(?P<prog>(?:tarantool)|(?:tarantool-ee)|(?:tt))" +
			version.FsSeparator +
			"(?P<ver>.*)$",
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
