package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/formatter"
	"github.com/tarantool/tt/sdk/log"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	clustercli "github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/replicaset"
	replicasetcmd "github.com/tarantool/tt/v3/cli/replicaset/cmd"
)

var (
	errNoTopologyInstances       = errors.New("no instances found in the cluster config")
	errUnsupportedTopologyFormat = errors.New("unsupported format")
	topologyConfigPath           string
	topologyFormat               string
)

const (
	topologyFormatJSON         = "json"
	topologyFormatTable        = "table"
	topologyStatusOK           = "OK"
	topologyStatusNotReachable = "not reachable"
)

type topologyOutput struct {
	Replicasets map[string][]topologyInstanceOutput `json:"replicasets"`
}

type topologyInstanceOutput struct {
	InstanceUUID string `json:"instance_uuid"`
	InstanceName string `json:"instance_name"`
	Hostname     string `json:"hostname"`
	Mode         string `json:"mode"`
	Status       string `json:"status"`
}

// hostnameExpr fetches the instance UUID and the hostname of the node.
const hostnameExpr = `return box.info.uuid, box.info.hostname`

type topologyDiscoveryResult struct {
	topology     *replicaset.Replicasets
	instanceUUID string
	hostname     string
	connected    bool
}

func discoverInstanceTopology(
	clusterConfig goconfig.Config,
	instName string,
	configDir string,
	connectCtx connect.ConnectCtx,
) topologyDiscoveryResult {
	var unavailable topologyDiscoveryResult

	instConfig, err := clustercli.InstanceConfig(clusterConfig, instName)
	if err != nil {
		log.Warnf("instance %q: failed to resolve configuration: %s", instName, err)

		return unavailable
	}

	uri := topologyInstanceURI(instConfig)
	if uri == "" {
		log.Warnf("instance %q: no client URI found, skipping", instName)

		return unavailable
	}

	groupName, rsName, _ := clustercli.FindInstance(clusterConfig, instName)

	uri = renderConfigTemplate(uri, instName, rsName, groupName)

	network, address := parseListenURI(uri, configDir)
	connOpts := makeConnOpts(network, address, connectCtx)

	conn, err := connectTopologyInstance(connOpts)
	if err != nil {
		log.Warnf("instance %q: failed to connect: %s", instName, err)

		return unavailable
	}

	defer func() { _ = conn.Close() }()

	result := topologyDiscoveryResult{
		topology: nil, instanceUUID: "", hostname: "", connected: true,
	}
	hostData, err := conn.Eval(hostnameExpr, []any{}, connector.RequestOpts{
		PushCallback: nil, ReadTimeout: 0, ResData: nil,
	})

	if err == nil && len(hostData) >= 2 {
		result.instanceUUID, _ = hostData[0].(string)
		result.hostname, _ = hostData[1].(string)
	}

	replicasets, err := replicasetcmd.DiscoverInstance(conn)
	if err != nil {
		log.Warnf("instance %q: discovery failed: %s", instName, err)

		return result
	}

	result.topology = &replicasets

	return result
}

// topologyInstanceURI prefers the advertised client endpoint over the listener.
func topologyInstanceURI(config goconfig.Config) string {
	var uri string

	_, _ = config.Get(goconfig.NewKeyPath("iproto/advertise/client"), &uri)

	if uri != "" {
		return uri
	}

	var listen []struct {
		URI string `yaml:"uri"`
	}

	_, err := config.Get(goconfig.NewKeyPath("iproto/listen"), &listen)

	if err != nil || len(listen) == 0 {
		return ""
	}

	return listen[0].URI
}

func connectTopologyInstance(opts connector.ConnectOpts) (connector.Connector, error) {
	return connector.Connect(opts)
}

