package remove

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
)

// Remove removes binary/directory and symlinks from directory.
func Remove(program string, directory string, cmdCtx *cmdcontext.CmdCtx) error {
	var linkPath string
	var err error

	if strings.HasPrefix(program, "tt") {
		linkPath, err = util.JoinAbspath(directory, "tt")
		if err != nil {
			return err
		}
	} else if strings.HasPrefix(program, "tarantool") {
		linkPath, _ = util.JoinAbspath(directory, "tarantool")
	} else {
		return fmt.Errorf("Unknown program: %s", program)
	}

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return fmt.Errorf("Couldn't find %s directory", directory)
	} else if err != nil {
		return fmt.Errorf("There was some problem with %s directory", directory)
	}
	path := filepath.Join(directory, program)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("There is no %s installed.", program)
	} else if err != nil {
		return fmt.Errorf("There was some problem locating %s", path)
	} else {
		log.Infof("%s found, removing...", program)
	}
	// Get path where symlink point.
	resolvedPath, err := util.ResolveSymlink(linkPath)
	if err != nil {
		return fmt.Errorf("Failed to resolve symlink %s: %s", linkPath, err)
	}
	// Remove symlink if it points to program.
	if strings.Contains(resolvedPath, program) {
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
