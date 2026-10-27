package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cluster"
	clustercmd "github.com/tarantool/tt/v3/cli/cluster/cmd"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/replicaset"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

const (
	addAction            = true
	tarantoolServiceName = "Tarantool"
	etcdServiceName      = "Etcd"
	urlHelpFooter        = "footer"
	urlHelpTarantoolAuth = "env_TT_CLI_auth"
	urlHelpEtcdAuth      = "env_TT_CLI_ETCD_auth"
)

var showCtx = clustercmd.ShowCtx{
	Username:   "",
	Password:   "",
	Collectors: sdkcluster.Factory{},
	Integrity:  integrity.IntegrityCtx{Repository: nil},
	Validate:   false,
}

var publishCtx = clustercmd.PublishCtx{
	Username:      "",
	Password:      "",
	Force:         false,
	Publishers:    sdkcluster.Factory{},
	Collectors:    sdkcluster.Factory{},
	Src:           nil,
	Config:        nil,
	Group:         "",
	Replicaset:    "",
	CheckTopology: nil,
}

var promoteCtx = clustercmd.PromoteCtx{
	InstName:   "",
	Publishers: sdkcluster.Factory{},
	Collectors: sdkcluster.Factory{},
	Username:   "",
	Password:   "",
	Force:      false,
}

var demoteCtx = clustercmd.DemoteCtx{
	InstName:   "",
	Publishers: sdkcluster.Factory{},
	Collectors: sdkcluster.Factory{},
	Username:   "",
	Password:   "",
	Force:      false,
}

var expelCtx = clustercmd.ExpelCtx{
	InstName:   "",
	Publishers: sdkcluster.Factory{},
	Collectors: sdkcluster.Factory{},
	Username:   "",
	Password:   "",
	Force:      false,
}

var switchCtx = clustercmd.SwitchCtx{
	InstName: "",
	Username: "",
	Password: "",
	Wait:     false,
	Timeout:  0,
}

var switchStatusCtx = clustercmd.SwitchStatusCtx{
	TaskID: "",
}

var rolesChangeCtx = clustercmd.RolesChangeCtx{
	InstName:       "",
	GroupName:      "",
	ReplicasetName: "",
	IsGlobal:       false,
	RoleName:       "",
	Publishers:     sdkcluster.Factory{},
	Collectors:     sdkcluster.Factory{},
	Username:       "",
	Password:       "",
	Force:          false,
}

const (
	// configStorageHelp names the services a cluster configuration URL
	// points to.
	configStorageHelp = "etcd or tarantool config storage"
	// urlHelpService is the URL help key describing the service.
	urlHelpService = "service"
	// urlHelpPrefix is the URL help key describing the key prefix.
	urlHelpPrefix = "prefix"
)

var workerPublishCtx clustercmd.WorkerPublishCtx

var (
	workerPublishUsername string
	workerPublishPassword string
)

var workerShowCtx clustercmd.WorkerShowCtx

var (
	workerShowUsername string
	workerShowPassword string
)

var workerDeleteCtx clustercmd.WorkerDeleteCtx

var (
	workerDeleteUsername string
	workerDeletePassword string
)

// errNoClusterConfig is wrapped by the error for an application target of
// tt cluster show or publish whose pair has no cluster config.
var errNoClusterConfig = errors.New("no cluster config")

// errPublishToVersion is wrapped by the error for an application target of
// tt cluster publish that runs from a template of its installed version.
var errPublishToVersion = errors.New("tt does not write into an installed version")

