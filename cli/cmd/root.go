package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
)

var (
	cmdCtx      cmdcontext.CmdCtx
	cliOpts     *config.CliOpts
	modulesInfo modules.ModulesInfo
	rootCmd     *cobra.Command

	// InjectedCmds is populated with the command to be injected into root.
	// TT-EE.
	InjectedCmds []*cobra.Command
)

// GetCmdCtxPtr returns a pointer to cmdCtx, which can be used to create injected commands.
// TT-EE.
func GetCmdCtxPtr() *cmdcontext.CmdCtx {
	return &cmdCtx
}

// injectCmds injects additional commands.
// TT-EE.
func injectCmds(root *cobra.Command) error {
	if root == nil {
		return fmt.Errorf("can't inject commands. The root is nil")
	}

	if InjectedCmds == nil {
		return nil
	}

	for i := range InjectedCmds {
		cmd := InjectedCmds[i]

		// Injected command must override the original one.
		// So, remove the original from the root.
		origCmds := root.Commands()
		for j := range origCmds {
			if cmd.Name() == origCmds[j].Name() {
				root.RemoveCommand(origCmds[j])
				break
			}
		}

		root.AddCommand(cmd)
	}

	return nil
}

// GetModulesInfoPtr returns a pointer to modulesInfo, which can be used to create
// injected commands.
// TT-EE.
func GetModulesInfoPtr() *modules.ModulesInfo {
	return &modulesInfo
}

// LogHandler is a custom log handler implementation used to print formatted error and warning
// log messages.
type LogHandler struct {
	baseHandler *cli.Handler
}

// HandleLog performs log handling in accordance with log entry level.
func (h *LogHandler) HandleLog(logEntry *log.Entry) error {
	if logEntry.Level >= log.WarnLevel {
		logEntry = &log.Entry{
			Logger:    logEntry.Logger,
			Fields:    logEntry.Fields,
			Level:     logEntry.Level,
			Timestamp: logEntry.Timestamp,
			// For warnings and errors display the whole message colored not only prefix bullet.
			Message: cli.Colors[logEntry.Level].Sprint(logEntry.Message),
		}
	}
	return h.baseHandler.HandleLog(logEntry)
}

// setWriter sets the underlying writer and returns the original one.
func (h *LogHandler) setWriter(w io.Writer) io.Writer {
	orig := h.baseHandler.Writer
	h.baseHandler.Writer = w
	return orig
}

// logErrorWriterDecorator is used to decorate messages sent to writer
// in the same way as log.Error.
type logErrorWriterDecorator struct {
	writer  io.Writer
	handler *LogHandler
}

// Write decorates the original output in the same way as log.Error.
func (d logErrorWriterDecorator) Write(p []byte) (int, error) {
	// Setup LogHandler to write to string to get the decorated string.
	var logdst strings.Builder
	orig := d.handler.setWriter(&logdst)
	log.Errorf(string(p))
	// Restore LogHandler with the original writer.
	d.handler.setWriter(orig)
	// Send the decorated data.
	return fmt.Fprint(d.writer, logdst.String())
}

// NewCmdRoot creates a new root command.
func NewCmdRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Tarantool CLI",
		Long:  "Utility for managing Tarantool packages and Tarantool-based applications",
		Example: `$ tt -L /path/to/local/dir version
  $ tt -S -I help
  $ tt completion

If tt was installed from a repository, then the basic replicaset examples were installed with it.
To start work with these examples create a symbolic link in system instances enabled directory
(requires root permissions) to enable the application:

Tarantool 2.*:
  # ln -s /etc/tarantool/instances.available/replicaset_example_tarantool_2 \
/etc/tarantool/instances.enabled/replicaset_example

Tarantool 3.*:
  # ln -s /etc/tarantool/instances.available/replicaset_example_tarantool_3 \
/etc/tarantool/instances.enabled/replicaset_example

After that tt will be able to manage the application using 'replicaset_example' name:
  # tt start replicaset_example
    • Starting an instance [replicaset_example:master]...
    • Starting an instance [replicaset_example:replica]...`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
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
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.IsSelfExec, "self", "s",
		false, "Skip searching for other tt versions to run")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.NoPrompt, "no-prompt", "",
		false, "Skip cli interaction using default behavior")

	integrity.RegisterIntegrityCheckFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheck)
	integrity.RegisterIntegrityCheckPeriodFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

	rootCmd.Flags().SetInterspersed(false)

	rootCmd.AddCommand(
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
		NewCartridgeCmd(),
		NewClusterCmd(),
		NewCoredumpCmd(),
		NewReplicasetCmd(),
		NewRunCmd(),
		NewSearchCmd(),
		NewCleanCmd(),
		NewCreateCmd(),
		NewBuildCmd(),
		NewInstallCmd(),
		NewUninstallCmd(),
		NewPackCmd(),
		NewInitCmd(),
		NewDaemonCmd(),
		NewCfgCmd(),
		NewInstancesCmd(),
		NewBinariesCmd(),
		NewEnvCmd(),
		NewDownloadCmd(),
		NewKillCmd(),
		NewLogCmd(),
		NewEnableCmd(),
		NewAeonCmd(),
		NewTcmCmd(),
	)
	if err := injectCmds(rootCmd); err != nil {
		panic(err.Error())
	}

	// Adjust logger color mapping (display errors with hi-intencity color and bold).
	cli.Colors[log.ErrorLevel] = color.New(color.Bold, color.FgHiRed)
	cli.Colors[log.FatalLevel] = color.New(color.Bold, color.FgHiRed)

	logHandler := &LogHandler{cli.Default}

	log.SetHandler(logHandler)

	// Setup decoration for Command's error messages.
	rootCmd.SetErr(&logErrorWriterDecorator{rootCmd.ErrOrStderr(), logHandler})
	rootCmd.InitDefaultHelpCmd()

	return rootCmd
}

