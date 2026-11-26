package cmake

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// scriptRun is what a run of the script of an export made (runExport).
type scriptRun struct {
	// calls are the argv of the tools the script ran, in order.
	calls [][]string
	// dirs are the directories of the script by the variables that name
	// them: TT_BUILD_DIR, TT_OUTPUT_DIR, TT_SHARE_DIR, TT_PROGRAMS_DIR and
	// TT_LINK_DIR.
	dirs map[string]string
	// out is what the script printed.
	out string
	// err is how it failed; nil when it did not.
	err error
}

// logFunction is the function of sh the fakes of tools log their argv
// with: each argument ends with a NUL, and each call with a \001.
const logFunction = `tt_test_log() {
	printf '%s\000' "$@" >> "$TT_TEST_LOG"
	printf '\001' >> "$TT_TEST_LOG"
}
`

// runExport runs script as the script of a rock runs that of the export of
// a component: by sh with set -eu, in a directory of its own, with the
// variables of an export, each directory a new one, and env over them.
// prelude defines the tools the script runs as functions of sh, which log
// their argv by tt_test_log.
func runExport(t *testing.T, prelude, script string, env map[string]string) scriptRun {
	t.Helper()

	root := t.TempDir()
	run := scriptRun{calls: nil, dirs: map[string]string{}, out: "", err: nil}

	for _, key := range []string{
		"TT_BUILD_DIR", "TT_OUTPUT_DIR", "TT_SHARE_DIR", "TT_PROGRAMS_DIR", "TT_LINK_DIR",
	} {
		run.dirs[key] = filepath.Join(root, strings.ToLower(key))
		require.NoError(t, os.MkdirAll(run.dirs[key], 0o750))
	}

	cwd := filepath.Join(root, "cwd")
	require.NoError(t, os.MkdirAll(cwd, 0o750))

	file := filepath.Join(root, "export.sh")
	require.NoError(t, os.WriteFile(file,
		[]byte("set -eu\n"+logFunction+prelude+"\n"+script+"\n"), 0o600))

	vars := map[string]string{
		"TT_TEST_LOG": filepath.Join(root, "log"), "TT_MODULE_EXT": ".so", "TT_BUILD_JOBS": "3",
		"TT_PLATFORM_OS": "linux", "TT_PLATFORM_ARCH": "amd64",
	}
	maps.Copy(vars, run.dirs)
	maps.Copy(vars, env)

	cmd := exec.CommandContext(t.Context(), "sh", file)

	cmd.Dir = cwd
	cmd.Env = os.Environ()

	for _, key := range slices.Sorted(maps.Keys(vars)) {
		cmd.Env = append(cmd.Env, key+"="+vars[key])
	}

	out, err := cmd.CombinedOutput()

	run.out, run.err = string(out), err

	data, err := os.ReadFile(vars["TT_TEST_LOG"])
	if err != nil {
		return run
	}

	for call := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte("\x01")), []byte("\x01")) {
		run.calls = append(run.calls,
			strings.Split(strings.TrimSuffix(string(call), "\x00"), "\x00"))
	}

	return run
}

// fakeCMake is a cmake that logs its argv and, building, makes each file of
// TT_TEST_MAKES, a list of paths relative to the build tree, its content its
// path.
const fakeCMake = `cmake() {
	tt_test_log cmake "$@"
	[ "$1" = --build ] || return 0
	for file in $TT_TEST_MAKES; do
		mkdir -p "$(dirname "$TT_BUILD_DIR/$file")"
		printf '%s\n' "$file" > "$TT_BUILD_DIR/$file"
	done
}
`

