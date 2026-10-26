package cmd

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewCheckCmd creates a new check command.
func NewCheckCmd() *cobra.Command {
	checkCmd := &cobra.Command{
		Use:   "check " + instanceTargetsUsage,
		Short: "Check an application file for syntax errors",
		Long: "Check the Lua file each instance runs, its script or app.file of its\n" +
			"config, for syntax errors, with the tarantool the instance runs on.",
		RunE: internalRunE(internalCheckModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(checkCmd)

	return checkCmd
}

// internalCheckModule is a default check module: it checks the application
// file of every instance the targets select, each file once: the script of
// an instance that runs one, as tt2 did, or app.file of its config. An
// instance whose config names no app.file has nothing of its own to check,
// and neither has an external process, whose binary tt knows nothing about.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	checked := map[string]bool{}

	for _, inst := range instances {
		if running.IsExternal(&inst) {
			log.Infof("%s: an external process (binary: %s) has nothing to check",
				running.GetAppInstanceName(inst), inst.Binary)

			continue
		}

		file, found, err := running.AppFile(&inst)
		if err != nil {
			return err
		}

		if !found {
			log.Infof("%s: the config names no application file (app.file) to check",
				running.GetAppInstanceName(inst))

			continue
		}

		if checked[file] {
			continue
		}

		checked[file] = true

		err = running.Check(context.Background(), &inst, file)
		if err != nil {
			return err
		}

		log.Infof("Result of check: syntax of file '%s' is OK", file)
	}

	return nil
}
