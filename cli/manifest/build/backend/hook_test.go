package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

func TestRunHook_shellReducedContract(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	out := filepath.Join(cwd, "out.txt")

	hook := manifest.Build{
		Backend: BackendShell,
		Command: "sh",
		Args: []string{"-c",
			`printf '%s|%s|%s|%s|%s' ` +
				`"$TT_PACKAGE" "$TT_VERSION" "$TT_PROJECT_ROOT" ` +
				`"${TT_OUTPUT_DIR-unset}" "${TT_COMPONENT_NAME-unset}" > out.txt`},
	}
	env := Env{
		ProjectRoot: "/proj",
		Package:     "my-app",
		Version:     "1.2.3",
		Extra:       map[string]string{"FOO": "bar"},
	}

	require.NoError(t, RunHook(context.Background(), hook, cwd, env, false))

	data, err := os.ReadFile(out) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	// TT_PACKAGE / TT_VERSION / TT_PROJECT_ROOT are set; the per-component
	// TT_OUTPUT_DIR and TT_COMPONENT_NAME are not exported at all.
	assert.Equal(t, "my-app|1.2.3|/proj|unset|unset", string(data))
}

func TestRunHook_shellExtraEnvExported(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()

	hook := manifest.Build{
		Backend: BackendShell,
		Command: "sh",
		Args:    []string{"-c", `printf '%s' "$FOO" > out.txt`},
		Env:     map[string]string{"FOO": "bar"},
	}
	env := Env{ProjectRoot: cwd, Package: "p", Version: "1", Extra: map[string]string{"FOO": "bar"}}

	require.NoError(t, RunHook(context.Background(), hook, cwd, env, false))

	data, err := os.ReadFile(filepath.Join(cwd, "out.txt")) //nolint:gosec // temp path
	require.NoError(t, err)
	assert.Equal(t, "bar", string(data))
}

func TestRunHook_nonZeroExitIsError(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	hook := manifest.Build{Backend: BackendShell, Command: "sh", Args: []string{"-c", "exit 3"}}

	err := RunHook(context.Background(), hook, cwd, Env{ProjectRoot: cwd}, false)
	assert.Error(t, err)
}

func TestRunHook_rejectsNonHookBackend(t *testing.T) {
	t.Parallel()

	err := RunHook(context.Background(),
		manifest.Build{Backend: BackendC}, t.TempDir(), Env{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not runnable")
}

func TestRunHook_requiresAbsCwd(t *testing.T) {
	t.Parallel()

	err := RunHook(context.Background(),
		manifest.Build{Backend: BackendShell, Command: "true"}, "rel/dir", Env{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}
