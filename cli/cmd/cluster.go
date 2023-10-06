package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/cluster"
	clustercmd "github.com/tarantool/tt/cli/cluster/cmd"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

const (
	defaultConfigFileName = "config.yaml"
)

var showCtx = clustercmd.ShowCtx{
	Validate: false,
}

var publishCtx = clustercmd.PublishCtx{
	Force: false,
}

func NewClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage cluster configuration",
	}

	uriHelp := fmt.Sprintf(`The URI specifies a etcd connection settings in the following format:
http(s)://[username:password@]host:port[/prefix][?arguments]

* prefix - a base path to Tarantool configuration in etcd.

Possible arguments:

* name - a name of an instance in the cluster configuration.
* timeout - a request timeout in seconds (default %.1f).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.
`, float64(cluster.DefaultEtcdTimeout)/float64(time.Second))

	show := &cobra.Command{
		Use:   "show (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)",
		Short: "Show a cluster configuration",
		Long: "Show a cluster configuration for an application, instance or" +
			" from etcd URI.\n\n" + uriHelp,
		Example: "tt show application_name\n" +
			"  tt show application_name:instance_name\n" +
			"  tt show https://user@pass@localhost:2379/?prefix=/tt\n" +
			"  tt show https://user@pass@localhost:2379/?prefix=/tt&name=instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterShowModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
		},
	}
	show.Flags().BoolVar(&showCtx.Validate, "validate", showCtx.Validate,
		"validate the configuration")
	clusterCmd.AddCommand(show)

	publish := &cobra.Command{
		Use:   "publish (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>) file",
		Short: "Publish a cluster configuration",
		Long: "Publish an application or an instance configuration to a cluster " +
			"configuration file or to a etcd URI.\n\n" +
			uriHelp,
		Example: "tt publish application_name cluster.yaml\n" +
			"  tt publish application_name:instance_name instance.yaml\n" +
			"  tt publish " +
			"https://user@pass@localhost:2379/?prefix=/tt cluster.yaml\n" +
			"  tt publish " +
			"https://user@pass@localhost:2379/?prefix=/tt&name=instance " +
			"instance.yaml",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterPublishModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
		},
	}
	publish.Flags().BoolVar(&publishCtx.Force, "force", publishCtx.Force,
		"force publish and skip validation")
	clusterCmd.AddCommand(publish)

	return clusterCmd
}

// internalClusterShowModule is an entrypoint for `cluster show` command.
func internalClusterShowModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if uri, err := url.Parse(args[0]); err == nil && uri.Scheme != "" {
		return clustercmd.ShowEtcd(showCtx, uri)
	}

	// It looks like an application or an application:instance.
	instanceCtx, name, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}

	if instanceCtx.ClusterConfigPath == "" {
		return fmt.Errorf("cluster configuration file does not exist for the application")
	}

	return clustercmd.ShowCluster(showCtx, instanceCtx.ClusterConfigPath, name)
}

// internalClusterPublishModule is an entrypoint for `cluster publish` command.
func internalClusterPublishModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	data, config, err := readSourceFile(args[1])
	if err != nil {
		return err
	}
	publishCtx.Src = data
	publishCtx.Config = config

	if uri, err := url.Parse(args[0]); err == nil && uri.Scheme != "" {
		return clustercmd.PublishEtcd(publishCtx, uri)
	}

	// It looks like an application or an application:instance.
	instanceCtx, name, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}

	configPath := instanceCtx.ClusterConfigPath
	if configPath == "" {
		if name != "" {
			return fmt.Errorf("can not to update an instance configuration " +
				"if a cluster configuration file does not exist for the application")
		}
		configPath = filepath.Join(instanceCtx.AppDir, defaultConfigFileName)
	}

	return clustercmd.PublishCluster(publishCtx, configPath, name)
}

// readSourceFile reads a configuration from a source file.
func readSourceFile(path string) ([]byte, *cluster.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read path %q: %s", path, err)
	}

	config, err := cluster.NewYamlCollector(data).Collect()
	if err != nil {
		err = fmt.Errorf("failed to read a configuration from path %q: %s",
			path, err)
		return nil, nil, err
	}

	return data, config, nil
}

// parseAppStr parses a string and returns an application instance context
// and an application instance name or an error.
func parseAppStr(cmdCtx *cmdcontext.CmdCtx, appStr string) (running.InstanceCtx, string, error) {
	var (
		runningCtx running.RunningCtx
		name       string
	)

	if !isConfigExist(cmdCtx) {
		return running.InstanceCtx{},
			"",
			fmt.Errorf("unable to resolve the application name %q: %w", appStr, errNoConfig)
	}

	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appStr})
	if err != nil {
		return running.InstanceCtx{}, "", err
	}

	colonIds := strings.Index(appStr, string(running.InstanceDelimiter))
	if colonIds != -1 {
		name = runningCtx.Instances[0].InstName
	}

	return runningCtx.Instances[0], name, nil
}