var (
	defaultSwitchTimeout       uint64 = 30
	clusterIntegrityPrivateKey string
	clusterURIHelp             = sdkconnect.MakeURLHelp(map[string]any{
		urlHelpService: configStorageHelp,
		urlHelpPrefix: "a base path to Tarantool configuration in " +
			configStorageHelp,
		"param_key":          "a target configuration key in the prefix",
		"param_name":         "a name of an instance in the cluster configuration",
		urlHelpTarantoolAuth: tarantoolServiceName,
		urlHelpEtcdAuth:      etcdServiceName,
		urlHelpFooter: `The priority of credentials:
environment variables < command flags < URL credentials.

HTTPS connection behavior:
* The https:// scheme enables TLS for both etcd and Tarantool Config Storage
  (TCS), even when no ssl_* URL arguments are specified.
* For etcd, certificate verification is skipped if neither ssl_ca_file nor
  ssl_ca_path is specified. A configured CA enables verification unless
  explicitly disabled.
* TCS keeps the certificate verification behavior of its TLS backend. The
  default Go backend uses system CAs unless ssl_ca_file is specified.
* Do not mix http:// and https:// addresses in an etcd endpoint list. If any
  endpoint uses https://, TLS is enabled for all endpoints.`,
	})

	failoverURIHelp = sdkconnect.MakeURLHelp(map[string]any{
		urlHelpService: configStorageHelp,
		urlHelpPrefix: "a base path to Tarantool configuration in " +
			configStorageHelp,
		urlHelpTarantoolAuth: tarantoolServiceName,
		urlHelpEtcdAuth:      etcdServiceName,
		urlHelpFooter: `The priority of credentials:
environment variables < command flags < URL credentials.`,
	})

	workerURIHelp = sdkconnect.MakeURLHelp(map[string]any{
		"service":            "etcd or tarantool config storage",
		"url_path":           "/prefix/host-name/worker-name",
		"prefix":             "a base path to the worker configuration",
		"path_host-name":     "a name of the host",
		"path_worker-name":   "a name of the worker",
		urlHelpTarantoolAuth: tarantoolServiceName,
		urlHelpEtcdAuth:      etcdServiceName,
		urlHelpFooter: `The priority of credentials:
environment variables < command flags < URL credentials.`,
	})
)

