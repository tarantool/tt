package cmd

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/replicaset"
	replicasetcmd "github.com/tarantool/tt/cli/replicaset/cmd"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/integrity"
)

var (
	orchestratorCartridge         bool
	orchestratorCentralizedConfig bool
	orchestratorCustom            bool
	orchestratorsEnabled          = map[replicaset.Orchestrator]*bool{
		replicaset.OrchestratorCentralizedConfig: &orchestratorCentralizedConfig,
		replicaset.OrchestratorCartridge:         &orchestratorCartridge,
		replicaset.OrchestratorCustom:            &orchestratorCustom,
	}

	replicasetUser                     string
	replicasetPassword                 string
	replicasetSslKeyFile               string
	replicasetSslCertFile              string
	replicasetSslCaFile                string
	replicasetSslCiphers               string
	replicasetForce                    bool
	replicasetTimeout                  int
	replicasetBootstrapTimeout         int
	replicasetIntegrityPrivateKey      string
	replicasetBootstrapVshard          bool
	replicasetCartridgeReplicasetsFile string
	replicasetGroupName                string
	replicasetReplicasetName           string
	replicasetInstanceName             string
	replicasetIsGlobal                 bool
	rebootstrapConfirmed               bool

	chosenReplicasetAliases []string
	lsnTimeout              int
	downgradeVersion        string

	replicasetUriHelp = "  The URI can be specified in the following formats:\n" +
		"  * [tcp://][username:password@][host:port]\n" +
		"  * [unix://][username:password@]socketpath\n" +
		"  To specify relative path without `unix://` use `./`."
)

// newUpgradeCmd creates a "replicaset upgrade" command.
func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "upgrade (<APP_NAME> | <URI>) [flags]\n\n" +
			replicasetUriHelp,
		DisableFlagsInUseLine: true,
		Short:                 "Upgrade tarantool cluster",
		Long: "Upgrade tarantool cluster.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetUpgradeModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringArrayVarP(&chosenReplicasetAliases, "replicaset", "r",
		[]string{}, "specify the replicaset name(s) to upgrade")

	cmd.Flags().IntVarP(&lsnTimeout, "timeout", "t", 5,
		"timeout for waiting the LSN synchronization (in seconds)")

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	return cmd
}

// newDowngradeCmd creates a "replicaset downgrade" command.
func newDowngradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "downgrade (<APP_NAME> | <URI>) [flags]\n\n" +
			replicasetUriHelp,
		DisableFlagsInUseLine: true,
		Short:                 "Downgrade tarantool cluster",
		Long: "Downgrade tarantool cluster.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n",
		Run: func(cmd *cobra.Command, args []string) {
			var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
			if downgradeVersion == "" {
				err := errors.New("need to specify the version to downgrade " +
					"use --version (-v) option")
				util.HandleCmdErr(cmd, err)
				os.Exit(1)
			} else if !versionPattern.MatchString(downgradeVersion) {
				err := errors.New("--version (-v) must be in the format " +
					"'x.x.x', where x is a number")
				util.HandleCmdErr(cmd, err)
				os.Exit(1)
			}

			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetDowngradeModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringArrayVarP(&chosenReplicasetAliases, "replicaset", "r",
		[]string{}, "specify the replicaset name(s) to downgrade")

	cmd.Flags().IntVarP(&lsnTimeout, "timeout", "t", 5,
		"timeout for waiting the LSN synchronization (in seconds)")

	cmd.Flags().StringVarP(&downgradeVersion, "version", "v", "",
		"version to downgrade the schema to")

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	return cmd
}

// newStatusCmd creates a "replicaset status" command.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "status [--cartridge|--config|--custom] [flags] " +
			"(<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)\n\n" +
			replicasetUriHelp,
		DisableFlagsInUseLine: true,
		Short:                 "Show a replicaset status",
		Long: "Show a replicaset status.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetStatusModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	return cmd
}

// newPromoteCmd creates a "replicaset promote" command.
func newPromoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "promote [--cartridge|--config|--custom] [-f] [--timeout secs] [flags] " +
			"(<APP_NAME:INSTANCE_NAME> | <URI>)\n\n" +
			replicasetUriHelp,
		DisableFlagsInUseLine: true,
		Short:                 "Promote an instance",
		Long: "Promote an instance.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetPromoteModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"to force a promotion:\n"+
			"  * config: skip instances not found locally\n"+
			"  * cartridge: force inconsistency")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "",
		replicasetcmd.DefaultTimeout, "promoting timeout")
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)

	return cmd
}

