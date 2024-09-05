package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

var forceRemove bool
var ErrCanceledByUser = errors.New("canceled by user")

// NewCleanCmd creates clean command.
func NewCleanCmd() *cobra.Command {
	var cleanCmd = &cobra.Command{
		Use:   "clean [INSTANCE_NAME]",
		Short: "Clean instance(s) files",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalCleanModule,
				args)
			util.HandleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	cleanCmd.Flags().BoolVarP(&forceRemove, "force", "f", false, "do not ask for confirmation")

	return cleanCmd
}

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
		return nil, err
	}

	return files, nil
}

func clean(run *running.InstanceCtx) error {
	removeFiles := map[string]bool{}
	confirm := false
	var err error

	for _, dir := range [...]string{run.LogDir, run.WalDir, run.VinylDir, run.MemtxDir} {
		removeFiles, err = collectFiles(removeFiles, dir)
		if err != nil {
			return err
		}
	}

	if !forceRemove {
		confirm, err = util.AskConfirm(os.Stdin, "\nConfirm")
		if err != nil {
			return err
		}
	}

	if confirm || forceRemove {
		for file, _ := range removeFiles {
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
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
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
