package rockscript_test

import (
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest/rockscript"
)

// update rewrites the golden scripts of testdata with what Render writes.
var update = flag.Bool("update", false, "rewrite the golden scripts")

// tricky is a literal of a manifest the script is to pass on as it is.
const tricky = "it's $HOME $(echo no) `echo no` \\ *\nand a line"

// demoScript is the script of a package of a hook of each kind, a component
// of shell with output and the env of its block, one of make in a namespace
// of its own, and files the layout takes that build.install does not.
func demoScript() rockscript.Script {
	return rockscript.Script{
		Package: "demo",
		Version: "1.2.3",
		PreBuild: &rockscript.Step{
			Hook: "pre_build", Backend: "shell", Cwd: "",
			Env: map[string]string{"GEN": tricky, "TT_PACKAGE": "not this"},
			Export: sdkbuild.Export{
				Script: "sh -c 'printf 42 > native/answer.txt'", Tools: []string{"sh"}, Lost: nil,
			},
		},
		Components: []rockscript.Step{
			{
				Component: "native", Backend: "shell", Cwd: "native", Namespace: "demo",
				Env: map[string]string{"CFLAGS": "-O3", "TT_OUTPUT_DIR": "/not/this"},
				Export: sdkbuild.Export{
					Script: sdkbuild.ShellQuote("./build.sh") + " " + sdkbuild.ShellQuote(tricky),
					Tools:  nil, Lost: []string{"the cache\nof objects"},
				},
				Outputs: []sdkbuild.OutputFile{
					{Path: "out/fast.so", To: sdkbuild.Module},
					{Path: "out/tool", To: sdkbuild.Program},
					{Path: "out/data.json", To: sdkbuild.Data},
					{Path: "out/fast.h", To: sdkbuild.Link},
				},
			},
			{
				Component: "calc", Backend: "make", Cwd: "calc/src", Namespace: "",
				Env: map[string]string{"PATH": "/opt/make/bin:/usr/bin:/bin"},
				Export: sdkbuild.Export{
					Script: "make -C . -f Makefile all", Tools: []string{"make"}, Lost: nil,
				},
			},
			{
				Component: "gw", Backend: "go", Cwd: "gw", Namespace: "demo", ProgramsOnly: true,
				Export: sdkbuild.Export{Script: "", Tools: nil, Lost: nil},
			},
		},
		PostBuild: &rockscript.Step{
			Hook: "post_build", Backend: "make", Cwd: "tools",
			Export: sdkbuild.Export{
				Script: "cat <<EOF > done.txt\n$TT_VERSION\nEOF\n", Tools: []string{"make"},
				Lost: nil,
			},
		},
		Lay: []rockscript.Lay{
			{Source: "native/out/fast.so", Dest: "lib/demo/out/fast.so"},
			{Source: "native/v1.2/x.json", Dest: "lua/demo/v1.2/x.json"},
		},
	}
}

// golden compares got with the golden file name of testdata, or rewrites it
// with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// TestRender pins the script of demoScript, and of a package of one
// component and no hook.
func TestRender(t *testing.T) {
	t.Parallel()

	golden(t, "demo.sh", rockscript.Render(demoScript()))

	golden(t, "single.sh", rockscript.Render(rockscript.Script{
		Package: "single", Version: "0.1.0", PreBuild: nil, PostBuild: nil,
		Components: []rockscript.Step{{
			Component: "lib", Backend: "make", Cwd: ".", Namespace: "single",
			Export: sdkbuild.Export{Script: "make -C . -f Makefile all\n", Tools: nil, Lost: nil},
		}},
		Lay: nil,
	}))
}

// TestCheckEnv: the script sets a variable of a name of sh alone.
func TestCheckEnv(t *testing.T) {
	t.Parallel()

	require.NoError(t, rockscript.CheckEnv(nil))
	require.NoError(t, rockscript.CheckEnv(map[string]string{"CFLAGS": "-O3", "_x1": ""}))

	for _, name := range []string{"FOO-BAR", "1X", "A B", "", "A\x00"} {
		err := rockscript.CheckEnv(map[string]string{"OK": "", name: "x"})
		require.ErrorIs(t, err, rockscript.ErrEnv, name)
		assert.Contains(t, err.Error(), `"`+strings.ReplaceAll(name, "\x00", `\x00`)+`"`)
	}

	err := rockscript.CheckEnv(map[string]string{"OK": "", "NUL": "a\x00b"})
	require.ErrorIs(t, err, rockscript.ErrNUL)
	assert.Equal(t, "the value of NUL holds a NUL byte, which no script of sh holds", err.Error())

	// The variables of the script and of the scripts of the exports.
	for _, name := range []string{"tt_root", "tt_state", "tt_stage", "tt_cmake_file", "cc_cxx"} {
		err := rockscript.CheckEnv(map[string]string{"OK": "", name: "x"})
		require.ErrorIs(t, err, rockscript.ErrEnvOwn, name)
		assert.Equal(t, "the script that builds the package without tt keeps a value of its "+
			"own in the variable: "+name, err.Error())
	}

	for _, name := range []string{"IFS", "CDPATH", "ENV", "PS4", "OPTIND", "OPTARG", "PPID"} {
		err := rockscript.CheckEnv(map[string]string{"OK": "", name: "x"})
		require.ErrorIs(t, err, rockscript.ErrEnvShell, name)
		assert.Equal(t, "the variable changes how sh runs the script that builds the package "+
			"without tt: "+name, err.Error())
	}

	// PATH, the tools and the variables of tt, which the script sets over
	// the env, are the env's.
	require.NoError(t, rockscript.CheckEnv(map[string]string{
		"PATH": "/bin", "CC": "cc", "CFLAGS": "-O3", "HOME": "/", "TT_PACKAGE": "x", "tt": "",
		"TT_ROOT": "", "ifs": "",
	}))
}