func discoverInstancesParallel(
	instanceNames []string,
	discover func(string) topologyDiscoveryResult,
) ([]replicaset.Replicasets, map[string]string, map[string]bool) {
	results := make([]topologyDiscoveryResult, len(instanceNames))

	var discoveryGroup sync.WaitGroup

	for index, instName := range instanceNames {
		discoveryGroup.Go(func() {
			results[index] = discover(instName)
		})
	}

	discoveryGroup.Wait()

	topologies := make([]replicaset.Replicasets, 0, len(results))
	hostnames := map[string]string{}
	reachable := map[string]bool{}

	for index, result := range results {
		if result.topology != nil {
			topologies = append(topologies, *result.topology)
		}

		if result.connected {
			reachable[instanceNames[index]] = true
			if result.instanceUUID != "" {
				reachable[result.instanceUUID] = true
				hostnames[result.instanceUUID] = result.hostname
			}
		}
	}

	return topologies, hostnames, reachable
}

// discoverClusterTopology loads the cluster config, discovers the live topology
// from all instances, and returns the merged result. It is shared by
// cluster topology, backup and restore planning.
func discoverClusterTopology(
	cmdCtx *cmdcontext.CmdCtx,
	configPath string,
	connectCtx connect.ConnectCtx,
) (replicaset.Replicasets, map[string]string, map[string]bool, error) {
	clusterConfig, configDir, err := loadTopologyConfig(cmdCtx, configPath)
	if err != nil {
		return replicaset.Replicasets{}, nil, nil,
			fmt.Errorf("failed to load topology config: %w", err)
	}

	instanceNames, err := clustercli.Instances(clusterConfig)
	if err != nil {
		return replicaset.Replicasets{}, nil, nil,
			fmt.Errorf("failed to find configured instances: %w", err)
	}

	if len(instanceNames) == 0 {
		return replicaset.Replicasets{}, nil, nil,
			errNoTopologyInstances
	}

	allTopologies, hostnames, reachable := discoverInstancesParallel(
		instanceNames,
		func(instName string) topologyDiscoveryResult {
			return discoverInstanceTopology(
				clusterConfig,
				instName,
				configDir,
				connectCtx,
			)
		},
	)

	configTopology := topologyFromConfig(clusterConfig)

	allTopologies = append(
		[]replicaset.Replicasets{configTopology},
		allTopologies...,
	)

	merged := mergeReplicasets(allTopologies)

	return merged, hostnames, reachable, nil
}

func loadTopologyConfig(
	cmdCtx *cmdcontext.CmdCtx,
	source string,
) (goconfig.Config, string, error) {
	uriOpts, err := sdkconnect.CreateURIOpts(source)
	if err == nil {
		return loadTopologyRemote(cmdCtx, uriOpts)
	}

	cfg, err := clustercli.GetClusterConfig(context.Background(), source, cmdCtx.Integrity)
	if err != nil {
		return goconfig.Config{}, "",
			fmt.Errorf("failed to load cluster config %q: %w", source, err)
	}

	return cfg.Snapshot(), filepath.Dir(source), nil
}

func loadTopologyRemote(
	cmdCtx *cmdcontext.CmdCtx, uriOpts sdkconnect.URIOpts,
) (goconfig.Config, string, error) {
	collectors, err := clustercli.NewCollectorFactory(cmdCtx.Integrity)
	if err != nil {
		return goconfig.Config{}, "", fmt.Errorf("failed to create data collectors: %w", err)
	}

	connOpts := sdkcluster.ConnectOpts{Username: replicasetUser, Password: replicasetPassword}

	stor, cleanup, storageType, err := sdkcluster.NewStorageConnection(connOpts, uriOpts)
	if err != nil {
		return goconfig.Config{}, "",
			fmt.Errorf("failed to connect to cluster config storage: %w", err)
	}
	defer cleanup()

	collector, err := collectors.NewRemoteStorage(stor, uriOpts.Prefix,
		uriOpts.Params["key"], uriOpts.Timeout, storageType)
	if err != nil {
		return goconfig.Config{}, "", fmt.Errorf("failed to create config collector: %w", err)
	}

	ctx := context.Background()

	data, err := clustercli.CollectDataBytes(ctx, collector)
	if err != nil {
		return goconfig.Config{}, "", fmt.Errorf("failed to collect cluster config: %w", err)
	}

	cfg, err := clustercli.BuildGoConfigFromBytes(ctx, data)
	if err != nil {
		return goconfig.Config{}, "", fmt.Errorf("failed to parse cluster config: %w", err)
	}

	return cfg, ".", nil
}

