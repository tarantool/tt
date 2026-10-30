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

	"github.com/tarantool/tt/v3/cli/manifest"
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

	// tt version --modules lists the table of the generated main with the Go
	// modules of the constructors: the local ones are built from directories.
	out, err = exec.CommandContext(t.Context(), result.Output, "version", "--modules").
		CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "Tarantool CLI core version")

	// The version of a core from a directory is the one git tells of it.
	if commit := checkoutCommit(t, coreDir); commit != "" {
		assert.Regexp(t, `(?m)^Tarantool CLI core version \S+\. commit: `+commit,
			string(out))
	}

	assert.Regexp(t, `(?m)^  aeon +\(devel\) +github\.com/tarantool/tt/modules/aeon$`, string(out))
	assert.Regexp(t, `(?m)^  builtin +\(devel\) +github\.com/tarantool/tt/v3$`, string(out))
	assert.Regexp(t, `(?m)^  hello +\(devel\) +example\.com/hello$`, string(out))

	out, err = exec.CommandContext(t.Context(), result.Output, "aeon", "--help").
		CombinedOutput()
	require.NoError(t, err, string(out))

	sum, err := os.ReadFile(filepath.Join(dir, "tt.manifest.sum"))
	require.NoError(t, err)

	lock, err := os.ReadFile(filepath.Join(dir, "tt.manifest.lock"))
	require.NoError(t, err)
	assert.Contains(t, string(lock), `path = './plugins/hello'`)
	assert.Regexp(t, `(?m)^\[core\][^\[]*content_hash = 'sha256:[0-9a-f]{64}'`, string(lock))
	assert.Regexp(t, `(?m)^\[modules\.hello\][^\[]*content_hash = 'sha256:[0-9a-f]{64}'`,
		string(lock))

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

	// A local module that changed fails a build from the lock and is
	// recorded again by a build that may write it.
	writeFile(t, filepath.Join(dir, "plugins", "hello", "hello.go"),
		strings.Replace(helloModule, "hello from a module", "hello again", 1))

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.ErrorIs(t, err, errLocalChanged)
	assert.Contains(t, err.Error(), "./plugins/hello changed since tt.manifest.lock was written")

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, GeneratedBy: "tt test",
	})
	require.NoError(t, err)

	relocked, err := os.ReadFile(filepath.Join(dir, "tt.manifest.lock"))
	require.NoError(t, err)
	assert.NotEqual(t, string(lock), string(relocked))

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.NoError(t, err)

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

	// The bundle carries no git directory: the version of the core is the
	// one stamped when it was made.
	if commit := checkoutCommit(t, coreDir); commit != "" {
		assert.Contains(t, string(out), "commit: "+commit)
	}
}

// checkoutCommit returns the abbreviated commit of the git checkout dir is,
// or nothing when it is not one.
func checkoutCommit(t *testing.T, dir string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "--short",
		"HEAD").Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
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

// libModule is a library that a tt module depends on and that is replaced:
// the greeting says which copy of it the binary is built from.
const libModule = `// Package lib is the library of the replaced hello module.
package lib

// Greeting is what the module says.
func Greeting() string {
	return "hello from lib v1"
}
`

// replaceHello is a tt module that says what the library tells it to and
// uses a library that [replace] pins to another version.
const replaceHello = `// Package hello is a tt module that greets with a library.
package hello

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"example.com/lib"
	"github.com/tarantool/tt/sdk"
)

// New mounts tt hello.
func New(s sdk.Services) []sdk.Mount {
	return []sdk.Mount{{Cmd: &cobra.Command{
		Use:   "hello",
		Short: "Greet",
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintln(s.Streams().IO().Out, lib.Greeting(), len(uuid.NewString()))

			return err
		},
	}}}
}
`

// replaceHelloGoMod requires a library that exists nowhere but in a local
// directory, and a published one at an older version than the build uses.
const replaceHelloGoMod = `module example.com/hello

go 1.26.10

require (
	example.com/lib v1.0.0
	github.com/google/uuid v1.3.0
	github.com/spf13/cobra v1.10.2
	github.com/tarantool/tt/sdk v0.0.0-20261028190206-9031d7ad96d9
	github.com/tarantool/tt/sdk/connect v0.0.0-20261028190206-9031d7ad96d9
)
`

