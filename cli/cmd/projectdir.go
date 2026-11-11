package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"
)

// errNotADirectory reports a -C path that exists but is not a directory.
var errNotADirectory = errors.New("not a directory")

// projectDir holds -C: the directory the manifest commands work on instead of
// the project the working directory is in. Empty means no -C.
//
// It is package state like cmdCtx, because the flag is declared on
// several command groups and read through absoluteWorkingDir, which every
// project-scope command already calls.
var projectDir string

// projectDirUsage is the one description both registrations show.
const projectDirUsage = "work on the project in DIR itself, not on the one the " +
	"current directory is in"

// projectNote says once in a process which project a command works on,
// when it is above the working directory.
var projectNote sync.Once //nolint:gochecknoglobals // Once per process.

// addProjectDirFlag declares -C on one command.
func addProjectDirFlag(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&projectDir, "directory", "C", "", projectDirUsage)
}

// addPersistentProjectDirFlag declares -C on a command group, so every
// subcommand under it accepts the flag without declaring it again.
func addPersistentProjectDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&projectDir, "directory", "C", "", projectDirUsage)
}

// absoluteWorkingDir returns the directory a command works in as an absolute
// path: the -C directory when one was given, and the caller's working
// directory otherwise. Paths the user types, such as a relative --registry,
// are relative to it; the project a command acts on is projectRootDir.
//
// A -C path is checked here rather than left to the first file the command
// opens, so a mistyped directory is reported as such instead of as a missing
// manifest. The working directory is not checked: the process is already in
// it.
func absoluteWorkingDir() (string, error) {
	if projectDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get the working directory: %w", err)
		}

		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path of %q: %w", dir, err)
		}

		return abs, nil
	}

	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("-C %s: %w", projectDir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("-C %s: %w", projectDir, err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("-C %s: %w", projectDir, errNotADirectory)
	}

	return abs, nil
}

// projectRootDir returns the project directory a project command - tt package
// and its subcommands, tt run, tt test, tt wasm - works on, as an absolute
// path, and says on stderr, once, which project it is when it is above the
// working directory.
//
// Without -C it is the project the working directory is in, as sdk/scope
// finds it for the instance commands and the modules: the working directory
// or the nearest of its parents with a project marker. So the command works
// from any directory inside the project. Outside every project, and inside an
// installed version, it is the working directory, and the command reports
// the manifest missing there.
//
// -C names the project itself and is taken as it is, nothing above it looked
// into: whoever typed it chose the directory.
//
// A failure to look for the project, such as a relative TT_USER_ROOT, leaves
// the working directory in its place, as it does for the process (see
// configure): the command then reports what is missing in that directory.
func projectRootDir() (string, error) {
	dir, above, err := locateProject()
	if err != nil {
		return "", err
	}

	if above {
		projectNote.Do(func() { log.Infof("working on the project in %s", dir) })
	}

	return dir, nil
}

// ProjectRoot returns the directory projectRootDir returns, without saying
// anything: the project the working directory, or -C, is in, for the core to
// take the Tarantool of a module's command from it (sdk.Services.Tarantool).
func ProjectRoot() (string, error) {
	dir, _, err := locateProject()

	return dir, err
}

// locateProject returns the directory of projectRootDir and whether it is
// above the working directory.
func locateProject() (string, bool, error) {
	start, err := absoluteWorkingDir()
	if err != nil {
		return "", false, err
	}

	if projectDir != "" {
		return start, false, nil
	}

	dir, found, err := scope.FindProject(start)
	if err != nil {
		log.Debugf("Failed to look for the project, %q stands for it: %s", start, err)

		return start, false, nil
	}

	if !found {
		return start, false, nil
	}

	return dir, dir != start, nil
}