// topologyFromConfig retains configured members even when they are unreachable.
// EffectiveAll resolves the v3 configuration inheritance once for the cluster.
func topologyFromConfig(clusterConfig goconfig.Config) replicaset.Replicasets {
	var topology replicaset.Replicasets

	all, err := clusterConfig.EffectiveAll()
	if err != nil {
		return topology
	}

	indices := map[string]int{}

	var groups map[string]any

	_, _ = clusterConfig.Get(goconfig.NewKeyPath("groups"), &groups)

	for _, groupName := range slices.Sorted(maps.Keys(groups)) {
		var configured map[string]any

		path := goconfig.KeyPath{"groups", groupName, "replicasets"}

		_, _ = clusterConfig.Get(path, &configured)

		for _, name := range slices.Sorted(maps.Keys(configured)) {
			key := append(slices.Clone(path), name).String()

			indices[key] = len(topology.Replicasets)

			var replicaSet replicaset.Replicaset

			replicaSet.Alias = name
			topology.Replicasets = append(topology.Replicasets, replicaSet)
		}
	}

	for _, path := range slices.Sorted(maps.Keys(all)) {
		parts := goconfig.NewKeyPath(path)
		if len(parts) != 6 || parts[0] != "groups" ||
			parts[2] != "replicasets" || parts[4] != "instances" {
			continue
		}

		key := parts[0:4].String()
		index, ok := indices[key]

		if !ok {
			index = len(topology.Replicasets)
			indices[key] = index

			var configured replicaset.Replicaset

			configured.Alias = parts[3]
			topology.Replicasets = append(topology.Replicasets, configured)
		}

		replicaSet := &topology.Replicasets[index]
		instance := all[path]

		var instanceUUID string

		_, _ = instance.Get(goconfig.NewKeyPath("database/instance_uuid"), &instanceUUID)

		if replicaSet.UUID == "" {
			_, _ = instance.Get(goconfig.NewKeyPath("database/replicaset_uuid"), &replicaSet.UUID)
		}

		var member replicaset.Instance

		member.Alias, member.UUID = parts[5], instanceUUID
		replicaSet.Instances = append(replicaSet.Instances, member)
	}

	return topology
}

// renderConfigTemplate replaces Tarantool config template variables in a URI.
func renderConfigTemplate(uri, instName, rsName, groupName string) string {
	uri = strings.ReplaceAll(uri, "{{ instance_name }}", instName)
	uri = strings.ReplaceAll(uri, "{{ replicaset_name }}", rsName)
	uri = strings.ReplaceAll(uri, "{{ group_name }}", groupName)

	return uri
}

// parseListenURI parses a Tarantool listen URI and returns the network type
// and address.
func parseListenURI(uri, configDir string) (string, string) {
	network, address := sdkconnect.ParseBaseURI(uri)

	if network == connector.UnixNetwork && !filepath.IsAbs(address) {
		address = filepath.Join(configDir, address)
	}

	return network, address
}

// mergeReplicasets merges per-instance discovery results into a single
// Replicasets. Runtime UUIDs are merged into the aliases loaded from config.
func mergeReplicasets(all []replicaset.Replicasets) replicaset.Replicasets {
	var merged replicaset.Replicasets

	for _, replicaSet := range all {
		if merged.Orchestrator == replicaset.OrchestratorUnknown {
			merged.Orchestrator = replicaSet.Orchestrator
		}

		if merged.State == replicaset.StateUnknown {
			merged.State = replicaSet.State
		}

		for _, candidate := range replicaSet.Replicasets {
			found := false

			for index := range merged.Replicasets {
				if (merged.Replicasets[index].UUID != "" &&
					merged.Replicasets[index].UUID == candidate.UUID) ||
					(merged.Replicasets[index].Alias != "" &&
						merged.Replicasets[index].Alias == candidate.Alias) {
					if merged.Replicasets[index].UUID == "" {
						merged.Replicasets[index].UUID = candidate.UUID
					}

					mergeInstances(&merged.Replicasets[index], candidate.Instances)

					found = true

					break
				}
			}

			if !found {
				merged.Replicasets = append(merged.Replicasets, candidate)
			}
		}
	}

	return merged
}

