package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

const (
	defaultTimeout = 3 * time.Second
	configFileName = "config.yaml"
)

type showOpts struct {
	Validate bool
}

var showFlags = showOpts{
	Validate: false,
}

type publicOpts struct {
	Force bool
}

var publicFlags = publicOpts{
	Force: false,
}

func NewClusterCmd() *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage cluster configuration",
	}

	uriHelp := fmt.Sprintf(`The URI specifies a etcd connection settings in the following format:
http(s)://[username:password@]host:port[/prefix][?arguments]

* prefix - a base path to Tarantool configuration in etcd.

Possible arguments:

* name - a name of an instance in the cluster configuration.
* timeout - a request timeout in seconds (default %.1f).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.
`, float64(defaultTimeout)/float64(time.Second))

	show := &cobra.Command{
		Use:   "show (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)",
		Short: "Show a cluster configuration",
		Long: "Show a cluster configuration for an application, instance or" +
			" from etcd URI.\n\n" + uriHelp,
		Example: "tt show application_name\n" +
			"  tt show application_name:instance_name\n" +
			"  tt show https://user@pass@localhost:2379/?prefix=/tt\n" +
			"  tt show https://user@pass@localhost:2379/?prefix=/tt&name=instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterShowModule, args)
			handleCmdErr(cmd, err)
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
	show.Flags().BoolVar(&showFlags.Validate, "validate", showFlags.Validate,
		"validate the configuration")
	clusterCmd.AddCommand(show)

	public := &cobra.Command{
		Use:   "public (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>) file",
		Short: "Publish a cluster configuration",
		Long: "Publish a cluster configuration from the file to an application " +
			"or instance to a cluster configuration file or to a etcd URI.\n\n" +
			uriHelp,
		Example: "tt public application_name cluster.yaml\n" +
			"  tt public application_name:instance_name instance.yaml\n" +
			"  tt public " +
			"https://user@pass@localhost:2379/?prefix=/tt cluster.yaml\n" +
			"  tt public " +
			"https://user@pass@localhost:2379/?prefix=/tt&name=instance " +
			"instance.yaml",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalClusterPublicModule, args)
			handleCmdErr(cmd, err)
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
	public.Flags().BoolVar(&publicFlags.Force, "force", publicFlags.Force,
		"force publish and skip validation")
	clusterCmd.AddCommand(public)

	return clusterCmd
}

// internalClusterShowModule is an entrypoint for `cluster show` command.
func internalClusterShowModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if uri, err := url.Parse(args[0]); err == nil && uri.Scheme != "" {
		return showEtcd(uri, showFlags.Validate)
	}

	// It looks like an application or an application:instance.
	path, name, err := parseAppStr(cmdCtx, args[0])
	if err != nil {
		return err
	}

	return showCluster(path, name, showFlags.Validate)
}

// showEtcd shows a configuration from etcd.
func showEtcd(uri *url.URL, validate bool) error {
	etcdOpts, err := parseEtcdOpts(uri)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", uri, err)
	}

	etcdcli, err := cluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}
	defer etcdcli.Close()

	prefix, timeout := etcdOpts.Prefix, etcdOpts.Timeout
	config, err := cluster.NewEtcdCollector(etcdcli, prefix, timeout).Collect()
	if err != nil {
		return fmt.Errorf("failed to collect a configuration from etcd: %w", err)
	}

	return printRawClusterConfig(config, uri.Query().Get("name"), validate)
}

// showFile shows a full cluster configuration for a configuration path.
func showCluster(path, name string, validate bool) error {
	config, err := cluster.GetClusterConfig(path)
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration: %w", err)
	}

	return printClusterConfig(config, name, validate)
}

