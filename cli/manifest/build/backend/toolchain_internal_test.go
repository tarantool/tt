package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// The go and cargo backends are run here on real toolchains, on a program
// small enough to build in a second or two. go is there wherever these tests
// run; cargo may not be, and its tests are skipped then.

// writeFiles writes files, a map of a slash path under dir to its content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// goModule writes a Go module of one main package into a new directory and
// returns it. With cgo, the program calls into C, so that it is a cgo
// program indeed.
func goModule(t *testing.T, cgo bool) string {
	t.Helper()

	main := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"gw\") }\n"
	if cgo {
		main = "package main\n\n// int answer(void) { return 42; }\nimport \"C\"\n\n" +
			"import \"fmt\"\n\nfunc main() { fmt.Println(\"gw\", C.answer()) }\n"
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":         "module example.com/gw\n\ngo 1.22\n",
		"cmd/gw/main.go": main,
	})

	return dir
}

// inspectOutput inspects the program the build put into out under name.
func inspectOutput(t *testing.T, out, name string) native.Program {
	t.Helper()

	prog, err := native.InspectProgram(filepath.Join(out, name))
	require.NoError(t, err)
	assert.True(t, prog.Executable)
	assert.Equal(t, []native.Platform{native.Host()}, prog.Platforms)

	return prog
}

// runProgram runs the program at path and returns what it printed.
func runProgram(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), path).Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(out))
}

func TestGoBackendBuildsAStaticProgram(t *testing.T) {
	t.Parallel()

	cwd := goModule(t, false)
	out := filepath.Join(t.TempDir(), ".rocks", "bin")

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Name: "gateway"}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gateway")
	assert.True(t, prog.Static(), "a Go program without cgo is static: %+v", prog)
	assert.Equal(t, "gw", runProgram(t, filepath.Join(out, "gateway")))
}

func TestGoBackendBuildsAStaticCgoProgram(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}

	requireStaticLibc(t)

	cwd := goModule(t, true)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Cgo: on(), Static: on()}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gw")
	assert.True(t, prog.Static(), "%+v", prog)
	assert.Equal(t, "gw 42", runProgram(t, filepath.Join(out, "gw")))
}

// requireStaticLibc skips the test when there is no C compiler in PATH, or
// no static C library for it to link a static cgo program with: the cc
// driver prints the bare name of a library it cannot find.
func requireStaticLibc(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler in PATH for cgo")
	}

	out, err := exec.CommandContext(context.Background(),
		"cc", "-print-file-name=libc.a").Output()
	if err != nil || !filepath.IsAbs(strings.TrimSpace(string(out))) {
		t.Skip("no static C library for cc")
	}
}

// TestGoBackendCgoIsDynamicWithoutStatic shows what the static check of the
// build is for: cgo alone links the C library dynamically.
func TestGoBackendCgoIsDynamicWithoutStatic(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the loader is checked on linux")
	}

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler in PATH for cgo")
	}

	cwd := goModule(t, true)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Cgo: on()}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gw")
	assert.False(t, prog.Static(), "%+v", prog)
	assert.NotEmpty(t, prog.Interpreter)
}

func TestGoBackendFailureIsAnError(t *testing.T) {
	t.Parallel()

	cwd := goModule(t, false)
	writeFiles(t, cwd, map[string]string{"cmd/gw/broken.go": "package main\n\nfunc broken( {}\n"})

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw"}
	env := Env{OutputDir: t.TempDir(), Component: "gw"}

	require.Error(t, goBackend{}.Run(context.Background(), build, cwd, env))
}

// requireCargo skips the test when cargo is not in PATH, and, when target is
// set, when the Rust target is not installed.
func requireCargo(t *testing.T, target string) {
	t.Helper()

	_, err := exec.LookPath(cargoTool)
	if err != nil {
		t.Skip("cargo is not in PATH")
	}

	if target == "" {
		return
	}

	sysroot, err := exec.CommandContext(context.Background(),
		"rustc", "--print", "sysroot").Output()
	if err != nil {
		t.Skipf("rustc cannot tell its sysroot: %s", err)
	}

	lib := filepath.Join(strings.TrimSpace(string(sysroot)), "lib", "rustlib", target)

	_, err = os.Stat(lib)
	if err != nil {
		t.Skipf("the Rust target %s is not installed", target)
	}
}