// newDemoteCmd creates a "replicaset demote" command.
func newDemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "demote [-f] [--timeout secs] [flags] <APP_NAME:INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Demote an instance",
		Long:                  "Demote an instance.",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetDemoteModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"skip instances not found locally")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "", replicasetcmd.DefaultTimeout, "timeout")
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)
	return cmd
}

// newExpelCmd creates a "replicaset expel" command.
func newExpelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "expel [-f] [--cartridge|--config|--custom] [--timeout secs] " +
			"<APP_NAME:INSTANCE_NAME>",
		Short: "Expel an instance from a replicaset",
		Long:  "Expel an instance from a replicaset.",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetExpelModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"skip instances not found locally")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "", replicasetcmd.DefaultTimeout, "timeout")
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)

	return cmd
}

// newBootstrapCmd creates a "replicaset bootstrap" command.
func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap [--timeout secs] [flags] <APP_NAME|APP_NAME:INSTANCE_NAME>",
		Short: "Bootstrap an application or instance",
		Long:  "Bootstrap an application or instance.",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetBootstrapModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetBootstrapVshard, "bootstrap-vshard", "", false,
		"bootstrap vshard")
	cmd.Flags().StringVarP(&replicasetCartridgeReplicasetsFile, "file", "", "",
		`file where replicasets configuration is described (default "<APP_DIR>/replicasets.yml")`)
	cmd.Flags().StringVarP(&replicasetReplicasetName, "replicaset", "",
		"", "replicaset name for an instance bootstrapping")
	cmd.Flags().IntVarP(&replicasetBootstrapTimeout, "timeout", "",
		replicasetcmd.VShardBootstrapDefaultTimeout, "timeout")

	return cmd
}

// newBootstrapVShardCmd creates a "vshard bootstrap" command.
func newBootstrapVShardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "bootstrap [--cartridge|--config|--custom] [--timeout secs] [flags] " +
			"(<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)\n\n" +
			replicasetUriHelp,
		DisableFlagsInUseLine: true,
		Short:                 "Bootstrap vshard in the cluster",
		Long: "Bootstrap vshard in the cluster.\n\n" +
			libconnect.EnvCredentialsHelp + "\n\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetBootstrapVShardModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)
	cmd.Flags().IntVarP(&replicasetBootstrapTimeout, "timeout", "",
		replicasetcmd.VShardBootstrapDefaultTimeout, "timeout")

	return cmd
}

// newVShardCmd creates a "replicaset vshard" subcommand.
func newVShardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vshard",
		Short:   "Manage vshard",
		Aliases: []string{"vs"},
	}

	cmd.AddCommand(newBootstrapVShardCmd())
	return cmd
}

// newRebootstrapCmd creates a "replicaset rebootstrap" command.
func newRebootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "rebootstrap <APP_NAME:INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Re-bootstraps an instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetRebootstrapModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().BoolVarP(&rebootstrapConfirmed, "yes", "y", false,
		"automatically confirm rebootstrap")

	return cmd
}

func newRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Adds or removes roles for Cartridge and Tarantool 3 orchestrator",
	}

	cmd.AddCommand(newRolesAddCmd())
	cmd.AddCommand(newRolesRemoveCmd())
	return cmd
}

// newRolesAddCmd creates a "replicaset roles add" command.
func newRolesAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "add [--cartridge|--config|--custom] [-f] [--timeout secs]" +
			"<APP_NAME:INSTANCE_NAME> <ROLE_NAME> [flags]",
		Short: "Adds a role for Cartridge and Tarantool 3 orchestrator",
		Long:  "Adds a role for Cartridge and Tarantool 3 orchestrator",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetRolesAddModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
	}

	cmd.Flags().StringVarP(&replicasetReplicasetName, "replicaset", "r", "",
		"name of a target replicaset")
	cmd.Flags().StringVarP(&replicasetGroupName, "group", "g", "",
		"name of a target group (vshard-group in the Cartridge case)")
	cmd.Flags().StringVarP(&replicasetInstanceName, "instance", "i", "",
		"name of a target instance")
	cmd.Flags().BoolVarP(&replicasetIsGlobal, "global", "G", false,
		"global config context")

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"to force a promotion:\n"+
			"  * config: skip instances not found locally\n"+
			"  * cartridge: force inconsistency")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "",
		replicasetcmd.DefaultTimeout, "adding timeout")
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)

	return cmd
}

