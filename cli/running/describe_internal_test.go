package running

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/tarantool"
)

// TestDescribeTarantool pins how tt instance status --details names the
// tarantool an instance starts on: its path, and where it comes from by the
// rule of the resolver, for every origin; and the error of an instance
// without one.
func TestDescribeTarantool(t *testing.T) {
	t.Setenv(tarantool.UseSystemEnv, "")

	describe := func(t *testing.T, fixture appFixture) (string, []string, error) {
		t.Helper()

		inst := fixture.instanceCtx(t)

		return DescribeTarantool(&inst)
	}

	for _, scp := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
		t.Run("the lock in the "+scp.String()+" scope", func(t *testing.T) {
			fixture := newAppFixture(t, scp, appFiles("", ""))
			path := fixture.installTarantool(t, fixtureTarantool)

			got, warnings, err := describe(t, fixture)
			require.NoError(t, err)
			assert.Equal(t, path+", version 3.8.1 app.manifest.lock bundles, installed in the "+
				scp.String()+" scope", got)
			assert.Empty(t, warnings)
		})
	}

	t.Run("the constraint", func(t *testing.T) {
		files := appFiles("", "")
		lock := strings.Replace(fixtureLock, "bundled_tarantool_version = \"3.8.1\"\n", "", 1)

		files["app.manifest.lock"] = lock

		fixture := newAppFixture(t, app.ScopeUser, files)

		fixture.installTarantool(t, "3.7.0")

		path := fixture.installTarantool(t, "3.9.0")

		got, _, err := describe(t, fixture)
		require.NoError(t, err)
		assert.Equal(t, path+", version 3.9.0, the highest installed in the user scope "+
			"that [platform].tarantool allows", got)
	})

	t.Run("the project", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeProject, appFiles("", ""))
		path := fixture.installTarantool(t, "3.7.0")

		got, warnings, err := describe(t, fixture)
		require.NoError(t, err)
		assert.Equal(t, path+", version 3.7.0 the project selects in .tarantool/bin", got)
		require.Len(t, warnings, 1, "the lock bundles another version")
		assert.Contains(t, warnings[0], "3.8.1")
	})

	t.Run("PATH", func(t *testing.T) {
		bin := t.TempDir()
		fakeTarantool(t, filepath.Join(bin, "tarantool"))
		t.Setenv("PATH", bin)

		fixture := newAppFixture(t, app.ScopeProject, appFiles("", ""))

		got, warnings, err := describe(t, fixture)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(bin, "tarantool")+", the first tarantool on PATH", got)
		assert.Empty(t, warnings)
	})

	t.Run("none", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeSystem, appFiles("", ""))

		_, _, err := describe(t, fixture)
		require.ErrorIs(t, err, tarantool.ErrNotInstalled)
		require.ErrorContains(t, err, "tt binary install")
	})
}