// TestBuildReplace replaces the libraries of a local module: one with a
// local directory, which the binary is built from and the lock records by
// the hash of its files, and one with another version of the module.
func TestBuildReplace(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	// The replacement of uuid is a version query, which only the module
	// proxy answers: the other tests here build from the module cache alone.
	proxy, err := exec.CommandContext(t.Context(), goTool, "env", "GOPROXY").Output()
	require.NoError(t, err)

	if strings.TrimSpace(string(proxy)) == "off" {
		t.Skip("a version query needs the module proxy, and GOPROXY is off")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins", "hello", "go.mod"), replaceHelloGoMod)
	writeFile(t, filepath.Join(dir, "plugins", "hello", "hello.go"), replaceHello)
	writeFile(t, filepath.Join(dir, "libs", "lib", "go.mod"),
		"module example.com/lib\n\ngo 1.26.10\n")
	writeFile(t, filepath.Join(dir, "libs", "lib", "lib.go"), libModule)

	manifestPath := filepath.Join(dir, "tt.manifest.toml")
	writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-hello"

[core]
path = "`+coreDir+`"

[dependencies]
hello = "./plugins/hello"

[replace]
"example.com/lib" = "./libs/lib"
"github.com/google/uuid" = "github.com/google/uuid@v1.5"
`)

	work := filepath.Join(dir, "work")

	result, err := Run(t.Context(), Options{
		Manifest: manifestPath, WorkDir: work, GeneratedBy: "tt test",
	})
	require.NoError(t, err)

	out, err := exec.CommandContext(t.Context(), result.Output, "hello").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "hello from lib v1 36\n", string(out))

	// The replace directives are in the go.mod the binary is built with.
	gomod, err := os.ReadFile(filepath.Join(work, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "example.com/lib => "+filepath.Join(dir, "libs", "lib")+"\n")
	assert.Contains(t, string(gomod), "github.com/google/uuid => github.com/google/uuid v1.5.0\n")

	// The lock records the directory by its content and the module by its
	// exact version.
	lock, err := os.ReadFile(filepath.Join(dir, "tt.manifest.lock"))
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^\[replace\.'example\.com/lib'\]\n\s*path = './libs/lib'\n`+
		`\s*content_hash = 'sha256:[0-9a-f]{64}'`, string(lock))
	assert.Equal(t, manifest.LockedReplace{
		Module: "github.com/google/uuid", Version: "v1.5.0",
	}, result.Lock.Replace["github.com/google/uuid"])

	output := filepath.Join(dir, "out", "tt")

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.NoError(t, err)

	// A replaced directory that changed fails a build from the lock...
	writeFile(t, filepath.Join(dir, "libs", "lib", "lib.go"),
		strings.Replace(libModule, "lib v1", "lib v2", 1))

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.ErrorIs(t, err, errLocalChanged)
	assert.Contains(t, err.Error(),
		"./libs/lib (replace example.com/lib) changed since tt.manifest.lock was written")

	// ...and an ordinary build is made of the new code.
	result, err = Run(t.Context(), Options{Manifest: manifestPath, GeneratedBy: "tt test"})
	require.NoError(t, err)

	out, err = exec.CommandContext(t.Context(), result.Output, "hello").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "hello from lib v2 36\n", string(out))

	_, err = Run(t.Context(), Options{
		Manifest: manifestPath, Output: output, Locked: true, GeneratedBy: "tt test",
	})
	require.NoError(t, err)

	// The bundle carries the replaced directory and builds offline, away
	// from everything it was made of.
	bundle := filepath.Join(dir, "bundle")

	_, err = Run(t.Context(), Options{Manifest: manifestPath, Vendor: bundle, Locked: true})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(bundle, "local", "replace", "example.com", "lib", "lib.go"))

	for _, name := range []string{"go.mod", filepath.Join("vendor", "modules.txt")} {
		data, err := os.ReadFile(filepath.Join(bundle, name))
		require.NoError(t, err)
		assert.Contains(t, string(data), "example.com/lib => ./local/replace/example.com/lib")
		assert.NotContains(t, string(data), dir, name)
	}

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

	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, string(buildOut))

	out, err = exec.CommandContext(t.Context(), binary, "hello").CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "hello from lib v2 36\n", string(out))
}

// TestBuildReplaceRefused refuses to replace a module the manifest has a
// place for, and a directory of another module.
func TestBuildReplaceRefused(t *testing.T) {
	_, err := exec.LookPath(goTool)
	if err != nil {
		t.Skip("go is not in PATH")
	}

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hello", "go.mod"), helloGoMod)
	writeFile(t, filepath.Join(dir, "hello", "hello.go"), helloModule)

	manifestPath := filepath.Join(dir, "tt.manifest.toml")

	for _, testCase := range []struct{ replace, want string }{
		{`"example.com/hello" = "./hello"`, `is the tt module "hello" of [dependencies]`},
		{`"github.com/tarantool/tt/sdk" = "./hello"`, "is a module of the core's repository"},
		{
			`"example.com/lib" = "./hello"`,
			"./hello holds module example.com/hello, not example.com/lib",
		},
	} {
		writeFile(t, manifestPath, `manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
path = "`+coreDir+`"

[dependencies]
hello = "./hello"

[replace]
`+testCase.replace+"\n")

		_, err = Run(t.Context(), Options{Manifest: manifestPath})
		require.ErrorContains(t, err, testCase.want)
		assert.NoFileExists(t, filepath.Join(dir, "tt.manifest.lock"))
	}
}