// TestCheckStep: no value of a step the script holds has a NUL byte.
func TestCheckStep(t *testing.T) {
	t.Parallel()

	require.NoError(t, rockscript.CheckStep(demoScript().Components[0]))

	for what, change := range map[string]func(step *rockscript.Step){
		"the script of its export": func(step *rockscript.Step) { step.Export.Script += "\x00" },
		"a tool of its export": func(step *rockscript.Step) {
			step.Export.Tools = []string{"cc", "m\x00ake"}
		},
		"what its export lacks": func(step *rockscript.Step) {
			step.Export.Lost = []string{"the cache\x00"}
		},
		"its working directory": func(step *rockscript.Step) { step.Cwd = "nat\x00ive" },
		"a file of its output": func(step *rockscript.Step) {
			step.Outputs = []sdkbuild.OutputFile{{Path: "out/\x00x.so", To: sdkbuild.Module}}
		},
	} {
		step := demoScript().Components[0]
		change(&step)

		err := rockscript.CheckStep(step)
		require.ErrorIs(t, err, rockscript.ErrNUL, what)
		assert.Equal(t, what+" holds a NUL byte, which no script of sh holds", err.Error())
	}
}

// writeFiles writes files, by path, under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))

		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o700))
	}
}

// runScript runs the script at the root of the sources src with sh and the
// arguments args, the environment env added to that of the test.
func runScript(t *testing.T, src string, env []string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", append([]string{"./tt-rock.sh"}, args...)...)

	cmd.Dir = src

	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// readTree reads the files under dir by their slash-separated paths.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)

	defer func() { _ = root.Close() }()

	files := map[string]string{}
	tree := root.FS()

	err = fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		data, err := fs.ReadFile(tree, path)

		files[path] = string(data)

		return err
	})
	require.NoError(t, err)

	return files
}

// requireSh skips the test where there is no sh.
func requireSh(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
}