// cargoCrate writes a crate without dependencies, which builds offline, and
// returns its directory.
func cargoCrate(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Cargo.toml": "[package]\nname = \"agent-crate\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[[bin]]\nname = \"agent\"\npath = \"src/main.rs\"\n\n" +
			"[dependencies]\n",
		"src/main.rs": "fn main() { println!(\"agent\"); }\n",
	})

	return dir
}

func TestCargoBackendBuildsTheBinary(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	cwd := cargoCrate(t)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendCargo, Bin: "agent", Profile: "dev"}
	env := Env{OutputDir: out, Component: "agent-component"}

	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))

	inspectOutput(t, out, "agent")
	assert.Equal(t, "agent", runProgram(t, filepath.Join(out, "agent")))
}

// cargoLibraryCrate writes a crate whose library is a Lua module without
// dependencies, which builds offline: require of it returns 42. Its symbol of
// Lua is left for Tarantool to resolve when it loads the module.
func cargoLibraryCrate(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Cargo.toml": "[package]\nname = \"agent-lib\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[dependencies]\n",
		"src/lib.rs": "use std::os::raw::{c_int, c_void};\n\n" +
			"extern \"C\" {\n    fn lua_pushinteger(l: *mut c_void, n: isize);\n}\n\n" +
			"#[no_mangle]\n" +
			"pub unsafe extern \"C\" fn luaopen_agent_lib(l: *mut c_void) -> c_int {\n" +
			"    lua_pushinteger(l, 42);\n    1\n}\n\n" +
			"#[no_mangle]\npub unsafe extern \"C\" fn luaopen_agent(l: *mut c_void) -> c_int {\n" +
			"    lua_pushinteger(l, 43);\n    1\n}\n",
	})

	return dir
}

// requireModule runs Tarantool in a directory whose .rocks holds the module
// file and returns what require of name gave; it skips the rest of the test
// without a tarantool in PATH.
func requireModule(t *testing.T, file, name string) string {
	t.Helper()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not in PATH: the module is not loaded")
	}

	app := t.TempDir()
	lib := filepath.Join(app, ".rocks", "lib", "tarantool")
	require.NoError(t, os.MkdirAll(lib, dirPerm))
	require.NoError(t, copyFile(file, filepath.Join(lib, filepath.Base(file))))

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, tarantool, "-e",
		"io.write(tostring(require('"+name+"'))) os.exit(0)")

	cmd.Dir = app

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return string(out)
}

func TestCargoBackendBuildsAModule(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	cwd := cargoLibraryCrate(t)
	out := t.TempDir()
	ext := moduleExt(runtime.GOOS, linuxModuleExt)
	env := Env{OutputDir: out, Component: "agent-component", Module: true}

	// The module is named after the library cargo builds, agent_lib.
	build := manifest.Build{Backend: BackendCargo, Profile: "dev"}
	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))

	module := filepath.Join(out, "agent_lib"+ext)
	assert.FileExists(t, module)

	// module renames it.
	build.Module = "agent"
	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))
	assert.FileExists(t, filepath.Join(out, "agent"+ext))

	assert.Equal(t, "42", requireModule(t, module, "agent_lib"))
	assert.Equal(t, "43", requireModule(t, filepath.Join(out, "agent"+ext), "agent"))
}

