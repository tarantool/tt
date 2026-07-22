package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/apex/log"

	clustercli "github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	replicasetcmd "github.com/tarantool/tt/cli/replicaset/cmd"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

var (
	topologyFormat           string
	topologyConfigPath       string
	topologyUnixConnectMutex sync.Mutex
)

const (
	topologyStatusOK           = "OK"
	topologyStatusNotReachable = "not reachable"
	topologyFormatJSON         = "json"
	topologyFormatTable        = "table"
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
	clusterConfig libcluster.ClusterConfig,
	instName string,
	configDir string,
	connectCtx connect.ConnectCtx,
) topologyDiscoveryResult {
	instConfig := libcluster.Instantiate(clusterConfig, instName)
	advertiseData, _ := instConfig.Get([]string{"iproto", "advertise", "client"})
	uri, _ := advertiseData.(string)
	if uri == "" {
		listenData, _ := instConfig.Get([]string{"iproto", "listen"})
		if listen, ok := listenData.([]any); ok && len(listen) > 0 {
			endpoint, _ := listen[0].(map[any]any)
			uri, _ = endpoint["uri"].(string)
		}
	}
	if uri == "" {
		log.Warnf("instance %q: no client URI found, skipping", instName)
		return topologyDiscoveryResult{}
	}

	groupName, rsName, _ := libcluster.FindInstance(clusterConfig, instName)
	uri = renderConfigTemplate(uri, instName, rsName, groupName)

	network, address := parseListenURI(uri, configDir)
	connOpts := makeConnOpts(network, address, connectCtx)
	conn, err := connectTopologyInstance(connOpts)
	if err != nil {
		log.Warnf("instance %q: failed to connect: %s", instName, err)
		return topologyDiscoveryResult{}
	}
	defer conn.Close()

	result := topologyDiscoveryResult{connected: true}
	hostData, err := conn.Eval(hostnameExpr, []any{}, connector.RequestOpts{})
	if err == nil && len(hostData) >= 2 {
		result.instanceUUID, _ = hostData[0].(string)
		result.hostname, _ = hostData[1].(string)
	}

	orchestrator, err := replicaset.EvalOrchestrator(conn)
	if err != nil {
		log.Warnf("instance %q: failed to determine orchestrator: %s", instName, err)
		return result
	}

	discoverer, err := replicasetcmd.MakeInstanceOrchestrator(orchestrator, conn)
	if err != nil {
		log.Warnf("instance %q: failed to create orchestrator: %s", instName, err)
		return result
	}

	replicasets, err := discoverer.Discovery(replicaset.SkipCache)
	if err != nil {
		log.Warnf("instance %q: discovery failed: %s", instName, err)
		return result
	}

	result.topology = &replicasets
	return result
}

func connectTopologyInstance(opts connector.ConnectOpts) (connector.Connector, error) {
	if opts.Network == connector.UnixNetwork {
		topologyUnixConnectMutex.Lock()
		defer topologyUnixConnectMutex.Unlock()
	}

	return connector.Connect(opts) //nolint:wrapcheck
}

func discoverInstancesParallel(
	instanceNames []string,
	discover func(string) topologyDiscoveryResult,
) ([]replicaset.Replicasets, map[string]string, map[string]bool) {
	results := make([]topologyDiscoveryResult, len(instanceNames))
	var wg sync.WaitGroup

	for i, instName := range instanceNames {
		wg.Add(1)
		go func() {
			defer wg.Done()

			results[i] = discover(instName)
		}()
	}

	wg.Wait()

	topologies := make([]replicaset.Replicasets, 0, len(results))
	hostnames := map[string]string{}
	reachable := map[string]bool{}
	for i, result := range results {
		if result.topology != nil {
			topologies = append(topologies, *result.topology)
		}
		if result.connected {
			reachable[instanceNames[i]] = true
			if result.instanceUUID != "" {
				reachable[result.instanceUUID] = true
				hostnames[result.instanceUUID] = result.hostname
			}
		}
	}

	return topologies, hostnames, reachable
}

