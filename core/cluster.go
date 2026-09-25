package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	gsintegrity "github.com/tarantool/go-storage/v2/integrity"

	"github.com/tarantool/tt/sdk"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/running"
)

var (
	// errNoSource reports the zero ClusterSource.
	errNoSource = errors.New("no cluster configuration source given")
	// errNoEnvironment reports an application asked for outside a tt
	// environment.
	errNoEnvironment = errors.New("no tt environment")
	// errNoClusterConfig reports an application without a cluster
	// configuration.
	errNoClusterConfig = errors.New("no cluster configuration")
)

// ClusterConfig returns the cluster configuration src names, read the way
// tt cluster show reads it.
func (s *services) ClusterConfig(
	ctx context.Context, src sdk.ClusterSource,
) (sdk.ClusterConfig, error) {
	s.mustBeReady("ClusterConfig")

	integ := cmd.GetCmdCtxPtr().Integrity

	if name, ok := src.App(); ok {
		return appClusterConfig(ctx, name, integ)
	}

	if path, ok := src.File(); ok {
		return fileClusterConfig(ctx, path, integ)
	}

	if uri, creds, ok := src.Storage(); ok {
		return storageClusterConfig(ctx, uri, creds, integ)
	}

	return sdk.ClusterConfig{}, errNoSource
}

// appClusterConfig returns the cluster configuration of the application name
// of the tt environment: its cluster config file, with the environment and
// the storage the file names.
func appClusterConfig(
	ctx context.Context, name string, integ integrity.IntegrityCtx,
) (sdk.ClusterConfig, error) {
	if cmd.GetCmdCtxPtr().Cli.ConfigPath == "" {
		return sdk.ClusterConfig{}, notFoundError{
			err: fmt.Errorf("cluster configuration of application %q: %w", name, errNoEnvironment),
		}
	}

	path, err := cmd.ClusterConfigPath(name)
	if errors.Is(err, running.ErrApplicationNotFound) {
		return sdk.ClusterConfig{}, notFoundError{
			err: fmt.Errorf("cluster configuration of application %q: %w", name, err),
		}
	}

	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration of application %q: %w",
			name, err)
	}

	if path == "" {
		return sdk.ClusterConfig{}, notFoundError{
			err: fmt.Errorf("application %q: %w", name, errNoClusterConfig),
		}
	}

	return clusterConfigFile(ctx, path, integ)
}

// fileClusterConfig returns the cluster configuration of the file at path,
// with the environment and the storage the file names.
func fileClusterConfig(
	ctx context.Context, path string, integ integrity.IntegrityCtx,
) (sdk.ClusterConfig, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return sdk.ClusterConfig{}, notFoundError{
			err: fmt.Errorf("cluster configuration file: %w", err),
		}
	}

	return clusterConfigFile(ctx, path, integ)
}

// clusterConfigFile reads the cluster configuration file at path with the
// environment and the storage the file names; relative paths in it are
// relative to the file's directory.
func clusterConfigFile(
	ctx context.Context, path string, integ integrity.IntegrityCtx,
) (sdk.ClusterConfig, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration file %q: %w", path, err)
	}

	cfg, err := cluster.GetClusterConfig(ctx, path, integ)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration: %w", err)
	}

	return sdk.ClusterConfig{Config: cfg.Snapshot(), Dir: filepath.Dir(abs)}, nil
}

// storageClusterConfig returns what the etcd or Tarantool config storage at
// uri holds under its prefix, as tt cluster show reads it. Nothing under the
// prefix, or at the key the URI names, is not found.
func storageClusterConfig(
	ctx context.Context, uri string, creds sdk.Credentials, integ integrity.IntegrityCtx,
) (sdk.ClusterConfig, error) {
	opts, err := connect.CreateURIOpts(uri)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration storage URI: %w", err)
	}

	factory, err := cluster.NewCollectorFactory(integ)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration: %w", err)
	}

	//nolint:contextcheck // The connection takes no context; the URI's timeout bounds it.
	stor, cleanup, storageType, err := sdkcluster.NewStorageConnection(
		sdkcluster.ConnectOpts{Username: creds.Username, Password: creds.Password}, opts)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration: %w", err)
	}
	defer cleanup()

	collector, err := factory.NewRemoteStorage(stor, opts.Prefix, opts.Params["key"],
		opts.Timeout, storageType)
	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration: %s collector: %w",
			storageType, err)
	}

	data, err := cluster.CollectDataBytes(ctx, collector)
	if errors.Is(err, sdkcluster.ErrNoConfiguration) || errors.Is(err, gsintegrity.ErrNotFound) {
		return sdk.ClusterConfig{}, notFoundError{err: fmt.Errorf("cluster configuration: %w", err)}
	}

	if err != nil {
		return sdk.ClusterConfig{}, fmt.Errorf("cluster configuration: %w", err)
	}

	cfg, err := cluster.BuildGoConfigFromBytes(ctx, data)
	if err != nil {
		return sdk.ClusterConfig{},
			fmt.Errorf("cluster configuration from %s: %w", storageType, err)
	}

	return sdk.ClusterConfig{Config: cfg, Dir: ""}, nil
}
