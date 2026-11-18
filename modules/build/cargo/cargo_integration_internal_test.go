//go:build integration

package cargo

import (
	"debug/elf"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

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

	harness.Context.Target = build.Target{OS: linuxOS, Arch: goarch}

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
