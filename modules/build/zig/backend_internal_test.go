package zig

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// A build of zig says what it makes, names its programs, checks what it
// needs first and names what else an incremental build reads.
var (
	_ build.Producer     = zigBuild{}
	_ build.ProgramNamer = zigBuild{}
	_ build.Preflighter  = zigBuild{}
	_ build.InputHasher  = zigBuild{}
)

// component is the component hello, in the directory hello, of no kind.
func component() build.Component {
	return build.Component{
		Name: "hello", Path: "hello", Namespace: "demo", Package: "", Expects: nil,
	}
}

// program is the component agent, in the directory agent, of the deprecated
// kind = "executable": it expects programs only.
func program() build.Component {
	return build.Component{
		Name: "agent", Path: "agent", Namespace: "", Package: "",
		Expects: []build.Result{build.Program},
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
) zigBuild {
	t.Helper()

	bld, err := buildtest.Validate(Backend(), component, block(rest, static))
	require.NoError(t, err)

	checked, ok := bld.(zigBuild)
	require.True(t, ok, "%T", bld)

	return checked
}

// writeTool writes the shell script body as zig in dir. The script is
// written aside and copied with no fork in between, so that starting it is
// not refused while parallel tests start processes.
func writeTool(t *testing.T, dir, body string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), zigTool)
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"+body), 0o600))
	require.NoError(t, os.Chmod(src, 0o700))
	require.NoError(t, build.CopyFile(filepath.Join(dir, zigTool), src))
}