// TestExport checks the script of the export against Run: it runs the
// cmake configureArgs and buildArgs make of the block, with the prefix of
// the Tarantool of the script and its jobs, in the build tree of the
// script, and copies the files of output where copyOutputs does.
func TestExport(t *testing.T) {
	t.Parallel()

	tricky := "it's $HOME `echo no` \\ *\nand a line"
	output := []build.OutputFile{
		{Path: "lib/m.so", To: build.Module}, {Path: "m.dylib", To: build.Module},
		{Path: "gen/data.json", To: build.Data}, {Path: "tools/tool", To: build.Program},
		{Path: "libx.a", To: build.Link}, {Path: "inc/x.h", To: build.Link},
	}
	makes := "lib/m.so m.dylib gen/data.json tools/tool libx.a inc/x.h"

	for _, test := range []struct {
		name   string
		config Config
		prefix string
		env    map[string]string
	}{
		{"defaults", Config{}, "", nil},
		{"the prefix of Tarantool", Config{}, "/opt/tarantool", nil},
		{
			"variables and targets",
			Config{
				BuildType: "Debug", Targets: []string{"m", "tool"},
				Variables: map[string]string{"GREETING": tricky, "ON:BOOL": "ON"},
			},
			"/opt/my tarantool", nil,
		},
		{
			"the prefix after those of the build",
			Config{Variables: map[string]string{"CMAKE_PREFIX_PATH": "/deps;/more"}},
			"/opt/tarantool", nil,
		},
		{
			"the prefixes of the build alone",
			Config{Variables: map[string]string{"CMAKE_PREFIX_PATH:PATH": "/deps"}}, "", nil,
		},
		{
			"an empty prefix path",
			Config{Variables: map[string]string{"CMAKE_PREFIX_PATH": ""}}, "/opt/tarantool", nil,
		},
		{
			"two prefix paths",
			Config{Variables: map[string]string{
				"CMAKE_PREFIX_PATH": "/a", "CMAKE_PREFIX_PATH:PATH": "/b",
			}},
			"/opt/tarantool", nil,
		},
		{"a parallel level", Config{}, "", map[string]string{parallelLevel: "2"}},
		{"an empty parallel level", Config{}, "", map[string]string{parallelLevel: ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bld := cmakeBuild{config: test.config, output: output}
			export, err := bld.Export(&build.ExportContext{})
			require.NoError(t, err)
			assert.Equal(t, []string{cmakeTool}, export.Tools)
			assert.Empty(t, export.Lost)

			env := map[string]string{"TT_TEST_MAKES": makes}
			if test.prefix != "" {
				env["TT_TARANTOOL_PREFIX"] = test.prefix
			}

			maps.Copy(env, test.env)

			run := runExport(t, fakeCMake, export.Script, env)
			require.NoError(t, run.err, "%s\nscript:\n%s", run.out, export.Script)

			// The jobs are those of a build of no -j, TT_BUILD_JOBS of the
			// script being the CPUs.
			tree := run.dirs["TT_BUILD_DIR"]
			environ := make([]string, 0, len(test.env))

			for key, value := range test.env {
				environ = append(environ, key+"="+value)
			}

			jobs := jobs(0, environ)
			if jobs > 0 {
				jobs = 3
			}

			assert.Equal(t, [][]string{
				append([]string{cmakeTool}, configureArgs(test.config, ".", tree, test.prefix)...),
				append([]string{cmakeTool}, buildArgs(test.config, tree, jobs)...),
			}, run.calls, export.Script)

			for path, content := range map[string]string{
				"TT_OUTPUT_DIR/m.so":      "lib/m.so",
				"TT_OUTPUT_DIR/m.dylib":   "m.dylib",
				"TT_SHARE_DIR/data.json":  "gen/data.json",
				"TT_PROGRAMS_DIR/tool":    "tools/tool",
				"TT_LINK_DIR/libx.a":      "libx.a",
				"TT_LINK_DIR/include/x.h": "inc/x.h",
			} {
				key, rel, _ := strings.Cut(path, "/")
				data, err := os.ReadFile(filepath.Join(run.dirs[key], rel))
				require.NoError(t, err, path)
				assert.Equal(t, content+"\n", string(data), path)
			}
		})
	}
}

// TestExportModuleExt checks that a .so module of output is copied as one of
// TT_MODULE_EXT, as copyOutputs copies it on macOS, and another file as it
// is.
func TestExportModuleExt(t *testing.T) {
	t.Parallel()

	bld := cmakeBuild{config: Config{}, output: []build.OutputFile{
		{Path: "lib/m.so", To: build.Module}, {Path: "lib/p.so", To: build.Program},
		{Path: "lib/d.so", To: build.Data},
	}}

	export, err := bld.Export(&build.ExportContext{})
	require.NoError(t, err)

	run := runExport(t, fakeCMake, export.Script, map[string]string{
		"TT_TEST_MAKES": "lib/m.so lib/p.so lib/d.so", "TT_MODULE_EXT": ".dylib",
	})
	require.NoError(t, run.err, run.out)

	assert.FileExists(t, filepath.Join(run.dirs["TT_OUTPUT_DIR"], "m.dylib"))
	assert.NoFileExists(t, filepath.Join(run.dirs["TT_OUTPUT_DIR"], "m.so"))
	assert.FileExists(t, filepath.Join(run.dirs["TT_PROGRAMS_DIR"], "p.so"))
	assert.FileExists(t, filepath.Join(run.dirs["TT_SHARE_DIR"], "d.so"))
}

// TestExportFailsOnAMissingOutput checks that the script fails on the files
// of output the build did not make, naming all of them, as copyOutputs
// does, and copies none.
func TestExportFailsOnAMissingOutput(t *testing.T) {
	t.Parallel()

	bld := cmakeBuild{config: Config{}, output: modules("lib/m.so", "m2.so", "it's.so")}

	export, err := bld.Export(&build.ExportContext{})
	require.NoError(t, err)

	run := runExport(t, fakeCMake, export.Script, map[string]string{"TT_TEST_MAKES": "lib/m.so"})
	require.Error(t, run.err)
	assert.Contains(t, run.out, `output "m2.so", "it's.so": not in the cmake build tree `+
		run.dirs["TT_BUILD_DIR"]+"; output names the files the build makes")
	assert.NoFileExists(t, filepath.Join(run.dirs["TT_OUTPUT_DIR"], "m.so"))
}