// internalClusterPublicModule is an entrypoint for `cluster public` command.
func internalClusterPublicModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	data, config, err := readSourceFile(args[1])
	if err != nil {
		return err
	}

	var (
		name          string // An instance name if exist.
		collector     cluster.Collector
		dataPublisher cluster.DataPublisher
	)
	if uri, err := url.Parse(args[0]); err == nil && uri.Scheme != "" {
		etcdOpts, err := parseEtcdOpts(uri)
		if err != nil {
			return fmt.Errorf("invalid URL %q: %w", uri, err)
		}

		name = uri.Query().Get("name")
		if !publicFlags.Force {
			if err := validateRawConfig(config, name); err != nil {
				return err
			}
		}

		etcdcli, err := cluster.ConnectEtcd(etcdOpts)
		if err != nil {
			return fmt.Errorf("failed to connect to etcd: %w", err)
		}
		defer etcdcli.Close()

		prefix, timeout := etcdOpts.Prefix, etcdOpts.Timeout
		collector = cluster.NewEtcdCollector(etcdcli, prefix, timeout)
		dataPublisher = cluster.NewEtcdDataPublisher(etcdcli, prefix, timeout)
	} else {
		path, instance, err := parseAppStr(cmdCtx, args[0])
		if err != nil {
			return err
		}

		name = instance
		if !publicFlags.Force {
			if err := validateRawConfig(config, name); err != nil {
				return err
			}
		}

		collector = cluster.NewFileCollector(path)
		dataPublisher = cluster.NewFileDataPublisher(path)
	}

	if name == "" {
		// The easy case, just publish the configuration as is.
		return dataPublisher.Publish(data)
	}

	src, err := collector.Collect()
	if err != nil {
		return fmt.Errorf("failed to get a cluster configuration to update "+
			"an instance %q: %w", name, err)
	}

	cconfig, err := cluster.MakeClusterConfig(src)
	if err != nil {
		return fmt.Errorf("failed to parse a target configuration: %w", err)
	}

	cconfig, err = cluster.ReplaceInstanceConfig(cconfig, name, config)
	if err != nil {
		return fmt.Errorf("failed to replace an instance %q configuration "+
			"in a cluster configuration: %w", name, err)
	}

	return cluster.NewYamlConfigPublisher(dataPublisher).Publish(cconfig.RawConfig)
}

// readSourceFile reads a configuration from a source file.
func readSourceFile(path string) ([]byte, *cluster.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read path %q: %s", path, err)
	}

	config, err := cluster.NewYamlCollector(data).Collect()
	if err != nil {
		err = fmt.Errorf("failed to read a configuration from path %q: %s",
			path, err)
		return nil, nil, err
	}

	return data, config, nil
}

// parseAppStr parses a string and returns an application configuration path
// and an application instance name or an error.
func parseAppStr(cmdCtx *cmdcontext.CmdCtx, appStr string) (string, string, error) {
	var (
		runningCtx running.RunningCtx
		name       string
	)

	if !isConfigExist(cmdCtx) {
		return "", "", fmt.Errorf("unable to resolve the application name %q: %w",
			appStr, errNoConfig)
	}

	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, []string{appStr})
	if err != nil {
		return "", "", err
	}

	appDir := filepath.Dir(runningCtx.Instances[0].AppPath)
	path := filepath.Join(appDir, configFileName)

	colonIds := strings.Index(appStr, string(running.InstanceDelimiter))
	if colonIds != -1 {
		name = runningCtx.Instances[0].InstName
	}

	return path, name, nil
}

// parseEtcdOpts parses etcd options from a URL.
func parseEtcdOpts(uri *url.URL) (cluster.EtcdOpts, error) {
	endpoint := url.URL{
		Scheme: uri.Scheme,
		Host:   uri.Host,
	}
	values := uri.Query()
	opts := cluster.EtcdOpts{
		Endpoints: []string{endpoint.String()},
		Prefix:    uri.Path,
		Username:  uri.User.Username(),
		KeyFile:   values.Get("ssl_key_file"),
		CertFile:  values.Get("ssl_cert_file"),
		CaPath:    values.Get("ssl_ca_path"),
		CaFile:    values.Get("ssl_ca_file"),
		Timeout:   defaultTimeout,
	}
	if password, ok := uri.User.Password(); ok {
		opts.Password = password
	}

	verifyPeerStr := values.Get("verify_peer")
	verifyHostStr := values.Get("verify_host")
	timeoutStr := values.Get("timeout")

	if verifyPeerStr != "" {
		verifyPeerStr = strings.ToLower(verifyPeerStr)
		if verify, err := strconv.ParseBool(verifyPeerStr); err == nil {
			if verify == false {
				opts.SkipHostVerify = true
			}
		} else {
			err = fmt.Errorf("invalid verify_peer, boolean expected: %w", err)
			return opts, err
		}
	}

	if verifyHostStr != "" {
		verifyHostStr = strings.ToLower(verifyHostStr)
		if verify, err := strconv.ParseBool(verifyHostStr); err == nil {
			if verify == false {
				opts.SkipHostVerify = true
			}
		} else {
			err = fmt.Errorf("invalid verify_host, boolean expected: %w", err)
			return opts, err
		}
	}

	if timeoutStr != "" {
		if timeout, err := strconv.ParseFloat(timeoutStr, 64); err == nil {
			opts.Timeout = time.Duration(timeout * float64(time.Second))
		} else {
			err = fmt.Errorf("invalid timeout, float expected: %w", err)
			return opts, err
		}
	}

	return opts, nil
}

