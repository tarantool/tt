//go:build integration

package zig

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

// The backend is run below on the real zig. zig may not be in PATH, and the
// tests are skipped then. A project of no dependency builds offline; the one
// of zig-tarantool is built against the checkout TT_TEST_ZIG_TARANTOOL names.

// zigTarantoolEnv names a checkout of zig-tarantool whose build.zig has the
// helpers addModule, installModule and addExecutable.
const zigTarantoolEnv = "TT_TEST_ZIG_TARANTOOL"

// requireZig skips the test when zig is not in PATH.
func requireZig(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath(zigTool)
	if err != nil {
		t.Skip("zig is not in PATH")
	}
}

// requireZigTarantool returns the checkout of zig-tarantool of
// TT_TEST_ZIG_TARANTOOL, and skips the test when it names none with the
// helpers.
func requireZigTarantool(t *testing.T) string {
	t.Helper()

	dir := os.Getenv(zigTarantoolEnv)
	if dir == "" {
		t.Skip(zigTarantoolEnv + " names no checkout of zig-tarantool")
	}

	data, err := os.ReadFile(filepath.Join(dir, "build.zig"))
	require.NoError(t, err)

	if !strings.Contains(string(data), "pub fn addModule(") {
		t.Skipf("the zig-tarantool of %s has no addModule", dir)
	}

	abs, err := filepath.Abs(dir)
	require.NoError(t, err)

	return abs
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

// writeFiles writes files, a map of a slash path under dir to its content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// plainProject is a zig project of no dependency: a library that is a Lua
// module, require of it returning 42, which installArtifact installs as
// lib/libanswer.so, and a program, answer-cli, linked with the C library
// when libc is set.
func plainProject(libc bool) map[string]string {
	link := "false"
	if libc {
		link = "true"
	}

	return map[string]string{
		"build.zig": `const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

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

    const exe = b.addExecutable(.{
        .name = "answer-cli",
        .root_module = b.createModule(.{
            .root_source_file = b.path("main.zig"),
            .target = target,
            .optimize = optimize,
            .link_libc = ` + link + `,
        }),
    });
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

pub fn main() void {
    std.debug.print("answer 42\n", .{});
}
`,
	}
}

// runProgram runs the program at path and returns what it printed.
func runProgram(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), path).CombinedOutput()
	require.NoError(t, err, string(out))

	return strings.TrimSpace(string(out))
}

// runTarantool runs the Lua script in Tarantool in a directory whose
// .rocks holds the module file, and returns what it printed.
func runTarantool(t *testing.T, tarantool, file, script string) string {
	t.Helper()

	app := t.TempDir()
	lib := filepath.Join(app, ".rocks", "lib", "tarantool")
	require.NoError(t, build.CopyFile(filepath.Join(lib, filepath.Base(file)), file))

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, tarantool, "-e", script)

	cmd.Dir = app

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return string(out)
}

// TestBuildsAModuleAndAProgram builds a project of no dependency with one
// block, which names both: the module installArtifact installs as
// libanswer.so, which Tarantool loads, and the program it runs.
func TestBuildsAModuleAndAProgram(t *testing.T) {
	t.Parallel()
	requireZig(t)

	harness := buildtest.New(t, Backend(), component())
	dirs := harness.Context.Dirs
	ext := harness.Context.Target.ModuleExt()

	writeFiles(t, dirs.Component, plainProject(false))

	require.NoError(t, harness.Run(t.Context(), validate(t, component(),
		map[string]any{"module": "answer", "bin": "answer-cli"}, false)), harness.Printed())

	module := filepath.Join(dirs.Output, "answer"+ext)
	assert.FileExists(t, module)
	assert.Equal(t, "answer 42", runProgram(t, filepath.Join(dirs.Programs, "answer-cli")))
	assert.NoDirExists(t, filepath.Join(dirs.Component, "zig-out"))
	assert.NoDirExists(t, filepath.Join(dirs.Component, ".zig-cache"))

	tarantool := requireTarantool(t)
	assert.Equal(t, "42", runTarantool(t, tarantool, module,
		"io.write(tostring(require('answer'))) os.exit(0)"))
}

