package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
)

// installedInstance returns the context of storage-1 of app, a Tarantool
// instance configured by the cluster config, run from a version installed
// into the user root of the test, with the integrity checks on.
func installedInstance(t *testing.T) *InstanceCtx {
	t.Helper()

	root := t.TempDir()
	version := filepath.Join(root, "versions", "app", "app_1.0.0")
	require.NoError(t, os.MkdirAll(version, 0o755))

	return &InstanceCtx{
		AppName:   "app",
		InstName:  "storage-1",
		AppDir:    version,
		Root:      root,
		Scope:     app.ScopeUser,
		Kind:      instances.KindCluster,
		Integrity: &integrity.IntegrityCtx{},
	}
}

// The links of the version are made before the start, and checked at every
// start after it.
func TestLinkIntegrityLayout(t *testing.T) {
	inst := installedInstance(t)

	require.NoError(t, checkIntegrityStart(inst), "a missing link is made, not refused")
	require.NoError(t, linkIntegrityLayout(inst))

	missing, err := scope.CheckVarLinks(inst.Root, "app", inst.AppDir)
	require.NoError(t, err)
	assert.False(t, missing)

	require.NoError(t, checkIntegrityStart(inst))
	require.NoError(t, linkIntegrityLayout(inst))

	// A link that leads elsewhere is refused before anything starts.
	link := filepath.Join(inst.AppDir, "var", "run")
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(t.TempDir(), link))

	err = checkIntegrityStart(inst)
	require.ErrorIs(t, err, scope.ErrVarLink)
	require.ErrorContains(t, err, `instance "storage-1" of "app"`)
	require.ErrorIs(t, linkIntegrityLayout(inst), scope.ErrVarLink)
}

// A version that cannot be written into gets no links, and the error says
// how to get them.
func TestLinkIntegrityLayoutReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}

	inst := installedInstance(t)
	require.NoError(t, os.Chmod(inst.AppDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(inst.AppDir, 0o755) })

	err := linkIntegrityLayout(inst)
	require.ErrorContains(t, err, "tt package install --force --integrity-check")
}

// Outside of the scenario nothing is linked: without the checks, for a
// project, for an external process.
func TestLinkIntegrityLayoutOutside(t *testing.T) {
	cases := map[string]func(inst *InstanceCtx){
		"no checks":        func(inst *InstanceCtx) { inst.Integrity = nil },
		"project":          func(inst *InstanceCtx) { inst.Scope = app.ScopeProject },
		"external process": func(inst *InstanceCtx) { inst.Kind = instances.KindExternal },
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			inst := installedInstance(t)
			change(inst)

			require.NoError(t, linkIntegrityLayout(inst))
			assert.NoDirExists(t, filepath.Join(inst.AppDir, "var"))
		})
	}
}

// With the checks on, the env of the record of a Tarantool instance and a
// script tt launches are refused: Tarantool Enterprise starts with an empty
// environment, which takes both away. An external process keeps its env.
func TestCheckIntegrityStartRefusals(t *testing.T) {
	inst := installedInstance(t)

	inst.Env = map[string]string{"TT_IPROTO_LISTEN": "3301", "A": "1"}

	err := checkIntegrityStart(inst)
	require.ErrorIs(t, err, errIntegrityEnv)
	require.ErrorContains(t, err, "(A, TT_IPROTO_LISTEN)")

	inst = installedInstance(t)
	inst.Kind = instances.KindScript

	require.ErrorIs(t, checkIntegrityStart(inst), errIntegrityScript)

	inst = installedInstance(t)
	inst.Kind = instances.KindExternal
	inst.Env = map[string]string{"GW_PORT": "8080"}

	require.NoError(t, checkIntegrityStart(inst))

	inst = installedInstance(t)
	inst.Integrity = nil
	inst.Env = map[string]string{"A": "1"}
	inst.Kind = instances.KindScript

	require.NoError(t, checkIntegrityStart(inst), "without the checks nothing changes")
}
