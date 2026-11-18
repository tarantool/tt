package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/runtimepin"
	"github.com/tarantool/tt/v3/internal/binary"
)

// runtimeOptions are the options of tt package resolve over a project root
// with nothing installed, run by tarantool 3.8.1 and tt 3.1.0.
func runtimeOptions(dir string) Options {
	opts := optsFor(dir)

	opts.Runtime = &runtimepin.Options{
		ProjectRoot: filepath.Join(dir, ".tarantool"),
		Tarantool:   runtimepin.Active{Version: "3.8.1-0-ge643f88a5e0c", Flavor: "ce"},
		Tt:          runtimepin.Active{Version: "3.1.0", Flavor: ""},
		Releases:    nil,
	}

	return opts
}

// TestResolve_pinsTheRuntime: tt package resolve writes the versions of the
// tarantool and the tt the project runs on into the lock, and a resolve
// after it finds the lock up to date.
func TestResolve_pinsTheRuntime(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)

	result, err := resolveWith(context.Background(), runtimeOptions(dir), &fakeResolver{})
	require.NoError(t, err)

	assert.Equal(t, "3.8.1", result.Lock.Tarantool)
	assert.Equal(t, "3.1.0", result.Lock.Tt)
	assert.Empty(t, result.Lock.Tcm)

	written := lockOnDisk(t, dir)
	assert.Equal(t, "3.8.1", written.Tarantool)
	assert.Equal(t, "3.1.0", written.Tt)

	again, err := resolveWith(context.Background(), runtimeOptions(dir), &fakeResolver{})
	require.NoError(t, err)
	assert.Empty(t, again.Rewrites)
}

// TestResolve_holdsTheRuntimePins: a pin that still satisfies [platform] is
// kept, whatever else there is: the installed 3.9.0 does not move it.
func TestResolve_holdsTheRuntimePins(t *testing.T) {
	t.Parallel()

	lock := lockOf(nil)

	lock.Tarantool = "3.7.1"

	dir := writeProject(t, baseManifest, lock)
	installRuntime(t, dir, binary.ProgramCe, "3.9.0")

	result, err := resolveWith(context.Background(), runtimeOptions(dir), &fakeResolver{})
	require.NoError(t, err)

	assert.Equal(t, "3.7.1", result.Lock.Tarantool)
	assert.Contains(t, result.Rewrites, `tt_version is "3.1.0", was ""`)
}

// TestUpdate_pinsTheRuntimeAfresh: a bare tt package update moves the
// runtime pins too, to the installation of the project.
func TestUpdate_pinsTheRuntimeAfresh(t *testing.T) {
	t.Parallel()

	lock := lockOf(nil)

	lock.Tarantool = "3.7.1"

	dir := writeProject(t, baseManifest, lock)
	installRuntime(t, dir, binary.ProgramCe, "3.9.0")

	result, err := updateWith(context.Background(), runtimeOptions(dir), &fakeResolver{}, "")
	require.NoError(t, err)

	assert.Equal(t, "3.9.0", result.Lock.Tarantool)
	assert.Contains(t, result.Rewrites, `tarantool_version is "3.9.0", was "3.7.1"`)
}

// TestAdd_holdsTheRuntimePins: a command that pins no runtime keeps the
// pins that still satisfy [platform] and drops the others with a warning.
func TestAdd_holdsTheRuntimePins(t *testing.T) {
	t.Parallel()

	lock := lockOf(nil)

	lock.Tarantool = "2.11.0"
	lock.Tt = "3.1.0"

	dir := writeProject(t, baseManifest, lock)

	var warnings []string

	opts := optsFor(dir)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := addWith(context.Background(), opts, &fakeResolver{}, "metrics", "", false)
	require.NoError(t, err)

	assert.Empty(t, result.Lock.Tarantool)
	assert.Equal(t, "3.1.0", result.Lock.Tt)
	assert.Contains(t, warnings, "app.manifest.lock pinned tarantool 2.11.0, which does not "+
		`satisfy ">=3.0.0" of [platform].tarantool; the pin is dropped, tt package resolve `+
		"pins another one")
}

// installRuntime lays an installation of program into the project scope of
// the project in dir, as tt binary install does.
func installRuntime(t *testing.T, dir string, program binary.Program, id string) {
	t.Helper()

	installation := binary.InstallationPath(filepath.Join(dir, ".tarantool"), program, id)
	require.NoError(t, os.MkdirAll(installation, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(installation, program.Exec()), nil, 0o755))
}