// newRolesRemoveCmd creates a "replicaset roles remove" command.
func newRolesRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "remove [--cartridge|--config|--custom] [-f] [--timeout secs]" +
			"<APP_NAME:INSTANCE_NAME> <ROLE_NAME> [flags]",
		Short: "Removes a role for Cartridge and Tarantool 3 orchestrator",
		Long:  "Removes a role for Cartridge and Tarantool 3 orchestrator",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetRolesRemoveModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
	}

	cmd.Flags().StringVarP(&replicasetReplicasetName, "replicaset", "r", "",
		"name of a target replicaset")
	cmd.Flags().StringVarP(&replicasetGroupName, "group", "g", "",
		"name of a target group (vhsard-group in the Cartridge case)")
	cmd.Flags().StringVarP(&replicasetInstanceName, "instance", "i", "",
		"name of a target instance")
	cmd.Flags().BoolVarP(&replicasetIsGlobal, "global", "G", false,
		"global config context")

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	cmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"to force a promotion:\n"+
			"  * config: skip instances not found locally\n"+
			"  * cartridge: force inconsistency")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "",
		replicasetcmd.DefaultTimeout, "adding timeout")
	integrity.RegisterWithIntegrityFlag(cmd.Flags(), &replicasetIntegrityPrivateKey)

	return cmd
}

// NewReplicasetCmd creates a replicaset command.
func NewReplicasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "replicaset",
		Short:   "Manage replicasets",
		Aliases: []string{"rs"},
	}

	cmd.AddCommand(newUpgradeCmd())
	cmd.AddCommand(newDowngradeCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newPromoteCmd())
	cmd.AddCommand(newDemoteCmd())
	cmd.AddCommand(newExpelCmd())
	cmd.AddCommand(newVShardCmd())
	cmd.AddCommand(newBootstrapCmd())
	cmd.AddCommand(newRebootstrapCmd())
	cmd.AddCommand(newRolesCmd())

	return cmd
}

// addOrchestratorFlags adds orchestrators flags for a command.
func addOrchestratorFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&orchestratorCentralizedConfig, "config", false,
		`to force the centralized config orchestrator`)
	cmd.Flags().BoolVar(&orchestratorCartridge, "cartridge", false,
		`to force the Cartridge orchestrator`)
	cmd.Flags().BoolVar(&orchestratorCustom, "custom", false,
		`to force a custom orchestrator`)
}

// addTarantoolConnectFlags adds flags to configure Tarantool connection.
func addTarantoolConnectFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&replicasetUser, "username", "u", "",
		`username for the URI case`)
	cmd.Flags().StringVarP(&replicasetPassword, "password", "p", "",
		`password for the URI case`)
	cmd.Flags().StringVar(&replicasetSslKeyFile, "sslkeyfile", "",
		`path to a private SSL key file for the URI case`)
	cmd.Flags().StringVar(&replicasetSslCertFile, "sslcertfile", "",
		`path to an SSL certificate file for the URI case`)
	cmd.Flags().StringVar(&replicasetSslCaFile, "sslcafile", "",
		`path to a trusted certificate authorities (CA) file for the URI case`)
	cmd.Flags().StringVar(&replicasetSslCiphers, "sslciphers", "",
		`colon-separated (:) list of SSL cipher suites for the URI case`)
}

// replicasetCtx describes a context for the replicaset command.
type replicasetCtx struct {
	// IsApplication is true when an application was specified.
	IsApplication bool
	// IsInstanceConnect is true when the instance connection was established.
	IsInstanceConnect bool
	// InstName is an instance name.
	InstName string
	// RunningCtx describes running context.
	RunningCtx running.RunningCtx
	// Conn is a connection to the instance.
	Conn connector.Connector
	// Orchestrator describes specified orchestrator.
	Orchestrator replicaset.Orchestrator
}

