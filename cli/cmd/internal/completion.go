package internal

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// InstPicker is function that takes a list of instances and returns
// some instance names.
type InstPicker func([]running.InstanceCtx) []string

// ValidArgsFunction is the function used for dynamic auto-completion.
// In case of app's completion, it uses appPicker, instPicker otherwise.
//
// The instances come from the application resolver, as the commands find
// them: those of the project the command runs in, or, once the application
// is typed, those of the application named before the colon. A failure to
// resolve completes nothing.
func ValidArgsFunction(
	cmdCtx *cmdcontext.CmdCtx,
	cmd *cobra.Command,
	toComplete string,
	appPicker InstPicker,
	instPicker InstPicker,
) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp

	var targets []app.Target

	appName, _, typed := strings.Cut(toComplete, string(running.InstanceDelimiter))
	if typed {
		targets = []app.Target{{App: appName, Instance: app.AllInstances}}
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	instances, err := running.Collect(ctx, targets,
		app.Options{Scope: "", Template: "", Start: "", Dir: "", Integrity: cmdCtx.Integrity})
	if err != nil {
		return nil, directive
	}

	if typed {
		return instPicker(instances), directive
	}

	return appPicker(instances), directive
}
