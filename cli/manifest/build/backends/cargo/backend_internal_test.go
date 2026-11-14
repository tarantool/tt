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

// A build of cargo names its programs and checks what it needs first.
var (
	_ build.ProgramNamer = cargoBuild{}
	_ build.Preflighter  = cargoBuild{}
)

// program is the executable component agent, in the directory agent.
func program() build.Component {
	return build.Component{
		Name: "agent", Kind: build.KindExecutable, Path: "agent", Namespace: "", Package: "",
	}
}

// module is the module component lib, in the directory lib.
func module() build.Component {
	return build.Component{
		Name: "lib", Kind: build.KindModule, Path: "lib", Namespace: "demo", Package: "",
	}
}

// block is a build block of the fields rest, static as asked.
func block(rest map[string]any, static bool) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: nil, Static: static},
		Incremental: false,
		Rest:        rest,
	}
}

// validate checks the block of rest for component and returns its build.
func validate(t *testing.T, component build.Component, rest map[string]any,
	static bool,
) cargoBuild {
	t.Helper()

	bld, err := buildtest.Validate(Backend(), component, block(rest, static))
	require.NoError(t, err)

	checked, ok := bld.(cargoBuild)
	require.True(t, ok, "%T", bld)

	return checked
}

// writeTool writes the shell script body as cargo in dir. The script is
// written aside and copied with no fork in between, so that starting it is
// not refused while parallel tests start processes.
func writeTool(t *testing.T, dir, body string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), cargoTool)
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"+body), 0o600))
	require.NoError(t, os.Chmod(src, 0o700))
	require.NoError(t, build.CopyFile(filepath.Join(dir, cargoTool), src))
}

// noTargetDir names no target directory in the environment of the build of
// harness, whatever the environment of the test names: an empty one names
// none.
func noTargetDir(harness *buildtest.Harness) {
	for _, key := range targetDirKeys {
		harness.Context.Env[key] = ""
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	bld := validate(t, program(), map[string]any{
		"bin":      "agent-bin",
		"package":  "agent-crate",
		"features": []any{"tls", "serde/derive"},
		"profile":  "dist",
	}, true)

	assert.Equal(t, cargoBuild{
		config: Config{
			Module: "", Package: "agent-crate", Bin: "agent-bin",
			Features: []string{"tls", "serde/derive"}, Profile: "dist",
		},
		bin: "agent-bin", module: false, static: true,
	}, bld)

	names, exhaustive := bld.Programs()
	assert.Equal(t, []string{"agent-bin"}, names)
	assert.True(t, exhaustive)

	bld = validate(t, module(), map[string]any{"module": "fast"}, false)
	assert.Equal(t, cargoBuild{
		config: Config{Module: "fast", Package: "", Bin: "", Features: nil, Profile: ""},
		bin:    "lib", module: true, static: false,
	}, bld)
}

func TestProgramsDefaultToTheComponent(t *testing.T) {
	t.Parallel()

	names, exhaustive := validate(t, program(), nil, false).Programs()
	assert.Equal(t, []string{"agent"}, names)
	assert.True(t, exhaustive)
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component build.Component
		rest      map[string]any
		field     string
		msg       string
	}{
		{"a bin of a dot", program(), map[string]any{"bin": ".."},
			"bin", `".." is not a file name`},
		{"a bin with a slash", program(), map[string]any{"bin": "bin/agent"},
			"bin", `"bin/agent" is not a file name`},
		{"a dotted module", module(), map[string]any{"module": "a.b"},
			"module", `"a.b" is not a module name: a letter or _, then letters, digits and _`},
		{"a module of a digit", module(), map[string]any{"module": "1a"},
			"module", `"1a" is not a module name: a letter or _, then letters, digits and _`},
		{"two features in one", program(), map[string]any{"features": []any{"a,b"}},
			"features", `"a,b" is not a feature name`},
		{"an empty feature", program(), map[string]any{"features": []any{""}},
			"features", `"" is not a feature name`},
		{"a profile with a space", program(), map[string]any{"profile": "my dist"},
			"profile", `"my dist" is not a profile name`},
		{"module on a program", program(), map[string]any{"module": "a"},
			"module", `names the library of a module component; kind "executable" ` +
				"builds the binary bin"},
		{"bin on a module", module(), map[string]any{"bin": "a"},
			"bin", "names the binary of an executable component; a module component " +
				"builds the library of the package, named by module"},
		{"a bin of a number", program(), map[string]any{"bin": int64(1)},
			"bin", "must be a string, not an integer"},
		{"features of a string", program(), map[string]any{"features": "tls"},
			"features", "must be an array, not a string"},
		{"a feature of a table", program(), map[string]any{
			"features": []any{map[string]any{}},
		}, "features[0]", "must be a string, not a table"},
		{"an unknown field", program(), map[string]any{"cgo": true},
			"cgo", "unknown field"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildtest.Validate(Backend(), test.component, block(test.rest, false))

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
	}
}

func TestBackend(t *testing.T) {
	t.Parallel()

	backend := Backend()

	assert.Equal(t, Name, backend.Name)
	assert.Equal(t, []string{build.KindModule, build.KindExecutable}, backend.Kinds)
	assert.Equal(t, build.OutputNone, backend.OutputFrom)
	assert.Equal(t, []string{"/target/"}, backend.Exclude)
	assert.False(t, backend.Incremental)
	assert.False(t, backend.Hook)

	fields, err := backend.Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"module", "package", "bin", "features", "profile"}, fields)
}

