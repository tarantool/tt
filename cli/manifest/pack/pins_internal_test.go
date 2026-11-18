package pack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// pinnedTt is the tt every request below pins.
const pinnedTt = "3.0.0"

// pinnedRequest asks for tarantool >=3.0.0 and tt >=3.0.0, the tarantool
// pinned at tarantool and tt at pinnedTt, with the project scope rooted at
// root and an empty cache.
func pinnedRequest(t *testing.T, root, tarantool string) RuntimeOptions {
	t.Helper()

	return RuntimeOptions{
		CacheDir:    filepath.Join(t.TempDir(), "empty"),
		ProjectRoot: root,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=3.0.0,<4.0.0"),
		},
		Pins: BundledVersions{Tarantool: tarantool, Tt: pinnedTt},
	}
}

// pinStage makes a staging directory as makeStageDir does: inside a
// directory of its own, which a mirror is unpacked next to it in.
func pinStage(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "stage-1")
	require.NoError(t, os.MkdirAll(dir, dirPerm))

	return dir
}

// TestBundleRuntimeTakesThePinnedInstallation: the pinned version is bundled
// over the active installation and over a higher one.
func TestBundleRuntimeTakesThePinnedInstallation(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramCe, "3.8.1")
	fakeInstallation(t, root, binary.ProgramCe, "3.9.0")
	fakeInstallation(t, root, binary.ProgramCe, "3.10.0")
	activate(t, root, binary.ProgramCe, "3.10.0")
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")
	fakeInstallation(t, root, binary.ProgramTt, "3.1.0")

	bundled, err := bundleRuntime(pinStage(t), pinnedRequest(t, root, "3.9.0"))
	require.NoError(t, err)
	assert.Equal(t, BundledVersions{Tarantool: "3.9.0", Tt: "3.0.0"}, bundled)
}

// TestBundleRuntimeOfAPinNothingHas fails, naming the pin and how to install
// it, though another version is at hand.
func TestBundleRuntimeOfAPinNothingHas(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramCe, "3.8.1")
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")

	bin := t.TempDir()
	writeTree(t, bin, map[string]string{"tarantool": "#!/bin/sh\n"})

	req := pinnedRequest(t, root, "3.9.0")

	req.ActiveTarantool = filepath.Join(bin, "tarantool")
	req.ActiveTarantoolVersion = "3.8.1-0-ge643f88a5e0c"
	req.ActiveTarantoolFlavor = flavorCE

	_, err := bundleRuntime(pinStage(t), req)
	require.ErrorIs(t, err, errNoRuntime)
	require.ErrorContains(t, err, "app.manifest.lock pins tarantool 3.9.0")
	require.ErrorContains(t, err, "the active tarantool (3.8.1-0-ge643f88a5e0c[ce])")
	require.ErrorContains(t, err, "tt binary install tarantool 3.9.0,")
}

// TestBundleRuntimeOfAPinTheActiveBinaryIs: the active tarantool built of the
// pinned release is bundled, recorded as the pin.
func TestBundleRuntimeOfAPinTheActiveBinaryIs(t *testing.T) {
	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")

	bin := t.TempDir()
	writeTree(t, bin, map[string]string{"tarantool": "#!/bin/sh\n"})

	req := pinnedRequest(t, root, "3.8.1")

	req.ActiveTarantool = filepath.Join(bin, "tarantool")
	req.ActiveTarantoolVersion = "3.8.1-0-ge643f88a5e0c"

	bundled, err := bundleRuntime(pinStage(t), req)
	require.NoError(t, err)
	assert.Equal(t, "3.8.1", bundled.Tarantool)
}

// TestBundleRuntimeOfAPinTheCacheHas takes the cache entry of exactly it.
func TestBundleRuntimeOfAPinTheCacheHas(t *testing.T) {
	req := pinnedRequest(t, "", "3.8.1")

	req.CacheDir = fakeCache(t, map[string][]string{
		"tarantool": {"3.8.1", "3.9.0"}, "tt": {"v3.0.0", "3.1.0"},
	})

	bundled, err := bundleRuntime(pinStage(t), req)
	require.NoError(t, err)
	assert.Equal(t, BundledVersions{Tarantool: "3.8.1", Tt: "3.0.0"}, bundled)
}

// TestBundleRuntimeOfAPinThatNoLongerFits fails: the lock is to be resolved
// again.
func TestBundleRuntimeOfAPinThatNoLongerFits(t *testing.T) {
	_, err := bundleRuntime(pinStage(t), pinnedRequest(t, "", "2.11.0"))
	require.ErrorIs(t, err, errNoRuntime)
	require.ErrorContains(t, err, "which does not satisfy")
	require.ErrorContains(t, err, "run tt package resolve")
}

// TestBundleRuntimeFromAMirror: a pinned tarantool the project does not have
// is unpacked from a mirror of programs, its headers and share/tarantool
// with it, and nothing of the unpacking is left.
func TestBundleRuntimeFromAMirror(t *testing.T) {
	installation := t.TempDir()
	writeTree(t, installation, map[string]string{
		"tarantool":                       "#!/bin/sh\n",
		"LICENSE":                         "BSD-2-Clause",
		"include/tarantool/module.h":      "/* module */",
		"share/tarantool/emmylua/box.lua": "---@meta",
	})

	dir := t.TempDir()

	_, err := mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramCe, Version: "3.9.0", Installation: installation,
		OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	require.NoError(t, err)

	found, ok, err := mirror.Open(dir)
	require.NoError(t, err)
	require.True(t, ok)

	root := t.TempDir()
	fakeInstallation(t, root, binary.ProgramTt, "3.0.0")

	stageDir := pinStage(t)

	req := pinnedRequest(t, root, "3.9.0")

	req.Mirrors = []*mirror.Source{found}

	bundled, err := bundleRuntime(stageDir, req)
	require.NoError(t, err)
	assert.Equal(t, "3.9.0", bundled.Tarantool)

	tarantool := filepath.Join(stageDir, runtimeDirName, runtimeTarantool)
	assert.FileExists(t, filepath.Join(tarantool, "tarantool"))
	assert.FileExists(t, filepath.Join(tarantool, "include", "tarantool", "module.h"))
	assert.FileExists(t, filepath.Join(tarantool, "share", "tarantool", "emmylua", "box.lua"))
	assert.FileExists(t, filepath.Join(tarantool, "LICENSE"))

	info, err := os.Stat(filepath.Join(tarantool, "tarantool"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the executable stays one")

	entries, err := os.ReadDir(filepath.Dir(stageDir))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the stage is left")
}
