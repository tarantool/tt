package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
)

// errInstanceRunning is wrapped by the error for an instance
// tt instance clean leaves alone because it runs.
var errInstanceRunning = errors.New("must be stopped")

var verboseList bool

// cleanDirKinds is the number of the kinds of directory clean removes the
// files of: the log, WAL, vinyl, memtx and data directories.
const cleanDirKinds = 5

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
	if missing(dirname) {
		return files, nil
	}

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

// missing reports whether there is no directory entry at path.
func missing(path string) bool {
	_, err := os.Lstat(path)

	return errors.Is(err, fs.ErrNotExist)
}

// cleanDirs returns the directories tt instance clean removes the files of:
// the log and the data directories of run, each once. An external process has
// one data directory at most, DataDir, the one it runs in without workdir.
func cleanDirs(run *running.InstanceCtx) []string {
	dirs := make([]string, 0, cleanDirKinds)

	for _, dir := range [...]string{
		run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir, run.DataDir,
	} {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// cleanGroup is what clean removes from dir: the files under it that no
// directory before it in cleanDirs holds, sorted.
type cleanGroup struct {
	dir   string
	files []string
}

// cleanGroups returns the files clean is going to remove from run, by
// directory: a file of a directory nested in another one counts once.
func cleanGroups(run *running.InstanceCtx) ([]cleanGroup, error) {
	seen := map[string]bool{}

	var groups []cleanGroup

	for _, dir := range cleanDirs(run) {
		files, err := collectFiles(map[string]bool{}, dir)
		if err != nil {
			return nil, err
		}

		group := cleanGroup{dir: dir, files: nil}

		for file := range files {
			if !seen[file] {
				seen[file] = true
				group.files = append(group.files, file)
			}
		}

		if len(group.files) == 0 {
			continue
		}

		slices.Sort(group.files)

		groups = append(groups, group)
	}

	return groups, nil
}

// printCleanFiles lists the files of group, relative to its directory.
func printCleanFiles(group cleanGroup) {
	for _, file := range group.files {
		relativePath, err := filepath.Rel(group.dir, file)
		if err == nil {
			relativePath = "." + string(filepath.Separator) + relativePath
		} else {
			relativePath = file
		}

		_, _ = fmt.Fprintf(os.Stdout, "to remove %q\n", relativePath)
	}
}

// previewClean prints what clean is going to remove from the stopped
// instances, by instance and directory, every file with --verbose, and the
// total, and returns the total. A running instance is left alone, so it has
// nothing in the preview.
func previewClean(instances []running.InstanceCtx) (int, error) {
	total := 0

	for i := range instances {
		run := &instances[i]
		if running.Status(run).Code != process_utils.ProcessStoppedCode {
			continue
		}

		groups, err := cleanGroups(run)
		if err != nil {
			return 0, err
		}

		if len(groups) == 0 {
			continue
		}

		_, _ = fmt.Fprintf(os.Stdout, "%s:\n", running.GetAppInstanceName(*run))

		for _, group := range groups {
			total += len(group.files)

			_, _ = fmt.Fprintf(os.Stdout, "to remove %d file(s) in %q\n",
				len(group.files), group.dir)

			if verboseList {
				printCleanFiles(group)
			}
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "Total files to remove: %d\n", total)

	return total, nil
}

// clean removes the files of the log and the data directories of run.
func clean(run *running.InstanceCtx) error {
	removeFiles := map[string]bool{}

	var err error

	for _, dir := range cleanDirs(run) {
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

// confirmClean shows what clean is going to remove from instances, the
// targets args select, and asks whether to. The preview is what the question
// is about: with --yes there is no question and no preview, and with nothing
// to remove there is nothing to ask about.
func confirmClean(instances []running.InstanceCtx, args []string) (bool, error) {
	if instanceYes {
		return true, nil
	}

	total, err := previewClean(instances)
	if err != nil {
		return false, err
	}

	if total == 0 {
		return true, nil
	}

	return confirm("Confirm clean of " + describeTargets(args))
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

	confirmed, err := confirmClean(instances, args)
	if err != nil {
		return err
	}

	if !confirmed {
		log.Info("Clean is cancelled.")

		return nil
	}

	return actOnInstances("clean", instances, func(run *running.InstanceCtx) error {
		status := running.Status(run)
		if status.Code != process_utils.ProcessStoppedCode {
			log.Infof("instance `%s` must be stopped", run.InstName)

			return exitcode.Silent(fmt.Errorf("instance %s %w",
				running.GetAppInstanceName(*run), errInstanceRunning))
		}

		err := clean(run)
		if err != nil {
			log.Infof("%s%c%s...\t[ERR] %s", run.AppName, running.InstanceDelimiter,
				run.InstName, err)

			return exitcode.Silent(err)
		}

		log.Infof("%s%c%s...\t[OK]", run.AppName, running.InstanceDelimiter, run.InstName)

		return nil
	})
}
