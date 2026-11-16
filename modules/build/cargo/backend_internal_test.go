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

// A build of cargo says what it makes, names its programs and checks what it
// needs first.
var (
	_ build.Producer     = cargoBuild{}
	_ build.ProgramNamer = cargoBuild{}
	_ build.Preflighter  = cargoBuild{}
)

// program is the component agent, in the directory agent, which wants
// programs only, as kind = "executable" of manifest_version 0.1 does.
func program() build.Component {
	return build.Component{
		Name: "agent", Path: "agent", Namespace: "", Package: "",
		Wants: []build.Result{build.Program},
	}
}

// module is the component lib, in the directory lib, which wants nothing.
func module() build.Component {
	return build.Component{
		Name: "lib", Path: "lib", Namespace: "demo", Package: "", Wants: nil,
	}
}

// both is the component both, in the directory both, which wants nothing,
// whose crate has a library and a binary.
func both() build.Component {
	return build.Component{
		Name: "both", Path: "both", Namespace: "demo", Package: "", Wants: nil,
	}
}

// block is a build block of the fields rest, static as asked.
func block(rest map[string]any, static bool) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: nil, Static: static},
		Incremental: false,
		Fields:      rest,
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
	assert.Equal(t, []build.Result{build.Program}, bld.Produces())

	bld = validate(t, module(), map[string]any{"module": "fast"}, false)
	assert.Equal(t, cargoBuild{
		config: Config{Module: "fast", Package: "", Bin: "", Features: nil, Profile: ""},
		bin:    "", module: true, static: false,
	}, bld)
	assert.Equal(t, []build.Result{build.Module}, bld.Produces())

	names, exhaustive = bld.Programs()
	assert.Empty(t, names)
	assert.True(t, exhaustive)
}

// TestWhatTheBlockBuilds checks that the fields of the block say what the
// build makes: module the module, bin the binary, both of them both, and
// neither the library under its own name - or, for a component of the
// deprecated kind = "executable", the binary named after the component.
func TestWhatTheBlockBuilds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component build.Component
		rest      map[string]any
		module    bool
		programs  []string
	}{
		{"neither", module(), nil, true, nil},
		{"module", module(), map[string]any{"module": "fast"}, true, nil},
		{"bin", module(), map[string]any{"bin": "fastctl"}, false, []string{"fastctl"}},
		{"both", module(), map[string]any{"module": "fast", "bin": "fastctl"}, true,
			[]string{"fastctl"}},
		{"neither for programs", program(), nil, false, []string{"agent"}},
		{"bin for programs", program(), map[string]any{"bin": "agentd"}, false,
			[]string{"agentd"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bld := validate(t, test.component, test.rest, false)
			assert.Equal(t, test.module, bld.module)

			names, _ := bld.Programs()
			assert.Equal(t, test.programs, names)

			var results []build.Result

			if test.module {
				results = append(results, build.Module)
			}

			if test.programs != nil {
				results = append(results, build.Program)
			}

			assert.Equal(t, results, bld.Produces())
		})
	}
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
		{"module for programs", program(), map[string]any{"module": "a"},
			"", "the build makes a module, which the component does not want"},
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
	assert.Equal(t, []build.Result{build.Module, build.Program}, backend.Produces)
	assert.False(t, backend.NoSourceLayout)
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

		harness := buildtest.New(t, Backend(), program())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = darwin

		err := validate(t, program(), nil, true).Preflight(t.Context(), harness.Context)
		require.ErrorIs(t, err, ErrStaticDarwin)
		assert.Contains(t, err.Error(), "static = true needs a linux build")
	})

	// A static module is only checked after the build, and on macOS one that
	// needs libSystem alone passes.
	t.Run("a static module for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), module())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = darwin

		require.NoError(t,
			validate(t, module(), nil, true).Preflight(t.Context(), harness.Context))
	})

	t.Run("a platform cargo has no target for", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), program())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = build.Target{OS: "linux", Arch: "s390x"}

		err := validate(t, program(), nil, false).Preflight(t.Context(), harness.Context)
		require.ErrorIs(t, err, errUnsupportedTarget)
	})

	t.Run("a missing tool", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), program())

		harness.Context.Env["PATH"] = t.TempDir()

		err := harness.Run(t.Context(), validate(t, program(), nil, false))
		require.ErrorIs(t, err, build.ErrNoTool)
		assert.Contains(t, err.Error(), `the cargo backend runs "cargo", which is not in PATH`)
	})

	t.Run("a tool of the project", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), program())

		harness.Context.Env["PATH"] = t.TempDir()
		writeTool(t, harness.Context.Dirs.Tools, "")

		require.NoError(t,
			validate(t, program(), nil, false).Preflight(t.Context(), harness.Context))
	})
}