// internalClusterTopologyModule is an entrypoint for cluster topology command.
func internalClusterTopologyModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switch topologyFormat {
	case topologyFormatJSON, topologyFormatTable, "":
	default:
		return fmt.Errorf("unsupported format: %s (use table or json)", topologyFormat)
	}

	clusterConfig, configDir, err := loadTopologyConfig(cmdCtx, topologyConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load topology config: %w", err)
	}

	instanceNames := libcluster.Instances(clusterConfig)
	if len(instanceNames) == 0 {
		return fmt.Errorf("no instances found in the cluster config")
	}

	connectCtx := connect.ConnectCtx{
		Username:    replicasetUser,
		Password:    replicasetPassword,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
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

	if err := printTopology(merged, hostnames, reachable); err != nil {
		return fmt.Errorf("failed to print topology: %w", err)
	}

	return nil
}

func loadTopologyConfig(
	cmdCtx *cmdcontext.CmdCtx,
	source string,
) (libcluster.ClusterConfig, string, error) {
	dataCollectors, err := createDataCollectors(cmdCtx.Integrity)
	if err != nil {
		return libcluster.ClusterConfig{}, "",
			fmt.Errorf("failed to create data collectors: %w", err)
	}
	collectors := libcluster.NewCollectorFactory(dataCollectors)

	if uriOpts, err := libconnect.CreateUriOpts(source); err == nil {
		connOpts := libcluster.ConnectOpts{
			Username: replicasetUser,
			Password: replicasetPassword,
		}
		collector, closeConnection, err := libcluster.CreateCollector(
			collectors, connOpts, uriOpts,
		)
		if err != nil {
			return libcluster.ClusterConfig{}, "",
				fmt.Errorf("failed to connect to cluster config storage: %w", err)
		}
		defer closeConnection()

		config, err := collector.Collect()
		if err != nil {
			return libcluster.ClusterConfig{}, "",
				fmt.Errorf("failed to collect cluster config: %w", err)
		}

		clusterConfig, err := libcluster.MakeClusterConfig(config)
		if err != nil {
			return libcluster.ClusterConfig{}, "",
				fmt.Errorf("failed to parse cluster config: %w", err)
		}

		return clusterConfig, ".", nil
	}

	clusterConfig, err := clustercli.GetClusterConfig(collectors, source)
	if err != nil {
		return libcluster.ClusterConfig{}, "",
			fmt.Errorf("failed to load cluster config %q: %w", source, err)
	}

	return clusterConfig, filepath.Dir(source), nil
}

func printTopology(
	merged replicaset.Replicasets,
	hostnames map[string]string,
	reachable map[string]bool,
) error {
	switch topologyFormat {
	case topologyFormatJSON:
		topology := replicasetsToTopology(merged, hostnames, reachable)
		return printTopologyJSON(topology) //nolint:wrapcheck
	default:
		return printTopologyTable(merged, hostnames, reachable) //nolint:wrapcheck
	}
}

