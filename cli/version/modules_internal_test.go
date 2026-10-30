package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// distributionBuildInfo is the build info of a distribution: a generated
// main module, the core and its SDK pinned to one commit of the core, a
// module of the core's repository that follows the core, a module from a
// directory and a module replaced by a fork.
func distributionBuildInfo() (*debug.BuildInfo, bool) {
	return &debug.BuildInfo{
		GoVersion: "go1.27.3",
		Path:      "ttdistribution",
		Main:      debug.Module{Path: "ttdistribution", Version: develVersion},
		Deps: []*debug.Module{
			{Path: "github.com/tarantool/tt/v3", Version: "v3.1.0"},
			{
				Path: "github.com/tarantool/tt/modules/aeon", Version: "v0.0.0",
				Replace: &debug.Module{
					Path:    "github.com/tarantool/tt/modules/aeon",
					Version: "v0.0.0-20261028190206-9031d7ad96d9",
				},
			},
			{
				Path: "example.com/corp/plugins/billing", Version: "v0.0.0",
				Replace: &debug.Module{Path: "/src/corp/plugins/billing", Version: develVersion},
			},
			{
				Path: "example.com/corp/plugins", Version: "v1.0.0",
			},
			{
				Path: "github.com/acme/tt-foo", Version: "v1.2.0",
				Replace: &debug.Module{Path: "github.com/fork/tt-foo", Version: "v1.2.1"},
			},
		},
	}, true
}

// TestModuleVersions checks that a module is reported with the Go module
// that holds the package of its constructor - the longest path that is a
// prefix of the package - and the version Go recorded for it.
func TestModuleVersions(t *testing.T) {
	t.Parallel()

	got := moduleVersions([]Module{
		{Name: "aeon", Package: "github.com/tarantool/tt/modules/aeon"},
		{Name: "billing", Package: "example.com/corp/plugins/billing/cmd"},
		{Name: "builtin", Package: "github.com/tarantool/tt/v3/core"},
		{Name: "foo", Package: "github.com/acme/tt-foo"},
		{Name: "lost", Package: "example.com/lost"},
		{Name: "main", Package: "ttdistribution"},
	}, distributionBuildInfo)

	assert.Equal(t, []ModuleVersion{
		{
			Name: "aeon", Path: "github.com/tarantool/tt/modules/aeon",
			Version: "v0.0.0-20261028190206-9031d7ad96d9", Replace: "",
		},
		{
			Name: "billing", Path: "example.com/corp/plugins/billing",
			Version: develVersion, Replace: "",
		},
		{Name: "builtin", Path: "github.com/tarantool/tt/v3", Version: "v3.1.0", Replace: ""},
		{
			Name: "foo", Path: "github.com/acme/tt-foo",
			Version: "v1.2.1", Replace: "github.com/fork/tt-foo",
		},
		{Name: "lost", Path: "", Version: unknownVersion, Replace: ""},
		{Name: "main", Path: "ttdistribution", Version: develVersion, Replace: ""},
	}, got)
}

// TestModuleVersionsWithoutBuildInfo checks that a binary without build
// info reports every module as unknown.
func TestModuleVersionsWithoutBuildInfo(t *testing.T) {
	t.Parallel()

	got := moduleVersions([]Module{{Name: "aeon", Package: "github.com/tarantool/tt/modules/aeon"}},
		func() (*debug.BuildInfo, bool) { return nil, false })

	assert.Equal(t, []ModuleVersion{
		{Name: "aeon", Path: "", Version: unknownVersion, Replace: ""},
	}, got)
}

// TestFormatModules checks the columns tt version --modules prints.
func TestFormatModules(t *testing.T) {
	t.Parallel()

	got := FormatModules([]ModuleVersion{
		{
			Name: "aeon", Path: "github.com/tarantool/tt/modules/aeon",
			Version: "v0.0.0-1", Replace: "",
		},
		{
			Name: "foo", Path: "github.com/acme/tt-foo",
			Version: "v1.2.1", Replace: "github.com/fork/tt-foo",
		},
		{Name: "lost", Path: "", Version: unknownVersion, Replace: ""},
	})

	assert.Equal(t, ""+
		"  aeon  v0.0.0-1   github.com/tarantool/tt/modules/aeon\n"+
		"  foo   v1.2.1     github.com/acme/tt-foo => github.com/fork/tt-foo\n"+
		"  lost  <unknown>  <unknown>\n", got)
}

// TestFlavourCoreVersion checks the line about the core tt version adds for
// a distribution with a version of its own, and that tt's own build adds
// none.
//
//nolint:paralleltest // Replaces the values the build stamps and the build info.
func TestFlavourCoreVersion(t *testing.T) {
	setCoreBuild(t, "", "", "", "")

	distribution := Flavour{
		Title: "Tarantool CLI EE", Version: &Info{Tag: "v3.0.0"}, Edition: "ee",
	}

	setBuildInfo(t, "v3.1.1-0.20260925120000-abcdef123456", true)

	line, ok := distribution.CoreVersion()
	assert.True(t, ok)
	assert.Equal(t, "Tarantool CLI core version 3.1.0. commit: abcdef123456 "+
		"(v3.1.1-0.20260925120000-abcdef123456)", line)

	setBuildInfo(t, "", false)

	line, ok = distribution.CoreVersion()
	assert.True(t, ok)
	assert.Equal(t, "Tarantool CLI core version <unknown>. commit: <unknown>", line)

	_, ok = Flavour{}.CoreVersion()
	assert.False(t, ok, "tt's own version is the core's")

	_, ok = Flavour{Title: "Tarantool CLI EE", Version: nil, Edition: "ee"}.CoreVersion()
	assert.False(t, ok, "a distribution without a version of its own shows the core's")
}
