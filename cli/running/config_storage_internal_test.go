package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
)

// bannerTarantool puts first in PATH a tarantool that prints banner for
// --version, so that a project without a tarantool of its own runs on it.
func bannerTarantool(t *testing.T, banner string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "tarantool")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho '"+banner+"'\n"), 0o755))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return path
}

// TestConfigStorageNeedsEnterprise pins that a Tarantool instance of the
// cluster config whose config points to a config storage does not start, and
// fails tt instance check, on Tarantool Community Edition, which takes
// neither config.etcd nor config.storage; Enterprise Edition, a config of
// the file alone and a tarantool whose flavor cannot be told let it through.
func TestConfigStorageNeedsEnterprise(t *testing.T) {
	const (
		community  = "Tarantool 3.8.1-0-ge643f88a5e0c"
		enterprise = "Tarantool Enterprise 3.8.1-0-g1a2b3c4-r1"
	)

	// A reference without endpoints keeps tt from connecting while it loads
	// the config, and tarantool refuses it all the same.
	cases := []struct {
		name    string
		global  string
		banner  string
		refused bool
	}{
		{name: "config.etcd on CE", global: "config:\n  etcd:\n    prefix: /myapp\n",
			banner: community, refused: true},
		{name: "config.storage on CE", global: "config:\n  storage:\n    prefix: /myapp\n",
			banner: community, refused: true},
		{name: "config.etcd on EE", global: "config:\n  etcd:\n    prefix: /myapp\n",
			banner: enterprise},
		{name: "a config of the file on CE", global: "", banner: community},
		{name: "a flavor that cannot be told", global: "config:\n  etcd:\n    prefix: /x\n",
			banner: "weird"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := bannerTarantool(t, test.banner)
			fixture := newAppFixture(t, app.ScopeProject, appFiles("", test.global))
			inst := fixture.instanceCtx(t)

			canStart, startErr := IsAbleToStartInstances([]InstanceCtx{inst})
			checkErr := CheckTarantool(t.Context(), &inst)

			if !test.refused {
				require.NoError(t, startErr)
				assert.True(t, canStart)
				require.NoError(t, checkErr)

				return
			}

			assert.False(t, canStart)

			for _, err := range []error{startErr, checkErr} {
				require.ErrorIs(t, err, ErrConfigStorageCE)
				require.ErrorContains(t, err, `instance "storage-1" of "myapp": `)
				require.ErrorContains(t, err, "and the instance runs on "+path+
					", Tarantool Community Edition")
				require.ErrorContains(t, err, filepath.Join(fixture.dir, ".templates",
					"default", "config.yaml"))
			}
		})
	}

	t.Run("an instance that runs its script", func(t *testing.T) {
		bannerTarantool(t, community)

		files := appFiles("{script: init.lua}", "config:\n  etcd:\n    prefix: /myapp\n")

		files["init.lua"] = ""

		fixture := newAppFixture(t, app.ScopeProject, files)
		inst := fixture.instanceCtx(t)

		require.NoError(t, CheckTarantool(t.Context(), &inst))

		_, err := IsAbleToStartInstances([]InstanceCtx{inst})
		require.NotErrorIs(t, err, ErrConfigStorageCE)
	})
}
