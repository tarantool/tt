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

// report returns what the user is told: the error, then where a command of
// tt 2 went or the commands of the parent whose names are close to the
// typed one, then where the usage is. The usage itself is not printed: a
// whole command list buries the one line that matters.
func (e *unknownCommandError) report() string {
	var report strings.Builder

	report.WriteString(e.Error())

	// cobra's own default, which it sets only when it suggests itself.
	if e.parent.SuggestionsMinimumDistance <= 0 {
		e.parent.SuggestionsMinimumDistance = 2
	}

	if hint, ok := movedCommandHint(e.parent, e.name); ok {
		report.WriteString("\n" + hint)
	} else if suggestions := e.parent.SuggestionsFor(e.name); len(suggestions) > 0 {
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

// takesArgs reports whether command declares positional arguments of its
// own, by an Args validator or by ValidArgs: an argument of it is not
// meant as a subcommand, so it is not an unknown one either.
func takesArgs(command *cobra.Command) bool {
	return command.Args != nil || len(command.ValidArgs) > 0 ||
		command.ValidArgsFunction != nil
}

// rejectUnknownSubcommands gives groupRunE to every command under root that
// has subcommands, nothing to run of its own and no positional arguments of
// its own, and marks it with groupAnnotation: its usage has no line of its
// own to run it by. cobra prints the help of such a command for any
// argument and exits 0, which lets a mistyped subcommand pass a script
// unnoticed.
func rejectUnknownSubcommands(root *cobra.Command) {
	for _, command := range root.Commands() {
		if command.HasSubCommands() && !command.Runnable() && !takesArgs(command) {
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

// Hints of movedCommands that more than one command shares; %s is the
// command typed.
const (
	instanceHint = "tt 3 manages instances with tt instance %s"
	binaryHint   = "tt 3 manages Tarantool, tt and tcm with tt binary %s"
	dataHint     = "tt 3 reads and replays snapshots and write-ahead logs with tt data %s"
	logsHint     = "tt 3 shows the logs of instances with tt instance logs"
)

// movedCommands are the hints for the top-level commands of tt 2 that tt 3
// has not, by name: where the command went, or what replaces it, as
// doc/migration_from_older_versions.md tells. A %s in a hint is the command
// typed.
//
//nolint:gochecknoglobals // A constant table, of the names tt 2 had.
var movedCommands = map[string]string{
	"start":     instanceHint,
	"stop":      instanceHint,
	"restart":   instanceHint,
	"status":    instanceHint,
	"kill":      instanceHint,
	"logrotate": instanceHint,
	"check":     instanceHint,
	"clean":     instanceHint,
	"log":       logsHint,
	"logs":      logsHint,
	"instances": "tt 3 lists instances with tt instance status, " +
		"and installed applications with tt package list --scope system",
	"cat":       dataHint,
	"play":      dataHint,
	"build":     "tt 3 builds packages with tt package build",
	"pack":      "tt 3 packs packages into .tt archives with tt package pack",
	"init":      "tt 3 creates a package with tt new",
	"install":   binaryHint,
	"uninstall": binaryHint,
	"search":    binaryHint,
	"binaries":  "tt 3 lists and switches versions with tt binary list and tt binary switch",
	"download":  "tt 3 installs the Tarantool SDK with tt binary install tarantool-sdk",
	"backup-id": "tt 3 prints the identifier of a new backup with tt backup id",
	"env": "tt env is gone in tt 3: source .env/activate.sh, " +
		"which the first tt binary install in a project creates",
	"cfg": "tt cfg is gone in tt 3, as tt.yaml is: " +
		"tt registry list shows the rock servers",
	"enable": "tt enable is gone in tt 3: projects are found from the current " +
		"directory, and tt package install installs applications",
	"cartridge": "tt 3 does not support Cartridge",
}

// movedCommandHint returns the hint of movedCommands for name, typed in
// place of a command of parent. ok is false for a parent other than the
// root and for a name tt 2 did not have either.
func movedCommandHint(parent *cobra.Command, name string) (string, bool) {
	hint, ok := movedCommands[name]
	if parent.HasParent() || !ok {
		return "", false
	}

	if strings.Contains(hint, "%s") {
		hint = fmt.Sprintf(hint, name)
	}

	return hint, true
}
