package replicaset

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	defaultCartridgeReplicasetsFilename = "replicasets.yml"
	defaultCartridgeInstancesFilename   = "instances.yml"
)

var (
	//go:embed lua/cartridge/get_topology_replicasets_body.lua
	cartridgeGetTopologyReplicasetsBody string

	//go:embed lua/cartridge/get_instance_info_body.lua
	cartridgeGetInstanceInfoBody string

	//go:embed lua/cartridge/edit_replicasets_body.lua
	cartridgeEditReplicasetsBody string

	//go:embed lua/cartridge/edit_instances_body.lua
	cartridgeEditInstancesBody string

	//go:embed lua/cartridge/failover_promote_body.lua
	cartridgeFailoverPromoteBody string

	//go:embed lua/cartridge/wait_healthy_body.lua
	cartridgeWaitHealthyBody string

	//go:embed lua/cartridge/bootstrap_vshard_body.lua
	cartridgeBootstrapVShardBody string

	cartridgeGetVersionBody = "return require('cartridge').VERSION or '1'"
)

// cartridgeTopology used to export topology information from a Tarantool
// instance with the Cartridge orchestrator.
type cartridgeTopology struct {
	// Failover is a string representation of a failover.
	Failover string
	// Provider is a string representation of a state provider.
	Provider string
	// Replicasets is an array of replicasets.
	Replicasets []Replicaset
	// IsCritical indicates whether instance has critical issues.
	IsCritical bool `mapstructure:"is_critical"`
	// IsBootstrapped indicates whether instance is bootstrapped.
	IsBootstrapped bool
}

// CartridgeInstance is an instance with the Cartridge orchestrator.
type CartridgeInstance struct {
	cachedDiscoverer
	evaler connector.Evaler
}

// cartridgeInstance describes a cartridge instance.
type cartridgeInstance struct {
	Failover       Failover
	ReplicasetUUID string
	InstanceUUID   string
}

// NewCartridgeInstance creates a new CartridgeInstance object for the evaler.
func NewCartridgeInstance(evaler connector.Evaler) *CartridgeInstance {
	inst := &CartridgeInstance{
		evaler: evaler,
	}
	inst.discoverer = inst
	return inst
}

// getCartridgeTopology returns a cartridge topology received from an instance.
func getCartridgeTopology(evaler connector.Evaler) (cartridgeTopology, error) {
	var topology cartridgeTopology
	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(cartridgeGetTopologyReplicasetsBody, args, opts)
	if err != nil {
		return topology, err
	}

	if len(data) != 1 {
		return topology, fmt.Errorf("unexpected response: %v", data)
	}

	if err := mapstructure.Decode(data[0], &topology); err != nil {
		return topology, fmt.Errorf("failed to parse a response: %w", err)
	}

	topology.IsBootstrapped = len(topology.Replicasets) > 0
	return topology, nil
}

// getCartridgeReplicasets converts cartridgeTopology to Replicasets.
func getCartridgeReplicasets(topology cartridgeTopology) Replicasets {
	replicasets := Replicasets{
		State:        StateUninitialized,
		Replicasets:  topology.Replicasets,
		Orchestrator: OrchestratorCartridge,
	}
	if topology.IsBootstrapped {
		replicasets.State = StateBootstrapped
		failover := ParseFailover(topology.Failover)
		provider := ParseStateProvider(topology.Provider)
		for i := range replicasets.Replicasets {
			replicasets.Replicasets[i].Failover = failover
			replicasets.Replicasets[i].StateProvider = provider
		}
	}
	return replicasets
}

// Promote promotes a cartridge instance.
func (c *CartridgeInstance) Promote(ctx PromoteCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return err
	}
	uuid, _, err := getCartridgeInstanceInfo(c.evaler)
	if err != nil {
		return err
	}
	var (
		inst  cartridgeInstance
		found bool
	)
