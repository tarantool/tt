package running

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
)

type mockRepository struct{}

func (mock *mockRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (mock *mockRepository) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (mock *mockRepository) ValidateAll() error {
	return nil
}

// TestWatchdogArgs pins the arguments the watchdog of an instance is
// started with: tt instance start --watchdog, its target and what pins its
// application directory, and --foreground for the watchdog of
// tt instance start --foreground.
func TestWatchdogArgs(t *testing.T) {
	inst := InstanceCtx{
		AppName: "myapp", InstName: "storage-1", Scope: app.ScopeSystem,
		AppDir: "/opt/tarantool/versions/myapp/myapp_1.0.0",
	}

	assert.Equal(t, []string{
		"instance", "start", "--watchdog", "--scope", "system",
		"--app-dir", "/opt/tarantool/versions/myapp/myapp_1.0.0", "myapp:storage-1",
	}, watchdogArgs(&inst, false))

	inst.Template = "big"

	assert.Equal(t, []string{
		"instance", "start", "--watchdog", "--scope", "system",
		"--app-dir", "/opt/tarantool/versions/myapp/myapp_1.0.0", "--template", "big",
		"myapp:storage-1",
	}, watchdogArgs(&inst, false))

	// The watchdog of tt instance start --foreground.
	assert.Equal(t, []string{
		"instance", "start", "--watchdog", "--scope", "system",
		"--app-dir", "/opt/tarantool/versions/myapp/myapp_1.0.0", "--template", "big",
		"--foreground", "myapp:storage-1",
	}, watchdogArgs(&inst, true))
}

// TestAppFile pins the application file tt instance check checks: app.file of
// the config, resolved against the application directory.
func TestAppFile(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, appFiles("", "app:\n  file: src/app.lua\n"))
	inst := fixture.instanceCtx(t)

	file, found, err := AppFile(&inst)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, filepath.Join(fixture.dir, "src", "app.lua"), file)

	fixture = newAppFixture(t, app.ScopeProject, appFiles("", ""))
	inst = fixture.instanceCtx(t)

	_, found, err = AppFile(&inst)
	require.NoError(t, err)
	assert.False(t, found)
}

// TestCheck pins that tt instance check runs the syntax check with the
// tarantool of the application.
func TestCheck(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, appFiles("", "app:\n  file: app.lua\n"))
	inst := fixture.instanceCtx(t)

	good := filepath.Join(fixture.dir, "app.lua")
	require.NoError(t, os.WriteFile(good, []byte("return 1\n"), 0o644))
	require.NoError(t, Check(t.Context(), &inst, good))

	bad := filepath.Join(fixture.dir, "bad.lua")
	require.NoError(t, os.WriteFile(bad, []byte("local = \n"), 0o644))

	err := Check(t.Context(), &inst, bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syntax errors detected")
}
