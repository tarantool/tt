package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	clustercmd "github.com/tarantool/tt/cli/cluster/cmd"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/integrity"
)

const addAction = true

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

var switchCtx = clustercmd.SwitchCtx{
	Username: "",
	Password: "",
	Wait:     false,
	Timeout:  0,
}

var switchStatusCtx = clustercmd.SwitchStatusCtx{
	TaskID: "",
}

var rolesChangeCtx = clustercmd.RolesChangeCtx{}

var (
	defaultSwitchTimeout       uint64 = 30
	clusterIntegrityPrivateKey string
	clusterUriHelp             = libconnect.MakeURLHelp(map[string]any{
		"service": "etcd or tarantool config storage",
		"prefix": "a base path to Tarantool configuration in" +
			" etcd or tarantool config storage",
		"param_key":            "a target configuration key in the prefix",
		"param_name":           "a name of an instance in the cluster configuration",
		"env_TT_CLI_auth":      "Tarantool",
		"env_TT_CLI_ETCD_auth": "Etcd",
		"footer": `The priority of credentials:
environment variables < command flags < URL credentials.`,
	})

	failoverUriHelp = libconnect.MakeURLHelp(map[string]any{
		"service":              "etcd",
		"prefix":               "a base path to Tarantool configuration in etcd",
		"env_TT_CLI_ETCD_auth": "Etcd",
		"footer": `The priority of credentials:
environment variables < command flags < URL credentials.`,
	})
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
		Run:                   RunModuleFunc(internalClusterReplicasetPromoteModule),
		Args:                  cobra.ExactArgs(2),
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
		Run:                   RunModuleFunc(internalClusterReplicasetDemoteModule),
		Args:                  cobra.ExactArgs(2),
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
		Run:                   RunModuleFunc(internalClusterReplicasetExpelModule),
		Args:                  cobra.ExactArgs(2),
	}

	expelCmd.Flags().StringVarP(&expelCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	expelCmd.Flags().StringVarP(&expelCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	expelCmd.Flags().BoolVarP(&expelCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(expelCmd.Flags(), &clusterIntegrityPrivateKey)

	rolesCmd := &cobra.Command{
		Use:   "roles",
		Short: "Add or remove roles in cluster replicaset",
	}

	addRolesCmd := &cobra.Command{
		Use:   "add <URI> <ROLE_NAME> [flags]",
		Short: "Add role to an instance, group or instance",
		Long:  "Add role to an instance, group or instance\n\n" + clusterUriHelp,
		Run:   RunModuleFunc(internalClusterReplicasetRolesAddModule),
		Example: "tt cluster replicaset roles add http://user:pass@localhost:3301" +
			" roles.metrics-export --instance_name master",
		Args: cobra.ExactArgs(2),
	}

	addRolesCmd.Flags().StringVarP(&rolesChangeCtx.ReplicasetName, "replicaset", "r", "",
		"name of a target replicaset")
	addRolesCmd.Flags().StringVarP(&rolesChangeCtx.GroupName, "group", "g", "",
		"name of a target group")
	addRolesCmd.Flags().StringVarP(&rolesChangeCtx.InstName, "instance", "i", "",
		"name of a target instance")
	addRolesCmd.Flags().BoolVarP(&rolesChangeCtx.IsGlobal, "global", "G", false,
		"global config context")

	addRolesCmd.Flags().StringVarP(&rolesChangeCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	addRolesCmd.Flags().StringVarP(&rolesChangeCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	addRolesCmd.Flags().BoolVarP(&rolesChangeCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(addRolesCmd.Flags(), &clusterIntegrityPrivateKey)

	removeRolesCmd := &cobra.Command{
		Use:   "remove <URI> <ROLE_NAME> [flags]",
		Short: "Remove role from instance, group, instance or globally",
		Long:  "Remove role from instance, group, instance or globally\n\n" + clusterUriHelp,
		Run:   RunModuleFunc(internalClusterReplicasetRolesRemoveModule),
		Example: "tt cluster replicaset roles remove http://user:pass@localhost:3301" +
			" roles.metrics-export --instance_name master",
		Args: cobra.ExactArgs(2),
	}

	removeRolesCmd.Flags().StringVarP(&rolesChangeCtx.ReplicasetName, "replicaset", "r", "",
		"name of a target replicaset")
	removeRolesCmd.Flags().StringVarP(&rolesChangeCtx.GroupName, "group", "g", "",
		"name of a target group")
	removeRolesCmd.Flags().StringVarP(&rolesChangeCtx.InstName, "instance", "i", "",
		"name of a target instance")
	removeRolesCmd.Flags().BoolVarP(&rolesChangeCtx.IsGlobal, "global", "G", false,
		"global config context")

	removeRolesCmd.Flags().StringVarP(&rolesChangeCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	removeRolesCmd.Flags().StringVarP(&rolesChangeCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	removeRolesCmd.Flags().BoolVarP(&rolesChangeCtx.Force, "force", "f", false,
		"skip selecting a key for patching")
	integrity.RegisterWithIntegrityFlag(removeRolesCmd.Flags(), &clusterIntegrityPrivateKey)

	rolesCmd.AddCommand(addRolesCmd)
	rolesCmd.AddCommand(removeRolesCmd)

	cmd.AddCommand(promoteCmd)
	cmd.AddCommand(demoteCmd)
	cmd.AddCommand(expelCmd)
	cmd.AddCommand(rolesCmd)

	return cmd
}

func newClusterFailoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "failover",
		Short:   "Manage supervised failover",
		Aliases: []string{"fo"},
	}

	switchCmd := &cobra.Command{
		Use:                   "switch <URI> <INSTANCE_NAME> [flags]",
		DisableFlagsInUseLine: true,
		Short:                 "Switch master instance",
		Long:                  "Switch master instance\n\n" + failoverUriHelp,
		Example:               "tt cluster failover switch http://localhost:2379/app instance_name",
		Run:                   RunModuleFunc(internalClusterFailoverSwitchModule),
		Args:                  cobra.ExactArgs(2),
	}

	switchCmd.Flags().StringVarP(&switchCtx.Username, "username", "u", "",
		"username (used as etcd credentials)")
	switchCmd.Flags().StringVarP(&switchCtx.Password, "password", "p", "",
		"password (used as etcd credentials)")
	switchCmd.Flags().Uint64VarP(&switchCtx.Timeout, "timeout", "t", defaultSwitchTimeout,
		"timeout for command execution")
	switchCmd.Flags().BoolVarP(&switchCtx.Wait, "wait", "w", false,
		"wait for the command to complete execution")

	switchStatusCmd := &cobra.Command{
		Use:                   "switch-status <URI> <TASK_ID>",
		DisableFlagsInUseLine: true,
		Short:                 "Show master switching status",
		Long:                  "Show master switching status\n\n" + failoverUriHelp,
		Run:                   RunModuleFunc(internalClusterFailoverSwitchStatusModule),
		Args:                  cobra.ExactArgs(2),
	}

	cmd.AddCommand(switchCmd)
	cmd.AddCommand(switchStatusCmd)

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
		Run:  RunModuleFunc(internalClusterShowModule),
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
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
		Run:  RunModuleFunc(internalClusterPublishModule),
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
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
	clusterCmd.AddCommand(newClusterFailoverCmd())

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

	if opts, err := libconnect.CreateUriOpts(args[0]); err == nil {
		return clustercmd.ShowUri(showCtx, opts)
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

	if opts, err := libconnect.CreateUriOpts(args[0]); err == nil {
		return clustercmd.PublishUri(publishCtx, opts)
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
	var err error
	promoteCtx.Collectors, promoteCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	promoteCtx.InstName = args[1]
	return clustercmd.Promote(args[0], promoteCtx)
}

// internalClusterReplicasetDemoteModule is a "cluster replicaset demote" command.
func internalClusterReplicasetDemoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	demoteCtx.Collectors, demoteCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	demoteCtx.InstName = args[1]
	return clustercmd.Demote(args[0], demoteCtx)
}

// internalClusterReplicasetExpelModule is a "cluster replicaset expel" command.
func internalClusterReplicasetExpelModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	expelCtx.Collectors, expelCtx.Publishers, err = createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	expelCtx.InstName = args[1]
	return clustercmd.Expel(args[0], expelCtx)
}

// internalClusterReplicasetRolesAddModule is a "cluster replicaset roles add" command.
func internalClusterReplicasetRolesAddModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if err = checkRolesChangeFlags(addAction); err != nil {
		return err
	}

	rolesChangeCtx.Collectors, rolesChangeCtx.Publishers, err =
		createDataCollectorsAndDataPublishers(cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	rolesChangeCtx.RoleName = args[1]
	return clustercmd.ChangeRole(args[0], rolesChangeCtx, replicaset.RolesAdder{})
}

// internalClusterReplicasetRolesRemoveModule is a "cluster replicaset roles remove" command.
func internalClusterReplicasetRolesRemoveModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error
	if err = checkRolesChangeFlags(!addAction); err != nil {
		return err
	}

	col, pub, err := createDataCollectorsAndDataPublishers(
		cmdCtx.Integrity,
		clusterIntegrityPrivateKey,
	)
	if err != nil {
		return err
	}

	rolesChangeCtx.Collectors = col
	rolesChangeCtx.Publishers = pub

	rolesChangeCtx.RoleName = args[1]
	return clustercmd.ChangeRole(args[0], rolesChangeCtx, replicaset.RolesRemover{})
}

// internalClusterFailoverSwitchModule is as "cluster failover switch" command.
func internalClusterFailoverSwitchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switchCtx.InstName = args[1]
	return clustercmd.Switch(args[0], switchCtx)
}

// internalClusterFailoverSwitchStatusModule is as "cluster failover switch-status" command.
func internalClusterFailoverSwitchStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switchStatusCtx.TaskID = args[1]
	return clustercmd.SwitchStatus(args[0], switchStatusCtx)
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
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appName},
		running.ConfigLoadCluster)
	if err != nil {
		return "", "", "", err
	}

	configPath := ""
	if len(runningCtx.Instances) != 0 {
		configPath = runningCtx.Instances[0].ClusterConfigPath
	}

	return configPath, appName, instName, nil
}

// checkRolesChangeFlags checks that flags from 'cluster rs roles add/remove' command
// have correct values.
func checkRolesChangeFlags(isAdd bool) error {
	action := "added"
	if !isAdd {
		action = "removed"
	}
	if rolesChangeCtx.IsGlobal == false && rolesChangeCtx.GroupName == "" &&
		rolesChangeCtx.ReplicasetName == "" && rolesChangeCtx.InstName == "" {

		return util.NewArgError(fmt.Sprintf("need to provide flag(s) with scope roles will %s",
			action))
	}
	return nil
}
