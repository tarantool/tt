//go:build integration

package cargo

import (
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// The backend is run below on the real cargo with what a build for another
// platform or with the crates of crates.io needs: zig, the Rust targets of
// the other architecture of Linux, a network. A test is skipped when its
// tool is not there.

// requireZig skips the test when zig is not in PATH.
func requireZig(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath(zigTool)
	if err != nil {
		t.Skip("zig is not in PATH")
	}
}

// elfMachines maps a GOARCH to the machine of its ELF files.
var elfMachines = map[string]elf.Machine{
	"amd64": elf.EM_X86_64,
	"arm64": elf.EM_AARCH64,
}

// requireELF opens the ELF file at path, checks that it is of the machine
// of goarch, and returns whether it has a loader.
func requireELF(t *testing.T, path, goarch string) bool {
	t.Helper()

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	assert.Equal(t, elfMachines[goarch], file.Machine, "%s is of %s", path, file.Machine)

	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			return true
		}
	}

	return false
}

// crossCrate is a crate of a library, a Lua module, and a binary, which
// call a C function a build script compiles and archives with the compiler
// and the archiver of the target the cc crate takes, CC_<target> and
// AR_<target>, as the build script of a crate that has C sources does.
var crossCrate = map[string]string{
	"Cargo.toml": `[package]
name = "agent-lib"
version = "0.1.0"
edition = "2021"
build = "build.rs"

[[bin]]
name = "agent"
path = "src/main.rs"

[dependencies]
`,
	"build.rs": `use std::{env, path::PathBuf, process::Command};

fn tool(name: &str, default: &str) -> String {
    let key = env::var("TARGET").unwrap().replace('-', "_");
    env::var(format!("{name}_{key}")).unwrap_or_else(|_| default.to_string())
}

fn main() {
    let out = PathBuf::from(env::var("OUT_DIR").unwrap());
    let object = out.join("answer.o");
    let status = Command::new(tool("CC", "cc"))
        .args(["-c", "-fPIC", "-O2", "src/answer.c", "-o"])
        .arg(&object)
        .status()
        .unwrap();
    assert!(status.success());
    let status = Command::new(tool("AR", "ar"))
        .arg("crs")
        .arg(out.join("libanswer.a"))
        .arg(&object)
        .status()
        .unwrap();
    assert!(status.success());
    println!("cargo:rustc-link-search=native={}", out.display());
    println!("cargo:rustc-link-lib=static=answer");
    println!("cargo:rerun-if-changed=src/answer.c");
}
`,
	"src/answer.c": "int answer(void) { return 42; }\n",
	"src/lib.rs": `use std::os::raw::{c_int, c_void};

extern "C" {
    fn answer() -> c_int;
    fn lua_pushinteger(l: *mut c_void, n: isize);
}

/// The answer of the C library, which the library links.
pub fn the_answer() -> i32 {
    unsafe { answer() }
}

#[no_mangle]
pub unsafe extern "C" fn luaopen_agent_lib(l: *mut c_void) -> c_int {
    lua_pushinteger(l, the_answer() as isize);
    1
}
`,
	// The C library of the build script is linked into the library alone,
	// which the binary uses.
	"src/main.rs": `fn main() {
    println!("{}", agent_lib::the_answer());
}
`,
}

// TestBuildsForAnotherLinux builds, on this Linux, a module and a program of
// a crate for the other architecture of Linux, linked by zig cc, the C of
// its build script compiled by zig cc too, and checks the machine of what it
// built: the module and the program of the gnu target, then the static
// program of the musl one, with no loader.
func TestBuildsForAnotherLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != linuxOS {
		t.Skip("a build for another Linux platform is checked on linux")
	}

	goarch := otherArch()
	arch := cargoArches[goarch]

	requireCargo(t, arch+"-unknown-linux-gnu")
	requireCargo(t, arch+"-unknown-linux-musl")
	requireZig(t)

	harness := buildtest.New(t, Backend(), both())
	dirs := harness.Context.Dirs

	noTargetDir(harness)
	writeFiles(t, dirs.Component, crossCrate)
	generateLockfile(t, dirs.Component)

	harness.SetTarget(build.Target{OS: linuxOS, Arch: goarch})

	fields := map[string]any{"module": "agent_lib", "bin": "agent", "profile": "dev"}
	module := filepath.Join(dirs.Modules, "agent_lib.so")
	program := filepath.Join(dirs.Programs, "agent")

	require.NoError(t, harness.Run(t.Context(), validate(t, both(), fields, false)),
		harness.Printed())
	requireELF(t, module, goarch)
	assert.True(t, requireELF(t, program, goarch), "a program of gnu has a loader")

	require.NoError(t, harness.Run(t.Context(), validate(t, both(), fields, true)),
		harness.Printed())
	requireELF(t, module, goarch)
	assert.False(t, requireELF(t, program, goarch), "a static program has no loader")
}