// TestBuildsAStaticProgram builds a program linked with the C library
// statically: for musl, as the build.zig takes -Dtarget.
func TestBuildsAStaticProgram(t *testing.T) {
	t.Parallel()
	requireZig(t)

	if _, ok := zigArches[runtime.GOARCH]; runtime.GOOS != "linux" || !ok {
		t.Skip("a static program is built for linux only")
	}

	harness := buildtest.New(t, Backend(), component())
	dirs := harness.Context.Dirs

	writeFiles(t, dirs.Component, plainProject(true))

	require.NoError(t, harness.Run(t.Context(), validate(t, component(),
		map[string]any{"bin": "answer-cli", "optimize": "Debug"}, true)), harness.Printed())

	path := filepath.Join(dirs.Programs, "answer-cli")
	assert.Equal(t, "answer 42", runProgram(t, path))

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	for _, prog := range file.Progs {
		assert.NotEqual(t, elf.PT_INTERP, prog.Type, "a static program has no loader")
	}

	libraries, err := file.ImportedLibraries()
	require.NoError(t, err)
	assert.Empty(t, libraries)
}

// consumerProject is the project of the how-to of tt, Build a module in Zig,
// against zig-tarantool at the relative path dep: a module hello of a Lua
// function and a C function, and a program hello-client of
// tarantool-client.
func consumerProject(dep string) map[string]string {
	return map[string]string{
		"build.zig.zon": `.{
    .name = .hello,
    .version = "0.1.0",
    .fingerprint = 0x3610a686cc406856,
    .minimum_zig_version = "0.16.0",
    .dependencies = .{
        .tarantool = .{ .path = "` + filepath.ToSlash(dep) + `" },
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
	}
}

// TestBuildsAModuleAndAProgramOfZigTarantool builds the project of the
// how-to against zig-tarantool: the module, whose Lua function and C function
// Tarantool calls, and the program, which talks to that Tarantool.
func TestBuildsAModuleAndAProgramOfZigTarantool(t *testing.T) {
	t.Parallel()
	requireZig(t)

	checkout := requireZigTarantool(t)
	harness := buildtest.New(t, Backend(), component())
	dirs := harness.Context.Dirs
	ext := harness.Context.Target.ModuleExt()

	dep, err := filepath.Rel(dirs.Component, checkout)
	require.NoError(t, err)

	writeFiles(t, dirs.Component, consumerProject(dep))

	require.NoError(t, harness.Run(t.Context(), validate(t, component(),
		map[string]any{"module": "hello", "bin": "hello-client"}, false)), harness.Printed())

	module := filepath.Join(dirs.Output, "hello"+ext)
	program := filepath.Join(dirs.Programs, "hello-client")

	assert.FileExists(t, module)
	assert.FileExists(t, program)

	tarantool := requireTarantool(t)

	// The program is run from a fiber, by popen, so that Tarantool serves it.
	printed := runTarantool(t, tarantool, module, `
box.cfg{listen = '127.0.0.1:0', log_level = 1}
box.schema.user.grant('guest', 'super')
box.schema.func.create('hello.add', {language = 'C'})
print('add', box.func['hello.add']:call({20, 22}))
print('mul', require('hello').mul(6, 7))
local popen = require('popen')
local uri = box.info.listen
local ph = popen.new({'`+program+`'}, {env = {TT_URI = uri}, stderr = popen.opts.PIPE})
print(ph:read({stderr = true, timeout = 30}))
print('exit', ph:wait().exit_code)
os.exit(0)
`)

	assert.Contains(t, printed, "add\t42\n")
	assert.Contains(t, printed, "mul\t42")
	assert.Contains(t, printed, "1 + 1 = 2\n")
	assert.Contains(t, printed, "exit\t0\n")
}
