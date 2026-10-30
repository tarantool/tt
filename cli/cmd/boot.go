package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// The phases below initialise tt in the order it runs them: Boot, then the
// commands (BuiltinCommands and whatever else is added to the root), then
// Configure, ConfigureHelp and Run. The core runs them, with
// its own steps in between. They are the core's interface to this package
// and are not stable.

// LegacyAnnotation is the key of the command annotation that marks a
// top-level command built by this package - a builtin - rather than
// contributed by a module. Its value is "true".
const LegacyAnnotation = "tt.core/legacy"

// BootOptions configure Boot.
type BootOptions struct {
	// Args are the command-line arguments, without the program name.
	Args []string
	// Flavour is how the distribution of tt presents itself. The zero
	// Flavour is tt's own.
	Flavour version.Flavour
	// Integrity is the integrity provider tt runs with: it registers the
	// integrity flags, checks the files and signs and verifies the data of
	// a configuration storage. Nil is the disabled provider, which checks
	// nothing and registers no flag.
	Integrity integrity.Provider
	// Modules are the modules the distribution is built from, as the table
	// of the core names them, for tt version --modules.
	Modules []version.Module
}

// Boot creates the root command - the global flags, no subcommands - and
// makes it the process's root. It parses the global flags in opts.Args and
// installs the process logger they ask for. The root and the commands built
// after Boot present tt as opts.Flavour and have the integrity flags of
// opts.Integrity, the provider of the process from then on.
//
// A flag error is not returned: Run reports it, once the logger is set up.
// An invalid --log-format leaves the default format in place until then.
func Boot(opts BootOptions) (*cobra.Command, error) {
	flavour = opts.Flavour
	modules = opts.Modules

	integrityprovider.Set(opts.Integrity)

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
	cmdCtx.Integrity, err = integrityprovider.Get().InitializeIntegrityCheck(
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