func TestPreflight(t *testing.T) {
	t.Parallel()

	darwin := build.Target{OS: darwinOS, Arch: "arm64"}

	t.Run("a static program for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, program())
		writeTool(t, harness.Context.Dirs.Bin, "")

		harness.Context.Target = darwin

		err := validate(t, program(), nil, true).Preflight(harness.Context)
		require.ErrorIs(t, err, ErrStaticDarwin)
		assert.Contains(t, err.Error(), "static = true needs a linux build")
	})

	// A static module is only checked after the build, and on macOS one that
	// needs libSystem alone passes.
	t.Run("a static module for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, module())
		writeTool(t, harness.Context.Dirs.Bin, "")

		harness.Context.Target = darwin

		require.NoError(t,
			validate(t, module(), nil, true).Preflight(harness.Context))
	})

	t.Run("a platform cargo has no target for", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, program())
		writeTool(t, harness.Context.Dirs.Bin, "")

		harness.Context.Target = build.Target{OS: "linux", Arch: "s390x"}

		err := validate(t, program(), nil, false).Preflight(harness.Context)
		require.ErrorIs(t, err, errUnsupportedTarget)
	})

	t.Run("a missing tool", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, program())

		harness.Context.Env["PATH"] = t.TempDir()

		err := harness.Run(t.Context(), validate(t, program(), nil, false))
		require.ErrorIs(t, err, build.ErrNoTool)
		assert.Contains(t, err.Error(), `the cargo backend runs "cargo", which is not in PATH`)
	})

	t.Run("a tool of the project", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, program())

		harness.Context.Env["PATH"] = t.TempDir()
		writeTool(t, harness.Context.Dirs.Bin, "")

		require.NoError(t,
			validate(t, program(), nil, false).Preflight(harness.Context))
	})
}

// fakeCargo writes a cargo into the bin directory of harness that records
// its working directory, its arguments and the target directory it was
// given, at <state>/cargo.args and <state>/cargo.env, makes a binary agent
// and a library agent_lib in the target directory, and reports both as
// cargo does, among a line of a build script; it fails when FAKE_FAIL is
// set.
func fakeCargo(t *testing.T, harness *buildtest.Harness) string {
	t.Helper()

	log := filepath.Join(harness.Context.Dirs.State, "cargo")

	writeTool(t, harness.Context.Dirs.Bin, `[ -n "$FAKE_FAIL" ] && exit 1
printf '%s\n' "$PWD" "$@" > "`+log+`.args"
printf '%s\n' "${CARGO_TARGET_DIR-unset}" > "`+log+`.env"
dir=${CARGO_TARGET_DIR:-$CARGO_BUILD_TARGET_DIR}
mkdir -p "$dir"
echo bin > "$dir/agent"
echo lib > "$dir/libagent_lib.so"
echo 'a build script says hi'
echo '{"reason":"compiler-artifact","target":{"kind":["bin"],"name":"agent"},'\
'"executable":"'"$dir"'/agent"}'
echo '{"reason":"compiler-artifact","target":{"kind":["cdylib"],"name":"agent_lib"},'\
'"filenames":["'"$dir"'/libagent_lib.so"]}'
echo 'compiling' >&2
`)

	return log
}