// fakeZig writes a zig into the tools directory of harness. zig version
// prints FAKE_ZIG_VERSION, 0.17.1 when it is not set; zig build --help prints
// the options of a build.zig, with -Dtarget unless FAKE_NO_TARGET is set;
// zig build records its working directory and its arguments at
// <state>/zig.args and the arguments of every run, one run a line, at
// <state>/zig.runs, and installs into the prefix of its -p the modules
// hello.so, libplain.so and a/b.dylib and the program hello-client, saying so
// on stderr; but for zig version, it fails when FAKE_FAIL is set.
func fakeZig(t *testing.T, harness *buildtest.Harness) string {
	t.Helper()

	log := filepath.Join(harness.Context.Dirs.State, "zig")

	writeTool(t, harness.Context.Dirs.Tools, `if [ "$1" = version ]; then
	echo "${FAKE_ZIG_VERSION:-0.17.1}"
	exit 0
fi
[ -n "$FAKE_FAIL" ] && exit 1
printf '%s\n' "$*" >> "`+log+`.runs"
case " $* " in *" --help "*)
	echo '  -Doptimize=[enum]            Prioritize performance, safety, or binary size'
	[ -z "$FAKE_NO_TARGET" ] &&
		echo '  -Dtarget=[string]            The CPU architecture, OS, and ABI to build for'
	exit 0;;
esac
printf '%s\n' "$PWD" "$@" > "`+log+`.args"
prefix=
while [ $# -gt 0 ]; do
	[ "$1" = -p ] && prefix=$2
	shift
done
mkdir -p "$prefix/lib/a" "$prefix/bin"
echo hello > "$prefix/lib/hello.so"
echo plain > "$prefix/lib/libplain.so"
echo ab > "$prefix/lib/a/b.dylib"
echo client > "$prefix/bin/hello-client"
echo 'installing' >&2
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

func TestValidate(t *testing.T) {
	t.Parallel()

	bld := validate(t, component(), map[string]any{
		"module":   "hello",
		"bin":      "hello-client",
		"steps":    []any{"install", "docs"},
		"optimize": "ReleaseFast",
		"options":  map[string]any{"verify-cabi": "true"},
	}, true)

	assert.Equal(t, zigBuild{
		config: Config{
			Module: "hello", Bin: "hello-client", Steps: []string{"install", "docs"},
			Optimize: "ReleaseFast", Options: map[string]string{"verify-cabi": "true"},
		},
		module: "hello", bin: "hello-client", static: true,
	}, bld)

	names, exhaustive := bld.Programs()
	assert.Equal(t, []string{"hello-client"}, names)
	assert.True(t, exhaustive)
	assert.Equal(t, []build.Result{build.Module, build.Program}, bld.Produces())
}

// TestWhatTheBlockBuilds checks that the fields of the block say what the
// build makes: module the module, bin the program, both of them both, and,
// for a component of the deprecated kind = "executable", neither the program
// named after the component.
func TestWhatTheBlockBuilds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		component build.Component
		rest      map[string]any
		module    string
		programs  []string
	}{
		{"module", component(), map[string]any{"module": "hello"}, "hello", nil},
		{"a dotted module", component(), map[string]any{"module": "a.b_c"}, "a.b_c", nil},
		{"bin", component(), map[string]any{"bin": "hello-client"}, "",
			[]string{"hello-client"}},
		{"both", component(), map[string]any{"module": "hello", "bin": "hello-client"},
			"hello", []string{"hello-client"}},
		{"neither of kind = executable", program(), nil, "", []string{"agent"}},
		{"bin of kind = executable", program(), map[string]any{"bin": "agentd"}, "",
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

			if test.module != "" {
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

	const moduleMsg = " is not a module name: names of a letter or _, then letters, digits " +
		"and _, joined by dots"

	tests := []struct {
		name      string
		component build.Component
		rest      map[string]any
		field     string
		msg       string
	}{
		{"neither module nor bin", component(), nil,
			"", "names neither module nor bin: the build is to name what it makes, " +
				"the module, the program or both"},
		{"a module of a digit", component(), map[string]any{"module": "1a"},
			"module", `"1a"` + moduleMsg},
		{"a module of an empty part", component(), map[string]any{"module": "a..b"},
			"module", `"a..b"` + moduleMsg},
		{"a module with a dash", component(), map[string]any{"module": "hello-zig"},
			"module", `"hello-zig"` + moduleMsg},
		{"a bin of a dot", component(), map[string]any{"bin": ".."},
			"bin", `".." is not a file name`},
		{"a bin with a slash", component(), map[string]any{"bin": "bin/agent"},
			"bin", `"bin/agent" is not a file name`},
		{"a step of an option", component(), map[string]any{
			"module": "hello", "steps": []any{"install", "-Dx=1"},
		}, "steps[1]", `"-Dx=1" is not a step name`},
		{"a step of two words", component(), map[string]any{
			"module": "hello", "steps": []any{"install docs"},
		}, "steps[0]", `"install docs" is not a step name`},
		{"an empty step", component(), map[string]any{
			"module": "hello", "steps": []any{""},
		}, "steps[0]", `"" is not a step name`},
		{"an unknown optimize", component(), map[string]any{
			"module": "hello", "optimize": "release",
		}, "optimize", `"release" is not Debug, ReleaseSafe, ReleaseFast or ReleaseSmall`},
		{"an option with =", component(), map[string]any{
			"module": "hello", "options": map[string]any{"a=b": "1"},
		}, "options", `"a=b" is not an option name`},
		{"an option of optimize", component(), map[string]any{
			"module": "hello", "options": map[string]any{"optimize": "Debug"},
		}, "options.optimize", "is the field optimize of the block"},
		{"module of kind = executable", program(), map[string]any{"module": "a"},
			"", "the build makes a module, which the component does not expect"},
		{"steps of a string", component(), map[string]any{
			"module": "hello", "steps": "install",
		}, "steps", "must be an array, not a string"},
		{"an option of a boolean", component(), map[string]any{
			"module": "hello", "options": map[string]any{"verify": true},
		}, "options.verify", "must be a string, not a boolean"},
		{"an unknown field", component(), map[string]any{"module": "hello", "profile": "dev"},
			"profile", "unknown field"},
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
	assert.Equal(t, []string{"/zig-out/", "/zig-pkg/"}, backend.Exclude)
	assert.True(t, backend.Incremental)
	assert.False(t, backend.Hook)

	fields, err := backend.Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"module", "bin", "steps", "optimize", "options"}, fields)

	// incremental = true is a field of the backend.
	_, err = buildtest.Validate(backend, component(), buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: nil, Static: false},
		Incremental: true,
		Rest:        map[string]any{"module": "hello"},
	})
	require.NoError(t, err)
}

func TestPreflight(t *testing.T) {
	t.Parallel()

	darwin := build.Target{OS: darwinOS, Arch: "arm64"}

	t.Run("a static program for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = darwin

		err := validate(t, component(), map[string]any{"bin": "hello-client"}, true).
			Preflight(t.Context(), harness.Context)
		require.ErrorIs(t, err, ErrStaticDarwin)
		assert.Contains(t, err.Error(), "static = true needs a linux build")
	})

	// A static module is only checked after the build, and on macOS one that
	// needs libSystem alone passes.
	t.Run("a static module for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = darwin

		require.NoError(t, validate(t, component(), map[string]any{"module": "hello"}, true).
			Preflight(t.Context(), harness.Context))
	})

	t.Run("a missing tool", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())

		harness.Context.Env["PATH"] = t.TempDir()

		err := harness.Run(t.Context(), validate(t, component(),
			map[string]any{"module": "hello"}, false))
		require.ErrorIs(t, err, build.ErrNoTool)
		assert.Contains(t, err.Error(), `the zig backend runs "zig", which is not in PATH`)
	})

	t.Run("a tool of the project", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())

		harness.Context.Env["PATH"] = t.TempDir()
		writeTool(t, harness.Context.Dirs.Tools, "")

		require.NoError(t, validate(t, component(), map[string]any{"module": "hello"}, false).
			Preflight(t.Context(), harness.Context))
	})
}

// TestRunCopiesTheModuleAndTheProgram: zig build is run in the working
// directory with the prefix and the cache in the work directory, the steps,
// the optimization mode and the options of the block, and the module and the
// program it installed are copied into the directories of their types.
func TestRunCopiesTheModuleAndTheProgram(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	facts := harness.Context
	log := fakeZig(t, harness)

	require.NoError(t, harness.Run(t.Context(), validate(t, component(), map[string]any{
		"module":  "hello",
		"bin":     "hello-client",
		"steps":   []any{"install", "client"},
		"options": map[string]any{"verify-cabi": "true", "api": "3"},
	}, false)))

	cwd, err := filepath.EvalSymlinks(facts.Dirs.Cwd)
	require.NoError(t, err)

	work := facts.Dirs.Work
	assert.Equal(t, []string{
		cwd, "build", "install", "client", "-p", filepath.Join(work, "zig-out"),
		"--cache-dir", filepath.Join(work, "zig-cache"),
		"--pkg-dir", filepath.Join(work, "zig-pkg"), "--release=safe",
		"-Dapi=3", "-Dverify-cabi=true",
	}, readLines(t, log+".args"))

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Output, "hello"+facts.Target.ModuleExt()))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))

	data, err = os.ReadFile(filepath.Join(facts.Dirs.Programs, "hello-client"))
	require.NoError(t, err)
	assert.Equal(t, "client\n", string(data))

	assert.NoFileExists(t, filepath.Join(facts.Dirs.Output, "hello-client"))
	assert.NoDirExists(t, filepath.Join(facts.Dirs.Component, "zig-out"))
	assert.Equal(t, "installing\n", harness.Printed())
}

// TestRunOfThePackageDirectory: zig 0.17 and later fetch the packages into
// zig-pkg/ of the work directory, --pkg-dir, unless the env of the build
// names a directory of its own; an older zig, which refuses --pkg-dir, and a
// development version of 0.17.0 are given none.
func TestRunOfThePackageDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		env     string
		pkg     bool
	}{
		{"zig 0.17.1", "0.17.1", "", true},
		{"zig 0.17.0", "0.17.0", "", true},
		{"a development version of 0.18.0", "0.18.0-dev.12+0123abcd", "", true},
		{"zig 1.0.0", "1.0.0", "", true},
		{"zig 0.16.0", "0.16.0", "", false},
		{"a development version of 0.17.0", "0.17.0-dev.3+abcdef01", "", false},
		{"a version tt cannot read", "master", "", false},
		{"ZIG_LOCAL_PKG_DIR of the env", "0.17.1", "/srv/zig-pkg", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, Backend(), component())
			log := fakeZig(t, harness)

			harness.Context.Env["FAKE_ZIG_VERSION"] = test.version
			if test.env != "" {
				harness.Context.Env["ZIG_LOCAL_PKG_DIR"] = test.env
			}

			require.NoError(t, harness.Run(t.Context(), validate(t, component(),
				map[string]any{"module": "hello"}, false)))

			args := readLines(t, log+".args")[1:]
			cache := slices.Index(args, "--cache-dir")
			require.GreaterOrEqual(t, cache, 0, args)

			work := harness.Context.Dirs.Work

			want := []string{"--cache-dir", filepath.Join(work, "zig-cache")}
			if test.pkg {
				want = append(want, "--pkg-dir", filepath.Join(work, "zig-pkg"))
			}

			assert.Equal(t, want, args[cache:cache+len(want)])
			assert.NotContains(t, args[cache+len(want):], "--pkg-dir")
		})
	}
}

// TestRunOfAnOptimizationMode: Debug, the mode of a zig build asked for
// none, is no --release; the others are theirs.
func TestRunOfAnOptimizationMode(t *testing.T) {
	t.Parallel()

	for mode, release := range map[string]string{
		"Debug": "", "ReleaseSafe": "--release=safe", "ReleaseFast": "--release=fast",
		"ReleaseSmall": "--release=small",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, Backend(), component())
			log := fakeZig(t, harness)

			require.NoError(t, harness.Run(t.Context(), validate(t, component(),
				map[string]any{"module": "hello", "optimize": mode}, false)))

			args := readLines(t, log+".args")[2:]
			assert.NotContains(t, strings.Join(args, " "), "-Doptimize")

			if release == "" {
				assert.NotContains(t, strings.Join(args, " "), "--release")
			} else {
				assert.Contains(t, args, release)
			}
		})
	}
}

// TestRunFindsTheModule: a module is taken under the name installModule of
// zig-tarantool gives it, or the one installArtifact gives a library, and a
// dotted one from its directory, and is copied under its name with the
// extension of the target.
func TestRunFindsTheModule(t *testing.T) {
	t.Parallel()

	for module, want := range map[string]string{
		"hello": "hello\n",
		"plain": "plain\n",
		"a.b":   "ab\n",
	} {
		t.Run(module, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, Backend(), component())
			facts := harness.Context

			fakeZig(t, harness)

			require.NoError(t, harness.Run(t.Context(), validate(t, component(),
				map[string]any{"module": module}, false)))

			path := filepath.Join(append([]string{facts.Dirs.Output},
				strings.Split(module, ".")...)...) + facts.Target.ModuleExt()

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, want, string(data))
			assert.NoFileExists(t, filepath.Join(facts.Dirs.Programs, "hello-client"),
				"a program the block does not name is not taken")
		})
	}
}

// TestRunOfWhatZigDidNotInstall: a module or a program the build did not
// install fails it, saying what it installed.
func TestRunOfWhatZigDidNotInstall(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	prefix := filepath.Join(harness.Context.Dirs.Work, "zig-out")

	fakeZig(t, harness)

	err := harness.Run(t.Context(), validate(t, component(),
		map[string]any{"module": "missing"}, false))
	require.ErrorIs(t, err, errNoArtifact)
	assert.Equal(t, `zig build installed no module "missing": `+filepath.Join(prefix, "lib")+
		" has none of missing.so, missing.dylib, libmissing.so, libmissing.dylib "+
		"(it installed a/b.dylib, hello.so, libplain.so); install it with installModule of "+
		"zig-tarantool, or name the step that installs it in steps", err.Error())

	err = harness.Run(t.Context(), validate(t, component(),
		map[string]any{"bin": "missing"}, false))
	require.ErrorIs(t, err, errNoArtifact)
	assert.Equal(t, `zig build installed no program "missing": `+filepath.Join(prefix, "bin")+
		" has no missing (it installed hello-client); install it with b.installArtifact, "+
		"or name the step that installs it in steps", err.Error())
}

// TestRunOfAFailedBuild: the build fails when zig build does, and copies
// nothing.
func TestRunOfAFailedBuild(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	fakeZig(t, harness)

	harness.Context.Env["FAKE_FAIL"] = "1"

	err := harness.Run(t.Context(), validate(t, component(), map[string]any{"module": "hello"},
		false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running zig build")
	assert.NoFileExists(t, filepath.Join(harness.Context.Dirs.Output,
		"hello"+harness.Context.Target.ModuleExt()))
}

// TestRunOfAStaticProgram: a static build of a program alone is made for
// musl, when the build.zig takes -Dtarget and the options name no target;
// a static build of a module and a program is made for the target of the
// module.
func TestRunOfAStaticProgram(t *testing.T) {
	t.Parallel()

	arch, ok := zigArches[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		t.Skip("a static program is built for linux only")
	}

	musl := "-Dtarget=" + arch + "-linux-musl"

	tests := []struct {
		name     string
		rest     map[string]any
		noTarget bool
		help     bool
		want     string
	}{
		{"a program", map[string]any{"bin": "hello-client"}, false, true, musl},
		{"a build.zig that takes no target", map[string]any{"bin": "hello-client"}, true, true,
			""},
		{"a target of the options", map[string]any{
			"bin": "hello-client", "options": map[string]any{"target": "native-linux-musl"},
		}, false, false, "-Dtarget=native-linux-musl"},
		{"a module and a program", map[string]any{"module": "hello", "bin": "hello-client"},
			false, false, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, Backend(), component())
			log := fakeZig(t, harness)

			if test.noTarget {
				harness.Context.Env["FAKE_NO_TARGET"] = "1"
			}

			require.NoError(t, harness.Run(t.Context(), validate(t, component(), test.rest, true)))

			runs := readLines(t, log+".runs")
			if test.help {
				require.Len(t, runs, 2)
				assert.True(t, strings.HasPrefix(runs[0], "build --help --cache-dir "), runs[0])
				assert.Contains(t, runs[0], " --pkg-dir "+
					filepath.Join(harness.Context.Dirs.Work, "zig-pkg"))
			} else {
				require.Len(t, runs, 1)
			}

			last := runs[len(runs)-1]
			if test.want == "" {
				assert.NotContains(t, last, "-Dtarget")
			} else {
				assert.True(t, strings.HasSuffix(last, " "+test.want), last)
			}
		})
	}
}

// TestInputs: an incremental build reads the version of zig and build.zig
// and build.zig.zon of the working directory besides.
func TestInputs(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	fakeZig(t, harness)

	bld := validate(t, component(), map[string]any{"module": "hello"}, false)

	inputs, err := bld.Inputs(t.Context(), harness.Context)
	require.NoError(t, err)
	assert.Equal(t, []string{"zig version 0.17.1"}, inputs)

	cwd := harness.Context.Dirs.Cwd
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "build.zig"), []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "build.zig.zon"), []byte("b"), 0o600))

	inputs, err = bld.Inputs(t.Context(), harness.Context)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"zig version 0.17.1",
		"build.zig sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
		"build.zig.zon sha256:3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d",
	}, inputs)
}