// replicasetFillCtx fills the replicaset command context.
func replicasetFillCtx(cmdCtx *cmdcontext.CmdCtx, ctx *replicasetCtx, args []string,
	isRunningCtxRequired bool) error {
	var err error
	ctx.Orchestrator, err = getOrchestrator()
	if err != nil {
		return err
	}

	connectCtx := connect.ConnectCtx{
		Username:    replicasetUser,
		Password:    replicasetPassword,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
	}
	var connOpts connector.ConnectOpts

	if err := running.FillCtx(cliOpts, cmdCtx, &ctx.RunningCtx, args); err == nil {
		ctx.IsApplication = true
		if len(ctx.RunningCtx.Instances) == 1 {
			if connectCtx.Username != "" || connectCtx.Password != "" {
				err = fmt.Errorf("username and password are not supported" +
					" with a connection via a control socket")
				return err
			}
			connOpts = makeConnOpts(
				connector.UnixNetwork,
				ctx.RunningCtx.Instances[0].ConsoleSocket,
				connectCtx,
			)
			ctx.IsInstanceConnect = true
			appName, instName, found := strings.Cut(args[0], string(running.InstanceDelimiter))
			if found {
				if instName != ctx.RunningCtx.Instances[0].InstName {
					return fmt.Errorf("instance %q not found", instName)
				}
				// Re-fill context for an application.
				ctx.InstName = instName
				err := running.FillCtx(cliOpts, cmdCtx, &ctx.RunningCtx, []string{appName})
				if err != nil {
					// Should not happen.
					return err
				}
			}
		}
		// In case of adding/removing role when user may not provide an instance.
		if (cmdCtx.CommandName == "add" || cmdCtx.CommandName == "remove") && ctx.InstName == "" {
			if len(ctx.RunningCtx.Instances) == 0 {
				return fmt.Errorf("there are no running instances")
			}
			// Trying to find alive instance to create connection with it.
			var err error
			for _, i := range ctx.RunningCtx.Instances {
				connOpts = makeConnOpts(
					connector.UnixNetwork,
					i.ConsoleSocket,
					connectCtx,
				)
				var conn connector.Connector
				conn, err = connector.Connect(connOpts)
				if err == nil {
					ctx.IsInstanceConnect = true
					conn.Close()
					break
				}
			}
			if err != nil {
				return fmt.Errorf("cannot connect to any instance from replicaset")
			}
		}
	} else {
		if isRunningCtxRequired {
			return err
		}
		connOpts, _, err = resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)
		if err != nil {
			return err
		}
		ctx.IsInstanceConnect = true
	}

	if ctx.IsInstanceConnect {
		// Connecting to the instance.
		var err error
		ctx.Conn, err = connector.Connect(connOpts)
		if err != nil {
			return fmt.Errorf("unable to establish connection: %s", err)
		}
	}

	return nil
}

// internalReplicasetUpgradeModule is a "upgrade" command for the replicaset module.
func internalReplicasetUpgradeModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}

	connectCtx := connect.ConnectCtx{
		Username:    replicasetUser,
		Password:    replicasetPassword,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
	}
	var connOpts connector.ConnectOpts
	connOpts, _, _ = resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)

	return replicasetcmd.Upgrade(replicasetcmd.DiscoveryCtx{
		IsApplication: ctx.IsApplication,
		RunningCtx:    ctx.RunningCtx,
		Conn:          ctx.Conn,
		Orchestrator:  ctx.Orchestrator,
	}, replicasetcmd.UpgradeOpts{
		ChosenReplicasetAliases: chosenReplicasetAliases,
		LsnTimeout:              lsnTimeout,
	}, connOpts)
}

// internalReplicasetDowngradeModule is a "upgrade" command for the replicaset module.
func internalReplicasetDowngradeModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}

	connectCtx := connect.ConnectCtx{
		Username:    replicasetUser,
		Password:    replicasetPassword,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
	}
	var connOpts connector.ConnectOpts
	connOpts, _, _ = resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)

	return replicasetcmd.Downgrade(replicasetcmd.DiscoveryCtx{
		IsApplication: ctx.IsApplication,
		RunningCtx:    ctx.RunningCtx,
		Conn:          ctx.Conn,
		Orchestrator:  ctx.Orchestrator,
	}, replicasetcmd.DowngradeOpts{
		ChosenReplicasetAliases: chosenReplicasetAliases,
		Timeout:                 lsnTimeout,
		DowngradeVersion:        downgradeVersion,
	}, connOpts)
}

