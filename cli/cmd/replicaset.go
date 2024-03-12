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

	replicasetUser        string
	replicasetPassword    string
	replicasetSslKeyFile  string
	replicasetSslCertFile string
	replicasetSslCaFile   string
	replicasetSslCiphers  string
	replicasetForce       bool
	replicasetTimeout     int
)

// newStatusCmd creates a "replicaset status" command.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "status [--cartridge|--config|--custom] [flags] " +
			"(<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)\n\n" +
			"  The URI can be specified in the following formats:\n" +
			"  * [tcp://][username:password@][host:port]\n" +
			"  * [unix://][username:password@]socketpath\n" +
			"  To specify relative path without `unix://` use `./`.",
		DisableFlagsInUseLine: true,
		Short:                 "Show a replicaset status",
		Long: "Show a replicaset status.\n\n" +
			"The command supports the following environment variables:\n\n" +
			"* " + connect.TarantoolUsernameEnv + " - specifies a username\n" +
			"* " + connect.TarantoolPasswordEnv + " - specifies a password\n" +
			"\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetStatusModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(cmd)
	addTarantoolConnectFlags(cmd)
	return cmd
}

// newPromoteCmd creates a "replicaset promote" command.
func newPromoteCmd() *cobra.Command {
	promoteCmd := &cobra.Command{
		Use: "promote [--cartridge|--config|--custom] [-f] [--timeout secs] [flags] " +
			"(<APP_NAME:INSTANCE_NAME> | <URI>)\n\n" +
			"  The URI can be specified in the following formats:\n" +
			"  * [tcp://][username:password@][host:port]\n" +
			"  * [unix://][username:password@]socketpath\n" +
			"  To specify relative path without `unix://` use `./`.",
		DisableFlagsInUseLine: true,
		Short:                 "Promote an instance",
		Long: "Promote an instance.\n\n" +
			"The command supports the following environment variables:\n\n" +
			"* " + connect.TarantoolUsernameEnv + " - specifies a username\n" +
			"* " + connect.TarantoolPasswordEnv + " - specifies a password\n" +
			"\n",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalReplicasetPromoteModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}

	addOrchestratorFlags(promoteCmd)
	addTarantoolConnectFlags(promoteCmd)
	promoteCmd.Flags().BoolVarP(&replicasetForce, "force", "f", false,
		"skip instances not found locally")
	promoteCmd.Flags().IntVarP(&replicasetTimeout, "timeout", "",
		replicasetcmd.DefaultTimeout, "election promoting timeout")

	return promoteCmd
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

// internalReplicasetPromoteModule is a "promote" command for the replicaset module.
func internalReplicasetPromoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	orchestrator, err := getOrchestrator()
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

	var (
		connOpts      connector.ConnectOpts
		runningCtx    running.RunningCtx
		isApplication bool
		instName      string
	)

	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err == nil {
		var (
			appName string
			found   bool
		)
		appName, instName, found = strings.Cut(args[0], string(running.InstanceDelimiter))
		if len(runningCtx.Instances) != 1 || !found {
			return fmt.Errorf("specify an instance to promote")
		}

		isApplication = true
		if connectCtx.Username != "" || connectCtx.Password != "" {
			err = fmt.Errorf("username and password are not supported" +
				" with a connection via a control socket")
			return err
		}
		connOpts = makeConnOpts(
			connector.UnixNetwork,
			runningCtx.Instances[0].ConsoleSocket,
			connectCtx,
		)

		// Re-fill context for an application.
		if err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appName}); err != nil {
			return err
		}
		isApplication = true
	} else {
		connOpts, _, err = resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)
		if err != nil {
			return err
		}
	}

	conn, err := connector.Connect(connOpts)
	if err != nil {
		return fmt.Errorf("unable to establish connection: %s", err)
	}
	defer conn.Close()

	collectors, publishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, "")
	if err != nil {
		return err
	}

	return replicasetcmd.Promote(replicasetcmd.PromoteCtx{
		InstName:      instName,
		Collectors:    collectors,
		Publishers:    publishers,
		IsApplication: isApplication,
		Conn:          conn,
		RunningCtx:    runningCtx,
		Orhestrator:   orchestrator,
		Force:         replicasetForce,
		Timeout:       replicasetTimeout,
	})
}

// internalReplicasetStatusModule is a "status" command for the replicaset module.
func internalReplicasetStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	orchestrator, err := getOrchestrator()
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

	var (
		connOpts          connector.ConnectOpts
		runningCtx        running.RunningCtx
		isInstanceConnect bool
		isApplication     bool
	)
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err == nil {
		if len(runningCtx.Instances) == 1 {
			if connectCtx.Username != "" || connectCtx.Password != "" {
				err = fmt.Errorf("username and password are not supported" +
					" with a connection via a control socket")
				return err
			}
			connOpts = makeConnOpts(
				connector.UnixNetwork,
				runningCtx.Instances[0].ConsoleSocket,
				connectCtx,
			)
			isInstanceConnect = true
			before, _, found := strings.Cut(args[0], string(running.InstanceDelimiter))
			if found {
				// Re-fill context for an application.
				appName = before
				err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appName})
				if err != nil {
					// Should not happen.
					return err
				}
			}
		}
		isApplication = true
	} else {
		var err error
		connOpts, _, err = resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)
		if err != nil {
			return err
		}
		isInstanceConnect = true
	}

	var conn connector.Connector
	if isInstanceConnect {
		// Connecting to the instance.
		var err error
		conn, err = connector.Connect(connOpts)
		if err != nil {
			return fmt.Errorf("unable to establish connection: %s", err)
		}
		defer conn.Close()
	}

	return replicasetcmd.Status(replicasetcmd.StatusCtx{
		RunningCtx:    runningCtx,
		IsApplication: isApplication,
		Conn:          conn,
		Orchestrator:  orchestrator,
	})
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
