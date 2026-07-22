package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apex/log"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	libcluster "github.com/tarantool/tt/lib/cluster"
	libconnect "github.com/tarantool/tt/lib/connect"
)

var (
	topologyFormat     string
	topologyConfigPath string
)

// hostnameExpr fetches the instance UUID and the hostname of the node.
const hostnameExpr = `return box.info.uuid, box.info.hostname`

func newDiscoverer(
	orchestrator replicaset.Orchestrator,
	conn connector.Connector,
) replicaset.Discoverer {
	switch orchestrator {
	case replicaset.OrchestratorCentralizedConfig:
		return replicaset.NewCConfigInstance(conn)
	case replicaset.OrchestratorCartridge:
		return replicaset.NewCartridgeInstance(conn)
	default:
		return replicaset.NewCustomInstance(conn)
	}
}

func discoverInstanceTopology(
	clusterConfig libcluster.ClusterConfig,
	instName string,
	configDir string,
	hostnames map[string]string,
) (*replicaset.Replicasets, bool) {
	instConfig := libcluster.Instantiate(clusterConfig, instName)
	listenData, err := instConfig.Get([]string{"iproto", "listen"})
	if err != nil {
		log.Warnf("instance %q: iproto.listen not found, skipping", instName)
		return nil, false
	}

	uri := extractListenURI(listenData)
	if uri == "" {
		log.Warnf("instance %q: no listen URI, skipping", instName)
		return nil, false
	}

	// Render Tarantool config template variables.
	groupName, rsName, _ := libcluster.FindInstance(clusterConfig, instName)
	uri = renderConfigTemplate(uri, instName, rsName, groupName)

	// Extract credentials from the cluster config for this instance.
	username, password := extractCredentials(instConfig)
	connectCtx := connect.ConnectCtx{
		Username:    username,
		Password:    password,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
	}

	// Build connection options and connect.
	network, address := parseListenURI(uri, configDir)
	connOpts := makeConnOpts(network, address, connectCtx)
	conn, err := connector.Connect(connOpts)
	if err != nil {
		log.Warnf("instance %q: failed to connect: %s", instName, err)
		return nil, false
	}
	defer conn.Close()

	// Determine orchestrator and create a discoverer.
	orchestrator, err := replicaset.EvalOrchestrator(conn)
	if err != nil {
		log.Warnf("instance %q: failed to determine orchestrator: %s", instName, err)
		return nil, false
	}

	discoverer := newDiscoverer(orchestrator, conn)

	replicasets, err := discoverer.Discovery(replicaset.SkipCache)
	if err != nil {
		log.Warnf("instance %q: discovery failed: %s", instName, err)
		return nil, false
	}

	// Collect hostname.
	hostData, err := conn.Eval(hostnameExpr, []any{}, connector.RequestOpts{})
	if err == nil {
		storeHostname(hostnames, hostData)
	}

	return &replicasets, true
}

// internalClusterTopologyModule is an entrypoint for `cluster topology` command.
func internalClusterTopologyModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switch topologyFormat {
	case "json", "table", "":
	default:
		return fmt.Errorf("unsupported format: %s (use table or json)", topologyFormat)
	}

	// Read and parse the cluster config.
	data, err := os.ReadFile(topologyConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read cluster config %q: %w", topologyConfigPath, err)
	}

	config, err := libcluster.NewYamlCollector(data).Collect()
	if err != nil {
		return fmt.Errorf("failed to parse cluster config: %w", err)
	}

	// Make a cluster config struct from the parsed config.
	clusterConfig, err := libcluster.MakeClusterConfig(config)
	if err != nil {
		return fmt.Errorf("failed to make cluster config: %w", err)
	}

	instanceNames := libcluster.Instances(clusterConfig)
	if len(instanceNames) == 0 {
		return fmt.Errorf("no instances found in the cluster config")
	}

	configDir := filepath.Dir(topologyConfigPath)

	// Connect to each instance, discover topology, collect hostnames.
	var allTopologies []replicaset.Replicasets
	hostnames := map[string]string{}

	for _, instName := range instanceNames {
		topology, ok := discoverInstanceTopology(
			clusterConfig,
			instName,
			configDir,
			hostnames,
		)

		if ok {
			allTopologies = append(allTopologies, *topology)
		}
	}

	if len(allTopologies) == 0 {
		return fmt.Errorf("failed to connect to any instance from the cluster config")
	}

	merged := mergeReplicasets(allTopologies)
	merged = filterUnreachable(merged)

	if err := printTopology(merged, hostnames); err != nil {
		return fmt.Errorf("failed to print topology: %w", err)
	}

	return nil
}

func printTopology(merged replicaset.Replicasets, hostnames map[string]string) error {
	switch topologyFormat {
	case "json":
		return printTopologyJSON(replicasetsToTopology(merged, hostnames)) //nolint:wrapcheck
	default:
		return printTopologyTable(merged, hostnames) //nolint:wrapcheck
	}
}

