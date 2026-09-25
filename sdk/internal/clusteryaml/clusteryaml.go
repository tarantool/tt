// Package clusteryaml builds a cluster configuration from YAML the way the tt
// core builds one: with the Tarantool inheritance hierarchy, so that the
// SDK's InstanceConfig and Instances work on the result.
//
// It is internal to the SDK: modules receive cluster configurations from
// Services.ClusterConfig and never parse the YAML themselves.
package clusteryaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-config/v2/collectors"
)

// Levels returns the Tarantool hierarchy a cluster configuration inherits
// along: global, then groups, replicasets and instances.
func Levels() []string {
	return goconfig.Levels(goconfig.Global, "groups", "replicasets", "instances")
}

// InheritanceOptions returns the per-key inheritance rules of a Tarantool
// cluster configuration: credentials are merged deeply rather than replaced.
// Deep merging is go-config's default for every key; the rule is stated as
// go-config's Tarantool builder states it, so that the two stay alike if
// the default changes.
func InheritanceOptions() []goconfig.InheritanceOption {
	return []goconfig.InheritanceOption{
		goconfig.WithInheritMerge("credentials", goconfig.MergeDeep),
	}
}

// Build parses data, a YAML cluster configuration, into a Config with the
// Tarantool inheritance hierarchy. Nothing is validated. Empty data builds
// an empty Config.
func Build(ctx context.Context, data []byte) (goconfig.Config, error) {
	builder := goconfig.NewBuilder()

	builder = builder.WithoutValidation()
	builder = builder.WithInheritance(Levels(), InheritanceOptions()...)

	if len(bytes.TrimSpace(data)) > 0 {
		source, err := collectors.NewSource(ctx, bytesSource{data: data},
			collectors.NewYamlFormat())
		if err != nil {
			return goconfig.Config{}, fmt.Errorf("cluster configuration: %w", err)
		}

		builder = builder.AddCollector(source)
	}

	cfg, errs := builder.Build(ctx)
	if len(errs) > 0 {
		return goconfig.Config{}, fmt.Errorf("cluster configuration: %w", errors.Join(errs...))
	}

	return cfg, nil
}

// bytesSource is a collectors.DataSource serving YAML held in memory.
type bytesSource struct {
	data []byte
}

// Name names the source in diagnostics.
func (bytesSource) Name() string { return "cluster-yaml" }

// SourceType reports that the source is none of the kinds go-config knows.
func (bytesSource) SourceType() goconfig.SourceType { return goconfig.UnknownSource }

// Revision reports no revision.
func (bytesSource) Revision() goconfig.RevisionType { return "" }

// FetchStream returns the YAML.
func (s bytesSource) FetchStream(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
