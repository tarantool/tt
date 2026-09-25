package sdktest

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log/logtest"
)

// Result is what one run of a command left behind.
type Result struct {
	// Err is the error the command failed with, or the one it gave
	// Services.Exit; nil when it succeeded.
	Err error
	// Exited reports that the command ended through Services.Exit rather
	// than by returning. In tt that would have ended the process, reporting
	// Err and exiting with its code.
	Exited bool
	// Stdout is what was written to stdout.
	Stdout string
	// Stderr is what was written to stderr: prompts, and whatever the command
	// wrote there itself. tt's report of Err is not in it.
	Stderr string
	// Records are the records the module logged.
	Records []logtest.Record
}

// ExitCode returns the exit code tt would end with for Err, as far as the
// SDK decides it: sdk.ExitCode(Err).
func (r Result) ExitCode() int {
	return sdk.ExitCode(r.Err)
}

// Run runs a module's command in new Services: see [Services.Run].
func Run(tb testing.TB, ctor sdk.Constructor, args ...string) Result {
	tb.Helper()

	return New(tb).Run(ctor, args...)
}

// Run does what the core does with a module, in miniature. It calls ctor
// with s not yet started, hangs the commands it returns on a root named tt -
// creating the groups on their paths that no mount provides - starts s, and
// executes the command line args. The test fails on a mount the core would
// refuse: one without a command, with a command that already has a parent,
// or whose name or aliases clash with a command next to it.
//
// cobra prints neither the error nor the usage: the error is in the result
// and reporting it is the core's job. A command that calls Services.Exit
// ends there, and the result has Exited set and the error Exit was given.
func (s *Services) Run(ctor sdk.Constructor, args ...string) Result {
	s.tb.Helper()

	if s.started.Load() {
		s.tb.Fatalf("sdktest: Run needs Services that are not started yet; " +
			"the constructor runs before tt is configured")
	}

	root := &cobra.Command{Use: "tt", SilenceErrors: true, SilenceUsage: true}
	s.hang(root, ctor(s))
	s.Start()

	root.SetArgs(append([]string{}, args...))
	root.SetIn(s.opts.stdin)
	root.SetOut(&s.stdout)
	root.SetErr(&s.stderr)

	exited, err := execute(root)

	return Result{
		Err:     err,
		Exited:  exited,
		Stdout:  s.Stdout(),
		Stderr:  s.Stderr(),
		Records: s.Records(),
	}
}

// execute executes root and returns the error it failed with. When the
// command called Services.Exit it returns true and the error Exit was given;
// any other panic goes on.
func execute(root *cobra.Command) (bool, error) {
	var exit *exitError

	err := func() error {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			var ok bool

			exit, ok = recovered.(*exitError)
			if !ok {
				panic(recovered)
			}
		}()

		return root.Execute()
	}()

	if exit != nil {
		return true, exit.err
	}

	return false, err
}

// hang adds mounts to root, the shallowest first so that a group a module
// provides is in place before the commands it mounts under it.
func (s *Services) hang(root *cobra.Command, mounts []sdk.Mount) {
	s.tb.Helper()

	sorted := slices.Clone(mounts)
	slices.SortStableFunc(sorted, func(a, b sdk.Mount) int {
		return cmp.Compare(len(strings.Fields(a.Path)), len(strings.Fields(b.Path)))
	})

	for _, mount := range sorted {
		if mount.Cmd == nil {
			s.tb.Fatalf("sdktest: mount under %q has no command", mount.Path)
		}

		if mount.Cmd.HasParent() {
			s.tb.Fatalf("sdktest: command %q already has a parent %q",
				mount.Cmd.Name(), mount.Cmd.Parent().CommandPath())
		}

		parent := root
		for name := range strings.FieldsSeq(mount.Path) {
			parent = child(parent, name)
		}

		for _, name := range append([]string{mount.Cmd.Name()}, mount.Cmd.Aliases...) {
			if clash := named(parent, name); clash != nil {
				s.tb.Fatalf("sdktest: command %q clashes with %q",
					parent.CommandPath()+" "+mount.Cmd.Name(), clash.CommandPath())
			}
		}

		parent.AddCommand(mount.Cmd)
	}
}

// child returns the subcommand of parent named name, created as a group with
// no description when there is none.
func child(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}

	group := &cobra.Command{Use: name}
	parent.AddCommand(group)

	return group
}

// named returns the subcommand of parent called name by its name or an
// alias, or nil.
func named(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name || slices.Contains(sub.Aliases, name) {
			return sub
		}
	}

	return nil
}
