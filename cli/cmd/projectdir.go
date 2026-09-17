package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// projectDir holds -C: the directory the manifest commands work on instead of
// the process working directory. Empty means the working directory itself.
//
// It is package state like cmdCtx and cliOpts, because the flag is declared on
// several command groups and read through absoluteWorkingDir, which every
// project-scope command already calls.
var projectDir string

// projectDirUsage is the one description both registrations show.
const projectDirUsage = "work on the project in DIR, as if it were the current directory"

// addProjectDirFlag declares -C on one command.
func addProjectDirFlag(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&projectDir, "directory", "C", "", projectDirUsage)
}

// addPersistentProjectDirFlag declares -C on a command group, so every
// subcommand under it accepts the flag without declaring it again.
func addPersistentProjectDirFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&projectDir, "directory", "C", "", projectDirUsage)
}

// absoluteWorkingDir returns the directory the project-scope commands work
// against as an absolute path: the -C directory when one was given, and the
// caller's working directory otherwise.
//
// A -C path is checked here rather than left to the first file the command
// opens, so a mistyped directory is reported as such instead of as a missing
// manifest. The working directory is not checked: the process is already in
// it.
func absoluteWorkingDir() (string, error) {
	if projectDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}

		return filepath.Abs(dir)
	}

	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("-C %s: %w", projectDir, err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("-C %s: not a directory", projectDir)
	}

	return abs, nil
}