loop:
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.UUID == uuid {
				inst.ReplicasetUUID = replicaset.UUID
				inst.InstanceUUID = instance.UUID
				inst.Failover = replicaset.Failover
				found = true
				break loop
			}
		}
	}
	if !found {
		return fmt.Errorf("instance with uuid %q not found in a configured replicaset", uuid)
	}
	return cartridgePromote(c.evaler, inst, ctx.Force, ctx.Timeout)
}

// Demote is not supported for a single instance by the Cartridge orchestrator.
func (c *CartridgeInstance) Demote(ctx DemoteCtx) error {
	return newErrDemoteByInstanceNotSupported(OrchestratorCartridge)
}

// discovery returns a replicaset topology for a single instance with the
// Cartridge orchestrator.
func (c *CartridgeInstance) discovery() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}
	topology, err := getCartridgeTopology(c.evaler)
	if err != nil {
		return replicasets, err
	}

	replicasets = getCartridgeReplicasets(topology)
	if len(replicasets.Replicasets) > 0 {
		replicasets, err = updateCartridgeInstance(c.evaler, nil, replicasets)
		if err != nil {
			return replicasets, err
		}
	}
	return recalculateMasters(replicasets), nil
}

// Expel is not supported for a single instance by the Cartridge orchestrator.
func (c *CartridgeInstance) Expel(ctx ExpelCtx) error {
	return newErrExpelByInstanceNotSupported(OrchestratorCartridge)
}

// Bootstrap is not supported for a single instance by the Cartridge orchestrator.
func (c *CartridgeInstance) Bootstrap(ctx BootstrapCtx) error {
	return newErrBootstrapByInstanceNotSupported(OrchestratorCartridge)
}

// BootstrapVShard bootstraps the vshard for a single instance by the Cartridge orchestrator.
func (c *CartridgeInstance) BootstrapVShard(ctx VShardBootstrapCtx) error {
	err := cartridgeBootstrapVShard(c.evaler, ctx.Timeout)
	if err != nil {
		return wrapCartridgeVShardBoostrapError(err)
	}
	return nil
}

// CartridgeApplication is an application with the Cartridge orchestrator.
type CartridgeApplication struct {
	cachedDiscoverer
	runningCtx running.RunningCtx
}

// NewCartridgeApplication creates a new CartridgeApplication object.
func NewCartridgeApplication(runningCtx running.RunningCtx) *CartridgeApplication {
	app := &CartridgeApplication{
		runningCtx: runningCtx,
	}
	app.discoverer = app
	return app
}

// discovery returns a replicaset topology for an application with
// the Cartridge orchestrator.
func (c *CartridgeApplication) discovery() (Replicasets, error) {
	replicasets := Replicasets{
		State:        StateUnknown,
		Orchestrator: OrchestratorCartridge,
	}

	var topology cartridgeTopology
	err := EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(inst running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			newTopology, err := getCartridgeTopology(evaler)
			if err != nil {
				return true, err
			}
			if topology.IsBootstrapped {
				if newTopology.IsBootstrapped {
					topology = newTopology
				}
			} else {
				topology = newTopology
			}

			// Stop if we already found a valid topology.
			return topology.IsBootstrapped && !topology.IsCritical, nil
		},
	))
	if err != nil {
		return replicasets, fmt.Errorf("failed to get topology: %w", err)
	}

	replicasets = getCartridgeReplicasets(topology)
	err = EvalForeachAlive(c.runningCtx.Instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			var err error
			replicasets, err = updateCartridgeInstance(evaler, &ictx, replicasets)
			return false, err
		},
	))

	return recalculateMasters(replicasets), err
}

// Promote promotes an instance in the cartridge application.
func (c *CartridgeApplication) Promote(ctx PromoteCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return err
	}
	var (
		targetInstance Instance
		inst           cartridgeInstance
		found          bool
	)
loop:
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.Alias == ctx.InstName {
				inst = cartridgeInstance{
					ReplicasetUUID: replicaset.UUID,
					InstanceUUID:   instance.UUID,
					Failover:       replicaset.Failover,
				}
				targetInstance = instance
				found = true
				break loop
			}
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
	}
	if !targetInstance.InstanceCtxFound {
		return fmt.Errorf("target instance should be online")
	}

	return cartridgePromote(MakeInstanceEvalFunc(targetInstance.InstanceCtx),
		inst, ctx.Force, ctx.Timeout)
}

