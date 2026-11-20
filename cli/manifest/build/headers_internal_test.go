package build

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// moduleBackends is a registry with a zig backend that makes modules and
// builds nothing, as a backend that compiles against the headers does.
func moduleBackends(t *testing.T) *buildreg.Registry {
	t.Helper()

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("zig", []sdkbuild.Backend{{
		Name:     "zig",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config: func() any {
			return new(struct {
				Steps []string `toml:"steps"`
			})
		},
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return headersBuild{}, nil
		},
		Incremental: false,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     nil,
		Hook:        false,
	}}, nil))

	return backends
}

// TestRun_headersWarningOnlyWhenCompiling checks that the warning that the
// headers are of another version is shown by a build that compiles a
// component, once per command, and not by a build of Lua alone.
func TestRun_headersWarningOnlyWhenCompiling(t *testing.T) {
	t.Parallel()

	// The warnings are shown once per process: one of this test alone.
	const warning = "the headers of the headers-warning test are of another version"

	var warnings []string

	lua := setupProject(t, manifestHead, map[string]string{"init.lua": "-- init"})
	opts := dryOptions(lua)

	opts.Tarantool.HeadersWarning = warning
	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	require.NoError(t, Run(context.Background(), opts))
	assert.NotContains(t, warnings, warning, "a build of Lua compiles nothing")

	native := setupProject(t, zigManifest, map[string]string{"fast/build.zig": ""})

	opts.ProjectDir = native
	opts.Backends = moduleBackends(t)
	opts.Tarantool.Prefix = fakeTarantoolPrefix(t)

	require.NoError(t, Run(context.Background(), opts))
	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 1, countOf(warnings, warning), "once per command: %q", warnings)
}

// countOf counts the items of list that are item.
func countOf(list []string, item string) int {
	count := 0

	for _, entry := range list {
		if entry == item {
			count++
		}
	}

	return count
}