// mergeInstances merges a source instance list into a replicaset, updating
// missing fields (URI, Mode) for already-known instances.
func mergeInstances(replicaSet *replicaset.Replicaset, sources []replicaset.Instance) {
	for _, src := range sources {
		found := false

		for index := range replicaSet.Instances {
			instance := &replicaSet.Instances[index]
			if (instance.UUID != "" && instance.UUID == src.UUID) ||
				(instance.Alias != "" && instance.Alias == src.Alias) {
				if instance.UUID == "" {
					instance.UUID = src.UUID
				}

				if instance.URI == "" {
					instance.URI = src.URI
				}

				if instance.Mode == replicaset.ModeUnknown {
					instance.Mode = src.Mode
				}

				found = true

				break
			}
		}

		if !found {
			replicaSet.Instances = append(replicaSet.Instances, src)
		}
	}
}

func replicasetsToTopology(
	replicasets replicaset.Replicasets,
	hostnames map[string]string,
	reachable map[string]bool,
) topologyOutput {
	topology := topologyOutput{
		Replicasets: make(map[string][]topologyInstanceOutput,
			len(replicasets.Replicasets)),
	}

	for _, replicaSet := range replicasets.Replicasets {
		instances := make([]topologyInstanceOutput, 0, len(replicaSet.Instances))
		for _, inst := range replicaSet.Instances {
			status := topologyStatusNotReachable
			if reachable[inst.UUID] || reachable[inst.Alias] {
				status = topologyStatusOK
			}

			instances = append(instances, topologyInstanceOutput{
				InstanceUUID: inst.UUID,
				InstanceName: inst.Alias,
				Hostname:     lookupHostname(inst.UUID, hostnames),
				Mode:         formatMode(inst.Mode),
				Status:       status,
			})
		}

		replicasetID := replicaSet.UUID
		if replicasetID == "" {
			replicasetID = replicaSet.Alias
		}

		topology.Replicasets[replicasetID] = instances
	}

	return topology
}

// lookupHostname returns the hostname for the instance UUID.
func lookupHostname(uuid string, hostnames map[string]string) string {
	if h, ok := hostnames[uuid]; ok {
		return h
	}

	return ""
}

// formatMode returns a human-readable string for the instance mode.
func formatMode(mode replicaset.Mode) string {
	if mode == replicaset.ModeRead {
		return "ro"
	}

	return mode.String()
}

func printTopologyJSON(topology topologyOutput) error {
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal topology: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stdout, string(data))

	return nil
}

// printTopologyTable prints the topology as a human-readable table.
func printTopologyTable(
	replicasets replicaset.Replicasets,
	hostnames map[string]string,
	reachable map[string]bool,
) error {
	if len(replicasets.Replicasets) == 0 {
		log.Warn("No replicasets found.")

		return nil
	}

	_, _ = fmt.Fprint(os.Stdout, "Active cluster topology\n\n")

	for _, replicaSet := range replicasets.Replicasets {
		alias := replicaSet.Alias
		if alias == "" {
			alias = replicaSet.UUID
		}

		replicasetReachable := false

		for _, inst := range replicaSet.Instances {
			if reachable[inst.UUID] || reachable[inst.Alias] {
				replicasetReachable = true
				break
			}
		}

		replicasetHeader := alias
		if replicaSet.UUID != "" {
			replicasetHeader = fmt.Sprintf("%s (%s)", alias, replicaSet.UUID)
		}

		if !replicasetReachable {
			replicasetHeader += "  " + topologyStatusNotReachable
		}

		_, _ = fmt.Fprintln(os.Stdout, replicasetHeader)

		instances := make([]replicaset.Instance, len(replicaSet.Instances))
		copy(instances, replicaSet.Instances)
		sort.SliceStable(instances, func(left, right int) bool {
			if (instances[left].Mode == replicaset.ModeRW) !=
				(instances[right].Mode == replicaset.ModeRW) {
				return instances[left].Mode == replicaset.ModeRW
			}

			return instances[left].Alias < instances[right].Alias
		})

		for _, inst := range instances {
			name := inst.Alias
			if name == "" {
				name = inst.UUID
			}

			hostname := lookupHostname(inst.UUID, hostnames)
			mode := formatMode(inst.Mode)
			status := topologyStatusNotReachable

			if reachable[inst.UUID] || reachable[inst.Alias] {
				status = topologyStatusOK
			}

			_, _ = fmt.Fprintf(os.Stdout, "    %s %s  %s  %s  %s\n",
				name, inst.UUID, hostname, mode, status)
		}
	}

	return nil
}