func newClusterReplicasetCmd() *cobra.Command {
	const (
		targetArgs = 2
		roleArgs   = 2
	)

	cmd := &cobra.Command{
		Use:     "replicaset",
		Short:   "manage replicaset via 3.0 cluster config source",
		Aliases: []string{"rs"},
	}

	promoteCmd := &cobra.Command{
		Use:                   "promote [-f] [flags] <URI> <INSTANCE_NAME>",
		DisableFlagsInUseLine: true,
		Short:                 "Promote an instance",
		Long:                  "Promote an instance\n\n" + clusterURIHelp,
		RunE:                  internalRunE(internalClusterReplicasetPromoteModule),
		Args:                  cobra.ExactArgs(targetArgs),
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
		Long:                  "Demote an instance\n\n" + clusterURIHelp,
		RunE:                  internalRunE(internalClusterReplicasetDemoteModule),
		Args:                  cobra.ExactArgs(targetArgs),
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
		Long:                  "Expel an instance\n\n" + clusterURIHelp,
		RunE:                  internalRunE(internalClusterReplicasetExpelModule),
		Args:                  cobra.ExactArgs(targetArgs),
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
		Long:  "Add role to an instance, group or instance\n\n" + clusterURIHelp,
		RunE:  internalRunE(internalClusterReplicasetRolesAddModule),
		Example: "tt cluster replicaset roles add http://user:pass@localhost:3301" +
			" roles.metrics-export --instance_name master",
		Args: cobra.ExactArgs(roleArgs),
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
		Long:  "Remove role from instance, group, instance or globally\n\n" + clusterURIHelp,
		RunE:  internalRunE(internalClusterReplicasetRolesRemoveModule),
		Example: "tt cluster replicaset roles remove http://user:pass@localhost:3301" +
			" roles.metrics-export --instance_name master",
		Args: cobra.ExactArgs(roleArgs),
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
	const commandArgs = 2

	cmd := &cobra.Command{
		Use:     "failover",
		Short:   "Manage supervised failover",
		Aliases: []string{"fo"},
	}

	switchCmd := &cobra.Command{
		Use:                   "switch <URI> <INSTANCE_NAME> [flags]",
		DisableFlagsInUseLine: true,
		Short:                 "Switch master instance",
		Long:                  "Switch master instance\n\n" + failoverURIHelp,
		Example:               "tt cluster failover switch http://localhost:2379/app instance_name",
		RunE:                  internalRunE(internalClusterFailoverSwitchModule),
		Args:                  cobra.ExactArgs(commandArgs),
	}

	switchCmd.Flags().StringVarP(&switchCtx.Username, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	switchCmd.Flags().StringVarP(&switchCtx.Password, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	switchCmd.Flags().Uint64VarP(&switchCtx.Timeout, "timeout", "t", defaultSwitchTimeout,
		"timeout for command execution")
	switchCmd.Flags().BoolVarP(&switchCtx.Wait, "wait", "w", false,
		"wait for the command to complete execution")

	switchStatusCmd := &cobra.Command{
		Use:                   "switch-status <URI> <TASK_ID>",
		DisableFlagsInUseLine: true,
		Short:                 "Show master switching status",
		Long:                  "Show master switching status\n\n" + failoverURIHelp,
		RunE:                  internalRunE(internalClusterFailoverSwitchStatusModule),
		Args:                  cobra.ExactArgs(commandArgs),
	}

	cmd.AddCommand(switchCmd)
	cmd.AddCommand(switchStatusCmd)

	return cmd
}

func newClusterWorkerCmd() *cobra.Command {
	const publishArgs = 2

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage worker configuration",
	}

	publishCmd := &cobra.Command{
		Use:                   "publish <URI> <FILE>",
		DisableFlagsInUseLine: true,
		Short:                 "Publish a worker configuration",
		Long:                  "Publish a worker configuration\n\n" + workerURIHelp,
		Example: "tt cluster worker publish " +
			"https://user:pass@localhost:2379/tdb-workers/tdb-cluster/host1/http-server-1 " +
			"worker.yaml",
		RunE: internalRunE(internalClusterWorkerPublishModule),
		Args: cobra.ExactArgs(publishArgs),
	}
	publishCmd.Flags().StringVarP(&workerPublishUsername, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	publishCmd.Flags().StringVarP(&workerPublishPassword, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	publishCmd.Flags().BoolVar(&workerPublishCtx.Force, "force", false,
		"force publish and skip checking existence")

	showCmd := &cobra.Command{
		Use:                   "show <URI>",
		DisableFlagsInUseLine: true,
		Short:                 "Show a worker configuration",
		Long:                  "Show a worker configuration\n\n" + workerURIHelp,
		Example: "tt cluster worker show " +
			"https://user:pass@localhost:2379/tdb-workers/tdb-cluster/host1/http-server-1",
		RunE: internalRunE(internalClusterWorkerShowModule),
		Args: cobra.ExactArgs(1),
	}
	showCmd.Flags().StringVarP(&workerShowUsername, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	showCmd.Flags().StringVarP(&workerShowPassword, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")

	deleteCmd := &cobra.Command{
		Use:                   "delete <URI>",
		DisableFlagsInUseLine: true,
		Short:                 "Delete a worker configuration",
		Long:                  "Delete a worker configuration\n\n" + workerURIHelp,
		Example: "tt cluster worker delete " +
			"https://user:pass@localhost:2379/tdb-workers/tdb-cluster/host1/http-server-1",
		RunE: internalRunE(internalClusterWorkerDeleteModule),
		Args: cobra.ExactArgs(1),
	}
	deleteCmd.Flags().StringVarP(&workerDeleteUsername, "username", "u", "",
		"username (used as etcd/tarantool config storage credentials)")
	deleteCmd.Flags().StringVarP(&workerDeletePassword, "password", "p", "",
		"password (used as etcd/tarantool config storage credentials)")
	deleteCmd.Flags().BoolVar(&workerDeleteCtx.Force, "force", false,
		"force delete and skip existence check")

	cmd.AddCommand(publishCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(deleteCmd)

	return cmd
}

func NewClusterCmd() *cobra.Command {
	const publishArgs = 2

	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage cluster configuration",
	}

	show := &cobra.Command{
		Use:   "show (<APP> | <APP>:<INSTANCE> | <URI>)",
		Short: "Show a cluster configuration",
		Long: "Show a cluster configuration for an application, instance," +
			" from etcd URI or from tarantool config storage URI.\n\n" + clusterURIHelp,
		Example: "tt cluster show application_name\n" +
			"  tt cluster show application_name:instance_name\n" +
			"  tt cluster show https://user:pass@localhost:2379/tt\n" +
			"  tt cluster show https://user:pass@localhost:2379/tt?name=instance",
		RunE: internalRunE(internalClusterShowModule),
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			return internal.ClusterValidArgsFunction(&cmdCtx, cmd, toComplete,
				internal.Active)
		},
	}
	show.Flags().StringVarP(&showCtx.Username, "username", "u", "",
		"username (used as etcd credentials only)")
	show.Flags().StringVarP(&showCtx.Password, "password", "p", "",
		"password (used as etcd credentials only)")
	show.Flags().BoolVar(&showCtx.Validate, "validate", showCtx.Validate,
		"validate the configuration")
	addInstanceFlags(show)
	clusterCmd.AddCommand(show)

	publish := &cobra.Command{
		Use:   "publish (<APP> | <APP>:<INSTANCE> | <URI>) file",
		Short: "Publish a cluster configuration",
		Long: "Publish an application or an instance configuration to a cluster " +
			"configuration file, to a etcd URI or to a tarantool config storage URI.\n\n" +
			"An application gets the configuration in the config.yaml of its pair, or " +
			"in the config storage its config.yaml points to with config.etcd or " +
			"config.storage. A configuration that drops an instance of the cluster " +
			"the application describes is refused.\n\n" +
			clusterURIHelp + "\n" +
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
		RunE: internalRunE(internalClusterPublishModule),
		Args: cobra.ExactArgs(publishArgs),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ClusterValidArgsFunction(&cmdCtx, cmd, toComplete,
				internal.Active)
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
	addInstanceFlags(publish)
	// Integrity flags.
	integrity.RegisterWithIntegrityFlag(publish.Flags(), &clusterIntegrityPrivateKey)

	clusterCmd.AddCommand(publish)
	clusterCmd.AddCommand(NewClusterTopologyCmd())
	clusterCmd.AddCommand(newClusterReplicasetCmd())
	clusterCmd.AddCommand(newClusterFailoverCmd())
	clusterCmd.AddCommand(newClusterWorkerCmd())

	return clusterCmd
}

// internalClusterShowModule is an entrypoint for `cluster show` command.
func internalClusterShowModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := sdkconnect.CreateURIOpts(args[0])
	if err == nil {
		factory, cerr := cluster.NewCollectorFactory(cmdCtx.Integrity)
		if cerr != nil {
			return cerr
		}

		showCtx.Collectors = factory

		return clustercmd.ShowURI(showCtx, opts)
	}

	// It looks like an application or an application:instance.
	configPath, instName, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}

	showCtx.Integrity = cmdCtx.Integrity

	return clustercmd.ShowCluster(showCtx, configPath, instName)
}

// internalClusterPublishModule is an entrypoint for `cluster publish` command.
func internalClusterPublishModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	dataCollectors, dataPublishers, err := cluster.NewCollectorAndPublisherFactories(
		cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	publishCtx.Collectors = dataCollectors
	publishCtx.Publishers = dataPublishers

	data, config, err := readSourceFile(args[1])
	if err != nil {
		return err
	}

	publishCtx.Src = data
	publishCtx.Config = config
	// A config storage given by its URI belongs to no pair, and nothing
	// checks what goes into it.
	publishCtx.CheckTopology = nil

	opts, err := sdkconnect.CreateURIOpts(args[0])
	if err == nil {
		return clustercmd.PublishURI(publishCtx, opts)
	}

	// It looks like an application or an application:instance.
	application, configPath, instName, err := parsePublishAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}

	publishCtx.CheckTopology = func(topology []string) error {
		return application.CheckTopology(topology, "the config to publish, "+args[1])
	}

	return clustercmd.PublishApp(publishCtx, configPath, instName)
}

// internalClusterReplicasetPromoteModule is a "cluster replicaset promote" command.
func internalClusterReplicasetPromoteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error

	promoteCtx.Collectors, promoteCtx.Publishers, err = cluster.NewCollectorAndPublisherFactories(
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

	demoteCtx.Collectors, demoteCtx.Publishers, err = cluster.NewCollectorAndPublisherFactories(
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

	expelCtx.Collectors, expelCtx.Publishers, err = cluster.NewCollectorAndPublisherFactories(
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

	err = checkRolesChangeFlags(addAction)
	if err != nil {
		return err
	}

	rolesChangeCtx.Collectors, rolesChangeCtx.Publishers, err =
		cluster.NewCollectorAndPublisherFactories(cmdCtx.Integrity, clusterIntegrityPrivateKey)
	if err != nil {
		return err
	}

	rolesChangeCtx.RoleName = args[1]

	return clustercmd.ChangeRole(args[0], rolesChangeCtx, replicaset.RolesAdder{})
}

// internalClusterReplicasetRolesRemoveModule is a "cluster replicaset roles remove" command.
func internalClusterReplicasetRolesRemoveModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var err error

	err = checkRolesChangeFlags(!addAction)
	if err != nil {
		return err
	}

	col, pub, err := cluster.NewCollectorAndPublisherFactories(
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

// internalClusterWorkerPublishModule is a "cluster worker publish" command.
func internalClusterWorkerPublishModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := sdkconnect.CreateURIOpts(args[0])
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", args[0], err)
	}

	data, err := os.ReadFile(args[1])
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", args[1], err)
	}

	workerPublishCtx.Src = data

	prefix, hostName, workerName, err := clustercmd.ParseWorkerPath(opts.Prefix)
	if err != nil {
		return fmt.Errorf("failed to parse URL path: %w", err)
	}

	workerPublishCtx.Key = clustercmd.BuildWorkerStorageKey(prefix, hostName, workerName)

	stg, closeFunc, err := clustercmd.ConnectStorage(
		opts, workerPublishUsername, workerPublishPassword)
	if err != nil {
		return fmt.Errorf("failed to connect to storage: %w", err)
	}
	defer closeFunc()

	workerPublishCtx.Storage = stg

	err = clustercmd.WorkerPublish(workerPublishCtx)
	if err != nil {
		return fmt.Errorf("failed to publish worker configuration: %w", err)
	}

	return nil
}

// internalClusterWorkerShowModule is a "cluster worker show" command.
func internalClusterWorkerShowModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := sdkconnect.CreateURIOpts(args[0])
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", args[0], err)
	}

	prefix, hostName, workerName, err := clustercmd.ParseWorkerPath(opts.Prefix)
	if err != nil {
		return fmt.Errorf("failed to parse URL path: %w", err)
	}

	workerShowCtx.Key = clustercmd.BuildWorkerStorageKey(prefix, hostName, workerName)

	stg, closeFunc, err := clustercmd.ConnectStorage(opts, workerShowUsername, workerShowPassword)
	if err != nil {
		return fmt.Errorf("failed to connect to storage: %w", err)
	}
	defer closeFunc()

	workerShowCtx.Storage = stg

	output, err := clustercmd.WorkerShow(workerShowCtx)
	if err != nil {
		return fmt.Errorf("failed to show worker configuration: %w", err)
	}

	_, _ = fmt.Fprint(os.Stdout, string(output))

	return nil
}

// internalClusterWorkerDeleteModule is a "cluster worker delete" command.
func internalClusterWorkerDeleteModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := sdkconnect.CreateURIOpts(args[0])
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", args[0], err)
	}

	prefix, hostName, workerName, err := clustercmd.ParseWorkerPath(opts.Prefix)
	if err != nil {
		return fmt.Errorf("failed to parse URL path: %w", err)
	}

	workerDeleteCtx.Key = clustercmd.BuildWorkerStorageKey(prefix, hostName, workerName)

	stg, closeFunc, err := clustercmd.ConnectStorage(
		opts, workerDeleteUsername, workerDeletePassword)
	if err != nil {
		return fmt.Errorf("failed to connect to storage: %w", err)
	}
	defer closeFunc()

	workerDeleteCtx.Storage = stg

	err = clustercmd.WorkerDelete(workerDeleteCtx)
	if err != nil {
		return fmt.Errorf("failed to delete worker configuration: %w", err)
	}

	return nil
}