// Demote is not supported for an application by the Cartridge orchestrator.
func (c *CartridgeApplication) Demote(ctx DemoteCtx) error {
	return newErrDemoteByAppNotSupported(OrchestratorCartridge)
}

// cartridgeReplicasetConfig describes a replicaset config for the cartridge application.
type cartridgeReplicasetConfig struct {
	Alias       string   `yaml:"alias,omitempty"`
	Instances   []string `yaml:"instances"`
	Roles       []string `yaml:"roles"`
	Weight      *float64 `yaml:"weight,omitempty"`
	AllRW       *bool    `yaml:"all_rw,omitempty"`
	VShardGroup *string  `yaml:"vshard_group,omitempty"`
}

// cartridgeInstanceConfig describes a instance config for the cartridge application.
type cartridgeInstanceConfig struct {
	URI string `yaml:"advertise_uri"`
}

// parseYaml parses YAML to the specified type.
func parseYaml[T any](filename string) (T, error) {
	content, err := os.ReadFile(filename)
	var ret T
	if err != nil {
		return ret, err
	}
	err = yaml.Unmarshal(content, &ret)
	return ret, err
}

// getCartridgeReplicasetsConfig extracts a replicasets config.
func getCartridgeReplicasetsConfig(appDir,
	filename string) (map[string]cartridgeReplicasetConfig, string, error) {
	if filename == "" {
		filename = filepath.Join(appDir, defaultCartridgeReplicasetsFilename)
	}
	filename, err := util.GetYamlFileName(filename, true)
	if err != nil {
		return nil, "", err
	}
	cfg, err := parseYaml[map[string]cartridgeReplicasetConfig](filename)
	if err != nil {
		return nil, filename, err
	}
	return cfg, filename, nil
}

// getCartridgeInstancesConfig extracts a instances config.
func getCartridgeInstancesConfig(appName,
	appDir string) (map[string]cartridgeInstanceConfig, error) {
	filename, err := util.GetYamlFileName(
		filepath.Join(appDir, defaultCartridgeInstancesFilename), true)
	if err != nil {
		return nil, err
	}
	rawCfg, err := parseYaml[map[string]cartridgeInstanceConfig](filename)
	if err != nil {
		return nil, err
	}

	cfg := map[string]cartridgeInstanceConfig{}
	appPrefix := fmt.Sprintf("%s.", appName)
	for key, instCfg := range rawCfg {
		instName, found := strings.CutPrefix(key, appPrefix)
		if found {
			cfg[instName] = instCfg
		}
	}
	return cfg, nil
}

