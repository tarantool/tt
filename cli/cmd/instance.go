package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
	"golang.org/x/term"
)

// instanceCmdName is the name of the tt instance group.
const instanceCmdName = "instance"

var (
	// errInstancesFailed is wrapped by the error of an instance command that
	// failed for every instance it acted on, and by the error of one that
	// failed for some of them, which ends tt with sdk.ExitPartial.
	errInstancesFailed = errors.New("failed")
	// errNoAnswer is wrapped by the error of a confirmation that got no
	// answer: one that cannot be asked, since the standard input is not a
	// terminal, and one the terminal closed.
	errNoAnswer = errors.New("no answer to the confirmation")
	// errNotTerminal is wrapped by the error of a confirmation asked with
	// no terminal on the standard input.
	errNotTerminal = errors.New("the standard input is not a terminal")
)

// stdinIsTerminal reports whether the standard input is a terminal, where a
// confirmation can be asked.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(syscall.Stdin)
}

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
			"the application, \"default\" unless --template names another.\n\n" +
			"A command that acts on several instances goes on after a failure of one " +
			"of them and exits 3 when it failed for some of them only. " +
			"What the exit codes mean is in tt help errors.",
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
		"confirm without a prompt, as is needed when the standard input is not a terminal")
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
// is confirmed already.
//
// The confirmation is asked on a terminal only (RFC 4.3). Without one on the
// standard input there is no one to answer, and an answer piped in is not
// the user's: the command fails at once, pointing at --yes, and does not read
// the standard input. A confirmation the terminal closes fails the same way.
func confirm(question string) (bool, error) {
	if instanceYes {
		return true, nil
	}

	if !stdinIsTerminal() {
		return false, fmt.Errorf("%w: %w; pass --yes to confirm without a prompt",
			errNoAnswer, errNotTerminal)
	}

	confirmed, err := util.AskConfirm(os.Stdin, question)
	if err != nil {
		return false, fmt.Errorf("%w: %w; pass --yes to confirm without a prompt",
			errNoAnswer, err)
	}

	return confirmed, nil
}

// actOnInstances runs act on every instance, in order, and goes on after a
// failure: one instance that cannot be stopped must not keep the others
// running. what names the action, such as "stop". The outcome is that of the
// command as a whole (instancesResult).
func actOnInstances(what string, instances []running.InstanceCtx,
	act func(inst *running.InstanceCtx) error,
) error {
	result := newInstancesResult(what, len(instances))

	for i := range instances {
		err := act(&instances[i])
		if err != nil {
			result.fail(running.GetAppInstanceName(instances[i]), err)
		}
	}

	return result.err()
}

// instancesResult collects the failures of an instance command that acts on
// several instances one by one, and makes up the outcome of the command:
//
//   - nil when the command succeeded for every instance;
//   - the error of the one instance, as it failed, when the command acted on
//     one instance: it is the error of the command, with its code;
//   - an error with sdk.ExitPartial when the command failed for some of the
//     instances, which tells a script that some went through;
//   - an error with sdk.ExitFailure when it failed for every instance.
//
// The failure of an instance among several is logged as it happens, naming
// the instance unless the error names it already, and unless it is marked
// with exitcode.Silent as reported already; the error of the command sums
// them up.
type instancesResult struct {
	// what names the action, such as "stop".
	what string
	// total is the number of instances the command acts on.
	total  int
	failed []string
	// only is the failure of the one instance the command acts on.
	only error
}

func newInstancesResult(what string, total int) *instancesResult {
	return &instancesResult{what: what, total: total, failed: nil, only: nil}
}

// fail records err, the failure of the instance name.
func (result *instancesResult) fail(name string, err error) {
	result.failed = append(result.failed, name)

	if result.total == 1 {
		result.only = err

		return
	}

	switch {
	case exitcode.IsSilent(err):
	case strings.Contains(err.Error(), name):
		log.Error(err.Error())
	default:
		log.Errorf("%s: %s", name, err)
	}
}

// failAll records err, one failure of all the instances names, such as one
// that stops them from starting before any does. It is logged once.
func (result *instancesResult) failAll(names []string, err error) {
	if len(names) == 0 {
		return
	}

	result.failed = append(result.failed, names...)

	if result.total == 1 {
		result.only = err

		return
	}

	if !exitcode.IsSilent(err) {
		log.Errorf("%s: %s", strings.Join(names, ", "), err)
	}
}

// err returns the outcome of the command.
func (result *instancesResult) err() error {
	if len(result.failed) == 0 {
		return nil
	}

	if result.total == 1 {
		return result.only
	}

	code := sdk.ExitPartial
	if len(result.failed) == result.total {
		code = sdk.ExitFailure
	}

	return sdk.Errorf(code, "%s %w for %d of %d instances: %s", result.what,
		errInstancesFailed, len(result.failed), result.total, strings.Join(result.failed, ", "))
}