// readSourceFile reads a configuration from a source file and returns the raw
// bytes together with the decoded YAML map.
func readSourceFile(path string) ([]byte, map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read path %q: %w", path, err)
	}

	var decoded map[string]any

	err = yaml.Unmarshal(data, &decoded)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read a configuration from path %q: %w", path, err)
	}

	return data, decoded, nil
}

// parseAppStr parses appStr, <APP> or <APP>:<INSTANCE>, and returns the
// cluster config of the application and the instance name, empty for none.
//
// The config is the one of the pair the application runs from, found as the
// instance commands find it (app.Load): an application has one config,
// whichever command reaches it. The instance does not have to be in the
// description, so that tt cluster publish can configure one before it runs
// on this host; but an external process or an instance that runs its script
// the description declares is no instance of the cluster config, and naming
// it is an error.
//
// A pair without a config, of instances that run their scripts and of
// external processes, has nothing to show, and tt cluster publish does not
// make one up next to its description: the author of the pair adds it, as
// adding it changes what the pair runs.
func parseAppStr(cmdCtx *cmdcontext.CmdCtx, appStr string) (string, string, error) {
	application, instName, err := loadAppTarget(cmdCtx, appStr, false)
	if err != nil {
		return "", "", err
	}

	configPath, err := appConfigFile(application)
	if err != nil {
		return "", "", err
	}

	return configPath, instName, nil
}

