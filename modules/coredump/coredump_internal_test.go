package coredump

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/coredump/internal/coredump"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/sdk/sdktest"
)

// TestPackSignals checks that the first SIGINT or SIGTERM tt gets ends the
// context of pack, with the signal as its cause, and gives the signal its
// default handling back, and that pack then exits 128 and the number of the
// signal, 130 and 143, saying that what it was making is removed. A pack
// that ends of itself releases the handling, and its error is as it is.
func TestPackSignals(t *testing.T) {
	t.Parallel()

	for signal, code := range map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143} {
		notified := make(chan chan<- os.Signal, 1)

		var stopped atomic.Int32

		pack := &packCmd{
			notify: func(c chan<- os.Signal, sig ...os.Signal) {
				assert.Equal(t, interruptSignals, sig)

				notified <- c
			},
			stopNotify: func(chan<- os.Signal) { stopped.Add(1) },
		}

		ctx, stop := pack.signalContext(t.Context())

		(<-notified) <- signal

		<-ctx.Done()
		assert.Positive(t, stopped.Load(), "the default handling is back")

		failed := errors.New("pack script execution failed: exit status 143")
		err := interruptedError(ctx, failed)
		require.ErrorIs(t, err, errInterrupted)
		assert.Equal(t, code, sdk.ExitCode(err), signal)
		require.EqualError(t, err, "pack is interrupted by "+coredump.SignalName(signal)+
			": what it was making is removed")
		require.NoError(t, interruptedError(ctx, nil))

		stop()
	}

	pack := &packCmd{
		notify: func(chan<- os.Signal, ...os.Signal) {}, stopNotify: func(chan<- os.Signal) {},
	}

	ctx, stop := pack.signalContext(t.Context())
	stop()

	failed := errors.New("not a core dump")
	assert.Equal(t, failed, interruptedError(ctx, failed))
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(interruptedError(ctx, failed)))
}

// TestInstancePackOptions pins what pack <APP>:<INSTANCE> packs of a crash:
// the archive named by its PID and its time, unless --time names another,
// with the tails of tt.log and of the file tarantool logs into of its own,
// and none with --no-logs, which the pack command takes.
func TestInstancePackOptions(t *testing.T) {
	t.Parallel()

	inst := sdk.LocalInstance{Name: "app:i1", Log: "/var/log/app/i1/tt.log",
		TarantoolLog: "/var/log/app/i1/tarantool.log"}
	crash := sdk.Crash{At: time.Unix(1763869440, 0), PID: 4242, Signal: syscall.SIGSEGV,
		CoreDumped: true}
	pack := &packCmd{directory: "/out"}

	opts := pack.instancePackOptions(inst, crash, "/core")
	assert.Equal(t, coredump.PackOptions{
		Core: "/core", Dir: "/out", PID: 4242, Time: "1763869440", Logs: []coredump.PackLog{
			{Name: "tt.log", Path: inst.Log}, {Name: "tarantool.log", Path: inst.TarantoolLog},
		},
	}, opts)

	pack.noLogs, pack.time = true, "1"

	opts = pack.instancePackOptions(inst, crash, "/core")
	assert.Empty(t, opts.Logs)
	assert.Equal(t, "1", opts.Time)

	flag := newPackCmd(sdktest.New(t)).Flags().Lookup("no-logs")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

// writeTarantool writes a file at path, as tt binary installs a tarantool
// there, and returns path.
func writeTarantool(t *testing.T, path string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("tarantool"), 0o755))

	return path
}

// TestPackTarantools checks the tarantools tt knows of, which pack tries
// for a core it is given no tarantool for when neither the executable the
// core names nor the tarantool of PATH dumped it: the Tarantool tt runs,
// when there is one, then those tt binary installed into the project, user
// and system scopes. pack took the Tarantool tt runs alone, and refused a
// core of another one.
//
//nolint:paralleltest // Sets the roots of the user and the system scopes.
func TestPackTarantools(t *testing.T) {
	version := sdk.TarantoolVersion{Major: 3, Minor: 9, Patch: 0}
	project, user, system := t.TempDir(), t.TempDir(), t.TempDir()

	t.Setenv(scope.UserRootEnv, user)
	t.Setenv(scope.SystemRootEnv, system)

	active := writeTarantool(t, filepath.Join(project, ".tarantool", "bin", "tarantool"))
	installed := writeTarantool(t, filepath.Join(user, "versions", "tarantool",
		"tarantool_3.8.2", "tarantool"))
	ee := writeTarantool(t, filepath.Join(system, "versions", "tarantool-ee",
		"tarantool-ee_3.5.1", "tarantool"))
	scopes := []coredump.Candidate{
		{Path: active, Origin: "the active tarantool of the project scope", Err: nil},
		{Path: installed, Origin: "a tarantool tt binary installed into the user scope", Err: nil},
		{Path: ee, Origin: "a tarantool tt binary installed into the system scope", Err: nil},
	}

	cases := []struct {
		name string
		opts []sdktest.Option
		want []coredump.Candidate
	}{
		{
			name: "runs",
			opts: []sdktest.Option{sdktest.WithTarantool("/opt/tarantool", version)},
			want: append([]coredump.Candidate{
				{Path: "/opt/tarantool", Origin: runsOrigin, Err: nil},
			}, scopes...),
		},
		{name: "none", opts: nil, want: scopes},
	}

	for _, testCase := range cases {
		services := sdktest.New(t, append(testCase.opts, sdktest.WithProject(project))...)
		services.Start()

		pack := &packCmd{services: services, executable: "", directory: "", time: "", pid: 0}

		assert.Equal(t, testCase.want, pack.tarantools(), testCase.name)
	}
}

