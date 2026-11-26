package build_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// The projects of the cases of exportCases of the backends that compile,
// which run under the integration build tag alone, with the toolchains of
// modules/build/README.md, and are skipped without them.

// exportWord is a value of a block that goes through the script of an
// export as a word of sh, and reaches the build as it is.
const exportWord = "it's a $HOME `echo no` \\ word"

// prefixEnv names the prefix of a Tarantool, the directory with its headers
// in include/tarantool, as tt takes it.
const prefixEnv = "TT_CLI_TARANTOOL_PREFIX"

// exportTarantool tells harness the Tarantool whose headers a build
// compiles against, as the core tells it, and the script of an export gets
// it from LuaRocks: that of the prefix of TT_CLI_TARANTOOL_PREFIX, else of
// the tarantool in PATH. A Tarantool with no module.h skips the test.
func exportTarantool(t *testing.T, harness *buildtest.Harness) {
	t.Helper()

	prefix := os.Getenv(prefixEnv)
	if prefix == "" {
		tarantool, err := exec.LookPath("tarantool")
		if err != nil {
			t.Skipf("%s names no prefix of Tarantool, and tarantool is not in PATH", prefixEnv)
		}

		resolved, err := filepath.EvalSymlinks(tarantool)
		require.NoError(t, err)

		prefix = filepath.Dir(filepath.Dir(resolved))
	}

	include := filepath.Join(prefix, "include", "tarantool")

	_, err := os.Stat(filepath.Join(include, "module.h"))
	if err != nil {
		t.Skipf("no headers of Tarantool, no module.h in %s; %s names a prefix with them",
			include, prefixEnv)
	}

	harness.SetTarantool(sdkbuild.Tarantool{
		Prefix: prefix, IncludeDir: include, Version: "", Executable: "",
	})
}

// runProgram returns what the program name of the directory of the programs
// of harness printed, for the observe of a case.
func runProgram(t *testing.T, harness *buildtest.Harness, name string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	out, err := exec.CommandContext(ctx, filepath.Join(harness.Context.Dirs.Programs, name)).
		CombinedOutput()
	require.NoError(t, err, string(out))

	return string(out)
}

// requireCC skips the test without a C compiler in PATH, $CC or cc, which
// cmake and cgo compile with.
func requireCC(t *testing.T) {
	t.Helper()

	compiler := os.Getenv("CC")
	if compiler == "" {
		compiler = "cc"
	}

	_, err := exec.LookPath(compiler)
	if err != nil {
		t.Skipf("no C compiler: %v", err)
	}
}