// parsePublishAppStr is parseAppStr for tt cluster publish, which writes the
// config it returns, and returns the application as well. An application of
// the user or the system scope that runs from a template of its installed
// version is refused: the version is immutable, and a config written into it
// would go with the version, or change what another host of the same version
// runs. The pair of the host, <root>/etc/<app>/, is the one to write into; a
// project is the project's own.
//
// The pair is read with its cluster config as far as the config can be read
// (app.Options.BestEffortConfig), without checking the description against
// the topology: the config to publish takes the place of a config that
// cannot be read, such as an empty storage before the first publication, or
// of one whose topology has lost an instance of the description, and is
// checked instead (app.App.CheckTopology). Where the config goes is the
// path of the file of the pair, which is there all the same.
func parsePublishAppStr(cmdCtx *cmdcontext.CmdCtx, appStr string) (
	*app.App, string, string, error,
) {
	application, instName, err := loadAppTarget(cmdCtx, appStr, true)
	if err != nil {
		return nil, "", "", err
	}

	if application.Scope != app.ScopeProject && !application.Pair.Host() {
		return nil, "", "", fmt.Errorf("application %q of the %s scope: %w: it runs from %s, "+
			"which belongs to the installed version; put instances.yaml and config.yaml "+
			"into %s to publish into the pair of the host, or publish to a config storage "+
			"by its URI", application.Name, application.Scope, errPublishToVersion,
			application.Pair.String(), app.HostDir(application.Root, application.Name))
	}

	// A pair whose description needs the config it lacks is incomplete, as
	// every other command says; read by the best effort, it loads all the
	// same.
	if errors.Is(application.ConfigError, app.ErrIncompletePair) {
		return nil, "", "", application.ConfigError
	}

	configPath, err := appConfigFile(application)
	if err != nil {
		return nil, "", "", err
	}

	return application, configPath, instName, nil
}

