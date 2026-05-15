package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/go-config/collectors"
	gcttarantool "github.com/tarantool/go-config/tarantool"
	"github.com/tarantool/tt/lib/integrity"
)

// fillOnlyMerge copies leaf values from src into dst only when the key is not
// already present in dst (fill-only semantics: never overwrite existing keys).
func fillOnlyMerge(dst *goconfig.MutableConfig, src goconfig.Config) error {
	ch, err := src.Walk(context.Background(), nil, -1)
	if err != nil {
		if errors.Is(err, goconfig.ErrPathNotFound) {
			return nil
		}
		return err
	}
	for v := range ch {
		p := v.Meta().Key
		if _, ok := dst.Lookup(p); ok {
			continue
		}
		var value any
		if err := v.Get(&value); err != nil {
			return fmt.Errorf("fillOnlyMerge get %s: %w", p, err)
		}
		if err := dst.Set(p, value); err != nil {
			return fmt.Errorf("fillOnlyMerge set %s: %w", p, err)
		}
	}
	return nil
}

// GetClusterConfig returns a cluster configuration loaded from a path to
// a config file. It uses a config file, etcd and default environment
// variables as sources. The function returns a cluster config as is, without
// merging of settings from scopes: global, group, replicaset, instance.
//
// The documented Tarantool config priority is
// `TT_*` > file > centralized (etcd / config storage) > `TT_*_DEFAULT`.
// gcttarantool.Builder folds `TT_*_DEFAULT` into its lowest slot, alongside
// the file pass, and the fill-only merge strategy means a single Build()
// call would let `TT_*_DEFAULT` block the post-Build centralized merge. To
// honor the documented order without taking the TT-1011 storage-handle path,
// the env-default layer is split out via two builds and applied last.
func GetClusterConfig(
	ctx context.Context,
	path string,
	integ integrity.IntegrityCtx,
) (*goconfig.MutableConfig, error) {
	if path == "" {
		return nil, fmt.Errorf("a configuration file must be set")
	}

	// Phase-1: env (excluding *_DEFAULT) + file → mut.
	mut, err := gcttarantool.New().
		WithoutValidation().
		WithEnvIgnore("TT_*_DEFAULT").
		WithConfigFile(path).
		BuildMutable(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load config from %q: %w", path, err)
	}

	// Read storage tt-side (etcd or TCS).
	storageCfg, cleanup, err := readStorageFromConfig(ctx, mut.Snapshot(), integ)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Fill-only merge storage layer (file > storage per Tarantool docs).
	if _, ok := storageCfg.Lookup(nil); ok {
		if err := fillOnlyMerge(mut, storageCfg); err != nil {
			return nil, fmt.Errorf("unable to merge storage config: %w", err)
		}
	}

	// Phase-2: build with full env (including *_DEFAULT) for the fill-only
	// merge of the lowest-priority defaults.
	def, err := gcttarantool.New().
		WithoutValidation().
		WithConfigFile(path).
		BuildMutable(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load config from %q with default env: %w", path, err)
	}
	if err := fillOnlyMerge(mut, def.Snapshot()); err != nil {
		return nil, fmt.Errorf("unable to merge default env config: %w", err)
	}

	return mut, nil
}

// GetInstanceConfig returns a goconfig.Config view for the named instance
// within the given cluster config. It delegates to cluster.InstanceConfig.
func GetInstanceConfig(
	cfg *goconfig.MutableConfig,
	instance string,
) (goconfig.Config, error) {
	if !HasInstance(cfg.Snapshot(), instance) {
		return goconfig.Config{},
			fmt.Errorf("an instance %q not found", instance)
	}
	return InstanceConfig(cfg.Snapshot(), instance)
}

// bytesSource implements collectors.DataSource backed by an in-memory byte slice.
// Used by BuildGoConfigFromBytes to create a goconfig collector from raw YAML bytes.
type bytesSource struct {
	name string
	data []byte
}

func (s bytesSource) Name() string                              { return s.name }
func (s bytesSource) SourceType() goconfig.SourceType          { return goconfig.UnknownSource }
func (s bytesSource) Revision() goconfig.RevisionType          { return "" }
func (s bytesSource) FetchStream(_ context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// NewBytesSource creates a goconfig Collector backed by the given YAML bytes.
// name is used as the source name (for diagnostics).
// The returned Collector can be passed to goconfig.Builder.AddCollector.
func NewBytesSource(name string, data []byte) (goconfig.Collector, error) {
	ctx := context.Background()
	return collectors.NewSource(
		ctx,
		bytesSource{name: name, data: data},
		collectors.NewYamlFormat(),
	)
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
func BuildGoConfigFromBytes(ctx context.Context, b []byte) (goconfig.Config, error) {
	builder := newClusterBuilder()

	if len(bytes.TrimSpace(b)) > 0 {
		src, err := collectors.NewSource(
			ctx,
			bytesSource{name: "cluster-yaml", data: b},
			collectors.NewYamlFormat(),
		)
		if err != nil {
			return goconfig.Config{}, fmt.Errorf("build go-config from bytes: create source: %w", err)
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
func BuildMutableFromBytes(ctx context.Context, b []byte) (*goconfig.MutableConfig, error) {
	builder := newClusterBuilder()

	if len(bytes.TrimSpace(b)) > 0 {
		src, err := collectors.NewSource(
			ctx,
			bytesSource{name: "cluster-yaml-mutable", data: b},
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
