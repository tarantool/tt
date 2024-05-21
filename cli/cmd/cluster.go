package cmd

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clustercmd "github.com/tarantool/tt/cli/cluster/cmd"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/integrity"
)

var showCtx = clustercmd.ShowCtx{
	Username: "",
	Password: "",
	Validate: false,
}

var publishCtx = clustercmd.PublishCtx{
	Username:   "",
	Password:   "",
	Group:      "",
	Replicaset: "",
	Force:      false,
}

var promoteCtx = clustercmd.PromoteCtx{
	Username: "",
	Password: "",
	Force:    false,
}

var demoteCtx = clustercmd.DemoteCtx{
	Username: "",
	Password: "",
	Force:    false,
}

var expelCtx = clustercmd.ExpelCtx{
	Username: "",
	Password: "",
	Force:    false,
}

var (
	clusterIntegrityPrivateKey string
	clusterUriHelp             = fmt.Sprintf(
		`The URI specifies a etcd or tarantool config storage `+
			`connection settings in the following format:
http(s)://[username:password@]host:port[/prefix][?arguments]

* prefix - a base path to Tarantool configuration in etcd or tarantool config storage.

Possible arguments:

* key - a target configuration key in the prefix.
* name - a name of an instance in the cluster configuration.
* timeout - a request timeout in seconds (default %.1f).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a colon-separated (:) list of SSL cipher suites the connection can use.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

You could also specify etcd/tarantool username and password with environment variables:
* %s - specifies an etcd username
* %s - specifies an etcd password
* %s - specifies a tarantool username
* %s - specifies a tarantool password

The priority of credentials:
environment variables < command flags < URL credentials.
`, float64(clustercmd.DefaultUriTimeout)/float64(time.Second),
		libconnect.EtcdUsernameEnv, libconnect.EtcdPasswordEnv,
		libconnect.TarantoolUsernameEnv, libconnect.TarantoolPasswordEnv)
)

func newClusterReplicasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "replicaset",
		Short:   "manage replicaset via 3.0 cluster config source",
		Aliases: []string{"rs"},
	}

	promoteCmd := &cobra.Command{
		Use:                   "promote [-f] [flags] <URI> <INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Promote an instance",
		Long:                  "Promote an instance\n\n" + clusterUriHelp,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterReplicasetPromoteModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
	}
	promoteCmd.Flags().StringVarP(&promoteCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	promoteCmd.Flags().StringVarP(&promoteCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	promoteCmd.Flags().BoolVarP(&promoteCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(promoteCmd.Flags(), &clusterIntegrityPrivateKey)

	demoteCmd := &cobra.Command{
		Use:                   "demote [-f] [flags] <URI> <INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Demote an instance",
		Long:                  "Demote an instance\n\n" + clusterUriHelp,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterReplicasetDemoteModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
	}

	demoteCmd.Flags().StringVarP(&demoteCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	demoteCmd.Flags().StringVarP(&demoteCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	demoteCmd.Flags().BoolVarP(&demoteCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(demoteCmd.Flags(), &clusterIntegrityPrivateKey)

	expelCmd := &cobra.Command{
		Use:                   "expel [-f] [flags] <URI> <INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Expel an instance",
		Long:                  "Expel an instance\n\n" + clusterUriHelp,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterReplicasetExpelModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(2),
	}

	expelCmd.Flags().StringVarP(&expelCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	expelCmd.Flags().StringVarP(&expelCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	expelCmd.Flags().BoolVarP(&expelCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(expelCmd.Flags(), &clusterIntegrityPrivateKey)

	cmd.AddCommand(promoteCmd)
	cmd.AddCommand(demoteCmd)
	cmd.AddCommand(expelCmd)

	return cmd
}

func NewClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage cluster configuration",
	}

	show := &cobra.Command{
		Use:   "show (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)",
		Short: "Show a cluster configuration",
		Long: "Show a cluster configuration for an application, instance," +
			" from etcd URI or from tarantool config storage URI.\n\n" + clusterUriHelp,
		Example: "tt cluster show application_name\n" +
			"  tt cluster show application_name:instance_name\n" +
			"  tt cluster show https://user:pass@localhost:2379/tt\n" +
			"  tt cluster show https://user:pass@localhost:2379/tt?name=instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterShowModule, args)
			util.HandleCmdErr(cmd, err)
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
	show.Flags().StringVarP(&showCtx.Username, "username", "u", "",
		"username (used as etcd credentials only)")
	show.Flags().StringVarP(&showCtx.Password, "password", "p", "",
		"password (used as etcd credentials only)")
	show.Flags().BoolVar(&showCtx.Validate, "validate", showCtx.Validate,
		"validate the configuration")
	clusterCmd.AddCommand(show)

	publish := &cobra.Command{
		Use:   "publish (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>) file",
		Short: "Publish a cluster configuration",
		Long: "Publish an application or an instance configuration to a cluster " +
			"configuration file, to a etcd URI or to a tarantool config storage URI.\n\n" +
			clusterUriHelp + "\n" +
			"By default, the command removes all keys in etcd with prefix " +
			"'/prefix/config/' and writes the result to '/prefix/config/all'. " +
			"You could work and update a target key with the 'key' argument.",
		Example: "tt cluster publish application_name cluster.yaml\n" +
			"  tt cluster publish application_name:instance_name instance.yaml\n" +
			"  tt cluster publish " +
			"https://user:pass@localhost:2379/tt cluster.yaml\n" +
			"  tt cluster publish " +
			"https://user:pass@localhost:2379/tt?name=instance " +
			"instance.yaml\n" +
			"  tt cluster publish --group group --replicaset replicaset " +
			"https://user:pass@localhost:2379/tt?name=instance " +
			"instance.yaml",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterPublishModule, args)
			util.HandleCmdErr(cmd, err)
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
	publish.Flags().StringVarP(&publishCtx.Username, "username", "u", "",
		"username (used as etcd credentials only)")
	publish.Flags().StringVarP(&publishCtx.Password, "password", "p", "",
		"password (used as etcd credentials only)")
	publish.Flags().StringVarP(&publishCtx.Group, "group", "", "", "group name")
	publish.Flags().StringVarP(&publishCtx.Replicaset, "replicaset", "", "", "replicaset name")
	publish.Flags().BoolVar(&publishCtx.Force, "force", publishCtx.Force,
		"force publish and skip validation")
	// Integrity flags.
	integrity.RegisterWithIntegrityFlag(publish.Flags(), &clusterIntegrityPrivateKey)

	clusterCmd.AddCommand(publish)
	clusterCmd.AddCommand(newClusterReplicasetCmd())

	return clusterCmd
}

// internalClusterShowModule is an entrypoint for `cluster show` command.
func internalClusterShowModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var dataCollectors libcluster.DataCollectorFactory
	checkFunc, err := integrity.GetCheckFunction(cmdCtx.Integrity)
	if err == integrity.ErrNotConfigured {
		dataCollectors = libcluster.NewDataCollectorFactory()
	} else if err != nil {
		return fmt.Errorf("failed to create collectors with integrity check: %w", err)
	} else {
		dataCollectors = libcluster.NewIntegrityDataCollectorFactory(checkFunc,
			func(path string) (io.ReadCloser, error) {
				return cmdCtx.Integrity.Repository.Read(path)
			})
	}
	showCtx.Collectors = libcluster.NewCollectorFactory(dataCollectors)

	if uri, err := parseUrl(args[0]); err == nil {
		return clustercmd.ShowUri(showCtx, uri)
	}

	// It looks like an application or an application:instance.
	configPath, _, instName, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}
	if configPath == "" {
		return fmt.Errorf("cluster configuration file does not exist for the application")
	}

	return clustercmd.ShowCluster(showCtx, configPath, instName)
}

// internalClusterPublishModule is an entrypoint for `cluster publish` command.
func internalClusterPublishModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	dataCollectors, dataPublishers, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}
	publishCtx.Collectors = libcluster.NewCollectorFactory(dataCollectors)
	publishCtx.Publishers = dataPublishers

	data, config, err := readSourceFile(args[1])
	if err != nil {
		return err
	}
	publishCtx.Src = data
	publishCtx.Config = config

	if uri, err := parseUrl(args[0]); err == nil {
		return clustercmd.PublishUri(publishCtx, uri)
	}

	// It looks like an application or an application:instance.
	configPath, appName, instName, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}
	if configPath == "" {
		if instName != "" {
			return fmt.Errorf("can not to update an instance configuration " +
				"if a cluster configuration file does not exist for the application")
		}
		configPath, err = running.GetClusterConfigPath(cliOpts,
			cmdCtx.Cli.ConfigDir, appName, false)
		if err != nil {
			return err
		}
	}
	return clustercmd.PublishCluster(publishCtx, configPath, instName)
}

// internalClusterReplicasetPromoteModule is a "cluster replicaset promote" command.
func internalClusterReplicasetPromoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	uri, err := parseUrl(args[0])
	if err != nil {
		return fmt.Errorf("failed to parse config source URI: %w", err)
	}

	promoteCtx.Collectors, promoteCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	promoteCtx.InstName = args[1]
	return clustercmd.Promote(uri, promoteCtx)
}

// internalClusterReplicasetDemoteModule is a "cluster replicaset demote" command.
func internalClusterReplicasetDemoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	uri, err := parseUrl(args[0])
	if err != nil {
		return fmt.Errorf("failed to parse config source URI: %w", err)
	}

	demoteCtx.Collectors, demoteCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	demoteCtx.InstName = args[1]
	return clustercmd.Demote(uri, demoteCtx)
}

