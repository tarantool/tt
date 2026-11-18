//go:build integration

package core_test

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test below runs tt package build and pack --target as the main
// package of tt builds tt, with the standard module build, whose backends
// build a cargo module for the other architecture of Linux: cargo with the
// Rust target of it, and zig, which links it. It is skipped without them.

// crossManifest is a package with a Lua component and a cargo module of no
// dependency, built in the dev profile, which cargo compiles at once.
const crossManifest = `manifest_version = '0.1'

[package]
name = 'crossapp'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.app]
path = '.'
include = ['*.lua']

[components.fastrs]
path = 'fastrs'

[components.fastrs.build]
backend = 'cargo'
module = 'fastrs'
profile = 'dev'
`

// crossCComponent is a component of the c backend, which builds for the
// machine alone.
const crossCComponent = `
[components.cmod]
path = 'cmod'

[components.cmod.build]
backend = 'c'
module = 'cmod'
sources = ['cmod.c']
`

// requireCross returns the GOARCH of the other architecture of this Linux,
// and skips the test unless cargo, the Rust target of the module for it and
// zig are there.
func requireCross(t *testing.T) string {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("a build for another Linux platform is checked on linux")
	}

	goarch, arch := "arm64", "aarch64"
	if runtime.GOARCH == goarch {
		goarch, arch = "amd64", "x86_64"
	}

	for _, tool := range []string{"cargo", "rustc", "zig"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not in PATH", tool)
		}
	}

	triple := arch + "-unknown-linux-gnu"

	out, err := exec.CommandContext(t.Context(), "rustc", "--print", "target-libdir",
		"--target", triple).Output()
	if err != nil {
		t.Skipf("rustc knows no %s: %v", triple, err)
	}

	info, err := os.Stat(strings.TrimSpace(string(out)))
	if err != nil || !info.IsDir() {
		t.Skipf("the Rust target %s is not installed", triple)
	}

	return goarch
}

// TestPackageBuildForAnotherLinux builds a package of a cargo module with
// tt package build --target for the other architecture of Linux: the module
// is an ELF file of that machine, installed into the tree of the target and
// not into .rocks/. tt package pack --target makes the binary rock of the
// platform and the archive --without-deps labeled with it, and refuses the
// archive with its dependencies, and a c component fails the build before
// anything is built.
func TestPackageBuildForAnotherLinux(t *testing.T) {
	goarch := requireCross(t)
	platform := "linux-" + goarch
	luaArch := map[string]string{"amd64": "linux-x86_64", "arm64": "linux-aarch64"}[goarch]
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[goarch]

	project := t.TempDir()
	registry := t.TempDir()

	writeFiles(t, project, map[string]string{
		"app.manifest.toml": crossManifest,
		"VERSION":           "1.0.0\n",
		"init.lua":          "return {}\n",
		"fastrs/Cargo.toml": "[package]\nname = \"fastrs\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[dependencies]\n",
		"fastrs/src/lib.rs": "use std::os::raw::{c_int, c_void};\n\n" +
			"extern \"C\" {\n    fn lua_pushinteger(l: *mut c_void, n: isize);\n}\n\n" +
			"#[no_mangle]\npub unsafe extern \"C\" fn luaopen_crossapp_fastrs(" +
			"l: *mut c_void) -> c_int {\n    lua_pushinteger(l, 42);\n    1\n}\n",
	})

	lockfile := exec.CommandContext(t.Context(), "cargo", "generate-lockfile", "--offline")

	lockfile.Dir = filepath.Join(project, "fastrs")

	out, err := lockfile.CombinedOutput()
	require.NoError(t, err, string(out))

	// zig, which links the build, keeps its cache out of the checkout: CI
	// points ZIG_GLOBAL_CACHE_DIR into it, and the tests of distbuild that
	// run alongside hash the checkout, the C sources of the cache of zig
	// among its files.
	zigCache := t.TempDir()

	tt := func(args ...string) result {
		t.Helper()

		return runTT(t, ttRun{
			program: "standard", dir: project,
			args: append([]string{"package"}, append(args, "--registry", registry)...),
			env:  []string{"ZIG_GLOBAL_CACHE_DIR=" + zigCache, "ZIG_LOCAL_CACHE_DIR=" + zigCache},
		})
	}

	got := tt("build", "--target", "linux/"+goarch)
	require.Equal(t, 0, got.code, got.stderr)

	tree := filepath.Join(project, "_build", "target", platform, "tree")
	module := filepath.Join(tree, "lib", "tarantool", "crossapp", "fastrs.so")

	assert.Contains(t, got.stderr, "built crossapp for "+platform+" into "+tree)

	file, err := elf.Open(module)
	require.NoError(t, err)
	assert.Equal(t, machine, file.Machine, "%s is of %s", module, file.Machine)
	require.NoError(t, file.Close())

	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "crossapp", "init.lua"))
	assert.NoDirExists(t, filepath.Join(project, ".rocks", "lib", "tarantool", "crossapp"))
	assert.NoDirExists(t, filepath.Join(project, "_build", "rock"))

	// The rock of the platform and the archive labeled with it.
	got = tt("pack", "--target", platform, "--format", "rock,tt", "--without-deps")
	require.Equal(t, 0, got.code, got.stderr)

	pack := filepath.Join(project, "_build", "pack")

	assert.Contains(t, got.stdout, filepath.Join(pack, "crossapp-1.0.0-1."+luaArch+".rock")+"\n")
	assert.Contains(t, got.stdout, filepath.Join(pack, "crossapp-1.0.0-"+platform+".tt")+"\n")

	// The archive with its dependencies would carry those of the machine.
	got = tt("pack", "--target", "linux/"+goarch)
	assert.Equal(t, 1, got.code)
	assert.Contains(t, got.stderr, "a .tt archive with its dependencies is packed for the "+
		"machine tt runs on alone")

	// A c component builds for the machine alone.
	writeFiles(t, project, map[string]string{
		"app.manifest.toml": crossManifest + crossCComponent,
		"cmod/cmod.c":       "int luaopen_crossapp_cmod(void *L) { return 0; }\n",
	})

	got = tt("build", "--target", "linux/"+goarch)
	assert.Equal(t, 1, got.code)
	assert.Contains(t, got.stderr, `building component "cmod": the build cannot be made for `+
		`the target platform: the c backend builds for the machine tt runs on alone, `+
		runtime.GOOS+"-"+runtime.GOARCH+", and the build is for "+platform)
}
