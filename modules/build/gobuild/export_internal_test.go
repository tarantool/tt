package gobuild

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

// fakeHostGo is a go of a host of freebsd/riscv64 that logs its argv, and, for
// a build, the target it is given as a call of env, and makes the file of
// -o.
const fakeHostGo = `go() {
	tt_test_log go "$@"
	case $1 in
	env)
		case $2 in
		GOHOSTOS) echo freebsd ;;
		GOHOSTARCH) echo riscv64 ;;
		esac
		;;
	build)
		tt_test_log env "GOOS=$GOOS" "GOARCH=$GOARCH" "CGO_ENABLED=$CGO_ENABLED"
		: > "$4"
		;;
	esac
}
`

// TestExport checks the script of the export against Run: it runs the go
// build goArgs makes of the block, with no -p, for the host as go names it,
// whatever the environment says, and CGO_ENABLED as targetEnv sets it.
func TestExport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		config Config
		static bool
	}{
		{"defaults", Config{}, false},
		{"a package", Config{Package: "./cmd/it's"}, false},
		{
			"tags and ldflags",
			Config{
				Tags:    []string{"prod", "fast"},
				Ldflags: []string{"-s", "-X", "main.greeting=it's $HOME `echo no`"},
			},
			false,
		},
		{"static without cgo", Config{}, true},
		{
			"static with cgo",
			Config{Cgo: true, Tags: []string{"prod"}, Ldflags: []string{"-s"}}, true,
		},
		{"cgo", Config{Cgo: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bld := goBuild{config: test.config, program: "gw", static: test.static}
			export, err := bld.Export(&build.ExportContext{})
			require.NoError(t, err)
			assert.Equal(t, []string{goTool}, export.Tools)
			assert.Empty(t, export.Lost)

			// The environment builds for another platform, which the script
			// does not.
			run := runExport(t, fakeHostGo, export.Script, map[string]string{
				"GOOS": "windows", "GOARCH": "386", "CGO_ENABLED": "1",
			})
			require.NoError(t, run.err, "%s\nscript:\n%s", run.out, export.Script)

			program := filepath.Join(run.dirs["TT_PROGRAMS_DIR"], "gw")
			args, err := goArgs(test.config, program, test.static, 0, nil)
			require.NoError(t, err)

			assert.Equal(t, [][]string{
				{goTool, "env", "GOHOSTOS"},
				{goTool, "env", "GOHOSTARCH"},
				append([]string{goTool}, args...),
				append([]string{"env"},
					targetEnv(test.config, build.Target{OS: "freebsd", Arch: "riscv64"})...),
			}, run.calls, export.Script)
			assert.FileExists(t, program)
		})
	}
}

// TestExportStaticDarwin checks that the script refuses a static program on
// macOS, as Preflight does, and builds one on Linux.
func TestExportStaticDarwin(t *testing.T) {
	t.Parallel()

	export, err := goBuild{config: Config{}, program: "gw", static: true}.Export(
		&build.ExportContext{})
	require.NoError(t, err)

	run := runExport(t, fakeHostGo, export.Script, map[string]string{"TT_PLATFORM_OS": "darwin"})
	require.Error(t, run.err)
	assert.Contains(t, run.out, "a static program cannot be built for darwin: "+
		"static = true needs a linux build")
	assert.Empty(t, run.calls)

	export, err = goBuild{config: Config{}, program: "gw", static: false}.Export(
		&build.ExportContext{})
	require.NoError(t, err)

	run = runExport(t, fakeHostGo, export.Script, map[string]string{"TT_PLATFORM_OS": "darwin"})
	require.NoError(t, run.err, run.out)
}

// TestExportUnquotableFlag checks that a flag Run cannot pass fails the
// export as it fails Run.
func TestExportUnquotableFlag(t *testing.T) {
	t.Parallel()

	_, err := goBuild{config: Config{Ldflags: []string{`a "b' c`}}, program: "gw", static: false}.
		Export(&build.ExportContext{})
	require.ErrorIs(t, err, errUnquotableFlag)
	assert.NotErrorIs(t, err, build.ErrNoExport)
}
