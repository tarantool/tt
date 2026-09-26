package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/util"
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
		NewStartCmd(),
		NewStopCmd(),
		NewStatusCmd(),
		NewRestartCmd(),
		NewLogrotateCmd(),
		NewCheckCmd(),
		NewConnectCmd(),
		NewRocksCmd(),
		NewCatCmd(),
		NewPlayCmd(),
		NewClusterCmd(),
		NewCoredumpCmd(),
		NewReplicasetCmd(),
		NewRunCmd(),
		NewTestCmd(),
		NewSearchCmd(),
		NewCleanCmd(),
		NewCreateCmd(),
		NewNewCmd(),
		NewInstallCmd(),
		NewUninstallCmd(),
		NewPackageCmd(),
		NewRegistryCmd(),
		NewErrorsHelpTopic(),
		NewDaemonCmd(),
		NewCfgCmd(),
		NewBinariesCmd(),
		NewEnvCmd(),
		NewDownloadCmd(),
		NewKillCmd(),
		NewLogCmd(),
		NewTcmCmd(),
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

// Configure configures tt for the command line Boot parsed: it loads the tt
// environment and its integrity checks.
func Configure() error {
	_, configPathEnvSet := os.LookupEnv("TT_CLI_CFG")
	if cmdCtx.Cli.ConfigPath == "" && configPathEnvSet {
		configPathEnv, err := filepath.Abs(os.Getenv("TT_CLI_CFG"))
		if err != nil {
			return fmt.Errorf("failed getting config path from environment variable: %w", err)
		}

		cmdCtx.Cli.ConfigPath = configPathEnv
	}

	err := configure.ValidateCliOpts(&cmdCtx.Cli)
	if err != nil {
		return err
	}

	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("can't get current dir: %w", err)
	}

	configPath, _ := util.GetYamlFileName(
		filepath.Join(currentDir, configure.ConfigName),
		false,
	)

	// Initialize integrity before loading tt.yaml because the configuration loader
	// uses the integrity repository.
	cmdCtx.Integrity, err = integrity.InitializeIntegrityCheck(
		cmdCtx.Cli.IntegrityCheck,
		filepath.Dir(configPath),
	)
	if err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	err = configure.Cli(&cmdCtx)
	if err != nil {
		//nolint:staticcheck // ST1005: user-facing message, integration tests match it.
		return fmt.Errorf("Failed to configure Tarantool CLI: %w", err)
	}

	cliOpts, cmdCtx.Cli.ConfigPath, err = configure.GetCliOpts(cmdCtx.Cli.ConfigPath,
		cmdCtx.Integrity.Repository)
	if err != nil {
		//nolint:staticcheck // ST1005: user-facing message, kept as it is printed.
		return fmt.Errorf("Failed to get Tarantool CLI configuration: %w", err)
	}

	if cmdCtx.Cli.ConfigPath == "" {
		// Config is not found, use current dir as base dir.
		cmdCtx.Cli.ConfigDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("can't get current dir: %w", err)
		}
	} else {
		cmdCtx.Cli.ConfigDir = filepath.Dir(cmdCtx.Cli.ConfigPath)
		log.Debugf("Using configuration file %q", cmdCtx.Cli.ConfigPath)
	}

	// TCM config, if any, is located next to tt config.
	tcmConfigBasename := filepath.Join(cmdCtx.Cli.ConfigDir, "tcm")

	cmdCtx.Cli.TcmCli.ConfigPath, _ = util.GetYamlFileName(tcmConfigBasename, false)

	return nil
}

// ConfigureHelp sets up the help of the tree: the help command, which
// completes the names of the top-level commands, and the help templates.
// The root help lists external under EXTERNAL COMMANDS, and the completion
// of the root offers them. The command tree must be complete.
func ConfigureHelp(external []ExternalCommand) {
	externalCommands = slices.Clone(external)

	configureHelpCommand(rootCmd, externalCommands)
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