// NewClusterTopologyCmd creates the command that shows the current cluster topology.
func NewClusterTopologyCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "topology -c <CLUSTER_CONFIG|URI> [flags]",
		Short: "Show the current cluster topology",
		Long: "Discover and print the current cluster topology by connecting to " +
			"cluster nodes via net.box.\n\n" +
			"The cluster configuration can be loaded from a file, etcd, or " +
			"Tarantool Config Storage. It determines the instances and their " +
			"listen URIs. Each instance is contacted and the live topology is " +
			"merged into one view.\n\n" +
			"JSON output contains replicasets keyed by UUID and their instances " +
			"(instance_uuid, instance_name, hostname, mode, status). If a replicaset " +
			"is unreachable and its UUID is not configured, its name is used as " +
			"the key. Unreachable instances have status \"not reachable\".\n\n" +
			clusterURIHelp,
		RunE: internalRunE(internalClusterTopologyModule),
		Args: cobra.NoArgs,
	}
	addTarantoolConnectFlags(command)
	command.Flags().StringVarP(&topologyConfigPath, "config", "c", "",
		"path or URI of the cluster configuration")
	command.Flags().StringVar(&topologyFormat, "format", topologyFormatTable,
		"output format: table or json")

	_ = command.MarkFlagRequired("config")

	return command
}

// internalClusterTopologyModule is the entry point for cluster topology.
func internalClusterTopologyModule(commandContext *cmdcontext.CmdCtx, _ []string) error {
	switch topologyFormat {
	case topologyFormatJSON, topologyFormatTable, "":
	default:
		return fmt.Errorf("%w: %s (use table or json)",
			errUnsupportedTopologyFormat, topologyFormat)
	}

	connectContext := topologyConnectContext()

	merged, hostnames, reachable, err := discoverClusterTopology(
		commandContext, topologyConfigPath, connectContext,
	)
	if err != nil {
		return fmt.Errorf("failed to discover cluster topology: %w", err)
	}

	err = printTopology(merged, hostnames, reachable)
	if err != nil {
		return fmt.Errorf("failed to print topology: %w", err)
	}

	return nil
}

func topologyConnectContext() connect.ConnectCtx {
	return connect.ConnectCtx{
		SrcFile: "", Language: 0, Format: formatter.DefaultFormat, Interactive: false,
		ConnectTarget: "", Binary: false, Evaler: "",
		Username: replicasetUser, Password: replicasetPassword,
		ConnectTimeout: replicasetConnectTimeout,
		SslKeyFile:     replicasetSslKeyFile, SslCertFile: replicasetSslCertFile,
		SslCaFile: replicasetSslCaFile, SslCiphers: replicasetSslCiphers,
		SslPassword: replicasetSslPassword, SslPasswordFile: replicasetSslPasswordFile,
	}
}

func printTopology(
	merged replicaset.Replicasets,
	hostnames map[string]string,
	reachable map[string]bool,
) error {
	switch topologyFormat {
	case topologyFormatJSON:
		return printTopologyJSON(replicasetsToTopology(merged, hostnames, reachable))
	default:
		return printTopologyTable(merged, hostnames, reachable)
	}
}
