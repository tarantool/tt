package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// minimalDistribution is the smallest distribution manifest: tt itself, on
// the core of the tt that builds it.
const minimalDistribution = `manifest_version = "0.1"
manifest_kind = "tt-distribution"
`

func parseDistribution(t *testing.T, src string) *manifest.Distribution {
	t.Helper()

	dist, warnings, err := manifest.ParseDistribution([]byte(src))
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.NoError(t, dist.Validate())

	return dist
}

func TestDistributionDefaults(t *testing.T) {
	t.Parallel()

	dist := parseDistribution(t, minimalDistribution)

	assert.Equal(t, "tt", dist.Name())
	assert.Equal(t, "github.com/tarantool/tt/v3", dist.CoreModule())
	assert.Empty(t, dist.Core.Tt)
	assert.Empty(t, dist.Core.Path)
	assert.Equal(t, manifest.IntegrityNone, dist.Distribution.IntegrityValue())
	assert.True(t, dist.Distribution.BuiltinValue())
	assert.True(t, dist.Distribution.ExternalModulesValue())
	assert.Empty(t, dist.Dependencies)
	assert.Equal(t, manifest.HashBytes([]byte(minimalDistribution)), dist.Hash())
}

func TestDistributionFull(t *testing.T) {
	t.Parallel()

	dist := parseDistribution(t, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-ee"

[core]
tt = "v3"
module = "github.com/acme/tt/v3"

[distribution]
title = "Tarantool CLI EE"
edition = "ee"
integrity = "rsa"
builtin = false
external_modules = false

[build]
ssl = "openssl_static"
cgo = true
tags = ["go_tarantool_ssl_disable"]
ldflags = ["-X", "main.x=1"]

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
foo = "github.com/acme/tt-foo@v1.2.0"
migrations = "./plugins/migrations"
inline = { module = "github.com/acme/tt-inline", symbol = "Make" }

[dependencies.bar]
module = "github.com/acme/tt-bar"
version = "v1.4.0"
package = "github.com/acme/tt-bar/cmd"
kind = "tt-module"
source = "registry"

[dependencies.local]
path = "../tt-local"
`)

	assert.Equal(t, "tt-ee", dist.Name())
	assert.Equal(t, "github.com/acme/tt/v3", dist.CoreModule())
	assert.Equal(t, "v3", dist.Core.Tt)
	assert.Equal(t, manifest.IntegrityRSA, dist.Distribution.IntegrityValue())
	assert.False(t, dist.Distribution.BuiltinValue())
	assert.False(t, dist.Distribution.ExternalModulesValue())
	assert.Equal(t, manifest.DistributionBuild{
		SSL:     "openssl_static",
		Cgo:     new(true),
		Tags:    []string{"go_tarantool_ssl_disable"},
		Ldflags: []string{"-X", "main.x=1"},
	}, dist.Build)

	assert.Equal(t, map[string]manifest.ModuleDependency{
		"aeon": {Module: "github.com/tarantool/tt/modules/aeon", Source: "registry"},
		"foo": {
			Module: "github.com/acme/tt-foo", Version: "v1.2.0", Source: "registry",
		},
		"migrations": {Path: "./plugins/migrations", Source: "path"},
		"inline": {
			Module: "github.com/acme/tt-inline", Symbol: "Make", Source: "registry",
		},
		"bar": {
			Module: "github.com/acme/tt-bar", Version: "v1.4.0",
			Package: "github.com/acme/tt-bar/cmd", Kind: "tt-module", Source: "registry",
		},
		"local": {Path: "../tt-local", Source: "path"},
	}, dist.Dependencies)

	local, bar, inline := dist.Dependencies["local"], dist.Dependencies["bar"],
		dist.Dependencies["inline"]

	assert.True(t, local.IsPath())
	assert.False(t, bar.IsPath())
	assert.Equal(t, "New", bar.SymbolValue())
	assert.Equal(t, "Make", inline.SymbolValue())
}

func TestDistributionErrors(t *testing.T) {
	t.Parallel()

	head := minimalDistribution

	cases := map[string]struct {
		src   string
		field string
	}{
		"bad name": {
			src: head + "[package]\nname = \"a/b\"\n", field: "package.name",
		},
		"tt and path": {
			src: head + "[core]\ntt = \"v3\"\npath = \"../tt\"\n", field: "core",
		},
		"bad core module": {
			src: head + "[core]\nmodule = \"/abs\"\n", field: "core.module",
		},
		"unknown integrity": {
			src: head + "[distribution]\nintegrity = \"gost\"\n", field: "distribution.integrity",
		},
		"bad edition": {
			src: head + "[distribution]\nedition = \"e e\"\n", field: "distribution.edition",
		},
		"unknown ssl": {
			src: head + "[build]\nssl = \"static\"\n", field: "build.ssl",
		},
		"bad tag": {
			src: head + "[build]\ntags = [\"a,b\"]\n", field: "build.tags",
		},
		"bad module name": {
			src: head + "[dependencies]\n\"1x\" = \"github.com/a/b\"\n", field: "dependencies.1x",
		},
		"builtin name": {
			src:   head + "[dependencies]\nbuiltin = \"github.com/a/b\"\n",
			field: "dependencies.builtin",
		},
		"unknown kind": {
			src:   head + "[dependencies.a]\nmodule = \"github.com/a/b\"\nkind = \"library\"\n",
			field: "dependencies.a.kind",
		},
		"unknown source": {
			src:   head + "[dependencies.a]\nmodule = \"github.com/a/b\"\nsource = \"git\"\n",
			field: "dependencies.a.source",
		},
		"no module": {
			src: head + "[dependencies.a]\nversion = \"v1.0.0\"\n", field: "dependencies.a.module",
		},
		"path with version": {
			src:   head + "[dependencies.a]\npath = \"./a\"\nversion = \"v1.0.0\"\n",
			field: "dependencies.a.version",
		},
		"registry with path": {
			src:   head + "[dependencies.a]\npath = \"./a\"\nsource = \"registry\"\n",
			field: "dependencies.a.path",
		},
		"foreign package": {
			src: head + "[dependencies.a]\nmodule = \"github.com/a/b\"\n" +
				"package = \"github.com/a/bc\"\n",
			field: "dependencies.a.package",
		},
		"unexported symbol": {
			src:   head + "[dependencies.a]\nmodule = \"github.com/a/b\"\nsymbol = \"new\"\n",
			field: "dependencies.a.symbol",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dist, _, err := manifest.ParseDistribution([]byte(testCase.src))
			require.NoError(t, err)

			err = dist.Validate()

			var verr *manifest.ValidationError

			require.ErrorAs(t, err, &verr)
			assert.Equal(t, testCase.field, verr.Field)
		})
	}
}

func TestDistributionBuiltinNameWithoutBuiltin(t *testing.T) {
	t.Parallel()

	parseDistribution(t, minimalDistribution+`[distribution]
builtin = false

[dependencies]
builtin = "github.com/acme/tt-builtin"
`)
}

func TestDistributionParseErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unknown field":   minimalDistribution + "unknown = 1\n",
		"bad short form":  minimalDistribution + "[dependencies]\na = \"github.com/a/b@\"\n",
		"inline non-text": minimalDistribution + "[dependencies]\na = { module = 1 }\n",
		"inline unknown":  minimalDistribution + "[dependencies]\na = { modul = \"x\" }\n",
		"no kind":         "manifest_version = \"0.1\"\n",
		"app kind":        "manifest_version = \"0.1\"\nmanifest_kind = \"app\"\n",
		"unknown kind":    "manifest_version = \"0.1\"\nmanifest_kind = \"plugin\"\n",
		"major":           "manifest_version = \"1.0\"\nmanifest_kind = \"tt-distribution\"\n",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := manifest.ParseDistribution([]byte(src))
			require.Error(t, err)
		})
	}
}

func TestDistributionNewerMinor(t *testing.T) {
	t.Parallel()

	_, warnings, err := manifest.ParseDistribution([]byte(`manifest_version = "0.9"
manifest_kind = "tt-distribution"
future = true
`))
	require.NoError(t, err)
	assert.Len(t, warnings, 1)
}

func TestDistributionKindErrorsNameTheCommand(t *testing.T) {
	t.Parallel()

	_, _, err := manifest.ParseDistribution([]byte("manifest_version = \"0.1\"\n"))
	require.ErrorContains(t, err, "tt package build")

	_, _, err = manifest.ParseManifest([]byte(minimalDistribution))
	require.ErrorContains(t, err, "tt binary build tt")

	_, _, err = manifest.ParseManifest([]byte(
		"manifest_version = \"0.1\"\nmanifest_kind = \"plugin\"\n"))

	var verr *manifest.ValidationError

	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "manifest_kind", verr.Field)
}

func TestAppManifestExplicitKind(t *testing.T) {
	t.Parallel()

	_, _, err := manifest.ParseManifest([]byte(`manifest_version = "0.1"
manifest_kind = "app"
[package]
name = "demo"
[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
`))
	require.NoError(t, err)
}

func TestDistributionLockRoundTrip(t *testing.T) {
	t.Parallel()

	lock := &manifest.DistributionLock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.1.0",
		ManifestHash:    "sha256:00",
		Core: manifest.LockedCore{
			Module:   "github.com/tarantool/tt/v3",
			Version:  "v3.0.0-20261028190206-9031d7ad96d9",
			Revision: "9031d7ad96d956744789412ac3ce7604c70196a4",
			Repository: map[string]string{
				"github.com/tarantool/tt/sdk":          "v0.0.0-20261028190206-9031d7ad96d9",
				"github.com/tarantool/tt/modules/aeon": "v0.0.0-20261028190206-9031d7ad96d9",
			},
		},
		Build: manifest.LockedBuild{
			Toolchain: "go1.27.3",
			SSL:       "default",
			Tags:      []string{"netgo", "osusergo"},
		},
		Modules: map[string]manifest.LockedModule{
			"aeon":       {Module: "github.com/tarantool/tt/modules/aeon"},
			"migrations": {Module: "example.com/migrations", Path: "./plugins/migrations"},
			"foo":        {Module: "github.com/acme/tt-foo", Version: "v1.2.0"},
		},
	}

	data, err := lock.Marshal()
	require.NoError(t, err)

	parsed, err := manifest.ParseDistributionLock(data)
	require.NoError(t, err)
	assert.Equal(t, lock, parsed)

	again, err := parsed.Marshal()
	require.NoError(t, err)
	assert.Equal(t, string(data), string(again))
}

func TestDistributionLockVersion(t *testing.T) {
	t.Parallel()

	_, err := manifest.ParseDistributionLock([]byte("manifest_version = \"0.1\"\n"))
	require.Error(t, err)

	_, err = manifest.ParseDistributionLock([]byte("lock_version = \"9.0\"\n"))
	require.ErrorIs(t, err, manifest.ErrUnsupportedVersion)
}
