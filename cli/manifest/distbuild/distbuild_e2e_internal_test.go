//go:build integration

package distbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helloModule is a tt module that mounts tt hello.
const helloModule = `// Package hello is a tt module that greets.
package hello

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

// New mounts tt hello.
func New(s sdk.Services) []sdk.Mount {
	return []sdk.Mount{{Cmd: &cobra.Command{
		Use:   "hello",
		Short: "Greet",
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintln(s.Streams().IO().Out, "hello from a module")

			return err
		},
	}}}
}
`

// helloGoMod requires the SDK at a commit of the core's repository, as a
// module developed outside it does; the build pins the SDK to the core.
const helloGoMod = `module example.com/hello

go 1.26.10

require (
	github.com/spf13/cobra v1.10.2
	github.com/tarantool/tt/sdk v0.0.0-20261028190206-9031d7ad96d9
	github.com/tarantool/tt/sdk/connect v0.0.0-20261028190206-9031d7ad96d9
)
`

// TestBuildLocalCore builds a distribution of the core of this checkout with
// a local module and a module of the core's repository, runs it, and builds
// it again from the lock.
func TestBuildLocalCore(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "hello", "go.mod"), helloGoMod)
	writeFile(t, filepath.Join(dir, "plugins", "hello", "hello.go"), helloModule)

	manifestPath := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-hello"

[core]
path = "`+coreDir+`"

[distribution]
title = "Tarantool CLI Hello"
edition = "hello"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
hello = "./plugins/hello"
`)

	result, err := Run(t.Context(), Options{Manifest: manifestPath, GeneratedBy: "tt test"})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "tt-hello"), result.Output)

	out, err := exec.CommandContext(t.Context(), result.Output, "hello").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "hello from a module\n", string(out))

	out, err = exec.CommandContext(t.Context(), result.Output, "version", "--short").
		CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "+hello")

	out, err = exec.CommandContext(t.Context(), result.Output, "aeon", "--help").
		CombinedOutput()
	require.NoError(t, err, string(out))

	sum, err := os.ReadFile(filepath.Join(dir, "tt.manifest.sum"))
	require.NoError(t, err)

	lock, err := os.ReadFile(filepath.Join(dir, "tt.manifest.lock"))
	require.NoError(t, err)
	assert.Contains(t, string(lock), `path = './plugins/hello'`)

	// The lock rebuilds the same modules and writes nothing.
	output := filepath.Join(dir, "out", "tt")

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.NoError(t, err)
	assert.FileExists(t, output)

	again, err := os.ReadFile(filepath.Join(dir, "tt.manifest.sum"))
	require.NoError(t, err)
	assert.Equal(t, string(sum), string(again))

	// A changed manifest makes the lock stale.
	f, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)

	_, err = f.WriteString("# changed\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = Run(t.Context(), Options{Manifest: manifestPath, Locked: true})
	require.ErrorIs(t, err, errStaleLock)
}

// TestBuildMountConflict fails a distribution whose module takes the name
// of a command of tt: the binary does not start.
func TestBuildMountConflict(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello", "go.mod"), helloGoMod)
	writeFile(t, filepath.Join(dir, "hello", "hello.go"),
		strings.Replace(helloModule, `Use:   "hello"`, `Use:   "binary"`, 1))

	manifestPath := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
path = "`+coreDir+`"

[dependencies]
hello = "./hello"
`)

	_, err = Run(t.Context(), Options{Manifest: manifestPath})
	require.ErrorContains(t, err, `duplicate command "binary"`)
	assert.NoFileExists(t, filepath.Join(dir, "tt"))
	assert.NoFileExists(t, filepath.Join(dir, "tt.manifest.lock"))
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

