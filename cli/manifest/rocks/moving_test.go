package rocks_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/deps"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// TestMovingVersionOfAPackage checks the versions tt derives for a package
// (version.Derive), as the rocks of the package have them
// (rocktree.RockVersion): one of a commit names fixed code though LuaRocks
// reads its dev as a branch, and one of a tree with uncommitted changes does
// not, whatever it is after.
func TestMovingVersionOfAPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		semver      string
		moving      bool
		uncommitted bool
	}{
		{semver: "1.2.0"},
		{semver: "v1.2.0"},
		{semver: "2.0.0-rc.1"},
		{semver: "1.2.1-dev.3+gabc1234"},
		{semver: "2.0.0-rc.1.dev.3+gabc1234"},
		{semver: "0.0.0-dev.36+g7170ebf"},
		{semver: "0.0.0-dev.36+g7170ebf0123456789abcdef0123456789abcdef"},
		// A pre-release tag of its own may say scm.
		{semver: "1.0.0-scm.dev.2+g1234567"},
		{semver: "1.2.0+dirty", moving: true, uncommitted: true},
		{semver: "2.0.0-rc.1+dirty", moving: true, uncommitted: true},
		{semver: "1.2.1-dev.3+gabc1234.dirty", moving: true, uncommitted: true},
		{semver: "0.0.0-dev.36+g7170ebf.dirty", moving: true, uncommitted: true},
		// Out of git: no commit to name.
		{semver: "0.0.0-dev.0", moving: true},
		{semver: "0.0.0-dev.0+dirty", moving: true, uncommitted: true},
	}

	for _, test := range tests {
		t.Run(test.semver, func(t *testing.T) {
			t.Parallel()

			raw, err := rocktree.RockVersion(test.semver)
			require.NoError(t, err)

			version, err := deps.ParseVersion(raw)
			require.NoError(t, err)

			assert.Equal(t, test.moving, rocks.Moving(version), raw)
			assert.Equal(t, test.uncommitted, rocks.Uncommitted(version), raw)
		})
	}
}

// TestMovingVersionOfARock checks the versions of rocks and constraints that
// are no version tt makes: a branch stays moving, whatever dev or scm and
// whatever after them.
func TestMovingVersionOfARock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw    string
		moving bool
	}{
		{raw: "scm-1", moving: true},
		{raw: "dev-1", moving: true},
		{raw: "scm", moving: true},
		{raw: "dev", moving: true},
		{raw: "1.0.dev-1", moving: true},
		{raw: "2.0.scm-3", moving: true},
		{raw: "dev.3.gabc1234-1", moving: true},
		{raw: "1.0.0.dev.3-1", moving: true},
		{raw: "1.0.0.dev.3.gxyz1234-1", moving: true},
		{raw: "1.0.0.dev.3.gabc-1", moving: true},
		{raw: "1.0.0.dev.3.gabc1234.extra-1", moving: true},
		{raw: "1.0.0-1"},
		{raw: "1.0.0"},
		{raw: "0.30.0pre1-1"},
		// A constraint has no revision, and a rebuilt rock another one.
		{raw: "0.0.0.dev.36.g7170ebf"},
		{raw: "0.0.0.dev.36.g7170ebf-2"},
		{raw: "0.0.0.dev.36.g7170ebf.dirty", moving: true},
	}

	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()

			version, err := deps.ParseVersion(test.raw)
			require.NoError(t, err)

			assert.Equal(t, test.moving, rocks.Moving(version))
		})
	}
}
