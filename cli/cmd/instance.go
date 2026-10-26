package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

// instanceCmdName is the name of the tt instance group.
const instanceCmdName = "instance"

// errNoAnswer is wrapped by the error of a confirmation that got no
// answer, such as one asked with nothing on the standard input.
var errNoAnswer = errors.New("no answer to the confirmation")

// instanceFlags hold the flags that every command of tt instance takes, and
// tt connect, its facade, with them: where the application of a target is
// looked for, and the template of the pair it runs from.
var instanceFlags struct {
	scope    string
	template string
}

// instanceYes is --yes of the commands of tt instance that ask for a
// confirmation.
var instanceYes bool

// NewInstanceCmd creates the tt instance group: the commands that act on the
// processes of the instances of an application on this host.
func NewInstanceCmd() *cobra.Command {
	instanceCmd := &cobra.Command{
		Use:   instanceCmdName,
		Short: "Manage the instances of applications",
		Long: "Start, stop and inspect the instances of an application on this host.\n\n" +
			"A target is " + instanceTargetForms + ". An application is looked for in " +
			"the project the command runs in when the project has its name, and in " +
			"the system scope otherwise; --scope project, user or system picks the " +
			"scope outright. The instances are those of the pair of the application: " +
			"the host's <root>/etc/<app>/, or the template .templates/<template>/ of " +
			"the application, \"default\" unless --template names another.",
	}

	instanceCmd.AddCommand(
		NewStartCmd(),
		NewStopCmd(),
		NewRestartCmd(),
		NewKillCmd(),
		NewStatusCmd(),
		NewLogsCmd(),
		NewLogrotateCmd(),
		NewCheckCmd(),
		NewCleanCmd(),
		newConnectCmd(instanceCmdName+" connect"),
	)

	return instanceCmd
}

// instanceTargetForms names the forms of the target of an instance command
// in prose, as instanceTargetsUsage writes them in the usage.
const instanceTargetForms = "<APP>:<INSTANCE>, every instance of an application " +
	"<APP>:*, a comma-separated list of them, or none, which is every instance " +
	"of the project the command runs in"

// addInstanceFlags declares the flags every command of tt instance takes:
// --scope and --template.
func addInstanceFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&instanceFlags.scope, running.ScopeFlag, "",
		"scope the application is looked for in: project, user or system")
	cmd.Flags().StringVar(&instanceFlags.template, running.TemplateFlag, "",
		`template of the application the instances are described in (default "`+
			app.DefaultTemplate+`")`)

	_ = cmd.RegisterFlagCompletionFunc(running.ScopeFlag, cobra.FixedCompletions(
		[]string{
			app.ScopeProject.String(), app.ScopeUser.String(), app.ScopeSystem.String(),
		}, cobra.ShellCompDirectiveNoFileComp))
}

// addYesFlag declares --yes, which skips the confirmation of cmd.
func addYesFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&instanceYes, "yes", "y", false,
		"skip the confirmation prompt")
}

// instanceTargetOptions are the options an instance command resolves its
// targets with: instanceOptions, with the scope and the template its flags
// give.
func instanceTargetOptions(cmdCtx *cmdcontext.CmdCtx) (app.Options, error) {
	opts, err := instanceOptions(cmdCtx)
	if err != nil {
		return app.Options{}, err
	}

	opts.Scope, err = app.ParseScope(instanceFlags.scope)
	if err != nil {
		return app.Options{}, err
	}

	opts.Template = instanceFlags.template

	return opts, nil
}

// confirm asks the user to confirm what question asks, unless --yes says it
// is confirmed already. A confirmation that cannot be read, such as one with
// nothing on the standard input, is an error that points at --yes.
func confirm(question string) (bool, error) {
	if instanceYes {
		return true, nil
	}

	confirmed, err := util.AskConfirm(os.Stdin, question)
	if err != nil {
		return false, fmt.Errorf("%w: %w; pass --yes to confirm without a prompt",
			errNoAnswer, err)
	}

	return confirmed, nil
}
