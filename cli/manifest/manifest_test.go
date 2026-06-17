package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// TestRoundTripMyApp parses the canonical my-app example, serializes it back
// and checks the bytes are identical.
func TestRoundTripMyApp(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "my-app.toml"))
	require.NoError(t, err)

	mfst, warnings, err := manifest.ParseManifest(data)
	require.NoError(t, err)
	require.Empty(t, warnings)

	_, err = mfst.Validate()
	require.NoError(t, err)

	out, err := mfst.Marshal()
	require.NoError(t, err)
	require.Equal(t, string(data), string(out), "round-trip must be byte-identical")
}

// TestDependencyForms checks the short (bare string) and long (table) forms
// decode side by side in one map.
func TestDependencyForms(t *testing.T) {
	t.Parallel()

	src := `
manifest_version = "0.1"
[package]
name = "demo"
[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[dependencies]
luasocket = ">=3.0.0,<4.0.0"
inline    = { source = "registry", version = ">=1.0.0" }

[dependencies.local-helper]
source = "path"
path   = "../helper"
`
	mfst, _, err := manifest.ParseManifest([]byte(src))
	require.NoError(t, err)

	// Short form: bare constraint, source defaults to registry.
	assert.Equal(t, manifest.Dependency{
		Source: "registry", Version: ">=3.0.0,<4.0.0", Path: "", Registry: "", Kind: "",
	}, mfst.Dependencies["luasocket"])
	// Inline table.
	assert.Equal(t, manifest.Dependency{
		Source: "registry", Version: ">=1.0.0", Path: "", Registry: "", Kind: "",
	}, mfst.Dependencies["inline"])
	// Long form: path source.
	assert.Equal(t, manifest.Dependency{
		Source: "path", Version: "", Path: "../helper", Registry: "", Kind: "",
	}, mfst.Dependencies["local-helper"])

	_, err = mfst.Validate()
	require.NoError(t, err)
}

// TestPlatformFlavors covers the flavor suffix variants and the tcm rule.
func TestPlatformFlavors(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, platform string) (*manifest.Manifest, error) {
		t.Helper()

		src := "manifest_version = \"0.1\"\n[package]\nname = \"demo\"\n[platform]\n" + platform
		mfst, _, err := manifest.ParseManifest([]byte(src))

		return mfst, err
	}

	t.Run("ce explicit", func(t *testing.T) {
		t.Parallel()

		mfst, err := parse(t, "tarantool = \">=3.0.0[ce]\"\ntt = \">=3.0.0\"\n")
		require.NoError(t, err)
		assert.Equal(t, "ce", mfst.Platform.Tarantool.Flavor)
		assert.Equal(t, ">=3.0.0", mfst.Platform.Tarantool.Version)
	})

	t.Run("ee explicit", func(t *testing.T) {
		t.Parallel()

		mfst, err := parse(t, "tarantool = \">=3.0.0[ee]\"\ntt = \">=3.0.0\"\n")
		require.NoError(t, err)
		assert.Equal(t, "ee", mfst.Platform.Tarantool.Flavor)
	})

	t.Run("no brackets defaults to ce", func(t *testing.T) {
		t.Parallel()

		mfst, err := parse(t, "tarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n")
		require.NoError(t, err)
		assert.Empty(t, mfst.Platform.Tarantool.Flavor)
		assert.Equal(t, "ce", mfst.Platform.Tarantool.EffectiveFlavor())
	})

	t.Run("unknown flavor is a parse error", func(t *testing.T) {
		t.Parallel()

		_, err := parse(t, "tarantool = \">=3.0.0[xx]\"\ntt = \">=3.0.0\"\n")
		require.Error(t, err)
	})

	t.Run("flavor without version is a parse error", func(t *testing.T) {
		t.Parallel()

		_, err := parse(t, "tarantool = \"[ce]\"\ntt = \">=3.0.0\"\n")
		require.Error(t, err)
	})

	t.Run("tcm with flavor is a validation error", func(t *testing.T) {
		t.Parallel()

		mfst, err := parse(t, "tarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\ntcm = \">=1.5.0[ee]\"\n")
		require.NoError(t, err) // Parses; the flavor is generic at this layer.

		_, err = mfst.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tcm")
	})

	t.Run("tcm without flavor is fine", func(t *testing.T) {
		t.Parallel()

		platform := "tarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\ntcm = \">=1.5.0,<2.0.0\"\n"
		mfst, err := parse(t, platform)
		require.NoError(t, err)

		_, err = mfst.Validate()
		require.NoError(t, err)
	})
}

// TestNamespaceTristate covers the three states of component.namespace.
func TestNamespaceTristate(t *testing.T) {
	t.Parallel()

	src := `
manifest_version = "0.1"
[package]
name = "pkg"
[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[components.absent]
path = "a/"

[components.flat]
path = "b/"
namespace = ""

[components.named]
path = "c/"
namespace = "custom"
`
	mfst, _, err := manifest.ParseManifest([]byte(src))
	require.NoError(t, err)

	// Field absent: nil pointer, falls back to package name.
	assert.Nil(t, mfst.Components["absent"].Namespace)
	assert.Equal(t, "pkg", mfst.Components["absent"].EffectiveNamespace("pkg"))

	// Empty string: flat layout.
	require.NotNil(t, mfst.Components["flat"].Namespace)
	assert.Empty(t, *mfst.Components["flat"].Namespace)
	assert.Empty(t, mfst.Components["flat"].EffectiveNamespace("pkg"))

	// Explicit string.
	require.NotNil(t, mfst.Components["named"].Namespace)
	assert.Equal(t, "custom", mfst.Components["named"].EffectiveNamespace("pkg"))
}

