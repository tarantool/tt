package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/logging"
)

var (
	errNilCommandRoot = errors.New("can't inject commands. The root is nil")
	errUnknownCommand = errors.New("unknown command ")
)

var (
	cmdCtx  cmdcontext.CmdCtx
	cliOpts *config.CliOpts
	rootCmd *cobra.Command
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

	err := InjectCommands(root)
	if err != nil {
		panic(err.Error())
	}

	root.SetErr(errorLogWriter{})

	return root
}

// rootLong is what the root help says tt is.
const rootLong = "Utility for managing Tarantool packages and Tarantool-based applications"

// rootDescription returns the root command's short and long descriptions.
// A distribution with a name of its own is named in both: its name is the
// short one and heads the long one.
func rootDescription() (string, string) {
	if !flavour.Custom() {
		return flavour.Name(), rootLong
	}

	first, size := utf8.DecodeRuneInString(rootLong)

	return flavour.Name(), flavour.Name() + " — " + string(unicode.ToLower(first)) +
		rootLong[size:]
}

// newRootCmd creates a root command with the global flags and no
// subcommands.
func newRootCmd() *cobra.Command {
	short, long := rootDescription()

	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: short,
		Long:  long,
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
		TraverseChildren: true,
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
