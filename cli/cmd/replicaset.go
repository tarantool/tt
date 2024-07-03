package cmd

import (
	"fmt"
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
	replicasetIntegrityPrivateKey      string
	replicasetBootstrapVshard          bool
	replicasetCartridgeReplicasetsFile string
	replicasetReplicasetName           string
	rebootstrapConfirmed               bool

	replicasetUriHelp = "  The URI can be specified in the following formats:\n" +
		"  * [tcp://][username:password@][host:port]\n" +
		"  * [unix://][username:password@]socketpath\n" +
		"  To specify relative path without `unix://` use `./`."
)

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
		`file where replicasets configuration is described (default "<APP_DIR>/instances.yml")`)
	cmd.Flags().StringVarP(&replicasetReplicasetName, "replicaset", "",
		"", "replicaset name for an instance bootstrapping")
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "", replicasetcmd.
		VShardBootstrapDefaultTimeout, "timeout")

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
	cmd.Flags().IntVarP(&replicasetTimeout, "timeout", "",
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

// NewReplicasetCmd creates a replicaset command.
func NewReplicasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "replicaset",
		Short:   "Manage replicasets",
		Aliases: []string{"rs"},
	}

	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newPromoteCmd())
	cmd.AddCommand(newDemoteCmd())
	cmd.AddCommand(newExpelCmd())
	cmd.AddCommand(newVShardCmd())
	cmd.AddCommand(newBootstrapCmd())
	cmd.AddCommand(newRebootstrapCmd())

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
	return replicasetcmd.Status(replicasetcmd.StatusCtx{
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
		Timeout:       replicasetTimeout,
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
		Timeout:         replicasetTimeout,
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