// TestRunScript runs the script of a package: the hooks and the components
// run in order, each in its directory with the variables of a build of tt
// and the env of its block, which they do not change; output is copied,
// the layout is laid out where a build made a file of it, and install
// copies the stage into the directories LuaRocks names.
func TestRunScript(t *testing.T) {
	t.Parallel()
	requireSh(t)

	src := t.TempDir()
	vars := `printf '%s\n' "$TT_PACKAGE $TT_VERSION ${TT_COMPONENT_NAME-no-component} ` +
		`${TT_OUTPUT_DIR-no-output} ${GEN-no-gen} ${CFLAGS-no-cflags}" ` +
		`>> "$TT_PROJECT_ROOT/log"`

	script := rockscript.Script{
		Package: "demo", Version: "1.2.3",
		PreBuild: &rockscript.Step{
			Hook: "pre_build", Backend: "shell",
			Env: map[string]string{"GEN": tricky, "TT_PACKAGE": "not this"},
			Export: sdkbuild.Export{
				Script: vars + "\nprintf 42 > native/answer.txt", Tools: []string{"sh"}, Lost: nil,
			},
		},
		Components: []rockscript.Step{{
			Component: "native", Backend: "shell", Cwd: "native", Namespace: "demo",
			Env: map[string]string{"CFLAGS": "-O3", "TT_OUTPUT_DIR": "/not/this"},
			Export: sdkbuild.Export{
				Script: vars + "\n" + `mkdir -p out
cat answer.txt > "$TT_OUTPUT_DIR/answer.so"
printf '%s' ` + sdkbuild.ShellQuote(tricky) + ` > out/arg.json
printf '#!/bin/sh\n' > out/tool
chmod +x out/tool
printf x > out/laid.so
cat > "$TT_LINK_DIR/libx.a" <<EOF
$TT_COMPONENT_NAME
EOF
cd /`,
				Tools: []string{"sh"}, Lost: nil,
			},
			Outputs: []sdkbuild.OutputFile{
				{Path: "out/arg.json", To: sdkbuild.Data},
				{Path: "out/tool", To: sdkbuild.Program},
			},
		}},
		PostBuild: &rockscript.Step{
			Hook: "post_build", Backend: "shell",
			Export: sdkbuild.Export{Script: vars, Tools: nil, Lost: nil},
		},
		Lay: []rockscript.Lay{
			{Source: "native/out/laid.so", Dest: "lib/demo/out/laid.so"},
			{Source: "native/out/missing.so", Dest: "lib/demo/out/missing.so"},
		},
	}

	writeFiles(t, src, map[string]string{
		"tt-rock.sh":     rockscript.Render(script),
		"native/keep.md": "kept\n",
	})

	out, err := runScript(t, src, []string{"CFLAGS=-O1", "LUA_INCDIR=/nonexistent"}, "build")
	require.NoError(t, err, out)

	assert.Equal(t, "demo 1.2.3 no-component no-output "+tricky+" -O1\n"+
		"demo 1.2.3 native "+src+"/.tt-rock/stage/lib/demo no-gen -O3\n"+
		"demo 1.2.3 no-component no-output no-gen -O1\n", readTree(t, src)["log"])

	assert.Equal(t, map[string]string{
		"lib/demo/answer.so":   "42",
		"lib/demo/out/laid.so": "x",
		"lua/demo/arg.json":    tricky,
		"bin/tool":             "#!/bin/sh\n",
	}, readTree(t, filepath.Join(src, ".tt-rock", "stage")))
	assert.Equal(t, map[string]string{"native/libx.a": "native\n"},
		readTree(t, filepath.Join(src, ".tt-rock", "link")))

	info, err := os.Stat(filepath.Join(src, ".tt-rock", "stage", "bin", "tool"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the program stays executable")

	rock := t.TempDir()

	out, err = runScript(t, src, []string{
		"LUADIR=" + filepath.Join(rock, "lua"), "LIBDIR=" + filepath.Join(rock, "lib"),
		"BINDIR=" + filepath.Join(rock, "bin"),
	}, "install")
	require.NoError(t, err, out)
	assert.Equal(t, map[string]string{
		"lib/demo/answer.so":   "42",
		"lib/demo/out/laid.so": "x",
		"lua/demo/arg.json":    tricky,
		"bin/tool":             "#!/bin/sh\n",
	}, readTree(t, rock))

	out, err = runScript(t, src, nil, "install")
	require.Error(t, err)
	assert.Contains(t, out, "LUADIR: names no directory of the Lua files of the rock")

	out, err = runScript(t, src, nil, "other")
	require.Error(t, err)
	assert.Equal(t, "tt-rock.sh: usage: sh ./tt-rock.sh build | install\n", out)
}

// TestRunScriptFails checks the failures of the script: a tool of an export
// that is not in PATH fails the build before any step runs, a file of output
// the build did not make fails it, and so does a step that fails.
func TestRunScriptFails(t *testing.T) {
	t.Parallel()
	requireSh(t)

	for name, test := range map[string]struct {
		step rockscript.Step
		want string
	}{
		"a tool": {rockscript.Step{
			Component: "native", Backend: "shell",
			Export: sdkbuild.Export{
				Script: "touch ran", Tools: []string{"no-such-tool-of-tt"}, Lost: nil,
			},
		}, `tt-rock.sh: the component "native" runs no-such-tool-of-tt, which is not in PATH`},
		"an output": {rockscript.Step{
			Component: "native", Backend: "shell",
			Export:  sdkbuild.Export{Script: "touch ran", Tools: nil, Lost: nil},
			Outputs: []sdkbuild.OutputFile{{Path: "out/x.so", To: sdkbuild.Module}},
		}, "tt-rock.sh: the build made no out/x.so, which its output lists"},
		"a build": {rockscript.Step{
			Component: "native", Backend: "shell",
			Export: sdkbuild.Export{Script: "touch ran\nfalse\ntouch on", Tools: nil, Lost: nil},
		}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := t.TempDir()
			writeFiles(t, src, map[string]string{"tt-rock.sh": rockscript.Render(rockscript.Script{
				Package: "demo", Version: "1.0.0", PreBuild: nil, PostBuild: nil,
				Components: []rockscript.Step{test.step}, Lay: nil,
			})})

			out, err := runScript(t, src, nil, "build")
			require.Error(t, err)
			assert.Equal(t, test.want, strings.TrimSpace(out))

			files := readTree(t, src)
			_, ran := files["ran"]

			assert.Equal(t, name != "a tool", ran, "the build ran")
			assert.NotContains(t, files, "on", "the build stops at the first failure")
		})
	}
}
