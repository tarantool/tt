package remove

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

// Remove removes binary/directory and symlinks from directory.
func Remove(program string, directory string, cmdCtx *cmdcontext.CmdCtx) error {
	var linkPath string
	var err error

	re := regexp.MustCompile(
		"^(?P<prog>tt|tarantool|tarantool-ee)(?:" + search.VersionCliSeparator + "(?P<ver>.*))?$",
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

	fileName := matches["prog"] + search.VersionFsSeparator + matches["ver"]
	path := filepath.Join(directory, fileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("there is no %s installed", program)
	} else if err != nil {
		return fmt.Errorf("there was some problem locating %s", path)
	} else {
		log.Infof("%s found, removing...", program)
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
	log.Infof("%s was removed!", program)
	return err
}

// RemoveProgram removes program and symlinks.
func RemoveProgram(program string, binDst string, headerDst string,
	cmdCtx *cmdcontext.CmdCtx) error {
	log.Infof("Removing binary...")
	err := Remove(program, binDst, cmdCtx)
	if err != nil {
		return err
	}
	if strings.Contains(program, "tarantool") {
		log.Infof("Removing headers...")
		err = Remove(program, headerDst, cmdCtx)
	}
	return err
}