// internalClusterReplicasetExpelModule is a "cluster replicaset expel" command.
func internalClusterReplicasetExpelModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	uri, err := parseUrl(args[0])
	if err != nil {
		return fmt.Errorf("failed to parse config source URI: %w", err)
	}

	expelCtx.Collectors, expelCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	expelCtx.InstName = args[1]
	return clustercmd.Expel(uri, expelCtx)
}

// readSourceFile reads a configuration from a source file.
func readSourceFile(path string) ([]byte, *libcluster.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read path %q: %s", path, err)
	}

	config, err := libcluster.NewYamlCollector(data).Collect()
	if err != nil {
		err = fmt.Errorf("failed to read a configuration from path %q: %s",
			path, err)
		return nil, nil, err
	}

	return data, config, nil
}

// parseUrl returns a URL, nil if string could be recognized as a URL,
// otherwise nil, an error.
func parseUrl(str string) (*url.URL, error) {
	uri, err := url.Parse(str)

	// The URL general form represented is:
	// [scheme:][//[userinfo@]host][/]path[?query][#fragment]
	// URLs that do not start with a slash after the scheme are interpreted as:
	// scheme:opaque[?query][#fragment]
	//
	// So it is enough to check scheme, host and opaque to avoid to handle
	// app:instance as a URL.
	if err != nil {
		return nil, err
	}
	if uri.Scheme != "" && uri.Host != "" && uri.Opaque == "" {
		return uri, nil
	}
	return nil, fmt.Errorf("specified string can not be recognized as URL")
}

// parseAppStr parses a string and returns an application cluster config path,
// application name and instance name or an error.
func parseAppStr(cmdCtx *cmdcontext.CmdCtx, appStr string) (string, string, string, error) {
	if !isConfigExist(cmdCtx) {
		return "", "", "",
			fmt.Errorf("unable to resolve the application name %q: %w", appStr, errNoConfig)
	}

	appName, instName, _ := strings.Cut(appStr, string(running.InstanceDelimiter))

	// Fill context for the entire application.
	// publish app:inst can work even if the `inst` instance doesn't exist right now.
	var runningCtx running.RunningCtx
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appName})
	if err != nil {
		return "", "", "", err
	}

	configPath := ""
	if len(runningCtx.Instances) != 0 {
		configPath = runningCtx.Instances[0].ClusterConfigPath
	}

	return configPath, appName, instName, nil
}