// internalReplicasetPromoteModule is a "promote" command for the replicaset module.
func internalReplicasetPromoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	if !ctx.IsInstanceConnect {
		return fmt.Errorf("specify an instance to promote")
	}
	defer ctx.Conn.Close()

	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}

	return replicasetcmd.Promote(replicasetcmd.PromoteCtx{
		InstName:      ctx.InstName,
		Collectors:    collectors,
		Publishers:    publishers,
		IsApplication: ctx.IsApplication,
		Conn:          ctx.Conn,
		RunningCtx:    ctx.RunningCtx,
		Orchestrator:  ctx.Orchestrator,
		Force:         replicasetForce,
		Timeout:       replicasetTimeout,
	})
}

// internalReplicasetDemoteModule is a "demote" command for the replicaset module.
func internalReplicasetDemoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, true); err != nil {
		return err
	}
	if !ctx.IsApplication {
		return fmt.Errorf("remote instance demoting is not supported")
	}
	if !ctx.IsInstanceConnect {
		return fmt.Errorf("specify an instance to demote")
	}
	defer ctx.Conn.Close()

	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}

	return replicasetcmd.Demote(replicasetcmd.DemoteCtx{
		InstName:     ctx.InstName,
		Publishers:   publishers,
		Collectors:   collectors,
		Conn:         ctx.Conn,
		RunningCtx:   ctx.RunningCtx,
		Orchestrator: ctx.Orchestrator,
		Force:        replicasetForce,
		Timeout:      replicasetTimeout,
	})
}

// internalReplicasetStatusModule is a "status" command for the replicaset module.
func internalReplicasetStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}
	return replicasetcmd.Status(replicasetcmd.DiscoveryCtx{
		IsApplication: ctx.IsApplication,
		RunningCtx:    ctx.RunningCtx,
		Conn:          ctx.Conn,
		Orchestrator:  ctx.Orchestrator,
	})
}

// internalReplicasetExpelModule is a "expel" command for the replicaset module.
func internalReplicasetExpelModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if _, _, found := strings.Cut(args[0], string(running.InstanceDelimiter)); !found {
		return fmt.Errorf("the command expects argument application_name:instance_name")
	}
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, true); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}
	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}

	return replicasetcmd.Expel(replicasetcmd.ExpelCtx{
		Instance:     ctx.InstName,
		Publishers:   publishers,
		Collectors:   collectors,
		Orchestrator: ctx.Orchestrator,
		RunningCtx:   ctx.RunningCtx,
		Force:        replicasetForce,
		Timeout:      replicasetTimeout,
	})
}

// internalReplicasetBootstrapVShardModule is a "bootstrap" command for
// the "replicaset vshard" module.
func internalReplicasetBootstrapVShardModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}
	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}
	return replicasetcmd.BootstrapVShard(replicasetcmd.VShardCmdCtx{
		IsApplication: ctx.IsApplication,
		RunningCtx:    ctx.RunningCtx,
		Conn:          ctx.Conn,
		Orchestrator:  ctx.Orchestrator,
		Publishers:    publishers,
		Collectors:    collectors,
		Timeout:       replicasetBootstrapTimeout,
	})
}

// internalReplicasetBootstrapModule is a "bootstrap" command for the "replicaset" module.
func internalReplicasetBootstrapModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	_, instName, found := strings.Cut(args[0], string(running.InstanceDelimiter))

	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, true); err != nil {
		return err
	}
	if ctx.IsInstanceConnect {
		defer ctx.Conn.Close()
	}
	bootstrapCtx := replicasetcmd.BootstapCtx{
		ReplicasetsFile: replicasetCartridgeReplicasetsFile,
		Orchestrator:    ctx.Orchestrator,
		RunningCtx:      ctx.RunningCtx,
		Timeout:         replicasetBootstrapTimeout,
		BootstrapVShard: replicasetBootstrapVshard,
		Replicaset:      replicasetReplicasetName,
	}
	if found {
		bootstrapCtx.Instance = instName
	}

	return replicasetcmd.Bootstrap(bootstrapCtx)
}

