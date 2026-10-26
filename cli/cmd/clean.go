package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
)

var verboseList bool

// NewCleanCmd creates the clean command of tt instance.
func NewCleanCmd() *cobra.Command {
	cleanCmd := &cobra.Command{
		Use:   "clean " + instanceTargetsUsage,
		Short: "Clean instance(s) files",
		Long: "Remove the data and the logs of the instances: the files of " +
			"<root>/var/lib/<app>/<instance>/ and <root>/var/log/<app>/<instance>/, " +
			"and of the data directories their config moves elsewhere. A running " +
			"instance is left alone.",
		RunE: internalRunE(internalCleanModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(cleanCmd)
	addYesFlag(cleanCmd)
	cleanCmd.Flags().BoolVarP(&verboseList, "verbose", "V", false,
		"list all files to remove before confirmation")

	return cleanCmd
}

// collectFiles adds the files under dirname to files. A directory that is
// not there holds nothing to remove: an instance that never ran has none.
func collectFiles(files map[string]bool, dirname string) (map[string]bool, error) {

	err := filepath.Walk(dirname,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				files[path] = true
			}

			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("failed to collect files in %q: %w", dirname, err)
	}

	return files, nil
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

// previewClean lists the files before the command asks for confirmation.
func previewClean(run *running.InstanceCtx) error {
	files := map[string]bool{}
	counts := map[string]int{}

	for _, dir := range []string{run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir, run.DataDir} {
		if dir == "" {
			continue
		}

		entries, err := collectFiles(map[string]bool{}, dir)
		if err != nil {
			return err
		}

		counts[dir] = len(entries)

		for file := range entries {
			files[file] = true
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nTotal files to remove: %d", len(files))

	for dir, count := range counts {
		_, _ = fmt.Fprintf(os.Stdout, "\nto remove %d file(s) in %q", count, dir)

		if verboseList {
			printCleanFiles(files, dir)
		}
	}

	return nil
}

// clean removes the files of the log and the data directories of run.
func clean(run *running.InstanceCtx) error {
	removeFiles := map[string]bool{}

	var err error

	// An external process has one data directory at most, DataDir, the one it
	// runs in without workdir.
	for _, dir := range [...]string{
		run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir, run.DataDir,
	} {
		if dir == "" {
			continue
		}

		removeFiles, err = collectFiles(removeFiles, dir)
		if err != nil {
			return err
		}
	}

	for file := range removeFiles {
		err = os.Remove(file)
		if err != nil {
			return fmt.Errorf("failed to remove %q: %w", file, err)
		}

		log.Debugf("removed %q", file)
	}

	return nil
}

// internalCleanModule is a default clean module. It asks once whether to
// clean what the targets select. A running instance is left alone and
// counts as failed: what the targets asked for is not done for it
// (actOnInstances).
func internalCleanModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	for _, run := range instances {
		if running.Status(&run).Code != process_utils.ProcessStoppedCode {
			continue
		}

		err = previewClean(&run)
		if err != nil {
			return err
		}
	}

	confirmed, err := confirm("Confirm clean of " + describeTargets(args))
	if err != nil {
		return err
	}

	if !confirmed {
		log.Info("Clean is cancelled.")

		return nil
	}

	for _, run := range instances {
		if running.Status(&run).Code != process_utils.ProcessStoppedCode {
			log.Infof("instance `%s` must be stopped", run.InstName)
			continue
		}
		statusMsg := "[OK]"
		if err := clean(&run); err != nil {
			statusMsg = "[ERR] " + err.Error()
		}
		log.Infof("%s%c%s...\t%s", run.AppName, running.InstanceDelimiter, run.InstName, statusMsg)
	}
	return nil
}
