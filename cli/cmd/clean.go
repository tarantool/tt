package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

var (
	forceRemove       bool
	verboseList       bool
	ErrCanceledByUser = errors.New("canceled by user")
)

// NewCleanCmd creates clean command.
func NewCleanCmd() *cobra.Command {
	cleanCmd := &cobra.Command{
		Use:   "clean [INSTANCE_NAME]",
		Short: "Clean instance(s) files",
		Run:   RunModuleFunc(internalCleanModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	cleanCmd.Flags().BoolVarP(&forceRemove, "force", "f", false, "do not ask for confirmation")
	cleanCmd.Flags().BoolVarP(&verboseList, "verbose", "V", false, "list all files to remove before confirmation")

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
		return nil, 0, err
	}

	return files, nFiles, nil
}

func clean(run *running.InstanceCtx) error {
	removeFiles := map[string]bool{}
	nFilesPerDir := map[string]uint32{}
	confirm := false
	var nFiles uint32
	var err error

	for _, dir := range [...]string{run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir} {
		removeFiles, nFiles, err = collectFiles(removeFiles, dir)
		if err != nil {
			return err
		}
		nFilesPerDir[dir] = nFiles
	}

	if !forceRemove {
		fmt.Printf("\nTotal files to remove: %d", len(removeFiles))
		if len(removeFiles) == 0 {
			return nil
		}

		for path, nF := range nFilesPerDir {
			fmt.Printf("\nto remove %d file(s) in %q", nF, path)
			if !verboseList {
				continue
			}
			for file := range removeFiles {
				if strings.HasPrefix(file, path) {
					fmt.Printf("\nto remove %q", "."+file[len(path):])
				}
			}
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
				return err
			}
			log.Debugf("removed %q", file)
		}

		return nil
	}

	return ErrCanceledByUser
}

// internalCleanModule is a default clean module.
func internalCleanModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadCluster)
	if err != nil {
		return err
	}

	for _, run := range runningCtx.Instances {
		status := running.Status(&run)
		if status.Code == process_utils.ProcessStoppedCode {
			var statusMsg string

			err := clean(&run)
			if errors.Is(err, ErrCanceledByUser) {
				statusMsg = ErrCanceledByUser.Error()
			} else if err != nil {
				statusMsg = "[ERR] " + err.Error()
			} else {
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
