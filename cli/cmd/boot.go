package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/version"
)

// The phases below initialise tt in the order it runs them: Boot, then the
// commands (BuiltinCommands and whatever else is added to the root), then
// InjectCommands, Configure, ConfigureHelp and Run. The core runs them, with
// its own steps in between. They are the core's interface to this package
// and are not stable.

// LegacyAnnotation is the key of the command annotation that marks a
// top-level command built by this package - a builtin, or one injected
// through InjectedCmds - rather than contributed by a module. Its value is
// "true".
const LegacyAnnotation = "tt.core/legacy"

// BootOptions configure Boot.
type BootOptions struct {
	// Args are the command-line arguments, without the program name.
	Args []string
	// Flavour is how the distribution of tt presents itself. The zero
	// Flavour is tt's own.
	Flavour version.Flavour
}

// Boot creates the root command - the global flags, no subcommands - and
// makes it the process's root. It parses the global flags in opts.Args and
// installs the process logger they ask for. The root and the commands built
// after Boot present tt as opts.Flavour.
//
// A flag error is not returned: Run reports it, once the logger is set up.
// An invalid --log-format leaves the default format in place until then.
func Boot(opts BootOptions) (*cobra.Command, error) {
	flavour = opts.Flavour
	rootCmd = newRootCmd()
	rootCmd.SetErr(errorLogWriter{})

	_ = rootCmd.ParseFlags(opts.Args)

	err := setupLogging()
	if err != nil {
		return nil, err
	}

	return rootCmd, nil
}

// BuiltinCommands returns new instances of tt's top-level commands, each
// annotated with LegacyAnnotation.
func BuiltinCommands() []*cobra.Command {
	commands := []*cobra.Command{
		NewVersionCmd(),
		NewCompletionCmd(),
		NewInstanceCmd(),
		NewServiceCmd(),
		NewConnectCmd(),
		NewRocksCmd(),
		NewCatCmd(),
		NewPlayCmd(),
		NewClusterCmd(),
		NewCoredumpCmd(),
		NewReplicasetCmd(),
		NewRunCmd(),
		NewTestCmd(),
		NewCreateCmd(),
		NewNewCmd(),
		NewPackageCmd(),
		NewRegistryCmd(),
		NewErrorsHelpTopic(),
		NewDaemonCmd(),
		NewBinaryCmd(),
		NewTcmCmd(),
		NewBackupCmd(),
		NewBackupIDCmd(),
		NewRestoreCmd(),
	}

	for _, command := range commands {
		markLegacy(command)
	}

	return commands
}

// markLegacy annotates command with LegacyAnnotation.
func markLegacy(command *cobra.Command) {
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}

	command.Annotations[LegacyAnnotation] = "true"
}

// isLegacy reports whether command carries LegacyAnnotation.
func isLegacy(command *cobra.Command) bool {
	return command.Annotations[LegacyAnnotation] == "true"
}

// InjectCommands adds InjectedCmds to root, annotated with LegacyAnnotation.
// An injected command replaces the top-level command of the same name.
// TT-EE.
func InjectCommands(root *cobra.Command) error {
	if root == nil {
		return errNilCommandRoot
	}

	for _, injected := range InjectedCmds {
		for _, existing := range root.Commands() {
			if existing.Name() != injected.Name() {
				continue
			}

			if !isLegacy(existing) {
				log.Debugf("Injected command %q replaces the command a module mounted there",
					existing.CommandPath())
			}

			root.RemoveCommand(existing)

			break
		}

		markLegacy(injected)
		root.AddCommand(injected)
	}

	return nil
}

// Configure configures tt for the command line Boot parsed: it finds the
// project and the executables the commands run, and sets up the integrity
// checks. No configuration file of tt is read: tt.yaml is gone.
func Configure() error {
	err := configure.ValidateCliOpts(&cmdCtx.Cli)
	if err != nil {
		return err
	}

	err = configure.Cli(&cmdCtx)
	if err != nil {
		//nolint:staticcheck // ST1005: user-facing message, integration tests match it.
		return fmt.Errorf("Failed to configure Tarantool CLI: %w", err)
	}

	// The files of the project are what is checked: the project replaces the
	// tt environment tt.yaml rooted.
	cmdCtx.Integrity, err = integrity.InitializeIntegrityCheck(
		cmdCtx.Cli.IntegrityCheck,
		cmdCtx.Cli.ProjectDir,
	)
	if err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	return nil
}

// ConfigureHelp sets up the help of the tree: the help command, which
// completes the names of the top-level commands, and the help templates.
// The root help lists external under EXTERNAL COMMANDS. The command tree
// must be complete.
func ConfigureHelp(external []ExternalCommand) {
	configureHelpCommand(rootCmd, external)
}

// Run executes the command line on the root Boot created and returns the
// process exit code, having reported the error the command failed with.
func Run() int {
	cmd, err := rootCmd.ExecuteC()
	if err == nil {
		return sdk.ExitOK
	}

	return reportError(cmd, err)
}

// ProjectDir returns the directory project commands work on, as an absolute
// path: the -C directory when one was given, and the working directory
// otherwise.
func ProjectDir() (string, error) {
	return absoluteWorkingDir()
}