const versionedBody = `
[package]
name = "demo"
[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
`

// TestFormatVersions tables the unknown-field and unknown-enum rules.
func TestFormatVersions(t *testing.T) {
	t.Parallel()

	t.Run("same minor, unknown field fails", func(t *testing.T) {
		t.Parallel()

		src := "manifest_version = \"0.1\"\nfuture_field = \"x\"\n" + versionedBody
		_, _, err := manifest.ParseManifest([]byte(src))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "future_field")
	})

	t.Run("newer minor, unknown field warns and continues", func(t *testing.T) {
		t.Parallel()

		src := "manifest_version = \"0.2\"\nfuture_field = \"x\"\n" + versionedBody
		mfst, warnings, err := manifest.ParseManifest([]byte(src))
		require.NoError(t, err)
		require.NotEmpty(t, warnings)
		assert.Contains(t, warnings[0], "future_field")
		assert.Equal(t, "demo", mfst.Package.Name)
	})

	t.Run("newer major is refused", func(t *testing.T) {
		t.Parallel()

		src := "manifest_version = \"1.0\"\n" + versionedBody
		_, _, err := manifest.ParseManifest([]byte(src))
		require.ErrorIs(t, err, manifest.ErrUnsupportedVersion)
	})

	t.Run("malformed version is rejected", func(t *testing.T) {
		t.Parallel()

		src := "manifest_version = \"0.1.0\"\n" + versionedBody
		_, _, err := manifest.ParseManifest([]byte(src))
		require.Error(t, err)
	})

	t.Run("unknown enum value fails on any version", func(t *testing.T) {
		t.Parallel()

		for _, ver := range []string{"0.1", "0.2"} {
			src := "manifest_version = \"" + ver + "\"\n" + versionedBody +
				"\n[dependencies.x]\nsource = \"ftp\"\n"
			mfst, _, err := manifest.ParseManifest([]byte(src))
			require.NoError(t, err, "ver %s parses", ver)

			_, err = mfst.Validate()
			require.Error(t, err, "ver %s: ftp source must fail validation", ver)
			assert.Contains(t, err.Error(), "ftp")
		}
	})
}

// TestValidateNegatives covers the structural validation failures.
func TestValidateNegatives(t *testing.T) {
	t.Parallel()

	base := func(t *testing.T, body string) *manifest.Manifest {
		t.Helper()

		src := "manifest_version = \"0.1\"\n[package]\nname = \"demo\"\n" +
			"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n" + body
		mfst, _, err := manifest.ParseManifest([]byte(src))
		require.NoError(t, err)

		return mfst
	}

	t.Run("two default products", func(t *testing.T) {
		t.Parallel()

		mfst := base(t, `
[components.a]
path = "a/"
[products.one]
components = ["a"]
default = true
[products.two]
components = ["a"]
default = true
`)
		_, err := mfst.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default")
	})

	t.Run("product references unknown component", func(t *testing.T) {
		t.Parallel()

		mfst := base(t, `
[components.a]
path = "a/"
[products.p]
components = ["a", "ghost"]
`)
		_, err := mfst.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ghost")
	})

	t.Run("product name with ::", func(t *testing.T) {
		t.Parallel()

		mfst := base(t, `
[components.a]
path = "a/"
[products."ns::p"]
components = ["a"]
`)
		_, err := mfst.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "::")
	})

	t.Run("reserved package name", func(t *testing.T) {
		t.Parallel()

		src := "manifest_version = \"0.1\"\n[package]\nname = \"bin\"\n" +
			"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n"
		mfst, _, err := manifest.ParseManifest([]byte(src))
		require.NoError(t, err)

		_, err = mfst.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reserved")
	})

	t.Run("single product needs no default", func(t *testing.T) {
		t.Parallel()

		mfst := base(t, `
[components.a]
path = "a/"
[products.only]
components = ["a"]
`)
		_, err := mfst.Validate()
		require.NoError(t, err)
	})
}

// TestManifestHashStable checks the hash is stable byte-for-byte and changes on
// any edit, including a comment.
func TestManifestHashStable(t *testing.T) {
	t.Parallel()

	src := "manifest_version = \"0.1\"\n[package]\nname = \"demo\"\n" +
		"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n"

	first, _, err := manifest.ParseManifest([]byte(src))
	require.NoError(t, err)

	second, _, err := manifest.ParseManifest([]byte(src))
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(first.Hash(), "sha256:"))
	assert.Equal(t, first.Hash(), second.Hash(), "identical bytes => identical hash")

	// A comment-only edit changes the raw bytes and therefore the hash.
	withComment := "# a comment\n" + src
	edited, _, err := manifest.ParseManifest([]byte(withComment))
	require.NoError(t, err)
	assert.NotEqual(t, first.Hash(), edited.Hash(), "any edit changes the hash")
}
