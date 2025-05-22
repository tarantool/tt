package cluster

import (
	"fmt"
	"time"

	"github.com/tarantool/go-tarantool/v2"
	libconnect "github.com/tarantool/tt/lib/connect"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gopkg.in/yaml.v3"
)

const (
	groupsLabel        = "groups"
	replicasetsLabel   = "replicasets"
	instancesLabel     = "instances"
	defaultEtcdTimeout = 3 * time.Second
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
func (config *InstanceConfig) UnmarshalYAML(unmarshal func(any) error) error {
	config.RawConfig = NewConfig()

	if err := unmarshal(&config.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal InstanceConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// config.UnmarshalYAML()->unmarshal()->...->
	//   config.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type instanceConfig InstanceConfig
	temp := instanceConfig(*config)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal InstanceConfig: %w", err)
	}
	*config = InstanceConfig(temp)

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
func (config *ReplicasetConfig) UnmarshalYAML(unmarshal func(any) error) error {
	config.RawConfig = NewConfig()

	if err := unmarshal(&config.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal ReplicasetConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// config.UnmarshalYAML()->unmarshal()->...->
	//   config.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type replicasetConfig ReplicasetConfig
	temp := replicasetConfig(*config)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal ReplicasetConfig: %w", err)
	}
	*config = ReplicasetConfig(temp)

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
func (config *GroupConfig) UnmarshalYAML(unmarshal func(any) error) error {
	config.RawConfig = NewConfig()

	if err := unmarshal(&config.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal GroupConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// config.UnmarshalYAML()->unmarshal()->...->
	//   config.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type groupConfig GroupConfig
	temp := groupConfig(*config)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal GroupConfig: %w", err)
	}
	*config = GroupConfig(temp)

	return nil
}

// ClusterConfig describes a cluster configuration.
type ClusterConfig struct {
	Config struct {
		Etcd struct {
			Endpoints []string `yaml:"endpoints"`
			Username  string   `yaml:"username"`
			Password  string   `yaml:"password"`
			Prefix    string   `yaml:"prefix"`
			Ssl       struct {
				KeyFile    string `yaml:"ssl_key"`
				CertFile   string `yaml:"ssl_cert"`
				CaPath     string `yaml:"ca_path"`
				CaFile     string `yaml:"ca_file"`
				VerifyPeer bool   `yaml:"verify_peer"`
				VerifyHost bool   `yaml:"verify_host"`
			} `yaml:"ssl"`
			Http struct {
				Request struct {
					Timeout float64 `yaml:"timeout"`
				} `yaml:"request"`
			} `yaml:"http"`
		} `yaml:"etcd"`
		Storage struct {
			Prefix    string  `yaml:"prefix"`
			Timeout   float64 `yaml:"timeout"`
			Endpoints []struct {
				Uri      string `yaml:"uri"`
				Login    string `yaml:"login"`
				Password string `yaml:"password"`
				Params   struct {
					Transport       string `yaml:"transport"`
					SslKeyFile      string `yaml:"ssl_key_file"`
					SslCertFile     string `yaml:"ssl_cert_file"`
					SslCaFile       string `yaml:"ssl_ca_file"`
					SslCiphers      string `yaml:"ssl_ciphers"`
					SslPassword     string `yaml:"ssl_password"`
					SslPasswordFile string `yaml:"ssl_password_file"`
				} `yaml:"params"`
			} `yaml:"endpoints"`
		} `yaml:"storage"`
	} `yaml:"config"`
	// RawConfig is a configuration of the global scope.
	RawConfig *Config `yaml:"-"`
	// Groups are parsed configurations per a group.
	Groups map[string]GroupConfig
}

// UnmarshalYAML helps to unmarshal a ClusterConfig object from YAML.
func (config *ClusterConfig) UnmarshalYAML(unmarshal func(any) error) error {
	config.RawConfig = NewConfig()

	if err := unmarshal(&config.RawConfig); err != nil {
		return fmt.Errorf("failed to unmarshal ClusterConfig: %w", err)
	}

	// unmarshal(c) leads to recursion:
	//
	// config.UnmarshalYAML()->unmarshal()->...->
	//   config.UnmarshalYAML()->unmarshal()->...
	//
	// The `parsed` type helps to break the recursion because the type does
	// not have `UnmarshalYAML` call.
	type parsed ClusterConfig
	temp := parsed(*config)
	if err := unmarshal(&temp); err != nil {
		return fmt.Errorf("failed to unmarshal ClusterConfig: %w", err)
	}
	*config = ClusterConfig(temp)

	return nil
}

// MakeClusterConfig creates a ClusterConfig object from a configuration.
func MakeClusterConfig(config *Config) (ClusterConfig, error) {
	cconfig := ClusterConfig{
		RawConfig: NewConfig(),
	}

	err := yaml.Unmarshal([]byte(config.String()), &cconfig)
	if err != nil {
		err = fmt.Errorf(
			"failed to parse a configuration data as a cluster config: %w",
			err)
		return cconfig, err

	}
	return cconfig, nil
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
					if instance.RawConfig != nil {
						config.Merge(instance.RawConfig)
					}
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
			for iname := range replicaset.Instances {
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

// ReplaceInstanceConfig replaces an instance configuration.
func ReplaceInstanceConfig(cconfig ClusterConfig,
	instance string, iconfig *Config,
) (ClusterConfig, error) {
	for gname, group := range cconfig.Groups {
		for rname, replicaset := range group.Replicasets {
			for iname := range replicaset.Instances {
				if instance == iname {
					path := []string{
						groupsLabel, gname,
						replicasetsLabel, rname,
						instancesLabel, iname,
					}
					newConfig := NewConfig()
					newConfig.Merge(cconfig.RawConfig)
					if err := newConfig.Set(path, iconfig); err != nil {
						err = fmt.Errorf("failed to set configuration: %w", err)
						return cconfig, err
					}
					return MakeClusterConfig(newConfig)
				}
			}
		}
	}

	return cconfig,
		fmt.Errorf("cluster configuration has not an instance %q", instance)
}

// FindInstance lookups for an instance in the config.
// If it is found, returns a group and replicaset name.
func FindInstance(cconfig ClusterConfig, name string) (string, string, bool) {
	for gname, group := range cconfig.Groups {
		for rname, replicaset := range group.Replicasets {
			for iname := range replicaset.Instances {
				if name == iname {
					return gname, rname, true
				}
			}
		}
	}
	return "", "", false
}

// SetInstanceConfig sets an instance configuration.
func SetInstanceConfig(cconfig ClusterConfig,
	group, replicaset, instance string, iconfig *Config,
) (ClusterConfig, error) {
	path := []string{
		groupsLabel, group,
		replicasetsLabel, replicaset,
		instancesLabel, instance,
	}
	newConfig := NewConfig()
	newConfig.Merge(cconfig.RawConfig)
	if err := newConfig.Set(path, iconfig); err != nil {
		err = fmt.Errorf("failed to set configuration: %w", err)
		return cconfig, err
	}
	return MakeClusterConfig(newConfig)
}

// FindGroupByReplicaset returns a group name by the replicaset name.
func FindGroupByReplicaset(cconfig ClusterConfig, replicaset string) (string, bool) {
	for gname, group := range cconfig.Groups {
		for rname := range group.Replicasets {
			if rname == replicaset {
				return gname, true
			}
		}
	}
	return "", false
}

func CreateCollector(
	collectors CollectorFactory,
	connOpts ConnectOpts,
	opts libconnect.UriOpts,
) (Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Params["key"], opts.Timeout

	var (
		collector Collector
		err       error
		closeFunc func()
	)

	tarantoolFunc := func(conn tarantool.Connector) error {
		if collectors != nil {
			collector, err = collectors.NewTarantool(conn, prefix, key, timeout)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to create tarantool config storage collector: %w", err)
			}
		}
		closeFunc = func() { conn.Close() }
		return nil
	}

	etcdFunc := func(client *clientv3.Client) error {
		if collectors != nil {
			collector, err = collectors.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd collector: %w", err)
			}
		}
		closeFunc = func() { client.Close() }
		return nil
	}

	if err := DoOnStorage(connOpts, opts, tarantoolFunc, etcdFunc); err != nil {
		return nil, nil, err
	}

	return collector, closeFunc, nil
}