// failingServices are Services whose Tarantool fails with another error than
// a Tarantool not found.
type failingServices struct {
	sdk.Services
}

// errBroken is the error of failingServices.Tarantool.
var errBroken = errors.New("broken TT_USE_SYSTEM_TARANTOOL")

// Tarantool fails with errBroken.
func (failingServices) Tarantool() (sdk.Tarantool, error) {
	return nil, errBroken
}

// TestInspectExperimentalFlag checks that the flag of the experimental
// extensions is off by default and that its help says they are
// experimental and names the commit of tarantool-gdb-tools tt embeds.
func TestInspectExperimentalFlag(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	flag := newInspectCmd(services).Flags().Lookup(experimentalFlag)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
	assert.Contains(t, flag.Usage, "Load the experimental gdb extensions of "+
		"tarantool-gdb-tools (vendored by tt at "+coredump.GDBToolsCommit()+")")
}

// fakeGDB is a gdb that answers the probe of inspect as gdb 17.1 with
// Python does, with shell builtins alone.
const fakeGDB = "#!/bin/sh\necho 'GNU gdb (GDB) 17.1'\necho 'tt-gdb-python: 17.1'\n"

// TestInspectExperimentalGDBTools checks that the flag reaches inspect: with
// it inspect hands the debugger the extensions of tarantool-gdb-tools,
// which it logs before it runs the debugger. PATH has a fake gdb alone,
// which answers the probe, and no sh, so that inspect fails to run gdb.sh
// whether gdb is installed or not; and should the flag not reach inspect,
// it looks the extensions of the release of the core up neither on
// GitHub, the proxy refuses the connection, nor in the cache of the user.
// The test sets the environment of the process and runs alone.
func TestInspectExperimentalGDBTools(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version"),
		[]byte("Tarantool 3.9.0-0-g8b9a0f59a95b\n"), 0o644))

	bin := t.TempDir()
	require.NoError(t, execfile.WriteFile(filepath.Join(bin, "gdb"), []byte(fakeGDB), 0o755))
	t.Setenv("PATH", bin)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(name, "http://127.0.0.1:9")
	}

	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	got := sdktest.Run(t, New, "coredump", "inspect", "--"+experimentalFlag, dir)
	require.ErrorIs(t, got.Err, exec.ErrNotFound)
	require.ErrorContains(t, got.Err, "inspect script execution failed")

	prefix := "gdb extensions of tarantool-gdb-tools " + coredump.GDBToolsCommit() +
		", which tt embeds: "
	messages := make([]string, 0, len(got.Records))

	for _, record := range got.Records {
		messages = append(messages, record.Message)
	}

	assert.True(t, slices.ContainsFunc(messages, func(message string) bool {
		return strings.HasPrefix(message, prefix)
	}), "no %q in %q", prefix, messages)
}

// TestPackTarantoolsError checks that a tarantool tt cannot find is a
// candidate with the error, which pack lists when none of the others
// dumped the core, and does not stop the search: the Tarantool tt runs, and
// the tarantools of a scope whose root is wrong.
//
//nolint:paralleltest // Sets the roots of the user and the system scopes.
func TestPackTarantoolsError(t *testing.T) {
	t.Setenv(scope.UserRootEnv, "relative")
	t.Setenv(scope.SystemRootEnv, t.TempDir())

	services := sdktest.New(t)
	services.Start()

	pack := &packCmd{
		services: failingServices{Services: services}, executable: "", directory: "", time: "",
		pid: 0,
	}

	got := pack.tarantools()
	require.Len(t, got, 2)
	assert.Equal(t, runsOrigin, got[0].Origin)
	assert.Empty(t, got[0].Path)
	require.ErrorIs(t, got[0].Err, errBroken)
	require.ErrorContains(t, got[0].Err, "cannot find the tarantool executable")
	assert.Equal(t, "the tarantools of the user scope", got[1].Origin)
	assert.Empty(t, got[1].Path)
	require.ErrorIs(t, got[1].Err, scope.ErrRelativeRoot)
}
