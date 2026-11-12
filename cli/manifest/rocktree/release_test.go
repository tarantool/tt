package rocktree_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

func TestRockVersionAt(t *testing.T) {
	t.Parallel()

	got, err := rocktree.RockVersionAt("1.2.0", 1)
	require.NoError(t, err)
	assert.Equal(t, "1.2.0-1", got)

	got, err = rocktree.RockVersionAt("1.2.1-dev.3+gabc1234", 12)
	require.NoError(t, err)
	assert.Equal(t, "1.2.1.dev.3.gabc1234-12", got)

	_, err = rocktree.RockVersionAt("1.2.0", 0)
	require.Error(t, err)
}

// TestReleaseRockspec pins the rockspec a package is released with: the
// fields the package has, the dependencies sorted with their ranges, and a
// build that is done.
func TestReleaseRockspec(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name:       "mylib",
		Version:    "1.2.0",
		Revision:   2,
		Summary:    "A library",
		Homepage:   "https://github.com/example/mylib",
		License:    "BSD-2-Clause",
		Maintainer: "A, B",
		SourceURL:  "git+https://github.com/example/mylib",
		SourceTag:  "v1.2.0",
		Platforms:  []string{"linux", "macosx"},
		Dependencies: map[string]string{
			"luasocket": ">= 3.0.0, < 4.0.0",
			"checks":    "",
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "mylib", spec.Package)
	assert.Equal(t, "1.2.0-2", spec.Version)
	assert.Equal(t, `-- The rock of the tt package mylib 1.2.0, which tt packs from the
-- files it lays out: its build is done.
rockspec_format = "3.0"
package = "mylib"
version = "1.2.0-2"
source = {
   url = "git+https://github.com/example/mylib",
   tag = "v1.2.0",
}
description = {
   summary = "A library",
   homepage = "https://github.com/example/mylib",
   license = "BSD-2-Clause",
   maintainer = "A, B",
}
supported_platforms = { "linux", "macosx" }
dependencies = {
   "checks",
   "luasocket >= 3.0.0, < 4.0.0",
}
build = {
   type = "none",
}
deploy = {
   wrap_bin_scripts = false,
}
`, string(spec.RawSource))

	require.Len(t, spec.Dependencies, 2)
	assert.Equal(t, "checks", spec.Dependencies[0].Name)
	assert.Equal(t, "luasocket", spec.Dependencies[1].Name)
	assert.Len(t, spec.Dependencies[1].Constraints, 2)
}

// TestReleaseRockspecOfABarePackage: a package that says nothing of itself
// gets the source of the rock it is installed as and no optional fields.
func TestReleaseRockspecOfABarePackage(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name: "bare", Version: "0.0.0-dev.3+gabc1234", Revision: 1,
	})
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.dev.3.gabc1234-1", spec.Version)

	source := string(spec.RawSource)

	assert.Contains(t, source, "source = {\n   url = \"file://.\",\n}\n")
	assert.Contains(t, source, "description = {\n   summary = \"\",\n}\n")
	assert.NotContains(t, source, "supported_platforms")
	assert.NotContains(t, source, "dependencies")
	assert.NotContains(t, source, "tag =")
}

// TestIsReleaseRockspec: the rockspec tt packs is told from any other.
func TestIsReleaseRockspec(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name: "a", Version: "1.0.0", Revision: 1,
	})
	require.NoError(t, err)

	assert.True(t, rocktree.IsReleaseRockspec(spec.RawSource))
	assert.False(t, rocktree.IsReleaseRockspec([]byte("package = \"a\"\nversion = \"1.0.0-1\"\n")))
	assert.False(t, rocktree.IsReleaseRockspec(nil))
}
