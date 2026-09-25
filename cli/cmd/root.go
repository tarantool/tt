package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/logging"
	"github.com/tarantool/tt/v3/cli/modules"
)

var (
	errNilCommandRoot = errors.New("can't inject commands. The root is nil")
	errUnknownCommand = errors.New("unknown command ")
)

var (
	cmdCtx      cmdcontext.CmdCtx
	cliOpts     *config.CliOpts
	modulesInfo modules.ModulesInfo
	rootCmd     *cobra.Command
	// logFormat is the value of the --log-format flag.
	logFormat = logging.FormatText

	// InjectedCmds is populated with the command to be injected into root.
	// TT-EE.
	InjectedCmds []*cobra.Command
)

// GetCmdCtxPtr returns a pointer to cmdCtx, which can be used to create injected commands.
// TT-EE.
func GetCmdCtxPtr() *cmdcontext.CmdCtx {
	return &cmdCtx
}

// GetModulesInfoPtr returns a pointer to modulesInfo, which can be used to create
// injected commands.
// TT-EE.
func GetModulesInfoPtr() *modules.ModulesInfo {
	return &modulesInfo
}

// errorLogWriter logs what cobra writes to its error stream - "Error: unknown
// flag: --foo", "Run 'tt --help' for usage." - as error records, so that it
// comes out in the configured log format like any other error.
type errorLogWriter struct{}

// Write logs p, without its trailing newline, as one error record.
func (errorLogWriter) Write(p []byte) (int, error) {
	log.Error(strings.TrimRight(string(p), "\n"))

	return len(p), nil
}

// NewCmdRoot creates a new root command with every builtin and injected
// command.
func NewCmdRoot() *cobra.Command {
	root := newRootCmd()
	root.AddCommand(BuiltinCommands()...)

	if err := InjectCommands(root); err != nil {
		panic(err.Error())
	}

	root.SetErr(errorLogWriter{})

	return root
}

// newRootCmd creates a root command with the global flags and no
// subcommands.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Tarantool CLI",
		Long:  "Utility for managing Tarantool packages and Tarantool-based applications",
		Example: `$ tt -L /path/to/local/dir version
  $ tt -S -I help
  $ tt completion`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			} else {
				return fmt.Errorf("%w%s", errUnknownCommand, args[0])
			}
		},
		ValidArgsFunction: RootShellCompletionCommands,
		TraverseChildren:  true,
	}

	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.IsSystem, "system", "S",
		false, "System launch")
	rootCmd.Flags().StringVarP(&cmdCtx.Cli.LocalLaunchDir, "local", "L",
		"", "Local launch")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.ForceInternal, "internal", "I",
		false, "Use internal module")
	rootCmd.Flags().StringVarP(&cmdCtx.Cli.ConfigPath, "cfg", "c",
		"", "Path to configuration file")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.Verbose, "verbose", "V",
		false, "Verbose output")
	rootCmd.Flags().Var(&logFormat, "log-format",
		"Format of the log written to stderr: text or json")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.IsSelfExec, "self", "s",
		false, "Skip searching for other tt versions to run")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.NoPrompt, "no-prompt", "",
		false, "Skip cli interaction using default behavior")

	integrity.RegisterIntegrityCheckFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheck)
	integrity.RegisterIntegrityCheckPeriodFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

	rootCmd.Flags().SetInterspersed(false)

	return rootCmd
}

// Execute runs the root command. On failure it reports the error and exits
// with its code; on success it returns.
// TT-EE.
func Execute() {
	if code := Run(); code != sdk.ExitOK {
		os.Exit(code)
	}
}

// InitRoot initializes global flags, configures CLI, configure
// external modules, collects information about available
// modules and configure `help` module. On failure it reports the error and
// exits with its code.
// TT-EE.
func InitRoot() {
	if err := initRoot(); err != nil {
		exitcode.Exit(err)
	}
}

// setupLogging installs the process logger the root flags ask for.
func setupLogging() error {
	level := slog.LevelInfo
	if cmdCtx.Cli.Verbose {
		level = slog.LevelDebug
	}

	return logging.Setup(logging.Options{
		Level:      level,
		Format:     logFormat,
		Writer:     os.Stderr,
		Redactor:   log.Secrets(),
		IsTerminal: nil,
		LookupEnv:  os.LookupEnv,
	})
}

// initRoot does the work of InitRoot and returns the first failure: it runs
// the boot phases with the builtin and injected commands.
func initRoot() error {
	root, err := Boot(BootOptions{Args: os.Args[1:]})
	if err != nil {
		return err
	}

	root.AddCommand(BuiltinCommands()...)

	if err := InjectCommands(root); err != nil {
		return err
	}

	return Configure(ConfigureOptions{ModuleOwner: nil})
}