func topologyFromConfig(clusterConfig libcluster.ClusterConfig) replicaset.Replicasets {
	var topology replicaset.Replicasets

	groupNames := make([]string, 0, len(clusterConfig.Groups))
	for groupName := range clusterConfig.Groups {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		group := clusterConfig.Groups[groupName]
		replicasetNames := make([]string, 0, len(group.Replicasets))
		for replicasetName := range group.Replicasets {
			replicasetNames = append(replicasetNames, replicasetName)
		}
		sort.Strings(replicasetNames)

		for _, replicasetName := range replicasetNames {
			configuredReplicaset := group.Replicasets[replicasetName]
			rs := replicaset.Replicaset{Alias: replicasetName}

			instanceNames := make([]string, 0, len(configuredReplicaset.Instances))
			for instanceName := range configuredReplicaset.Instances {
				instanceNames = append(instanceNames, instanceName)
			}
			sort.Strings(instanceNames)

			for _, instanceName := range instanceNames {
				instanceConfig := libcluster.Instantiate(clusterConfig, instanceName)
				instanceUUIDData, _ := instanceConfig.Get([]string{"database", "instance_uuid"})
				replicasetUUID, _ := instanceConfig.Get([]string{
					"database", "replicaset_uuid",
				})
				if rs.UUID == "" {
					rs.UUID, _ = replicasetUUID.(string)
				}
				instanceUUID, _ := instanceUUIDData.(string)
				rs.Instances = append(rs.Instances, replicaset.Instance{
					Alias: instanceName,
					UUID:  instanceUUID,
				})
			}

			topology.Replicasets = append(topology.Replicasets, rs)
		}
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
	network, address := libconnect.ParseBaseURI(uri)

	if network == connector.UnixNetwork && strings.HasPrefix(address, "./") {
		address = filepath.Join(configDir, address)
	}

	return network, address
}

// mergeReplicasets merges per-instance discovery results into a single
// Replicasets. Runtime UUIDs are merged into the aliases loaded from config.
func mergeReplicasets(all []replicaset.Replicasets) replicaset.Replicasets {
	var merged replicaset.Replicasets

	for _, rs := range all {
		if merged.Orchestrator == replicaset.OrchestratorUnknown {
			merged.Orchestrator = rs.Orchestrator
		}

		if merged.State == replicaset.StateUnknown {
			merged.State = rs.State
		}

		for _, r := range rs.Replicasets {
			found := false

			for i := range merged.Replicasets {
				if (merged.Replicasets[i].UUID != "" &&
					merged.Replicasets[i].UUID == r.UUID) ||
					(merged.Replicasets[i].Alias != "" &&
						merged.Replicasets[i].Alias == r.Alias) {

					if merged.Replicasets[i].UUID == "" {
						merged.Replicasets[i].UUID = r.UUID
					}
					mergeInstances(&merged.Replicasets[i], r.Instances)
					found = true
					break
				}
			}

			if !found {
				merged.Replicasets = append(merged.Replicasets, r)
			}
		}
	}

	return merged
}

// mergeInstances merges a source instance list into a replicaset, updating
// missing fields (URI, Mode) for already-known instances.
func mergeInstances(rs *replicaset.Replicaset, sources []replicaset.Instance) {
	for _, src := range sources {
		found := false

		for i := range rs.Instances {
			if (rs.Instances[i].UUID != "" && rs.Instances[i].UUID == src.UUID) ||
				(rs.Instances[i].Alias != "" && rs.Instances[i].Alias == src.Alias) {

				if rs.Instances[i].UUID == "" {
					rs.Instances[i].UUID = src.UUID
				}
				if rs.Instances[i].URI == "" {
					rs.Instances[i].URI = src.URI
				}

				if rs.Instances[i].Mode == replicaset.ModeUnknown {
					rs.Instances[i].Mode = src.Mode
				}

				found = true

				break
			}
		}

		if !found {
			rs.Instances = append(rs.Instances, src)
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

	for _, rs := range replicasets.Replicasets {
		instances := make([]topologyInstanceOutput, 0, len(rs.Instances))
		for _, inst := range rs.Instances {
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
		replicasetID := rs.UUID
		if replicasetID == "" {
			replicasetID = rs.Alias
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

	fmt.Println(string(data))

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

	log.Info("Active cluster topology\n")

	for _, rs := range replicasets.Replicasets {
		alias := rs.Alias
		if alias == "" {
			alias = rs.UUID
		}
		replicasetReachable := false
		for _, inst := range rs.Instances {
			if reachable[inst.UUID] || reachable[inst.Alias] {
				replicasetReachable = true
				break
			}
		}
		replicasetHeader := alias
		if rs.UUID != "" {
			replicasetHeader = fmt.Sprintf("%s (%s)", alias, rs.UUID)
		}
		if !replicasetReachable {
			replicasetHeader += "  " + topologyStatusNotReachable
		}
		log.Info(replicasetHeader)

		instances := make([]replicaset.Instance, len(rs.Instances))
		copy(instances, rs.Instances)
		sort.SliceStable(instances, func(i, j int) bool {
			if (instances[i].Mode == replicaset.ModeRW) !=
				(instances[j].Mode == replicaset.ModeRW) {

				return instances[i].Mode == replicaset.ModeRW
			}
			return instances[i].Alias < instances[j].Alias
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
			log.Infof("    %s %s  %s  %s  %s",
				name, inst.UUID, hostname, mode, status)
		}
	}
	return nil
}
