package internal

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// InstanceFilter keeps the instances a command completes: those it can act
// on, such as the running ones for tt instance stop.
type InstanceFilter func(inst *running.InstanceCtx) bool

// The filters of the commands that complete instances.
var (
	// Any keeps every instance.
	Any InstanceFilter = func(*running.InstanceCtx) bool { return true }
	// Active keeps the running instances.
	Active InstanceFilter = running.IsInstanceActive
	// Inactive keeps the instances that do not run.
	Inactive InstanceFilter = running.IsInstanceInactive
)

// The flags of the command line being completed that the resolver takes,
// those of tt instance and of every other command that takes an
// application.
const (
	scopeFlag    = sdk.ScopeFlag
	templateFlag = sdk.TemplateFlag
)

// ValidArgsFunction completes the target of an instance command in the one
// form the usage and the errors give it, <APP>:<INSTANCE>, in the item of a
// comma-separated list being typed:
//
//   - before the colon, the applications a target may name, each followed by
//     the colon: the project the command runs in and the applications of the
//     system scope, or those of the scope --scope names when it is typed
//     already (app.Names);
//   - after it, the instances of the pair of the application named, found as
//     the command finds them, with --scope and --template, that filter keeps.
//
// The selector * is not offered: a shell would expand it as a pattern. A
// failure to resolve completes nothing.
func ValidArgsFunction(
	cmdCtx *cmdcontext.CmdCtx,
	cmd *cobra.Command,
	toComplete string,
	filter InstanceFilter,
) ([]string, cobra.ShellCompDirective) {
	return completeTarget(cmdCtx, cmd, toComplete, filter, true)
}

// ClusterValidArgsFunction completes the target of a cluster-level command,
// such as tt cluster show: an application, offered as it is, since a bare
// name is a target there, or one of its instances that filter keeps, as
// ValidArgsFunction offers them.
func ClusterValidArgsFunction(
	cmdCtx *cmdcontext.CmdCtx,
	cmd *cobra.Command,
	toComplete string,
	filter InstanceFilter,
) ([]string, cobra.ShellCompDirective) {
	return completeTarget(cmdCtx, cmd, toComplete, filter, false)
}

// completeTarget completes a target as ValidArgsFunction says, offering an
// application followed by the colon when instanceLevel is set, and bare
// otherwise.
func completeTarget(
	cmdCtx *cmdcontext.CmdCtx,
	cmd *cobra.Command,
	toComplete string,
	filter InstanceFilter,
	instanceLevel bool,
) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp

	// The instances come from the description of the pair, and those still
	// running from their pid files: the config is read as far as it can be,
	// so that a config storage out of reach does not keep tt instance stop
	// from completing what it may stop.
	opts := app.Options{
		Scope:             "",
		Template:          flagValue(cmd, templateFlag),
		Start:             "",
		Dir:               "",
		Integrity:         cmdCtx.Integrity,
		BestEffortConfig:  true,
		SkipTopologyCheck: false,
	}

	scp, err := app.ParseScope(flagValue(cmd, scopeFlag))
	if err != nil {
		return nil, directive
	}

	opts.Scope = scp

	// The items of the list before the one being typed stay as they are.
	listed := ""
	if last := strings.LastIndexByte(toComplete, ','); last >= 0 {
		listed, toComplete = toComplete[:last+1], toComplete[last+1:]
	}

	appName, _, typed := strings.Cut(toComplete, string(running.InstanceDelimiter))
	if !typed {
		suffix := ""
		if instanceLevel {
			suffix = string(running.InstanceDelimiter)

			directive |= cobra.ShellCompDirectiveNoSpace
		}

		var completions []string

		for _, name := range app.Names(opts) {
			completions = append(completions, listed+name+suffix)
		}

		return completions, directive
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	instances, err := running.Collect(ctx,
		[]app.Target{{App: appName, Instance: app.AllInstances}}, opts)
	if err != nil {
		return nil, directive
	}

	var completions []string

	for i := range instances {
		if filter(&instances[i]) {
			completions = append(completions, listed+running.GetAppInstanceName(instances[i]))
		}
	}

	return completions, directive
}

// flagValue returns the value of the string flag name of cmd, empty when cmd
// has no such flag.
func flagValue(cmd *cobra.Command, name string) string {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		return ""
	}

	return flag.Value.String()
}
