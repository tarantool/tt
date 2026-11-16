package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-config/v2/collectors"
	gcttarantool "github.com/tarantool/go-config/v2/tarantool"
	"github.com/tarantool/go-storage/v2"
	"github.com/tarantool/go-storage/v2/driver/dummy"
	gsintegrity "github.com/tarantool/go-storage/v2/integrity"
	"github.com/tarantool/go-storage/v2/marshaller"
	"github.com/tarantool/tt/sdk/integrity"
)

var (
	errAConfigurationFileMustBeSet = errors.New("a configuration file must be set")
)

// LocalInstances returns the instances of the topology the cluster config
// file at path declares by itself, without the config storage it may point
// to: what is known of the topology here while that storage cannot be read
// (StorageError).
func LocalInstances(ctx context.Context, path string) ([]string, error) {
	mut, err := gcttarantool.New().
		WithoutValidation().
		WithEnvIgnore("TT_*_DEFAULT").
		WithConfigFile(path).
		BuildMutable(ctx)
	if err != nil {
		return nil, loadError(path, err)
	}

	return Instances(mut.Snapshot())
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
//
// The sources are the layers of the config, in Tarantool's priority:
// `TT_*` > file > centralized (etcd / config storage) > `TT_*_DEFAULT`.
// Tarantool makes the config of an instance of each source on its own,
// global, group, replicaset and instance scopes merged, and merges these
// instance configs by the priority: an instance key of the file wins over
// a global one of the storage, and so does a global key of the file over
// an instance one of the storage, and `TT_*` over both. The effective
// configs of go-config resolve the layers of a build so, and the config
// storage is a layer of the build, between the file and `TT_*_DEFAULT`.
// The cluster config as a whole is the layers merged key by key, the file
// over the storage, as Tarantool merges the cluster configs of its sources.
func GetClusterConfig(
	ctx context.Context,
	path string,
	integ integrity.IntegrityCtx,
) (*goconfig.MutableConfig, error) {
	if path == "" {
		return nil, errAConfigurationFileMustBeSet
	}

	// The config storage is the one TT_* and the file point to, as
	// Tarantool reads it: TT_*_DEFAULT come after the storage.
	ref, err := gcttarantool.New().
		WithoutValidation().
		WithEnvIgnore("TT_*_DEFAULT").
		WithConfigFile(path).
		Build(ctx)
	if err != nil {
		return nil, loadError(path, err)
	}

	// Read storage tt-side (etcd or TCS).
	storageData, cleanup, err := readStorageFromConfig(ctx, ref, integ)
	if err != nil {
		return nil, err
	}

	if cleanup != nil {
		cleanup()
	}

	return buildClusterConfig(ctx, path, storageData)
}

// buildClusterConfig builds the cluster config of the file at path with
// storageData, the YAML of the config storage it points to, if any, as the
// storage layer, and the TT_* environment.
func buildClusterConfig(
	ctx context.Context,
	path string,
	storageData []byte,
) (*goconfig.MutableConfig, error) {
	builder := gcttarantool.New().
		WithoutValidation().
		WithConfigFile(path)

	if len(bytes.TrimSpace(storageData)) > 0 {
		store, err := readStore(ctx, storageData)
		if err != nil {
			return nil, err
		}

		builder = builder.WithStorage(store)
	}

	mut, err := builder.BuildMutable(ctx)
	if err != nil {
		return nil, loadError(path, err)
	}

	return mut, nil
}

// readStore returns a store that holds data, the cluster config tt has read
// from a config storage, for the builder of go-config to read as the
// storage layer. tt reads the storage itself, with its integrity checks and
// its endpoints tried in turn, and the builder takes the storage layer from
// a store only; one in memory serves the config tt has.
func readStore(ctx context.Context, data []byte) (*gsintegrity.Store[[]byte], error) {
	codec, err := gsintegrity.NewCodecBuilder[[]byte]().
		WithMarshaller(marshaller.NewBytesMarshaller()).
		Build()
	if err != nil {
		return nil, fmt.Errorf("storage config codec: %w", err)
	}

	store := codec.Bind(storage.NewStorage(dummy.New()))

	err = store.Put(ctx, "all", data)
	if err != nil {
		return nil, fmt.Errorf("storage config: %w", err)
	}

	return store, nil
}

// GetInstanceConfig returns a goconfig.Config view for the named instance
// within the given cluster config. It delegates to cluster.InstanceConfig.
func GetInstanceConfig(
	cfg *goconfig.MutableConfig,
	instance string,
) (goconfig.Config, error) {
	if !HasInstance(cfg.Snapshot(), instance) {
		return goconfig.Config{},
			fmt.Errorf("an %w%q not found", errInstanceNotFound, instance)
	}

	return InstanceConfig(cfg.Snapshot(), instance)
}

// bytesSource implements collectors.DataSource backed by an in-memory byte slice.
// Used by BuildGoConfigFromBytes to create a goconfig collector from raw YAML bytes.
type bytesSource struct {
	name string
	data []byte
}

func (s bytesSource) Name() string                    { return s.name }
func (s bytesSource) SourceType() goconfig.SourceType { return goconfig.UnknownSource }
func (s bytesSource) Revision() goconfig.RevisionType { return "" }
func (s bytesSource) FetchStream(_ context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// NewBytesSource creates a goconfig Collector backed by the given YAML bytes.
// name is used as the source name (for diagnostics).
// The returned Collector can be passed to goconfig.Builder.AddCollector.
func NewBytesSource(name string, data []byte) (goconfig.Collector, error) {
	ctx := context.Background()

	src, err := collectors.NewSource(
		ctx,
		bytesSource{name: name, data: data},
		collectors.NewYamlFormat(),
	)
	if err != nil {
		return nil, fmt.Errorf("create source %q: %w", name, err)
	}

	return src, nil
}

// clusterLevels returns the standard Tarantool hierarchy level names used for
// go-config inheritance (Global → groups → replicasets → instances).
func clusterLevels() []string {
	return goconfig.Levels(goconfig.Global, "groups", "replicasets", "instances")
}

// clusterInheritanceOpts returns the standard per-key inheritance options
// (credentials merge strategy). Pass alongside clusterLevels() to
// goconfig.Builder.WithInheritance.
func clusterInheritanceOpts() []goconfig.InheritanceOption {
	return []goconfig.InheritanceOption{
		goconfig.WithInheritMerge("credentials", goconfig.MergeDeep),
	}
}

// newClusterBuilder returns a new goconfig.Builder preconfigured with the
// standard Tarantool cluster inheritance hierarchy and WithoutValidation.
func newClusterBuilder() goconfig.Builder {
	b := goconfig.NewBuilder()

	b = b.WithoutValidation()
	b = b.WithInheritance(clusterLevels(), clusterInheritanceOpts()...)

	return b
}

// BuildGoConfigFromBytes parses YAML bytes into a goconfig.Config with
// inheritance configured to match the Tarantool cluster hierarchy
// (Global → groups → replicasets → instances). No validation is performed.
//
// A nil or empty slice produces an empty but fully-configured Config.
func BuildGoConfigFromBytes(ctx context.Context, data []byte) (goconfig.Config, error) {
	builder := newClusterBuilder()

	if len(bytes.TrimSpace(data)) > 0 {
		src, err := collectors.NewSource(
			ctx,
			bytesSource{name: "cluster-yaml", data: data},
			collectors.NewYamlFormat(),
		)
		if err != nil {
			return goconfig.Config{},
				fmt.Errorf("build go-config from bytes: create source: %w", err)
		}

		builder = builder.AddCollector(src)
	}

	cfg, errs := builder.Build(ctx)
	if len(errs) > 0 {
		return goconfig.Config{}, fmt.Errorf("build go-config from bytes: %w", errors.Join(errs...))
	}

	return cfg, nil
}

// BuildMutableFromBytes parses YAML bytes into a *goconfig.MutableConfig with
// inheritance configured to match the Tarantool cluster hierarchy. No
// validation is performed (WithoutValidation), so Set calls never roll back.
//
// A nil or empty slice produces an empty but fully-configured MutableConfig.
func BuildMutableFromBytes(ctx context.Context, data []byte) (*goconfig.MutableConfig, error) {
	builder := newClusterBuilder()

	if len(bytes.TrimSpace(data)) > 0 {
		src, err := collectors.NewSource(
			ctx,
			bytesSource{name: "cluster-yaml-mutable", data: data},
			collectors.NewYamlFormat(),
		)
		if err != nil {
			return nil,
				fmt.Errorf("build mutable go-config from bytes: create source: %w", err)
		}

		builder = builder.AddCollector(src)
	}

	mut, errs := builder.BuildMutable(ctx)
	if len(errs) > 0 {
		return nil, fmt.Errorf("build mutable go-config from bytes: %w", errors.Join(errs...))
	}

	return &mut, nil
}
