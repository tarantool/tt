package coredump

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/coredump/internal/coredump"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/sdktest"
)

// TestPackTarantool checks the executable pack packs a core with: the one
// --executable names, else the Tarantool tt runs, else none, for the pack
// script to look up in PATH.
func TestPackTarantool(t *testing.T) {
	t.Parallel()

	version := sdk.TarantoolVersion{Major: 3, Minor: 9, Patch: 0}

	cases := []struct {
		name       string
		executable string
		opts       []sdktest.Option
		want       string
	}{
		{
			name:       "flag",
			executable: "/opt/tarantool",
			opts:       []sdktest.Option{sdktest.WithTarantool("/usr/bin/tarantool", version)},
			want:       "/opt/tarantool",
		},
		{
			name:       "services",
			executable: "",
			opts: []sdktest.Option{
				sdktest.WithTarantool("/app/.tarantool/bin/tarantool", version),
			},
			want: "/app/.tarantool/bin/tarantool",
		},
		{name: "none", executable: "", opts: nil, want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			services := sdktest.New(t, testCase.opts...)
			services.Start()

			pack := &packCmd{
				services: services, executable: testCase.executable, directory: "", time: "",
				pid: 0,
			}

			got, err := pack.tarantool()
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
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

func TestPackTarantoolError(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	pack := &packCmd{
		services: failingServices{Services: services}, executable: "", directory: "", time: "",
		pid: 0,
	}

	_, err := pack.tarantool()
	require.ErrorIs(t, err, errBroken)
	assert.ErrorContains(t, err, "cannot find the tarantool executable")
}