// howTo is the how-to whose example crate TestBuildsTheModuleOfTheHowTo
// builds, from the directory of the package of the test.
var howTo = filepath.Join("..", "..", "..", "doc", "how-to", "rust-modules.md")

// codeBlocks returns the code blocks of the markdown file at path whose
// language is lang, in order.
func codeBlocks(t *testing.T, path, lang string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var (
		blocks []string
		block  strings.Builder
		inside bool
	)

	for line := range strings.Lines(string(data)) {
		switch trimmed := strings.TrimRight(line, "\n"); {
		case !inside && trimmed == "```"+lang:
			inside = true

			block.Reset()
		case inside && trimmed == "```":
			inside = false

			blocks = append(blocks, block.String())
		case inside:
			block.WriteString(line)
		}
	}

	return blocks
}

// codeBlock returns the first code block of the how-to of lang that has
// mark.
func codeBlock(t *testing.T, lang, mark string) string {
	t.Helper()

	for _, block := range codeBlocks(t, howTo, lang) {
		if strings.Contains(block, mark) {
			return block
		}
	}

	require.Failf(t, "no code block", "%s has no %s block with %q", howTo, lang, mark)

	return ""
}

// requireCrates writes the Cargo.lock of the crate in dir and fetches its
// crates, which needs crates.io, and skips the test when cargo cannot.
func requireCrates(t *testing.T, dir string) {
	t.Helper()

	for _, args := range [][]string{{"generate-lockfile"}, {"fetch", "--locked"}} {
		cmd := exec.CommandContext(t.Context(), cargoTool, args...)

		cmd.Dir = dir

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("cargo %s cannot fetch the crates of crates.io: %s", args[0],
				strings.TrimSpace(string(out)))
		}
	}
}

// requireTarantool returns the tarantool of PATH, and skips the rest of the
// test without one.
func requireTarantool(t *testing.T) string {
	t.Helper()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not in PATH: the module is not loaded")
	}

	return tarantool
}

// runTarantool runs the Lua script in Tarantool in a directory whose .rocks
// holds the module file as lib/tarantool/<namespace>/<file>, and returns
// what it printed.
func runTarantool(t *testing.T, tarantool, file, namespace, script string) string {
	t.Helper()

	app := t.TempDir()
	lib := filepath.Join(app, ".rocks", "lib", "tarantool", namespace)
	require.NoError(t, build.CopyFile(filepath.Join(lib, filepath.Base(file)), file))

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, tarantool, "-e", script)

	cmd.Dir = app

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return string(out)
}

// TestBuildsTheModuleOfTheHowTo builds the example crate of Build a module
// in Rust, its Cargo.toml and src/lib.rs as the how-to has them, of the
// tarantool crate of crates.io, as the module of the component fastrs of
// the package myapp, and runs the Lua of the how-to in Tarantool: require
// of the module and its function mul, and the C stored procedure add of
// box.schema.func.create. Then it downloads the crates into a mirror, as tt
// package download does, and builds the module again from the mirror with
// an empty CARGO_HOME and a proxy that refuses every connection, which
// Tarantool loads as well. The crates are fetched from crates.io: the test
// is skipped when they cannot be.
func TestBuildsTheModuleOfTheHowTo(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	component := build.Component{
		Name: "fastrs", Path: "libs/fastrs", Namespace: "myapp", Package: "myapp", Wants: nil,
	}
	harness := buildtest.New(t, Backend(), component)
	dirs := harness.Context.Dirs

	noTargetDir(harness)
	writeFiles(t, dirs.Component, map[string]string{
		"Cargo.toml": codeBlock(t, "toml", `name = "fastrs"`),
		"src/lib.rs": codeBlock(t, "rust", "luaopen_myapp_fastrs"),
	})
	requireCrates(t, dirs.Component)

	tarantool := requireTarantool(t)
	module := filepath.Join(dirs.Modules, "fastrs"+harness.Context.Target.ModuleExt())
	script := "box.cfg{log_level = 1}\n" + codeBlock(t, "lua", "box.schema.func.create") +
		"io.write('called') os.exit(0)\n"
	bld := validate(t, component, nil, false)

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, "called", runTarantool(t, tarantool, module, "myapp", script))

	mirror := filepath.Join(t.TempDir(), "mirror", Name)

	require.NoError(t, harness.Vendor(t.Context(), bld, mirror), harness.Printed())

	vendored, err := filepath.Glob(filepath.Join(mirror, "tarantool-11.*"))
	require.NoError(t, err)
	assert.Len(t, vendored, 1, "the tarantool crate is vendored")
	require.NoError(t, os.Remove(module))

	harness.Context.Env["CARGO_HOME"] = t.TempDir()
	harness.Context.Env["CARGO_HTTP_PROXY"] = "http://127.0.0.1:9"
	harness.Context.Vendored = mirror

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, "called", runTarantool(t, tarantool, module, "myapp", script))
}