// Bootstrap bootstraps replicasets or a certain instance by the Cartridge orchestrator.
func (c *CartridgeApplication) Bootstrap(ctx BootstrapCtx) error {
	if len(c.runningCtx.Instances) == 0 {
		return fmt.Errorf("failed to bootstrap: there are no running instances")
	}
	var (
		appDir  = c.runningCtx.Instances[0].AppDir
		appName = c.runningCtx.Instances[0].AppName
	)
	instancesCfg, err := getCartridgeInstancesConfig(appName, appDir)
	if err != nil {
		return fmt.Errorf("failed to get instances config: %w", err)
	}
	discovered, err := c.Discovery(SkipCache)
	if err != nil {
		return fmt.Errorf("failed to discovery: %w", err)
	}

	var (
		eval      InstanceEvalFunc
		instances = filterDiscovered(c.runningCtx.Instances, discovered)
	)
	if ctx.Instance != "" {
		// Bootstrap an instance.
		eval = func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			return true,
				c.bootstrapInstance(ctx.Instance, ctx.Replicaset, evaler, discovered,
					instancesCfg, ctx.Timeout)
		}
	} else {
		// Bootstrap a replicasets from the config.
		replicasetCfg, replicasetscCfgPath, err := getCartridgeReplicasetsConfig(appDir,
			ctx.ReplicasetsFile)
		if err != nil {
			return fmt.Errorf("failed to get replicasets config: %w", err)
		}
		log.Infof("Bootstrap replicasets described in %s", replicasetscCfgPath)
		eval = func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			return true,
				c.bootstrapReplicasets(evaler, discovered, replicasetCfg, instancesCfg, ctx.Timeout)
		}
		if discovered.State != StateBootstrapped {
			// Initial bootstrapping, use some instance from the config.
			cfgInstNames := map[string]struct{}{}
			for _, replicaset := range replicasetCfg {
				for _, inst := range replicaset.Instances {
					cfgInstNames[inst] = struct{}{}
				}
			}
			instances = filterInstances(c.runningCtx.Instances, func(
				inst running.InstanceCtx) bool {
				_, ok := cfgInstNames[inst.InstName]
				return ok
			})
		}
	}

	if len(instances) == 0 {
		return fmt.Errorf("not found any instance to perform bootstrapping")
	}
	err = EvalForeach(instances, InstanceEvalFunc(eval))
	if err != nil {
		return err
	}

	if ctx.BootstrapVShard {
		// VShard bootstrapping takes the instances from the discovery cache, so re-discovery.
		_, err = c.Discovery(SkipCache)
		if err != nil {
			return fmt.Errorf("failed to re-discovery: %w", err)
		}
		// Bootstrap vshard.
		err = c.BootstrapVShard(VShardBootstrapCtx{Timeout: ctx.Timeout})
	}
	return err
}

// bootstrapInstance bootstrap an instance.
func (c *CartridgeApplication) bootstrapInstance(instanceName, replicasetName string,
	evaler connector.Evaler, discovered Replicasets,
	instancesCfg map[string]cartridgeInstanceConfig, timeout int) error {
	if replicasetName == "" {
		return fmt.Errorf("a replicaset name is empty")
	}
	var (
		replicasetUUID string
		found          bool
	)
	for _, replicaset := range discovered.Replicasets {
		if replicaset.Alias == replicasetName {
			found = true
			replicasetUUID = replicaset.UUID
			break
		}
		for _, instance := range replicaset.Instances {
			if instance.Alias == instanceName {
				return fmt.Errorf("instance %q is bootstrapped already", instanceName)
			}
		}
	}
	if !found {
		return fmt.Errorf("a replicaset %q not found in the bootstrapped cluster", replicasetName)
	}
	instancesUUID := map[string]string{}
	joinOpts, err := getCartridgeJoinServersOpts(instancesCfg, []string{instanceName},
		instancesUUID)
	if err != nil {
		return err
	}
	opts := []cartridgeEditReplicasetsOpts{{
		UUID:        &replicasetUUID,
		JoinServers: joinOpts,
	}}
	return cartridgeEditReplicasets(evaler, opts, timeout)
}

// bootstrapReplicasets bootstraps replicasets from the config.
func (c *CartridgeApplication) bootstrapReplicasets(evaler connector.Evaler, discovered Replicasets,
	replicasetsCfg map[string]cartridgeReplicasetConfig,
	instancesCfg map[string]cartridgeInstanceConfig,
	timeout int) error {

	majorVer, err := getCartridgeMajorVersion(evaler)
	if err != nil {
		return fmt.Errorf("failed to get cartridge major version: %w", err)
	}
	if majorVer < 2 && discovered.State != StateBootstrapped {
		if len(replicasetsCfg) == 0 {
			return fmt.Errorf("empty replicasets config")
		}
		for rname, cfg := range replicasetsCfg {
			// Create first replicaset with single instance, since in the old Cartridge
			// bootstrapping cluster from scratch should be performed
			// on a single-server replicaset only.
			if len(cfg.Instances) == 0 {
				return fmt.Errorf("replicaset %q is empty", rname)
			}
			instances := cfg.Instances
			initialReplicasetCfg := cfg
			initialReplicasetCfg.Instances, cfg.Instances = instances[:1], instances[1:]

			initialCfg := map[string]cartridgeReplicasetConfig{}
			initialCfg[rname] = initialReplicasetCfg
			if len(cfg.Instances) == 0 {
				// There are no more instances to bootstrap.
				delete(replicasetsCfg, rname)
			} else {
				replicasetsCfg[rname] = cfg
			}

			if err := updateCartridgeReplicasets(evaler, discovered, initialCfg,
				instancesCfg, timeout); err != nil {
				return err
			}
			break
		}
	}

	if err := updateCartridgeReplicasets(evaler, discovered, replicasetsCfg,
		instancesCfg, timeout); err != nil {
		return err
	}

	return err
}

