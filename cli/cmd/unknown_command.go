package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// unknownCommandError is the error of a command line that names, where a
// command of parent goes, a command parent does not have.
type unknownCommandError struct {
	// parent is the command whose subcommand was asked for.
	parent *cobra.Command
	// name is what was typed in place of the subcommand.
	name string
}

// Error returns the first line of the report, the one cobra writes too.
func (e *unknownCommandError) Error() string {
	return fmt.Sprintf("unknown command %q for %q", e.name, e.parent.CommandPath())
}

// report returns what the user is told: the error, then the commands of the
// parent whose names are close to the typed one, then where the usage is.
// The usage itself is not printed: a whole command list buries the one line
// that matters.
func (e *unknownCommandError) report() string {
	var report strings.Builder

	report.WriteString(e.Error())

	// cobra's own default, which it sets only when it suggests itself.
	if e.parent.SuggestionsMinimumDistance <= 0 {
		e.parent.SuggestionsMinimumDistance = 2
	}

	if suggestions := e.parent.SuggestionsFor(e.name); len(suggestions) > 0 {
		report.WriteString("\nDid you mean this?")

		for _, suggestion := range suggestions {
			report.WriteString("\n    " + suggestion)
		}
	}

	fmt.Fprintf(&report, "\nRun '%s --help' for usage.", e.parent.CommandPath())

	return report.String()
}

// groupRunE is the RunE of a command that only groups subcommands: alone it
// prints its help, and with an argument, which cobra did not find a
// subcommand of that name for, it fails with an unknownCommandError.
func groupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help() //nolint:wrapcheck // A failed write of the help, as cobra's.
	}

	return commandError(cmd, &unknownCommandError{parent: cmd, name: args[0]})
}

const (
	// groupAnnotation is the key of the annotation that marks a command
	// rejectUnknownSubcommands gave groupRunE. Its value is groupMarked.
	groupAnnotation = "tt.core/group"
	// groupMarked is the value of groupAnnotation.
	groupMarked = "true"
)

// rejectUnknownSubcommands gives groupRunE to every command under root that
// has subcommands and nothing to run of its own, and marks it with
// groupAnnotation: its usage has no line of its own to run it by. cobra
// prints the help of such a command for any argument and exits 0, which
// lets a mistyped subcommand pass a script unnoticed.
func rejectUnknownSubcommands(root *cobra.Command) {
	for _, command := range root.Commands() {
		if command.HasSubCommands() && !command.Runnable() {
			command.RunE = groupRunE

			if command.Annotations == nil {
				command.Annotations = map[string]string{}
			}

			command.Annotations[groupAnnotation] = groupMarked
		}

		rejectUnknownSubcommands(command)
	}
}

// isGroup reports whether command carries groupAnnotation.
func isGroup(command *cobra.Command) bool {
	return command.Annotations[groupAnnotation] == groupMarked
}

// runsItself reports whether command runs on its own, not only as the
// group of its subcommands: its usage has a line to run it by.
func runsItself(command *cobra.Command) bool {
	return command.Runnable() && !isGroup(command)
}
