package internal

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/running"
)

// InstPicker is function that takes a list of instances and returns
// some instance names.
type InstPicker func([]running.InstanceCtx) []string

// ValidArgsFunction is the function used for dynamic auto-completion.
// In case of app's completion, it uses appPicker, instPicker otherwise.
func ValidArgsFunction(
	cliOpts *config.CliOpts,
	cmdCtx *cmdcontext.CmdCtx,
	cmd *cobra.Command,
	toComplete string,
	appPicker InstPicker,
	instPicker InstPicker,
) (args []string, directive cobra.ShellCompDirective) {
	directive = cobra.ShellCompDirectiveNoFileComp

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, nil, false); err != nil {
		return
	}

	if strings.ContainsRune(toComplete, running.InstanceDelimiter) {
		args = instPicker(runningCtx.Instances)
		return
	}

	args = appPicker(runningCtx.Instances)
	return
}
