package cluster

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
)

const (
	groupsLabel        = "groups"
	replicasetsLabel   = "replicasets"
	instancesLabel     = "instances"
	defaultEtcdTimeout = 3 * time.Second
)

var (
	mainEnvCollector = NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s", middle)
	})
	defaultEnvCollector = NewEnvCollector(func(path []string) string {
		middle := strings.ToUpper(strings.Join(path, "_"))
		return fmt.Sprintf("TT_%s_DEFAULT", middle)
	})
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
				CertFile   string `yaml:"cert_file"`
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

// collectEtcdConfig collects a configuration from etcd with options from
// the cluster configuration.
func collectEtcdConfig(collectors CollectorFactory,
	clusterConfig ClusterConfig) (*Config, error) {
	etcdConfig := clusterConfig.Config.Etcd
	opts := EtcdOpts{
		Endpoints: etcdConfig.Endpoints,
		Username:  etcdConfig.Username,
		Password:  etcdConfig.Password,
		KeyFile:   etcdConfig.Ssl.KeyFile,
		CertFile:  etcdConfig.Ssl.CertFile,
		CaPath:    etcdConfig.Ssl.CaPath,
		CaFile:    etcdConfig.Ssl.CaFile,
	}
	if !etcdConfig.Ssl.VerifyPeer || !etcdConfig.Ssl.VerifyHost {
		opts.SkipHostVerify = true
	}
	if etcdConfig.Http.Request.Timeout != 0 {
		var err error
		timeout := fmt.Sprintf("%fs", etcdConfig.Http.Request.Timeout)
		opts.Timeout, err = time.ParseDuration(timeout)
		if err != nil {
			fmtErr := "unable to parse a etcd request timeout: %w"
			return nil, fmt.Errorf(fmtErr, err)
		}
	} else {
		opts.Timeout = defaultEtcdTimeout
	}

	etcd, err := ConnectEtcd(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to etcd: %w", err)
	}
	defer etcd.Close()

	etcdCollector, err := collectors.NewEtcd(etcd, etcdConfig.Prefix, "", opts.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd collector: %w", err)
	}

	etcdRawConfig, err := etcdCollector.Collect()
	if err != nil {
		return nil, fmt.Errorf("unable to get config from etcd: %w", err)
	}
	return etcdRawConfig, nil
}

// collectTarantoolConfig collects a configuration from tarantool config
// storage with options from the tarantool configuration.
func collectTarantoolConfig(collectors CollectorFactory,
	clusterConfig ClusterConfig) (*Config, error) {
	tarantoolConfig := clusterConfig.Config.Storage
	var opts []connector.ConnectOpts
	for _, endpoint := range tarantoolConfig.Endpoints {
		var network, address string
		if !connect.IsBaseURI(endpoint.Uri) {
			network = connector.TCPNetwork
			address = endpoint.Uri
		} else {
			network, address = connect.ParseBaseURI(endpoint.Uri)
		}

		if endpoint.Params.Transport == "" || endpoint.Params.Transport != "ssl" {
			opts = append(opts, connector.ConnectOpts{
				Network:  network,
				Address:  address,
				Username: endpoint.Login,
				Password: endpoint.Password,
			})
		} else {
			opts = append(opts, connector.ConnectOpts{
				Network:  network,
				Address:  address,
				Username: endpoint.Login,
				Password: endpoint.Password,
				Ssl: connector.SslOpts{
					KeyFile:  endpoint.Params.SslKeyFile,
					CertFile: endpoint.Params.SslCertFile,
					CaFile:   endpoint.Params.SslCaFile,
					Ciphers:  endpoint.Params.SslCiphers,
				},
			})
		}
	}

	pool, err := connector.ConnectPool(opts)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to tarantool config storage: %w", err)
	}
	defer pool.Close()

	tarantoolCollector, err := collectors.NewTarantool(pool,
		tarantoolConfig.Prefix, "",
		time.Duration(tarantoolConfig.Timeout*float64(time.Second)))
	if err != nil {
		return nil, fmt.Errorf("failed to create tarantool config storage collector: %w", err)
	}

	tarantoolRawConfig, err := tarantoolCollector.Collect()
	if err != nil {
		return nil, fmt.Errorf("failed to get config from tarantool config storage: %w", err)
	}
	return tarantoolRawConfig, nil
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
func GetClusterConfig(collectors CollectorFactory, path string) (ClusterConfig, error) {
	ret := ClusterConfig{}
	if path == "" {
		return ret, fmt.Errorf("a configuration file must be set")
	}

	config := NewConfig()

	mainEnvConfig, err := mainEnvCollector.Collect()
	if err != nil {
		fmtErr := "failed to collect a config from environment variables: %w"
		return ret, fmt.Errorf(fmtErr, err)
	}
	config.Merge(mainEnvConfig)

	collector, err := collectors.NewFile(path)
	if err != nil {
		return ret, fmt.Errorf("failed to create a file collector: %w", err)
	}

	fileConfig, err := collector.Collect()
	if err != nil {
		fmtErr := "unable to get cluster config from %q: %w"
		return ret, fmt.Errorf(fmtErr, path, err)
	}
	config.Merge(fileConfig)

	clusterConfig, err := MakeClusterConfig(config)
	if err != nil {
		return ret, fmt.Errorf("unable to parse cluster config from file: %w", err)
	}
	if len(clusterConfig.Config.Etcd.Endpoints) > 0 {
		etcdConfig, err := collectEtcdConfig(collectors, clusterConfig)
		if err != nil {
			return ret, err
		}
		config.Merge(etcdConfig)
	}

	if len(clusterConfig.Config.Storage.Endpoints) > 0 {
		tarantoolConfig, err := collectTarantoolConfig(collectors, clusterConfig)
		if err != nil {
			return ret, err
		}
		config.Merge(tarantoolConfig)
	}

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

// ReplaceInstanceConfig replaces an instance configuration.
func ReplaceInstanceConfig(cconfig ClusterConfig,
	instance string, iconfig *Config) (ClusterConfig, error) {
	for gname, group := range cconfig.Groups {
		for rname, replicaset := range group.Replicasets {
			for iname, _ := range replicaset.Instances {
				if instance == iname {
					path := []string{groupsLabel, gname,
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