// getCartridgeJoinServersOpts returns opts to join new servers.
// It lookups for an instance in the UUID map and if an instance is not found,
// generates new UUID and adds the instance to the join options.
func getCartridgeJoinServersOpts(instancesCfg map[string]cartridgeInstanceConfig,
	instances []string, instancesUUID map[string]string) ([]cartridgeJoinServersOpts, error) {
	opts := make([]cartridgeJoinServersOpts, 0)
	for _, instance := range instances {
		if _, UUIDExists := instancesUUID[instance]; UUIDExists {
			continue
		}
		cfg, found := instancesCfg[instance]
		if !found {
			return nil, fmt.Errorf("instance %q not found in the instance config", instance)
		}
		instanceUUID := uuid.New().String()
		instancesUUID[instance] = instanceUUID
		opts = append(opts, cartridgeJoinServersOpts{
			URI:  cfg.URI,
			UUID: &instanceUUID,
		})
	}
	return opts, nil
}

// updateCartridgeReplicasets updates replicasets using the config.
// If some instance was not discovered, creates it.
func updateCartridgeReplicasets(evaler connector.Evaler, discovered Replicasets,
	replicasetCfg map[string]cartridgeReplicasetConfig,
	instancesCfg map[string]cartridgeInstanceConfig,
	timeout int) error {
	instanceUUID := map[string]string{}
	replicasetUUID := map[string]string{}
	for _, replicaset := range discovered.Replicasets {
		replicasetUUID[replicaset.Alias] = replicaset.UUID
		for _, instance := range replicaset.Instances {
			instanceUUID[instance.Alias] = instance.UUID
		}
	}

	var editOpts []cartridgeEditReplicasetsOpts
	for rname, rcfg := range replicasetCfg {
		replicasetName := rname
		opts := cartridgeEditReplicasetsOpts{
			Alias:       &replicasetName,
			Roles:       rcfg.Roles,
			AllRW:       rcfg.AllRW,
			Weight:      rcfg.Weight,
			VshardGroup: rcfg.VShardGroup,
		}
		if uuid, found := replicasetUUID[replicasetName]; found {
			// Link opts to the existing replicaset.
			// admin_edit_topology() recognizes replicasets by UUID.
			opts.UUID = &uuid
		}
		var err error
		opts.JoinServers, err = getCartridgeJoinServersOpts(instancesCfg,
			rcfg.Instances, instanceUUID)
		if err != nil {
			return err
		}
		var failoverPriority []string
		for _, inst := range rcfg.Instances {
			uuid, ok := instanceUUID[inst]
			if !ok {
				return fmt.Errorf("instance %q uuid not found", inst)
			}
			failoverPriority = append(failoverPriority, uuid)
		}
		opts.FailoverPriority = failoverPriority
		editOpts = append(editOpts, opts)
	}

	return cartridgeEditReplicasets(evaler, editOpts, timeout)
}