// Execute root command.
// If received error is of an ArgError type, usage help is printed.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var argError *util.ArgError
		if errors.As(err, &argError) {
			log.Error(argError.Error())
			rootCmd.Usage()
		}
		os.Exit(1)
	}
}

// InitRoot initializes global flags, configures CLI, configure
// external modules, collects information about available
// modules and configure `help` module.
func InitRoot() {
	rootCmd = NewCmdRoot()
	rootCmd.ParseFlags(os.Args[1:])

	var err error

	_, configPathEnvSet := os.LookupEnv("TT_CLI_CFG")
	if cmdCtx.Cli.ConfigPath == "" && configPathEnvSet {
		configPathEnv, err := filepath.Abs(os.Getenv("TT_CLI_CFG"))
		if err != nil {
			log.Fatalf("failed getting config path from environment variable: %s", err)
		}
		cmdCtx.Cli.ConfigPath = configPathEnv
	}

	if err := configure.ValidateCliOpts(&cmdCtx.Cli); err != nil {
		log.Fatal(err.Error())
	}

	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("can't get current dir: %s", err.Error())
	}

	configPath, _ := util.GetYamlFileName(
		filepath.Join(currentDir, configure.ConfigName),
		false,
	)

	cmdCtx.Integrity, err = integrity.InitializeIntegrityCheck(
		cmdCtx.Cli.IntegrityCheck,
		filepath.Dir(configPath),
	)
	if err != nil {
		log.Fatalf("integrity check failed: %s", err)
	}

	// Configure Tarantool CLI.
	if err := configure.Cli(&cmdCtx); err != nil {
		log.Fatalf("Failed to configure Tarantool CLI: %s", err)
	}

	cliOpts, cmdCtx.Cli.ConfigPath, err = configure.GetCliOpts(cmdCtx.Cli.ConfigPath,
		cmdCtx.Integrity.Repository)
	if err != nil {
		log.Fatalf("Failed to get Tarantool CLI configuration: %s", err)
	}
	if cmdCtx.Cli.ConfigPath == "" {
		// Config is not found, use current dir as base dir.
		if cmdCtx.Cli.ConfigDir, err = os.Getwd(); err != nil {
			log.Fatal(err.Error())
		}
	} else {
		cmdCtx.Cli.ConfigDir = filepath.Dir(cmdCtx.Cli.ConfigPath)
		log.Debugf("Using configuration file %q", cmdCtx.Cli.ConfigPath)
	}

	// Getting modules information.
	modulesInfo, err = modules.GetModulesInfo(&cmdCtx, rootCmd.Name(), cliOpts)
	if err != nil {
		log.Fatalf("Failed to configure Tarantool CLI command: %s", err)
	}

	// External commands must be configured in a special way.
	// This is necessary, for example, so that we can pass arguments to these commands.
	if len(os.Args) > 1 {
		configure.ExternalCmd(rootCmd, &cmdCtx, &modulesInfo, cmdCtx.Cli.ForceInternal, os.Args[1:])
	}

	// Configure help command.
	err = configureHelpCommand(&cmdCtx, rootCmd)
	if err != nil {
		log.Fatalf("Failed to set up help command: %s", err)
	}
}