// readLines reads the lines of the file at path.
func readLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestRunCopiesTheBinary: cargo build of the binary is run in the working
// directory, in the target directory of the project, and the binary it
// reports is copied into the output directory under its bin name.
func TestRunCopiesTheBinary(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, program())
	facts := harness.Context
	log := fakeCargo(t, harness)

	noTargetDir(harness)

	require.NoError(t, harness.Run(t.Context(), validate(t, program(),
		map[string]any{"profile": "dev", "package": "agent-crate"}, false)))

	cwd, err := filepath.EvalSymlinks(facts.Dirs.Cwd)
	require.NoError(t, err)
	assert.Equal(t, []string{
		cwd, "build", "--profile", "dev", "--bin", "agent",
		"--message-format=json-render-diagnostics", "--package", "agent-crate",
	}, readLines(t, log+".args"))

	targetDir := filepath.Join(facts.Dirs.State, targetDirName)
	assert.Equal(t, []string{targetDir}, readLines(t, log+".env"))

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Output, "agent"))
	require.NoError(t, err)
	assert.Equal(t, "bin\n", string(data))

	assert.Equal(t, "compiling\n", harness.Printed(), "stdout is read, not shown")
}

// TestRunCopiesTheLibrary: the library of a module component is copied
// under the name of the library cargo reports, or under module.
func TestRunCopiesTheLibrary(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, module())
	facts := harness.Context
	log := fakeCargo(t, harness)

	noTargetDir(harness)

	require.NoError(t, harness.Run(t.Context(), validate(t, module(), nil, false)))

	want := []string{
		"rustc", "--lib", "--crate-type", "cdylib", "--profile", "release",
		"--message-format=json-render-diagnostics",
	}
	if runtime.GOOS == darwinOS {
		want = append(want, "--", "-C", "link-arg=-undefined", "-C", "link-arg=dynamic_lookup")
	}

	assert.Equal(t, want, readLines(t, log+".args")[1:])

	ext := facts.Target.ModuleExt()

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Output, "agent_lib"+ext))
	require.NoError(t, err)
	assert.Equal(t, "lib\n", string(data))

	require.NoError(t, harness.Run(t.Context(), validate(t, module(),
		map[string]any{"module": "agent"}, false)))
	assert.FileExists(t, filepath.Join(facts.Dirs.Output, "agent"+ext))
}

// TestRunKeepsTheTargetDirOfTheEnvironment: a target directory the
// environment of the build names is the user's choice and is kept.
func TestRunKeepsTheTargetDirOfTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, key := range targetDirKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, program())
			log := fakeCargo(t, harness)
			mine := t.TempDir()

			noTargetDir(harness)

			harness.Context.Env[key] = mine

			require.NoError(t, harness.Run(t.Context(), validate(t, program(), nil, false)))

			// CARGO_TARGET_DIR is left as the environment has it: mine, or
			// set to nothing by noTargetDir beside CARGO_BUILD_TARGET_DIR.
			want := ""
			if key == "CARGO_TARGET_DIR" {
				want = mine
			}

			assert.Equal(t, []string{want}, readLines(t, log+".env"))
			assert.FileExists(t, filepath.Join(mine, "agent"))
			assert.NoDirExists(t, filepath.Join(harness.Context.Dirs.State, targetDirName))
		})
	}
}

// TestRunOfAStaticBinaryNamesTheTarget: a static build that fails says
// which Rust target it needs.
func TestRunOfAStaticBinaryNamesTheTarget(t *testing.T) {
	t.Parallel()

	arch, ok := cargoArches[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		t.Skip("a static program is built for linux only")
	}

	harness := buildtest.New(t, program())
	fakeCargo(t, harness)

	harness.Context.Env["FAKE_FAIL"] = "1"

	err := harness.Run(t.Context(), validate(t, program(), nil, true))
	require.Error(t, err)

	target := arch + "-unknown-linux-musl"
	assert.Contains(t, err.Error(), "a static build is made for the target "+target+
		", which rustup target add "+target+" installs")
}

// TestRunOfAnUnknownBinIsAnError: a binary cargo does not report fails the
// build.
func TestRunOfAnUnknownBinIsAnError(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, program())
	fakeCargo(t, harness)

	err := harness.Run(t.Context(), validate(t, program(), map[string]any{"bin": "missing"},
		false))
	require.ErrorIs(t, err, errNoArtifact)
}