// extractListenURI extracts the first URI from the iproto.listen config value.
func extractListenURI(listenData any) string {
	arr, ok := listenData.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}

	m, ok := arr[0].(map[any]any)
	if !ok {
		return ""
	}

	uri, _ := m["uri"].(string)

	return uri
}

// extractCredentials reads credentials from the cluster config for an instance.
// It prefers a user with the "super" role; otherwise falls back to the first
// user with a password. Returns empty strings if no credentials are found.
func extractCredentials(instConfig *libcluster.Config) (string, string) {
	usersData, err := instConfig.Get([]string{"credentials", "users"})
	if err != nil {
		return "", ""
	}

	users, ok := usersData.(map[any]any)
	if !ok {
		return "", ""
	}

	var firstName, firstPassword string
	for name, userData := range users {
		userMap, ok := userData.(map[any]any)
		if !ok {
			continue
		}

		password, _ := userMap["password"].(string)
		if password == "" {
			continue
		}

		nameStr, _ := name.(string)

		// Prefer a user with the "super" role.
		if roles, ok := userMap["roles"].([]any); ok {
			for _, role := range roles {
				if roleStr, ok := role.(string); ok && roleStr == "super" {
					return nameStr, password
				}
			}
		}

		if firstName == "" {
			firstName = nameStr
			firstPassword = password
		}
	}

	if replicasetUser != "" {
		firstName = replicasetUser
	}
	if replicasetPassword != "" {
		firstPassword = replicasetPassword
	}

	return firstName, firstPassword
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
// Replicasets. Replicasets are deduplicated by UUID; instances are merged by
// UUID.
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
				if merged.Replicasets[i].UUID == r.UUID {
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

// filterUnreachable removes instances that could not be connected to
// and replicasets that become empty as a result.
func filterUnreachable(replicasets replicaset.Replicasets) replicaset.Replicasets {
	var filtered []replicaset.Replicaset

	for _, rs := range replicasets.Replicasets {
		var live []replicaset.Instance

		for _, inst := range rs.Instances {
			if inst.Mode != replicaset.ModeUnknown {
				live = append(live, inst)
			}
		}

		if len(live) > 0 {
			rs.Instances = live
			filtered = append(filtered, rs)
		}
	}

	replicasets.Replicasets = filtered

	return replicasets
}

// mergeInstances merges a source instance list into a replicaset, updating
// missing fields (URI, Mode) for already-known instances.
func mergeInstances(rs *replicaset.Replicaset, sources []replicaset.Instance) {
	for _, src := range sources {
		found := false

		for i := range rs.Instances {
			if rs.Instances[i].UUID == src.UUID {
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

// storeHostname extracts a UUID→hostname pair from an eval response.
func storeHostname(hostnames map[string]string, data []any) {
	if len(data) < 2 {
		return
	}

	uuid, ok := data[0].(string)
	if !ok {
		return
	}

	hostname, _ := data[1].(string)
	hostnames[uuid] = hostname
}

// replicasetsToTopology converts a discovery result into a backup manifest
// topology.
func replicasetsToTopology(
	replicasets replicaset.Replicasets,
	hostnames map[string]string,
) backup.Topology {
	topology := backup.Topology{
		Replicasets: make(map[string][]backup.TopologyInstance,
			len(replicasets.Replicasets)),
	}

	for _, rs := range replicasets.Replicasets {
		instances := make([]backup.TopologyInstance, 0, len(rs.Instances))
		for _, inst := range rs.Instances {
			instances = append(instances, backup.TopologyInstance{
				InstanceUUID: inst.UUID,
				InstanceName: inst.Alias,
				Hostname:     lookupHostname(inst.UUID, hostnames),
			})
		}
		topology.Replicasets[rs.UUID] = instances
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

// printTopologyJSON prints the topology as JSON matching the backup manifest
// topology.replicasets structure.
func printTopologyJSON(topology backup.Topology) error {
	data, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal topology: %w", err)
	}

	log.Infof("%s", string(data))

	return nil
}

// printTopologyTable prints the topology as a human-readable table. The master
// of each replicaset (Mode == RW) is marked with M.
func printTopologyTable(
	replicasets replicaset.Replicasets,
	hostnames map[string]string,
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
		log.Infof("%s (%s)", alias, rs.UUID)

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
			marker := " "
			if inst.Mode == replicaset.ModeRW {
				marker = "M"
			}
			name := inst.Alias
			if name == "" {
				name = inst.UUID
			}
			hostname := lookupHostname(inst.UUID, hostnames)
			log.Infof("    %s %s %s  %s  %s",
				marker, name, inst.UUID, hostname, inst.Mode.String())
		}
	}
	return nil
}