// fakeCargo writes a cargo into the tools directory of harness that records
// its working directory, its arguments and the target directory it was
// given, at <state>/cargo.args and <state>/cargo.env, and the arguments of
// every run, one run a line, at <state>/cargo.runs, makes a binary agent
// and a library agent_lib in the target directory, and reports both as
// cargo does, among a line of a build script; it fails when FAKE_FAIL is
// set.
func fakeCargo(t *testing.T, harness *buildtest.Harness) string {
	t.Helper()

	log := filepath.Join(harness.Context.Dirs.State, "cargo")

	writeTool(t, harness.Context.Dirs.Tools, `[ -n "$FAKE_FAIL" ] && exit 1
printf '%s\n' "$PWD" "$@" > "`+log+`.args"
printf '%s\n' "$*" >> "`+log+`.runs"
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
// reports is copied into the directory of the programs under its bin name.
func TestRunCopiesTheBinary(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), program())
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

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Programs, "agent"))
	require.NoError(t, err)
	assert.Equal(t, "bin\n", string(data))
	assert.NoFileExists(t, filepath.Join(facts.Dirs.Modules, "agent"))

	assert.Equal(t, "compiling\n", harness.Printed(), "stdout is read, not shown")
}

// TestRunJobs: the jobs of the build reach every run of cargo as -j, that
// of the library and that of the binary, and none is given without them.
func TestRunJobs(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), both())
	log := fakeCargo(t, harness)
	bld := validate(t, both(), map[string]any{"module": "fast", "bin": "agent"}, false)

	noTargetDir(harness)

	require.NoError(t, harness.Run(t.Context(), bld))

	for _, run := range readLines(t, log+".runs") {
		assert.NotContains(t, strings.Fields(run), "-j", run)
	}

	require.NoError(t, os.Remove(log+".runs"))

	harness.SetJobs(4)

	require.NoError(t, harness.Run(t.Context(), bld))

	runs := readLines(t, log+".runs")
	require.Len(t, runs, 2)
	assert.True(t, strings.HasPrefix(runs[0], "rustc "), runs[0])
	assert.True(t, strings.HasPrefix(runs[1], "build "), runs[1])

	for _, run := range runs {
		assert.Contains(t, run, " -j 4", run)
	}
}

// TestRunCopiesTheLibraryAndTheBinary: a block that names module and bin
// builds the library, into the directory of the modules, then the binary,
// into that of the programs; a static one builds the binary alone for musl.
func TestRunCopiesTheLibraryAndTheBinary(t *testing.T) {
	t.Parallel()

	for _, static := range []bool{false, true} {
		// A static program is built for linux alone: macOS has no static
		// libSystem to link it against.
		if static && runtime.GOOS == darwinOS {
			continue
		}

		harness := buildtest.New(t, Backend(), both())
		facts := harness.Context
		log := fakeCargo(t, harness)

		noTargetDir(harness)

		require.NoError(t, harness.Run(t.Context(), validate(t, both(),
			map[string]any{"module": "agent_mod", "bin": "agent", "profile": "dev"}, static)))

		ext := facts.Target.ModuleExt()

		assert.FileExists(t, filepath.Join(facts.Dirs.Modules, "agent_mod"+ext))
		assert.FileExists(t, filepath.Join(facts.Dirs.Programs, "agent"))
		assert.NoFileExists(t, filepath.Join(facts.Dirs.Programs, "agent_mod"+ext))
		assert.NoFileExists(t, filepath.Join(facts.Dirs.Modules, "agent"))

		runs := readLines(t, log+".runs")
		require.Len(t, runs, 2)
		assert.True(t, strings.HasPrefix(runs[0], "rustc --lib --crate-type cdylib"), runs[0])
		assert.NotContains(t, runs[0], "--target")
		assert.True(t, strings.HasPrefix(runs[1], "build --profile dev --bin agent"), runs[1])

		arch, ok := cargoArches[runtime.GOARCH]
		if static && runtime.GOOS == "linux" && ok {
			assert.Contains(t, runs[1], "--target "+arch+"-unknown-linux-musl")
		} else {
			assert.NotContains(t, runs[1], "--target")
		}
	}
}

// TestRunCopiesTheLibrary: the library of a module component is copied
// under the name of the library cargo reports, or under module.
func TestRunCopiesTheLibrary(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), module())
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

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Modules, "agent_lib"+ext))
	require.NoError(t, err)
	assert.Equal(t, "lib\n", string(data))

	require.NoError(t, harness.Run(t.Context(), validate(t, module(),
		map[string]any{"module": "agent"}, false)))
	assert.FileExists(t, filepath.Join(facts.Dirs.Modules, "agent"+ext))
}

// TestRunKeepsTheTargetDirOfTheEnvironment: a target directory the
// environment of the build names is the user's choice and is kept.
func TestRunKeepsTheTargetDirOfTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, key := range targetDirKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, Backend(), program())
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

	harness := buildtest.New(t, Backend(), program())
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

	harness := buildtest.New(t, Backend(), program())
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

	harness := buildtest.New(t, Backend(), component)

	noTargetDir(harness)

	binary := map[string]string{
		"Cargo.toml": "[package]\nname = \"agent-crate\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[[bin]]\nname = \"agent\"\npath = \"src/main.rs\"\n\n" +
			"[dependencies]\n",
		"src/main.rs": "fn main() { println!(\"agent\"); }\n",
	}

	// A library that is a Lua module: require of it returns 42. Its symbol
	// of Lua is left for Tarantool to resolve when it loads the module.
	library := map[string]string{
		"Cargo.toml": "[package]\nname = \"agent-lib\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[dependencies]\n",
		"src/lib.rs": "use std::os::raw::{c_int, c_void};\n\n" +
			"extern \"C\" {\n    fn lua_pushinteger(l: *mut c_void, n: isize);\n}\n\n" +
			"#[no_mangle]\n" +
			"pub unsafe extern \"C\" fn luaopen_agent_lib(l: *mut c_void) -> c_int {\n" +
			"    lua_pushinteger(l, 42);\n    1\n}\n\n" +
			"#[no_mangle]\npub unsafe extern \"C\" fn luaopen_agent(l: *mut c_void) -> c_int {\n" +
			"    lua_pushinteger(l, 43);\n    1\n}\n",
	}

	switch component.Name {
	case program().Name:
		writeFiles(t, harness.Context.Dirs.Component, binary)
	case both().Name:
		// One crate of both: the library and the binary agent.
		writeFiles(t, harness.Context.Dirs.Component, library)
		writeFiles(t, harness.Context.Dirs.Component, map[string]string{
			"Cargo.toml": "[package]\nname = \"agent-lib\"\nversion = \"0.1.0\"\n" +
				"edition = \"2021\"\n\n[[bin]]\nname = \"agent\"\npath = \"src/main.rs\"\n\n" +
				"[dependencies]\n",
			"src/main.rs": binary["src/main.rs"],
		})
	default:
		writeFiles(t, harness.Context.Dirs.Component, library)
	}

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
	assert.Equal(t, "agent", runProgram(t, filepath.Join(harness.Context.Dirs.Programs, "agent")))
}

// TestBuildsAModuleAndABinary builds one crate of a library and a binary
// with one block, which names both: the module Tarantool loads and the
// program it runs.
func TestBuildsAModuleAndABinary(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	harness := crateHarness(t, both())
	dirs := harness.Context.Dirs
	ext := harness.Context.Target.ModuleExt()

	require.NoError(t, harness.Run(t.Context(), validate(t, both(),
		map[string]any{"profile": "dev", "module": "agent_lib", "bin": "agent"}, false)))

	library := filepath.Join(dirs.Modules, "agent_lib"+ext)
	assert.FileExists(t, library)
	assert.Equal(t, "agent", runProgram(t, filepath.Join(dirs.Programs, "agent")))
	assert.NoFileExists(t, filepath.Join(dirs.Modules, "agent"))
	assert.NoDirExists(t, filepath.Join(dirs.Component, "target"))
	assert.Equal(t, "42", requireModule(t, library, "agent_lib"))
}

func TestBuildsAModule(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	harness := crateHarness(t, module())
	out := harness.Context.Dirs.Modules
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

	path := filepath.Join(harness.Context.Dirs.Programs, "agent")
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

		file := filepath.Join(facts.Dirs.Programs, "agent")
		if component.Wants == nil {
			file = filepath.Join(facts.Dirs.Modules, "agent_lib"+facts.Target.ModuleExt())
		}

		for range 2 {
			require.NoError(t, os.RemoveAll(filepath.Dir(file)))
			require.NoError(t, harness.Run(t.Context(), validate(t, component,
				map[string]any{"profile": "dev"}, false)))
			assert.FileExists(t, file)
			assert.NoDirExists(t, filepath.Join(facts.Dirs.Component, "target"))
			assert.DirExists(t, filepath.Join(facts.Dirs.State, targetDirName, "debug"))
		}
	}
}