// getOrchestartor returns a chosen orchestrator or an unknown one.
func getOrchestrator() (replicaset.Orchestrator, error) {
	orchestrator := replicaset.OrchestratorUnknown
	cnt := 0
	for k, v := range orchestratorsEnabled {
		if *v {
			orchestrator = k
			cnt++
		}
	}
	if cnt > 1 {
		return orchestrator, fmt.Errorf("only one type of orchestrator can be forced")
	}
	return orchestrator, nil
}

// internalReplicasetRebootstrapModule is a "rebootstrap" command for the replicaset module.
func internalReplicasetRebootstrapModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) > 1 {
		return util.NewArgError("only one instance supported for re-bootstrap")
	}
	if len(args) < 1 {
		return util.NewArgError("instance for rebootstrap is not specified")
	}

	appName, instName, found := strings.Cut(args[0], string(running.InstanceDelimiter))
	if !found {
		return util.NewArgError(
			"an instance name is not specified. Please use app:instance format.")
	}

	return replicaset.Rebootstrap(*cmdCtx, *cliOpts, replicaset.RebootstrapCtx{
		AppName:      appName,
		InstanceName: instName,
		Confirmed:    rebootstrapConfirmed,
	})
}

// internalReplicasetRolesAddModule is a "roles add" command for the replicaset module.
func internalReplicasetRolesAddModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	defer ctx.Conn.Close()
	if ctx.IsApplication && replicasetInstanceName == "" && ctx.InstName == "" &&
		!replicasetIsGlobal && replicasetGroupName == "" && replicasetReplicasetName == "" {
		return fmt.Errorf("there is no destination provided in which to add role")
	}
	if ctx.InstName != "" && replicasetInstanceName != "" &&
		replicasetInstanceName != ctx.InstName {
		return fmt.Errorf("there are different instance names passed after" +
			" app name and in flag arg")
	}
	if replicasetInstanceName != "" {
		ctx.InstName = replicasetInstanceName
	}

	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}

	return replicasetcmd.RolesChange(replicasetcmd.RolesChangeCtx{
		InstName:       ctx.InstName,
		GroupName:      replicasetGroupName,
		ReplicasetName: replicasetReplicasetName,
		IsGlobal:       replicasetIsGlobal,
		RoleName:       args[1],
		Collectors:     collectors,
		Publishers:     publishers,
		IsApplication:  ctx.IsApplication,
		Conn:           ctx.Conn,
		RunningCtx:     ctx.RunningCtx,
		Orchestrator:   ctx.Orchestrator,
		Force:          replicasetForce,
		Timeout:        replicasetTimeout,
	}, replicaset.RolesAdder{})
}

// internalReplicasetRolesRemoveModule is a "roles remove" command for the replicaset module.
func internalReplicasetRolesRemoveModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var ctx replicasetCtx
	if err := replicasetFillCtx(cmdCtx, &ctx, args, false); err != nil {
		return err
	}
	defer ctx.Conn.Close()
	if ctx.IsApplication && replicasetInstanceName == "" && ctx.InstName == "" &&
		!replicasetIsGlobal && replicasetGroupName == "" && replicasetReplicasetName == "" {
		return fmt.Errorf("there is no destination provided where to remove role")
	}
	if ctx.InstName != "" && replicasetInstanceName != "" &&
		replicasetInstanceName != ctx.InstName {
		return fmt.Errorf("there are different instance names passed after" +
			" app name and in flag arg")
	}
	if replicasetInstanceName != "" {
		ctx.InstName = replicasetInstanceName
	}

	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, replicasetIntegrityPrivateKey)
	if err != nil {
		return err
	}

	return replicasetcmd.RolesChange(replicasetcmd.RolesChangeCtx{
		InstName:       ctx.InstName,
		GroupName:      replicasetGroupName,
		ReplicasetName: replicasetReplicasetName,
		IsGlobal:       replicasetIsGlobal,
		RoleName:       args[1],
		Collectors:     collectors,
		Publishers:     publishers,
		IsApplication:  ctx.IsApplication,
		Conn:           ctx.Conn,
		RunningCtx:     ctx.RunningCtx,
		Orchestrator:   ctx.Orchestrator,
		Force:          replicasetForce,
		Timeout:        replicasetTimeout,
	}, replicaset.RolesRemover{})
}