// loadAppTarget loads the application appStr, <APP> or <APP>:<INSTANCE>,
// names, and returns it with the instance name, empty for none, which must
// not name an external process or an instance that runs its script. With
// bestEffort, the cluster config of its pair is read as far as it can be
// (app.Options.BestEffortConfig).
func loadAppTarget(cmdCtx *cmdcontext.CmdCtx, appStr string, bestEffort bool) (
	*app.App, string, error,
) {
	appName, instName, _ := strings.Cut(appStr, string(running.InstanceDelimiter))

	application, err := loadApp(cmdCtx, appName, bestEffort)
	if err != nil {
		return nil, "", err
	}

	err = running.RequireClusterRecord(application, instName)
	if err != nil {
		return nil, "", err
	}

	return application, instName, nil
}

// appConfigFile returns the cluster config of the pair of application, which
// must have one.
func appConfigFile(application *app.App) (string, error) {
	if application.Pair.ConfigFile == "" {
		return "", fmt.Errorf("application %q: %w: %s has instances.yaml and no "+
			"config.yaml next to it; tt does not create it: add it to the pair, or use the "+
			"URI of a config storage", application.Name, errNoClusterConfig,
			application.Pair.String())
	}

	return application.Pair.ConfigFile, nil
}

// loadApp loads the application appName and its pair as the instance
// commands do, with --scope and --template of the command; with bestEffort,
// reading the cluster config as far as it can be read.
func loadApp(cmdCtx *cmdcontext.CmdCtx, appName string, bestEffort bool) (*app.App, error) {
	opts, err := instanceTargetOptions(cmdCtx)
	if err != nil {
		return nil, err
	}

	opts.BestEffortConfig = bestEffort

	application, err := app.Load(context.Background(), appName, opts)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve the application %q: %w", appName, err)
	}

	return application, nil
}

// LoadApp loads the application appName and its pair as the instance
// commands do, for the core: the project search starts in the directory the
// commands work in, the application is looked for and its pair chosen as
// lookup says, as --scope and --template of an instance command, and the
// files are read through the integrity checks.
func LoadApp(ctx context.Context, appName string, lookup sdk.AppLookup) (*app.App, error) {
	opts, err := lookupOptions(&cmdCtx, lookup)
	if err != nil {
		return nil, err
	}

	return app.Load(ctx, appName, opts)
}

// checkRolesChangeFlags checks that flags from 'cluster rs roles add/remove' command
// have correct values.
func checkRolesChangeFlags(isAdd bool) error {
	action := "added"
	if !isAdd {
		action = "removed"
	}

	if !rolesChangeCtx.IsGlobal && rolesChangeCtx.GroupName == "" &&
		rolesChangeCtx.ReplicasetName == "" && rolesChangeCtx.InstName == "" {
		return util.NewArgError("need to provide flag(s) with scope roles will " + action)
	}

	return nil
}
