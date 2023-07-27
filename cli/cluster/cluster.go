package cluster

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	groupsLabel      = "groups"
	replicasetsLabel = "replicasets"
	instancesLabel   = "instances"
)

// InstanceConfig describes an instance configuration.
type InstanceConfig struct {
	// RawConfig is a raw configuration of the instance scope.
	RawConfig *Config `yaml:"-"`
}

// MakeInstanceConfig creates an InstanceConfig object from a configuration.
func MakeInstanceConfig(config *Config) (InstanceConfig, error) {
	var cconfig InstanceConfig

	err := yaml.Unmarshal([]byte(config.String()), &cconfig)
	return cconfig, err
}

// UnmarshalYAML helps to unmarshal an InstanceConfig object from YAML.
func (c *InstanceConfig) UnmarshalYAML(unmarshal func(any) error) error {
	c.RawConfig = NewConfig()

	if err := unmarshal(&c.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal InstanceConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// c.UnmarshalYAML()->unmarshal()->...->c.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type parsed InstanceConfig
	temp := parsed(*c)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal InstanceConfig: %w", err)
	}
	*c = InstanceConfig(temp)

	return nil
}

// ReplicasetConfig describes a replicaset configuration.
type ReplicasetConfig struct {
	// RawConfig is a raw configuration of the replicaset scope.
	RawConfig *Config `yaml:"-"`
	// Instances are configurations at an instance scope.
	Instances map[string]InstanceConfig
}

// UnmarshalYAML helps to unmarshal a ReplicasetConfig object from YAML.
func (c *ReplicasetConfig) UnmarshalYAML(unmarshal func(any) error) error {
	c.RawConfig = NewConfig()

	if err := unmarshal(&c.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal ReplicasetConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// c.UnmarshalYAML()->unmarshal()->...->c.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type parsed ReplicasetConfig
	temp := parsed(*c)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal ReplicasetConfig: %w", err)
	}
	*c = ReplicasetConfig(temp)

	return nil
}

// GroupConfig describes a group configuration.
type GroupConfig struct {
	// RawConfig is a raw configuration of the group scope.
	RawConfig *Config `yaml:"-"`
	// Replicasets are parsed configurations per a replicaset.
	Replicasets map[string]ReplicasetConfig
}

// UnmarshalYAML helps to unmarshal a GroupConfig object from YAML.
func (c *GroupConfig) UnmarshalYAML(unmarshal func(any) error) error {
	c.RawConfig = NewConfig()

	if err := unmarshal(&c.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal GroupConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// c.UnmarshalYAML()->unmarshal()->...->c.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type parsed GroupConfig
	temp := parsed(*c)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal GroupConfig: %w", err)
	}
	*c = GroupConfig(temp)

	return nil
}

// ClusterConfig describes a cluster configuration.
type ClusterConfig struct {
	// RawConfig is a configuration of the global scope.
	RawConfig *Config `yaml:"-"`
	// Groups are parsed configurations per a group.
	Groups map[string]GroupConfig
}

// UnmarshalYAML helps to unmarshal a ClusterConfig object from YAML.
func (c *ClusterConfig) UnmarshalYAML(unmarshal func(any) error) error {
	c.RawConfig = NewConfig()

	if err := unmarshal(&c.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal ClusterConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// c.UnmarshalYAML()->unmarshal()->...->c.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type parsed ClusterConfig
	temp := parsed(*c)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal ClusterConfig: %w", err)
	}
	*c = ClusterConfig(temp)

	return nil
}

// MakeClusterConfig creates a ClusterConfig object from a configuration.
func MakeClusterConfig(config *Config) (ClusterConfig, error) {
	cconfig := ClusterConfig{
		RawConfig: NewConfig(),
	}

	err := yaml.Unmarshal([]byte(config.String()), &cconfig)
	return cconfig, err
}

// mergeExclude merges a high priority configuration with a low priority
// configuration exclude some path.
func mergeExclude(high, low *Config, excludePath []string) {
	lowCopy := NewConfig()
	lowCopy.Merge(low)
	lowCopy.Set(excludePath, nil)
	high.Merge(lowCopy)
}

// findInstance finds an instance with the name in the config and returns
// it full configuration merged from scopes: global, group, replicaset,
// instance or nil.
func findInstance(cluster ClusterConfig, name string) *Config {
	for _, group := range cluster.Groups {
		for _, replicaset := range group.Replicasets {
			for iname, instance := range replicaset.Instances {
				if iname == name {
					config := NewConfig()
					config.Merge(instance.RawConfig)
					mergeExclude(config, replicaset.RawConfig,
						[]string{instancesLabel})
					mergeExclude(config, group.RawConfig,
						[]string{replicasetsLabel})
					mergeExclude(config, cluster.RawConfig,
						[]string{groupsLabel})
					return config
				}
			}
		}
	}
	return nil
}

// Instances returns a list of instance names from the cluster config.
func Instances(cluster ClusterConfig) []string {
	instances := []string{}
	for _, group := range cluster.Groups {
		for _, replicaset := range group.Replicasets {
			for iname, _ := range replicaset.Instances {
				instances = append(instances, iname)
			}
		}
	}

	return instances
}

// HasInstance returns true if an instance with the name exists in the config.
func HasInstance(cluster ClusterConfig, name string) bool {
	return findInstance(cluster, name) != nil
}

// Instantiate returns a fetched instance config from the cluster config. If
// the cluster config has the instance then it returns a merged config of the
// instance from scopes: global, group, replicaset, instance. If the cluster
// config has not the instance then it returns a global scope of the cluster
// config.
func Instantiate(cluster ClusterConfig, name string) *Config {
	iconfig := findInstance(cluster, name)
	if iconfig != nil {
		return iconfig
	}

	iconfig = NewConfig()
	mergeExclude(iconfig, cluster.RawConfig, []string{groupsLabel})

	return iconfig
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
func GetClusterConfig(path string) (ClusterConfig, error) {
	ret := ClusterConfig{}
	if path == "" {
		return ret, fmt.Errorf("a configuration file must be set")
	}

	collector := NewFileCollector(path)
	config, err := collector.Collect()
	if err != nil {
		fmtErr := "unable to get cluster config from %q: %s"
		return ret, fmt.Errorf(fmtErr, path, err)
	}

	defaultEnvCollector := NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s_DEFAULT", middle)
	})
	defaultEnvConfig, err := defaultEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from default environment variables: %w"
		return ret, fmt.Errorf(fmtErr, err)
	}

	config.Merge(defaultEnvConfig)
	return MakeClusterConfig(config)
}

// GetInstanceConfig returns a full configuration for an instance with the
// name from a cluster config. It merges the configuration from all configured
// sources and scopes: environment, global, group, replicaset, instance.
func GetInstanceConfig(cluster ClusterConfig, instance string) (InstanceConfig, error) {
	if !HasInstance(cluster, instance) {
		return InstanceConfig{}, fmt.Errorf("an instance %q not found", instance)
	}

	mainEnvCollector := NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s", middle)
	})
	mainEnvConfig, err := mainEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from environment variables: %w"
		return InstanceConfig{}, fmt.Errorf(fmtErr, err)
	}

	iconfig := NewConfig()
	iconfig.Merge(mainEnvConfig)
	iconfig.Merge(Instantiate(cluster, instance))

	return MakeInstanceConfig(iconfig)
}