// cmakeProject is a CMake project that configures the build type, the
// variable WORD and CMAKE_PREFIX_PATH into the data file gen/vars.txt and
// writes the header gen/answer.h, and builds the module answer, a library
// fnv for linking and a program tool, each of its own target; with
// tarantool, the module includes module.h of the Tarantool of
// CMAKE_PREFIX_PATH, which find_path finds there, as the usual
// FindTarantool.cmake finds it.
func cmakeProject(tarantool bool) map[string]string {
	find, include := "", ""
	if tarantool {
		find = `find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
    message(FATAL_ERROR "tarantool/module.h not found")
endif()
`
		include = "#include <module.h>\n"
	}

	return map[string]string{
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.15)
project(demo C)
` + find + `
file(WRITE ${CMAKE_BINARY_DIR}/gen/vars.txt
    "type=${CMAKE_BUILD_TYPE}\nword=${WORD}\nprefix=${CMAKE_PREFIX_PATH}\n")
file(WRITE ${CMAKE_BINARY_DIR}/gen/answer.h "#define ANSWER 42\n")

add_library(answer MODULE answer.c)
set_target_properties(answer PROPERTIES PREFIX "" SUFFIX ".so"
    LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/lib)
if(TARANTOOL_INCLUDE_DIR)
    target_include_directories(answer PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
endif()
if(APPLE)
    target_link_options(answer PRIVATE -undefined dynamic_lookup)
endif()

add_library(fnv STATIC fnv.c)

add_executable(tool tool.c)
target_compile_definitions(tool PRIVATE "TYPE=\"${CMAKE_BUILD_TYPE}\"")
`,
		"answer.c": include + "int luaopen_answer(void *L) { (void)L; return 0; }\n",
		"fnv.c":    "unsigned fnv(unsigned x) { return x * 16777619u; }\n",
		"tool.c": "#include <stdio.h>\n\nint main(void) { printf(\"tool %s\\n\", TYPE); " +
			"return 0; }\n",
	}
}

// cmakeOutput is the output of a block of cmakeProject.
func cmakeOutput() []sdkbuild.OutputFile {
	return []sdkbuild.OutputFile{
		{Path: "lib/answer.so", To: sdkbuild.Module},
		{Path: "gen/vars.txt", To: sdkbuild.Data},
		{Path: "tool", To: sdkbuild.Program},
		{Path: "libfnv.a", To: sdkbuild.Link},
		{Path: "gen/answer.h", To: sdkbuild.Link},
	}
}

// goProject is a Go program that prints the word its ldflags set and the tag
// it is built with; with cgo, a C function computes its answer.
func goProject(cgo bool) map[string]string {
	answer := "package main\n\nfunc answer() int { return 42 }\n"
	if cgo {
		answer = "package main\n\n// int answer(void) { return 42; }\nimport \"C\"\n\n" +
			"func answer() int { return int(C.answer()) }\n"
	}

	return map[string]string{
		"go.mod":    "module example.com/hello\n\ngo 1.22\n",
		"answer.go": answer,
		"main.go": `package main

import "fmt"

var word = "none"

func main() {
	fmt.Println("word:", word, "tag:", tag, "answer:", answer())
}
`,
		"tag.go":   "//go:build demo\n\npackage main\n\nconst tag = \"demo\"\n",
		"notag.go": "//go:build !demo\n\npackage main\n\nconst tag = \"none\"\n",
	}
}

// requireLinux skips the test off Linux: a static program is built there
// alone.
func requireLinux(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}
}

// cargoArches are the architectures of the Rust targets of Linux, by
// GOARCH.
var cargoArches = map[string]string{"amd64": "x86_64", "arm64": "aarch64"}

// requireMusl skips the test off Linux and without the Rust target of musl
// of the machine, which a static program is built for.
func requireMusl(t *testing.T) {
	t.Helper()
	requireLinux(t)

	triple := cargoArches[runtime.GOARCH] + "-unknown-linux-musl"

	out, err := exec.CommandContext(t.Context(), "rustc", "--print", "target-libdir",
		"--target", triple).Output()
	if err != nil {
		t.Skipf("rustc names no directory of the Rust target %s: %v", triple, err)
	}

	info, err := os.Stat(strings.TrimSpace(string(out)))
	if err != nil || !info.IsDir() {
		t.Skipf("the Rust target %s is not installed", triple)
	}
}

// cargoProject is a package of a library, a module of Tarantool, and a
// binary, which prints whether the feature extra is on, with a profile fast
// of its own and the Cargo.lock --locked takes: it depends on no crate, so
// it builds offline.
func cargoProject() map[string]string {
	return map[string]string{
		"Cargo.toml": `[package]
name = "demo-lib"
version = "0.1.0"
edition = "2021"

[[bin]]
name = "demo-cli"
path = "src/main.rs"

[features]
extra = []

[profile.fast]
inherits = "release"
`,
		"Cargo.lock": `# This file is automatically @generated by Cargo.
# It is not intended for manual editing.
version = 3

[[package]]
name = "demo-lib"
version = "0.1.0"
`,
		"src/lib.rs": `#[no_mangle]
pub extern "C" fn luaopen_demo(_l: *mut core::ffi::c_void) -> i32 {
    0
}
`,
		"src/main.rs": `fn main() {
    println!("extra {}", cfg!(feature = "extra"));
}
`,
	}
}

// zigProject is a zig project of no dependency: a library that is a Lua
// module, which installArtifact installs as lib/libanswer.so, and a program,
// answer-cli, linked with the C library when libc is set, which prints the
// option word of the build.zig.
func zigProject(libc bool) map[string]string {
	link := "false"
	if libc {
		link = "true"
	}

	return map[string]string{
		"build.zig": `const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const word = b.option([]const u8, "word", "the word answer-cli prints") orelse "none";

    const lib = b.addLibrary(.{
        .name = "answer",
        .linkage = .dynamic,
        .root_module = b.createModule(.{
            .root_source_file = b.path("answer.zig"),
            .target = target,
            .optimize = optimize,
        }),
    });
    lib.linker_allow_shlib_undefined = true;
    b.installArtifact(lib);

    const options = b.addOptions();
    options.addOption([]const u8, "word", word);

    const exe = b.addExecutable(.{
        .name = "answer-cli",
        .root_module = b.createModule(.{
            .root_source_file = b.path("main.zig"),
            .target = target,
            .optimize = optimize,
            .link_libc = ` + link + `,
        }),
    });
    exe.root_module.addOptions("config", options);
    b.installArtifact(exe);
}
`,
		"answer.zig": `extern fn lua_pushinteger(l: ?*anyopaque, n: isize) void;

export fn luaopen_answer(l: ?*anyopaque) c_int {
    lua_pushinteger(l, 42);
    return 1;
}
`,
		"main.zig": `const std = @import("std");
const config = @import("config");

pub fn main() void {
    std.debug.print("word {s}\n", .{config.word});
}
`,
	}
}

// zigTarantoolEnv names a checkout of zig-tarantool whose build.zig has the
// helpers addModule, installModule and addExecutable.
const zigTarantoolEnv = "TT_TEST_ZIG_TARANTOOL"

// zigTarantoolProject writes into the working directory of harness the
// project of the how-to of tt, Build a module in Zig, against the checkout
// of zig-tarantool of TT_TEST_ZIG_TARANTOOL by a .path relative to it: a
// module hello and a program hello-client. A checkout with no helpers skips
// the test, and so does none.
func zigTarantoolProject(t *testing.T, harness *buildtest.Harness) {
	t.Helper()

	checkout := os.Getenv(zigTarantoolEnv)
	if checkout == "" {
		t.Skip(zigTarantoolEnv + " names no checkout of zig-tarantool")
	}

	data, err := os.ReadFile(filepath.Join(checkout, "build.zig"))
	require.NoError(t, err)

	if !strings.Contains(string(data), "pub fn addModule(") {
		t.Skipf("the zig-tarantool of %s has no addModule", checkout)
	}

	// zig resolves a .path against the real path of the project.
	dir, err := filepath.EvalSymlinks(harness.Context.Dirs.Cwd)
	require.NoError(t, err)

	checkout, err = filepath.EvalSymlinks(checkout)
	require.NoError(t, err)

	rel, err := filepath.Rel(dir, checkout)
	require.NoError(t, err)

	for name, content := range map[string]string{
		"build.zig.zon": `.{
    .name = .hello,
    .version = "0.1.0",
    .fingerprint = 0x3610a686cc406856,
    .minimum_zig_version = "0.17.1",
    .dependencies = .{
        .tarantool = .{ .path = "` + filepath.ToSlash(rel) + `" },
    },
    .paths = .{ "build.zig", "build.zig.zon", "src" },
}
`,
		"build.zig": `const std = @import("std");
const tarantool = @import("tarantool");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const hello = tarantool.addModule(b, .{
        .name = "hello",
        .root_source_file = b.path("src/hello.zig"),
        .target = target,
        .optimize = optimize,
    });
    tarantool.installModule(b, hello);

    const client = tarantool.addExecutable(b, .{
        .name = "hello-client",
        .root_source_file = b.path("src/client.zig"),
        .target = target,
        .optimize = optimize,
    });
    b.installArtifact(client);
}
`,
		"src/hello.zig": `const t = @import("tarantool");

fn add(a: i64, b: i64) i64 {
    return a + b;
}

fn mul(a: i64, b: i64) i64 {
    return a * b;
}

comptime {
    t.proc.exportProc("add", add);
    t.lua.exportModule("hello", .{ .mul = mul });
}
`,
		"src/client.zig": `const std = @import("std");
const client = @import("tarantool-client");

pub fn main(init: std.process.Init) !void {
    const address = init.environ_map.get("TT_URI") orelse "127.0.0.1:3301";
    var conn = try client.Connection.connect(init.gpa, init.io, .{
        .address = address,
        .user = "guest",
        .password = "",
    });
    defer conn.close();

    const two = try conn.eval("return 1 + 1", .{}, i64);
    std.debug.print("1 + 1 = {d}\n", .{two});
}
`,
	} {
		file := filepath.Join(harness.Context.Dirs.Cwd, name)

		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
		require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	}
}