// The backend is run below on the real cargo, on crates small enough to
// build in a second or two. cargo may not be in PATH, and the tests are
// skipped then.

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

// writeFiles writes files, a map of a slash path under dir to its content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// crateHarness returns a harness of component whose directory holds a crate
// without dependencies, which builds offline, and names no target directory
// in its environment: the build builds in that of the project.
func crateHarness(t *testing.T, component build.Component) *buildtest.Harness {
	t.Helper()

	harness := buildtest.New(t, component)

	noTargetDir(harness)

	if component.Kind == build.KindExecutable {
		writeFiles(t, harness.Context.Dirs.Component, map[string]string{
			"Cargo.toml": "[package]\nname = \"agent-crate\"\nversion = \"0.1.0\"\n" +
				"edition = \"2021\"\n\n[[bin]]\nname = \"agent\"\npath = \"src/main.rs\"\n\n" +
				"[dependencies]\n",
			"src/main.rs": "fn main() { println!(\"agent\"); }\n",
		})

		return harness
	}

	// A library that is a Lua module: require of it returns 42. Its symbol
	// of Lua is left for Tarantool to resolve when it loads the module.
	writeFiles(t, harness.Context.Dirs.Component, map[string]string{
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

	return harness
}

// runProgram runs the program at path and returns what it printed.
func runProgram(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), path).Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(out))
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
	require.NoError(t, build.CopyFile(filepath.Join(lib, filepath.Base(file)), file))

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, tarantool, "-e",
		"io.write(tostring(require('"+name+"'))) os.exit(0)")

	cmd.Dir = app

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return string(out)
}

func TestBuildsTheBinary(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	harness := crateHarness(t, program())

	require.NoError(t, harness.Run(t.Context(), validate(t, program(),
		map[string]any{"profile": "dev"}, false)))
	assert.Equal(t, "agent", runProgram(t, filepath.Join(harness.Context.Dirs.Output, "agent")))
}

func TestBuildsAModule(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	harness := crateHarness(t, module())
	out := harness.Context.Dirs.Output
	ext := harness.Context.Target.ModuleExt()

	// The module is named after the library cargo builds, agent_lib.
	require.NoError(t, harness.Run(t.Context(), validate(t, module(),
		map[string]any{"profile": "dev"}, false)))

	library := filepath.Join(out, "agent_lib"+ext)
	assert.FileExists(t, library)

	// module renames it.
	require.NoError(t, harness.Run(t.Context(), validate(t, module(),
		map[string]any{"profile": "dev", "module": "agent"}, false)))
	assert.FileExists(t, filepath.Join(out, "agent"+ext))

	assert.Equal(t, "42", requireModule(t, library, "agent_lib"))
	assert.Equal(t, "43", requireModule(t, filepath.Join(out, "agent"+ext), "agent"))
}

func TestBuildsAStaticBinary(t *testing.T) {
	t.Parallel()

	arch, ok := cargoArches[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		t.Skip("a static program is built for linux only")
	}

	requireCargo(t, arch+"-unknown-linux-musl")

	harness := crateHarness(t, program())

	require.NoError(t, harness.Run(t.Context(), validate(t, program(), nil, true)))

	path := filepath.Join(harness.Context.Dirs.Output, "agent")
	assert.Equal(t, "agent", runProgram(t, path))

	// A static-pie program has a dynamic segment of its own, and is still
	// static: no loader, no library.
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

// TestBuildsInTheTargetDir: cargo builds in the target directory of the
// project, leaving no target/ in the sources, and the backend finds what it
// built there, from scratch and when cargo has nothing to do.
func TestBuildsInTheTargetDir(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	for _, component := range []build.Component{program(), module()} {
		harness := crateHarness(t, component)
		facts := harness.Context

		file := "agent"
		if component.Kind == build.KindModule {
			file = "agent_lib" + facts.Target.ModuleExt()
		}

		for range 2 {
			require.NoError(t, os.RemoveAll(facts.Dirs.Output))
			require.NoError(t, harness.Run(t.Context(), validate(t, component,
				map[string]any{"profile": "dev"}, false)))
			assert.FileExists(t, filepath.Join(facts.Dirs.Output, file))
			assert.NoDirExists(t, filepath.Join(facts.Dirs.Component, "target"))
			assert.DirExists(t, filepath.Join(facts.Dirs.State, targetDirName, "debug"))
		}
	}
}