// Expel expels an instance from a Cartridge replicasets.
func (c *CartridgeApplication) Expel(ctx ExpelCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to discovery: %w", err)
	}

	var (
		uuid  string
		found bool
	)
	for _, replicaset := range replicasets.Replicasets {
		for _, instance := range replicaset.Instances {
			if instance.Alias == ctx.InstName {
				uuid = instance.UUID
				found = true
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("instance %q not found in a configured replicaset", ctx.InstName)
	}

	return cartridgeExpel(c.runningCtx, replicasets, ctx.InstName, uuid, ctx.Timeout)
}

// BootstrapVShard bootstraps vshard for an application by the Cartridge orchestrator.
func (c *CartridgeApplication) BootstrapVShard(ctx VShardBootstrapCtx) error {
	replicasets, err := c.Discovery(UseCache)
	if err != nil {
		return fmt.Errorf("failed to discovery: %w", err)
	}

	var lastErr error
	evaler := func(inst running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		lastErr = cartridgeBootstrapVShard(evaler, ctx.Timeout)
		// If lastErr is not nil, try again with another instance.
		return lastErr == nil, nil
	}
	err = EvalForeachAliveDiscovered(c.runningCtx.Instances, replicasets, InstanceEvalFunc(evaler))
	for _, e := range []error{err, wrapCartridgeVShardBoostrapError(lastErr)} {
		if e != nil {
			return fmt.Errorf("failed to bootstrap vshard: %w", e)
		}
	}
	return nil
}

// getCartridgeInstanceInfo returns an additional instance information.
func getCartridgeInstanceInfo(
	evaler connector.Evaler) (uuid string, rw bool, err error) {
	info := []struct {
		UUID string
		RW   bool
	}{}

	args := []any{}
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(cartridgeGetInstanceInfoBody, args, opts)
	if err != nil {
		return "", false, err
	}

	if err := mapstructure.Decode(data, &info); err != nil {
		return "", false, fmt.Errorf("failed to parse a response: %w", err)
	}
	if len(info) != 1 {
		return "", false, fmt.Errorf("unexpected response")
	}
	return info[0].UUID, info[0].RW, nil
}

// updateCartridgeInstance receives and updates an additional instance
// information about the instance in the replicasets.
func updateCartridgeInstance(evaler connector.Evaler,
	ictx *running.InstanceCtx, replicasets Replicasets) (Replicasets, error) {
	uuid, rw, err := getCartridgeInstanceInfo(evaler)
	if err != nil {
		return replicasets, err
	}
	for _, replicaset := range replicasets.Replicasets {
		for i, _ := range replicaset.Instances {
			if replicaset.Instances[i].UUID == uuid {
				if rw {
					replicaset.Instances[i].Mode = ModeRW
				} else {
					replicaset.Instances[i].Mode = ModeRead
				}
				if ictx != nil {
					replicaset.Instances[i].InstanceCtx = *ictx
					replicaset.Instances[i].InstanceCtxFound = true
				}
			}
		}
	}
	return replicasets, nil
}

// getCartridgeVersion returns the version of the cartridge.
func getCartridgeVersion(evaler connector.Evaler) (string, error) {
	var reqOpts connector.RequestOpts
	args := []any{}
	errPrefix := "failed to get cartridge version: "
	ret, err := evaler.Eval(cartridgeGetVersionBody, args, reqOpts)
	if err != nil {
		return "", fmt.Errorf(errPrefix+"%w", err)
	}
	if len(ret) != 1 {
		return "", errors.New(errPrefix + "unexpected response length")
	}
	version, ok := ret[0].(string)
	if !ok {
		return "", fmt.Errorf(errPrefix+"unexpected version type: %T", ret[0])
	}
	return version, nil
}

// getCartridgeMajorVersion returns the major version of the cartridge.
func getCartridgeMajorVersion(evaler connector.Evaler) (int, error) {
	ver, err := getCartridgeVersion(evaler)
	if err != nil {
		return 0, err
	}
	switch ver {
	case "scm-1":
		return 2, nil
	case "unknown":
		return 0, fmt.Errorf("cartridge version is unknown")
	default:
		parsed, err := version.Parse(ver)
		if err != nil {
			return 0, err
		}
		return int(parsed.Major), nil
	}
}

// cartridgeHealthCheckIsNeeded checks if we need to wait cluster healthy
// after replicaset editing.
// https://github.com/tarantool/cartridge-cli/blob/76044114f412b1fa15e353f84e7de1f0c3d98566/cli/cluster/cluster.go#L15-L22
func cartridgeHealthCheckIsNeeded(evaler connector.Evaler) (bool, error) {
	major, err := getCartridgeMajorVersion(evaler)
	if err != nil {
		return false, err
	}
	return major < 2, nil
}

// cartridgeJoinServersOpts describes options for server joining.
type cartridgeJoinServersOpts struct {
	URI  string  `msgpack:"uri"`
	UUID *string `msgpack:"uuid,omitempty"`
}

// cartridgeEditReplicasetsOpts describes options for replicaset editing.
type cartridgeEditReplicasetsOpts struct {
	UUID             *string                    `msgpack:"uuid,omitempty"`
	Alias            *string                    `msgpack:"alias,omitempty"`
	Roles            []string                   `msgpack:"roles,omitempty"`
	AllRW            *bool                      `msgpack:"all_rw,omitempty"`
	Weight           *float64                   `msgpack:"weight,omitempty"`
	VshardGroup      *string                    `msgpack:"vshard_group,omitempty"`
	JoinServers      []cartridgeJoinServersOpts `msgpack:"join_servers,omitempty"`
	FailoverPriority []string                   `msgpack:"failover_priority,omitempty"`
}

// cartridgeEditInstancesOpts describes options for instances editing.
type cartridgeEditInstancesOpts struct {
	URI        *string  `msgpack:"uri,omitempty"`
	UUID       *string  `msgpack:"uuid,omitempty"`
	Zone       *string  `msgpack:"zone,omitempty"`
	Labels     []string `msgpack:"labels,omitempty"`
	Disabled   *bool    `msgpack:"disabled,omitempty"`
	Electable  *bool    `msgpack:"electable,omitempty"`
	Rebalancer *bool    `msgpack:"rebalancer,omitempty"`
	Expelled   *bool    `msgpack:"expelled,omitempty"`
}

// cartridgeWaitHealthy waits until the cluster becomes healthy.
func cartridgeWaitHealthy(evaler connector.Evaler, timeout int) error {
	var reqOpts connector.RequestOpts
	args := []any{timeout}
	if _, err := evaler.Eval(cartridgeWaitHealthyBody, args, reqOpts); err != nil {
		return fmt.Errorf("failed to wait healthy: %w", err)
	}
	return nil
}

// cartridgeEditReplicasets edits replicasets topology in the cartridge app.
func cartridgeEditReplicasets(evaler connector.Evaler,
	opts []cartridgeEditReplicasetsOpts, timeout int) error {
	var reqOpts connector.RequestOpts
	args := []any{opts}
	if _, err := evaler.Eval(cartridgeEditReplicasetsBody, args, reqOpts); err != nil {
		return fmt.Errorf("failed to edit replicasets: %w", err)
	}
	healthCheckIsNeeded, err := cartridgeHealthCheckIsNeeded(evaler)
	if err != nil {
		return err
	}
	if healthCheckIsNeeded {
		if err := cartridgeWaitHealthy(evaler, timeout); err != nil {
			return err
		}
	}
	return nil
}

// cartridgeEditInstances edits instances in the cartridge app.
func cartridgeEditInstances(evaler connector.Evaler,
	opts []cartridgeEditInstancesOpts, timeout int) error {
	var reqOpts connector.RequestOpts
	args := []any{opts}
	if _, err := evaler.Eval(cartridgeEditInstancesBody, args, reqOpts); err != nil {
		return fmt.Errorf("failed to edit instances: %w", err)
	}
	healthCheckIsNeeded, err := cartridgeHealthCheckIsNeeded(evaler)
	if err != nil {
		return err
	}
	if healthCheckIsNeeded {
		if err := cartridgeWaitHealthy(evaler, timeout); err != nil {
			return err
		}
	}
	return nil
}

// cartridgeFailoverPromoteOpts describes options for promoting via failover.
type cartridgeFailoverPromoteOpts struct {
	Leaders            map[string]string `msgpack:"replicaset_leaders"`
	ForceInconsistency bool              `msgpack:"force_inconsistency"`
	SkipErrorOnChange  bool              `msgpack:"skip_error_on_change"`
}

// cartridgeFailoverPromote is a cartridge.failover_promote() wrapper.
func cartridgeFailoverPromote(evaler connector.Evaler, opts cartridgeFailoverPromoteOpts) error {
	var reqOpts connector.RequestOpts
	args := []any{opts}
	if _, err := evaler.Eval(cartridgeFailoverPromoteBody, args, reqOpts); err != nil {
		return fmt.Errorf("failed to failover promote: %w", err)
	}
	return nil
}

// cartridgePromote promotes an instance in the cartridge replicaset.
// https://www.tarantool.io/en/doc/2.11/book/cartridge/cartridge_dev/#manual-leader-promotion
func cartridgePromote(evaler connector.Evaler,
	inst cartridgeInstance, force bool, timeout int) error {
	switch inst.Failover {
	case FailoverOff, FailoverEventual:
		opts := cartridgeEditReplicasetsOpts{
			UUID:             &inst.ReplicasetUUID,
			FailoverPriority: []string{inst.InstanceUUID},
		}
		if err := cartridgeEditReplicasets(evaler,
			[]cartridgeEditReplicasetsOpts{opts}, timeout); err != nil {
			return err
		}
	case FailoverElection, FailoverStateful:
		leaders := map[string]string{}
		leaders[inst.ReplicasetUUID] = inst.InstanceUUID
		opts := cartridgeFailoverPromoteOpts{
			Leaders:            leaders,
			ForceInconsistency: force,
			// Anyway print an error if vclockkeeper was changed in
			// etcd during inconsistency forcing.
			// https://github.com/tarantool/cartridge/blob/1c07213c058cfb500a8046175407ed46acf6cb44/cartridge/failover.lua#L1112-L1114
			SkipErrorOnChange: false,
		}
		if err := cartridgeFailoverPromote(evaler, opts); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unexpected failover")
	}
	return waitRW(evaler, timeout)
}

// cartridgeBootstrapVShard bootstraps the vshard.
func cartridgeBootstrapVShard(evaler connector.Evaler, timeout int) error {
	reqOpts := connector.RequestOpts{}
	_, err := evaler.Eval(cartridgeBootstrapVShardBody, []any{timeout}, reqOpts)
	return err
}

// wrapCartridgeVShardBoostrapError wraps a cartridge vshard bootstrap error
// with an additional information.
// https://github.com/tarantool/cartridge/issues/1148
func wrapCartridgeVShardBoostrapError(err error) error {
	if err != nil && strings.Contains(err.Error(), "Sharding config is empty") {
		return fmt.Errorf(
			"it's possible that there are no running instances of some configured vshard groups. "+
				"In this case existing storages are bootstrapped, "+
				"but Cartridge returns an error: %w", err)
	}
	return err
}

// cartridgeExpel expels an instance from the replicaset.
func cartridgeExpel(runningCtx running.RunningCtx,
	discovered Replicasets, name, uuid string, timeout int) error {

	found := false
	var lastErr error
	eval := func(instance running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		if instance.InstName == name {
			return false, nil
		}
		found = true
		expelled := true
		opts := cartridgeEditInstancesOpts{
			UUID:     &uuid,
			Expelled: &expelled,
		}
		err := cartridgeEditInstances(evaler, []cartridgeEditInstancesOpts{opts}, timeout)
		if err != nil {
			// Try again with another instance.
			lastErr = err
			return false, nil
		}

		lastErr = nil
		return true, nil
	}

	err := EvalForeachAliveDiscovered(runningCtx.Instances, discovered, InstanceEvalFunc(eval))
	for _, e := range []error{err, lastErr} {
		if e != nil {
			return fmt.Errorf("failed to expel instance: %w", e)
		}
	}

	if !found {
		return fmt.Errorf("not found any other instance joined to cluster")
	}

	return nil
}