func TestCargoBackendBuildsAStaticBinary(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}

	arch, ok := cargoArches[runtime.GOARCH]
	if !ok {
		t.Skipf("no cargo target for %s", runtime.GOARCH)
	}

	requireCargo(t, arch+"-unknown-linux-musl")

	cwd := cargoCrate(t)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendCargo, Static: on()}
	env := Env{OutputDir: out, Component: "agent"}

	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))

	// A static-pie program has a dynamic segment of its own, and is still
	// static: no loader, no library.
	prog := inspectOutput(t, out, "agent")
	assert.True(t, prog.Static(), "%+v", prog)
	assert.Equal(t, "agent", runProgram(t, filepath.Join(out, "agent")))
}

// unsetCargoTargetDir clears the target directory tt's own environment may
// name, which a build inherits. A test calling it cannot run in parallel.
func unsetCargoTargetDir(t *testing.T) {
	t.Helper()

	for _, key := range cargoTargetDirKeys {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}

// TestCargoEnviron: a cargo build gets CARGO_TARGET_DIR of the project,
// unless the environment of the build names a target directory itself.
func TestCargoEnviron(t *testing.T) {
	unsetCargoTargetDir(t)

	env := Env{CargoTargetDir: "/proj/_build/cargo"}
	assert.Equal(t, "/proj/_build/cargo", lastEnvValue(cargoEnviron(env), "CARGO_TARGET_DIR"))

	for _, key := range cargoTargetDirKeys {
		env.Extra = map[string]string{key: "/mine"}

		got := cargoEnviron(env)
		assert.Equal(t, "/mine", lastEnvValue(got, key))
		assert.Equal(t, -1, indexOfEnv(got, "CARGO_TARGET_DIR=/proj/_build/cargo"), key)
	}

	// An empty one names none: cargo refuses it.
	env.Extra = map[string]string{"CARGO_TARGET_DIR": ""}
	assert.Equal(t, "/proj/_build/cargo", lastEnvValue(cargoEnviron(env), "CARGO_TARGET_DIR"))

	_, ok := lookupEnv(cargoEnviron(Env{}), "CARGO_TARGET_DIR")
	assert.False(t, ok, "no target directory of the project: cargo's own")

	t.Setenv("CARGO_TARGET_DIR", "/inherited")
	assert.Equal(t, "/inherited", lastEnvValue(cargoEnviron(Env{CargoTargetDir: "/proj"}),
		"CARGO_TARGET_DIR"))
}

// TestCargoBackendBuildsInTheTargetDir: cargo builds in the target directory
// of the project, leaving no target/ in the sources, and the backend finds
// what it built there, from scratch and when cargo has nothing to do.
func TestCargoBackendBuildsInTheTargetDir(t *testing.T) {
	requireCargo(t, "")
	unsetCargoTargetDir(t)

	targetDir := filepath.Join(t.TempDir(), "cargo")

	for _, crate := range []struct {
		dir   string
		build manifest.Build
		env   Env
		file  string
	}{
		{
			dir:   cargoCrate(t),
			build: manifest.Build{Backend: BackendCargo, Bin: "agent", Profile: "dev"},
			env:   Env{Component: "agent", CargoTargetDir: targetDir},
			file:  "agent",
		},
		{
			dir:   cargoLibraryCrate(t),
			build: manifest.Build{Backend: BackendCargo, Profile: "dev"},
			env:   Env{Component: "lib", Module: true, CargoTargetDir: targetDir},
			file:  "agent_lib" + moduleExt(runtime.GOOS, linuxModuleExt),
		},
	} {
		for range 2 {
			crate.env.OutputDir = t.TempDir()

			require.NoError(t, cargoBackend{}.Run(context.Background(), crate.build, crate.dir,
				crate.env))
			assert.FileExists(t, filepath.Join(crate.env.OutputDir, crate.file))
			assert.NoDirExists(t, filepath.Join(crate.dir, "target"))
			assert.DirExists(t, filepath.Join(targetDir, "debug"))
		}
	}
}

func TestCargoBackendUnknownBinIsAnError(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	build := manifest.Build{Backend: BackendCargo, Profile: "dev"}
	env := Env{OutputDir: t.TempDir(), Component: "missing"}

	require.Error(t, cargoBackend{}.Run(context.Background(), build, cargoCrate(t), env))
}
