package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	forceRemove       bool
	verboseList       bool
	ErrCanceledByUser = errors.New("canceled by user")
)

// NewCleanCmd creates clean command.
func NewCleanCmd() *cobra.Command {
	cleanCmd := &cobra.Command{
		Use:   "clean " + instanceTargetsUsage,
		Short: "Clean instance(s) files",
		RunE:  internalRunE(internalCleanModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				&cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	cleanCmd.Flags().BoolVarP(&forceRemove, "force", "f", false, "do not ask for confirmation")
	cleanCmd.Flags().BoolVarP(&verboseList, "verbose", "V", false,
		"list all files to remove before confirmation")

	return cleanCmd
}

func collectFiles(files map[string]bool, dirname string) (map[string]bool, uint32, error) {
	var nFiles uint32

	err := filepath.Walk(dirname,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				files[path] = true
				nFiles++
			}

			return nil
		})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to collect files in %q: %w", dirname, err)
	}

	return files, nFiles, nil
}

func printCleanFiles(files map[string]bool, dir string) {
	for file := range files {
		relativePath, err := filepath.Rel(dir, file)
		if err != nil || !filepath.IsLocal(relativePath) {
			continue
		}

		if relativePath != "." {
			relativePath = "." + string(filepath.Separator) + relativePath
		}

		_, _ = fmt.Fprintf(os.Stdout, "\nto remove %q", relativePath)
	}
}

func clean(run *running.InstanceCtx) error {
	removeFiles := map[string]bool{}
	nFilesPerDir := map[string]uint32{}
	confirm := false

	var (
		nFiles uint32
		err    error
	)

	// An external process has one data directory at most, DataDir, the one it
	// runs in without workdir.
	for _, dir := range [...]string{
		run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir, run.DataDir,
	} {
		if dir == "" {
			continue
		}

		removeFiles, nFiles, err = collectFiles(removeFiles, dir)
		if err != nil {
			return err
		}

		nFilesPerDir[dir] = nFiles
	}

	if !forceRemove {
		_, _ = fmt.Fprintf(os.Stdout, "\nTotal files to remove: %d", len(removeFiles))
		if len(removeFiles) == 0 {
			return nil
		}

		for path, nF := range nFilesPerDir {
			_, _ = fmt.Fprintf(os.Stdout, "\nto remove %d file(s) in %q", nF, path)

			if !verboseList {
				continue
			}

			printCleanFiles(removeFiles, path)
		}

		confirm, err = util.AskConfirm(os.Stdin, "\nConfirm")
		if err != nil {
			return err
		}
	}

	if confirm || forceRemove {
		for file := range removeFiles {
			err = os.Remove(file)
			if err != nil {
				return fmt.Errorf("failed to remove %q: %w", file, err)
			}

			log.Debugf("removed %q", file)
		}

		return nil
	}

	return ErrCanceledByUser
}

// internalCleanModule is a default clean module.
func internalCleanModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	for _, run := range instances {
		status := running.Status(&run)
		if status.Code == process_utils.ProcessStoppedCode {
			var statusMsg string

			err := clean(&run)
			switch {
			case errors.Is(err, ErrCanceledByUser):
				statusMsg = ErrCanceledByUser.Error()
			case err != nil:
				statusMsg = "[ERR] " + err.Error()
			default:
				statusMsg = "[OK]"
			}

			log.Infof("%s%c%s...\t%s", run.AppName, running.InstanceDelimiter, run.InstName,
				statusMsg)
		} else {
			log.Infof("instance `%s` must be stopped", run.InstName)
		}
	}

	return nil
}
