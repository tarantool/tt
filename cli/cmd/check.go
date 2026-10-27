package cmd

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewCheckCmd creates the check command of tt instance.
func NewCheckCmd() *cobra.Command {
	checkCmd := &cobra.Command{
		Use:   "check " + instanceTargetsUsage,
		Short: "Check an application file for syntax errors",
		Long: "Check the Lua file each instance runs, its script or app.file of its\n" +
			"config, for syntax errors, with the tarantool the instance runs on, and\n" +
			"that the binary of each external process is found.",
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
// instance whose config names no app.file has nothing of its own to check.
// An external process has its binary found, as its start would find it, in
// .rocks/bin/ of the application or in PATH: tt knows nothing else about it.
//
// An instance whose file has a syntax error fails, and so does every other
// instance that runs the same file; the error is reported once. So does an
// instance its tarantool cannot run, as a config kept in a config storage
// on Tarantool Community Edition (running.CheckTarantool), which would only
// tell after the start.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	checked := map[string]error{}

	return actOnInstances("check", instances, func(inst *running.InstanceCtx) error {
		if running.IsExternal(inst) {
			path, err := running.ResolveBinary(inst)
			if err != nil {
				return err
			}

			log.Infof("%s: an external process, its binary %s is found",
				running.GetAppInstanceName(*inst), path)

			return nil
		}

		err := running.CheckTarantool(context.Background(), inst)
		if err != nil {
			return err
		}

		file, found, err := running.AppFile(inst)
		if err != nil {
			return err
		}

		if !found {
			log.Infof("%s: the config names no application file (app.file) to check",
				running.GetAppInstanceName(*inst))

			return nil
		}

		if err, done := checked[file]; done {
			return exitcode.Silent(err)
		}

		err = running.Check(context.Background(), inst, file)
		checked[file] = err

		if err != nil {
			return err
		}

		log.Infof("Result of check: syntax of file '%s' is OK", file)

		return nil
	})
}
