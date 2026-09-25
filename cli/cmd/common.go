package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/modules"
	"github.com/tarantool/tt/v3/cli/util"
)

// errNoConfig is returned if environment config file tt.yaml not found.
var errNoConfig = errors.New(configure.ConfigName +
	" not found, you need to create a tt environment config" +
	" or provide exact config location with --cfg option")

// isConfigExist returns `true` if environment config file tt.yaml exist.
func isConfigExist(cmdCtx *cmdcontext.CmdCtx) bool {
	return cmdCtx.Cli.ConfigPath != ""
}

// RunModuleFuncE returns a cobra RunE that runs the command through
// modules.RunCmd, so that an external module can take the place of the
// internal implementation. The error goes back to the root, which reports it
// and exits with its code.
func RunModuleFuncE(internalModule modules.InternalFunc) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cmdCtx.CommandName = cmd.Name()

		err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalModule, args)

		return commandError(cmd, err)
	}
}

// RunModuleFunc is RunModuleFuncE for a command declared with Run. Having no
// way to return the error to the root, it reports it and exits the way the
// root would.
// TT-EE.
func RunModuleFunc(internalModule modules.InternalFunc) func(*cobra.Command, []string) {
	runE := RunModuleFuncE(internalModule)

	return func(cmd *cobra.Command, args []string) {
		if err := runE(cmd, args); err != nil {
			os.Exit(reportError(cmd, err))
		}
	}
}

// commandError hands err, returned by cmd's own code, to the root. cobra is
// told to print neither the error nor the usage: the root reports the error,
// and prints the usage only for an ArgError.
func commandError(cmd *cobra.Command, err error) error {
	if err != nil {
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
	}

	return err
}

// reportError tells the user about err, the error cmd failed with, and returns
// the process exit code for it: exitcode.Code, the codes `tt help errors`
// describes, so a failure of the system the command ran on exits 2 from any
// command even when nothing on its way up said so.
//
// An error cobra detected itself - an unknown flag, a wrong number of
// arguments - has already been printed by cobra, with the usage, so it is
// not printed again. An error from the command's own code is logged once;
// an ArgError or an sdk.UsageError is logged by its own message and followed
// by cmd's usage.
func reportError(cmd *cobra.Command, err error) int {
	var (
		argError   *util.ArgError
		usageError *sdk.UsageError
	)

	switch {
	case !cmd.SilenceErrors:
	case errors.As(err, &argError):
		log.Error(argError.Error())

		_ = cmd.Usage()
	case errors.As(err, &usageError):
		log.Error(usageError.Error())

		_ = cmd.Usage()
	default:
		exitcode.Report(err)
	}

	return exitcode.Code(err)
}