// TestBundleOffline makes a bundle of the core of this checkout and a local
// module, copies it away from everything it was made of and builds it with
// an empty module cache and no network, as build.sh does.
func TestBundleOffline(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "hello", "go.mod"), helloGoMod)
	writeFile(t, filepath.Join(dir, "plugins", "hello", "hello.go"),
		strings.Replace(helloModule, `"hello from a module"`, "greeting", 1))
	writeFile(t, filepath.Join(dir, "plugins", "hello", "greeting.go"),
		"package hello\n\nimport _ \"embed\"\n\n//go:embed greeting.txt\nvar greeting string\n")
	writeFile(t, filepath.Join(dir, "plugins", "hello", "greeting.txt"), "hello, offline")
	// Neither a VCS directory nor a nested module is carried into the bundle.
	writeFile(t, filepath.Join(dir, "plugins", "hello", ".git", "HEAD"), "ref: refs/heads/x\n")
	writeFile(t, filepath.Join(dir, "plugins", "hello", "nested", "go.mod"),
		"module example.com/nested\n")

	manifestPath := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-hello"

[core]
path = "`+coreDir+`"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
hello = "./plugins/hello"
`)

	bundle := filepath.Join(dir, "bundle")

	result, err := Run(t.Context(), Options{Manifest: manifestPath, Vendor: bundle})
	require.NoError(t, err)
	assert.Equal(t, bundle, result.Output)
	assert.FileExists(t, filepath.Join(dir, "tt.manifest.lock"))
	assert.NoDirExists(t, filepath.Join(bundle, "local", "modules", "hello", ".git"))
	assert.NoDirExists(t, filepath.Join(bundle, "local", "modules", "hello", "nested"))

	// The embedded files of the core and of the module are vendored.
	assert.FileExists(t, filepath.Join(bundle, "vendor", "example.com", "hello", "greeting.txt"))
	assert.DirExists(t, filepath.Join(bundle, "vendor", "github.com", "tarantool", "tt", "v3",
		"cli", "create", "builtin_templates", "templates", "cluster", ".templates"))
	assert.FileExists(t, filepath.Join(bundle, "vendor", "github.com", "tarantool", "tt", "v3",
		"cli", "running", "lua", "launcher.lua"))

	// The bundle names no directory of the machine that made it.
	for _, name := range []string{"go.mod", "build.env", filepath.Join("vendor", "modules.txt")} {
		data, err := os.ReadFile(filepath.Join(bundle, name))
		require.NoError(t, err)
		assert.NotContains(t, string(data), dir, name)
		assert.NotContains(t, string(data), coreDir, name)
	}

	// A second bundle replaces the first.
	_, err = Run(t.Context(), Options{Manifest: manifestPath, Vendor: bundle, Locked: true})
	require.NoError(t, err)

	moved := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.Rename(bundle, moved))

	goroot, err := exec.CommandContext(t.Context(), goTool, "-C", moved, "env", "GOROOT").Output()
	require.NoError(t, err)

	binary := filepath.Join(t.TempDir(), "tt")
	build := exec.CommandContext(t.Context(), "sh", filepath.Join(moved, "build.sh"),
		"default", binary)

	build.Env = append(os.Environ(),
		"PATH="+filepath.Join(strings.TrimSpace(string(goroot)), "bin")+":/usr/bin:/bin",
		"GOMODCACHE="+t.TempDir(), "GOPROXY=off", "GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local")

	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))

	out, err = exec.CommandContext(t.Context(), binary, "hello").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "hello, offline\n", string(out))

	out, err = exec.CommandContext(t.Context(), binary, "version").CombinedOutput()
	require.NoError(t, err, string(out))
}

// TestBuildCoreOverride builds a distribution on the core of this checkout
// while its manifest asks for a version of the core: nothing is fetched and
// the lock is left alone.
func TestBuildCoreOverride(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
tt = "v3.99.0"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
`)

	_, err = Run(t.Context(), Options{Manifest: manifestPath, Core: coreDir, Locked: true})
	require.ErrorIs(t, err, errCoreLocked)

	result, err := Run(t.Context(), Options{
		Manifest: manifestPath, Core: coreDir, CoreDir: dir,
		Environ: append(os.Environ(), "GOPROXY=off"),
	})
	require.NoError(t, err)
	assert.FileExists(t, result.Output)
	assert.NoFileExists(t, filepath.Join(dir, "tt.manifest.lock"))
	assert.NoFileExists(t, filepath.Join(dir, "tt.manifest.sum"))
}