// printRawClusterConfig prints a raw cluster configuration or an instance
// configuration if the instance name is specified.
func printRawClusterConfig(config *cluster.Config,
	instance string, validate bool) error {
	cconfig, err := cluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	if instance == "" {
		var err error
		if validate {
			err = validateClusterConfig(cconfig, false)
		}
		printConfig(cconfig.RawConfig)
		return err
	}

	return printInstanceConfig(cconfig, instance, false, validate)
}

// printClusterConfig prints a full-merged cluster configuration or an instance
// configuration if the instance name is specified.
func printClusterConfig(cconfig cluster.ClusterConfig,
	instance string, validate bool) error {
	if instance == "" {
		var err error
		if validate {
			err = validateClusterConfig(cconfig, true)
		}
		printConfig(cconfig.RawConfig)
		return err
	}

	return printInstanceConfig(cconfig, instance, true, validate)
}

// printInstanceConfig prints an instance configuration in the cluster.
func printInstanceConfig(config cluster.ClusterConfig,
	instance string, full, validate bool) error {
	if !cluster.HasInstance(config, instance) {
		return fmt.Errorf("instance %q not found", instance)
	}

	var (
		err     error
		iconfig *cluster.Config
	)
	if full {
		ic, _ := cluster.GetInstanceConfig(config, instance)
		iconfig = ic.RawConfig
	} else {
		iconfig = cluster.Instantiate(config, instance)
	}

	if validate {
		err = validateInstanceConfig(iconfig, instance)
	}
	printConfig(iconfig)
	return err
}

// vlidateRawConfig validates a raw cluster or an instance configuration. The
// configuration belongs to an instance if name != "".
func validateRawConfig(config *cluster.Config, name string) error {
	if name == "" {
		return validateRawClusterConfig(config)
	} else {
		return validateInstanceConfig(config, name)
	}
}

// validateRawClusterConfig validates a raw cluster configuration or an
// instance configuration if the instance name is specified.
func validateRawClusterConfig(config *cluster.Config) error {
	cconfig, err := cluster.MakeClusterConfig(config)
	if err != nil {
		return err
	}

	return validateClusterConfig(cconfig, false)
}

// validateClusterConfig validates a cluster configuration.
func validateClusterConfig(cconfig cluster.ClusterConfig, full bool) error {
	var errs []error
	if err := cluster.Validate(cconfig.RawConfig, cluster.TarantoolSchema); err != nil {
		err = fmt.Errorf("an invalid cluster configuration: %s", err)
		errs = append(errs, err)
	}

	for _, name := range cluster.Instances(cconfig) {
		var iconfig *cluster.Config
		if full {
			ic, err := cluster.GetInstanceConfig(cconfig, name)
			if err != nil {
				return err
			}
			iconfig = ic.RawConfig
		} else {
			iconfig = cluster.Instantiate(cconfig, name)
		}
		if err := validateInstanceConfig(iconfig, name); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateInstanceConfig validates an instance configuration.
func validateInstanceConfig(config *cluster.Config, name string) error {
	if err := cluster.Validate(config, cluster.TarantoolSchema); err != nil {
		return fmt.Errorf("an invalid instance %q configuration: %w", name, err)
	}
	return nil
}

// printConfig just prints a configuration to stdout.
func printConfig(config *cluster.Config) {
	fmt.Print(config.String())
}
