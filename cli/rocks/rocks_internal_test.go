package rocks

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/tarantool"
)

func TestAddLuarocksRepoOpts(t *testing.T) {
	tests := []struct {
		name string
		repo string
		args []string
		want []string
	}{
		{
			"Rocks repo is not specified",
			"",
			[]string{},
			[]string{},
		},
		{
			"Rock repo is specified",
			"local_path",
			[]string{},
			[]string{"--server", "local_path"},
		},
		{
			"Rock repo is specified and --only-server opt is provided",
			"local_path",
			[]string{"--only-server", "/other/repo"},
			[]string{"--only-server", "/other/repo"}, // No --server option is added.
		},
		{
			"Rock repo is specified and --server is passed",
			"local_path",
			[]string{"--server", "/other/repo"},
			[]string{"--server", "/other/repo local_path"},
		},
		{
			"Rock repo is specified and --server= is passed",
			"local_path",
			[]string{"--server=/other/repo"},
			[]string{"--server=/other/repo local_path"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addLuarocksRepoOpts(tt.repo, tt.args)
			require.Equal(t, tt.want, got)
		})
	}
}

// resolvedGuess stands, in the cases of TestSetupTarantoolPrefix, for the
// guess tarantool.ResolvePrefix makes of the executable of the case.
const resolvedGuess = "<the guess of tarantool.ResolvePrefix>"

func TestSetupTarantoolPrefix(t *testing.T) {
	type prefixInput struct {
		cli          cmdcontext.CliCtx
		data         *[]byte
		tntPrefixEnv string
	}

	type prefixOutput struct {
		prefix string
		err    error
	}

	assert := assert.New(t)
	// The prefix is found along the real path of the executable: on macOS
	// the temporary directory is under /var, a link to /private/var.
	testDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	err = os.Mkdir(testDir+"/bin", os.ModePerm)
	require.NoError(t, err)

	tntBinPath := testDir + "/bin/tarantool"

	testCases := make(map[prefixInput]prefixOutput)

	tntOkData := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr -DENABLE_BACKTRACE=yes'")

	// With no headers beside the executable the prefix is a guess: /usr, or,
	// on a host whose /usr has the headers of another Tarantool, as a CI
	// runner has, the directory above the executable. Which one is the
	// business of tarantool.ResolvePrefix, tested there; here the guess is
	// to be the one it makes.
	testCases[prefixInput{cli: cmdcontext.CliCtx{
		TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntOkData}] = prefixOutput{
		prefix: resolvedGuess,
		err:    nil,
	}

	testCases[prefixInput{
		cli: cmdcontext.CliCtx{
			TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
		},
		data:         &tntOkData,
		tntPrefixEnv: "/tnt/prefix",
	}] = prefixOutput{
		prefix: "/tnt/prefix",
		err:    nil,
	}

	tntBadData0 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -D_FAIL_HERE_=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntBadData0}] = prefixOutput{
		err: tarantool.ErrBannerNoPrefix,
	}

	tntBadData1 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntBadData1}] = prefixOutput{
		err: tarantool.ErrBannerShort,
	}

	// The prefix of a tarantool tt binary installed comes first, before
	// the environment.
	testCases[prefixInput{
		cli: cmdcontext.CliCtx{
			TarantoolPrefix: "/project/.tarantool",
			TarantoolCli:    cmdcontext.TarantoolCli{Executable: tntBinPath},
		},
		data:         &tntOkData,
		tntPrefixEnv: "/tnt/prefix",
	}] = prefixOutput{
		prefix: "/project/.tarantool",
		err:    nil,
	}

	for input, output := range testCases {
		_ = os.Unsetenv(tarantool.PrefixEnv)

		tntFile, err := os.Create(tntBinPath)
		require.NoError(t, err)

		_, err = tntFile.Write(*input.data)
		require.NoError(t, err)

		_ = tntFile.Close()

		err = os.Chmod(tntFile.Name(), 0o755)
		require.NoError(t, err)

		if input.tntPrefixEnv != "" {
			t.Setenv(tarantool.PrefixEnv, input.tntPrefixEnv)
		}

		tarantoolPrefix, err := GetTarantoolPrefix(&input.cli)

		_ = os.Unsetenv(tarantool.PrefixEnv)

		if err == nil {
			want := output.prefix
			if want == resolvedGuess {
				guess, guessErr := tarantool.ResolvePrefix(tarantool.PrefixQuery{
					Project: "", Env: "", Executable: tntBinPath, System: "",
				})
				require.NoError(t, guessErr)

				want = guess.Dir
			}

			assert.Equal(want, tarantoolPrefix)
		} else {
			assert.Equal(output.err, err)
		}

		_ = os.Remove(tntBinPath)
	}

	// The headers beside the executable come before the prefix its build
	// names, and a LuaRocks build is not told where else to look.
	require.NoError(t, os.WriteFile(tntBinPath, tntOkData, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(testDir, "include", "tarantool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "include", "tarantool", "module.h"),
		[]byte("#define PACKAGE_VERSION \"2.10.2-0-gb924f0b\"\n"), 0o600))

	cli := cmdcontext.CliCtx{TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath}}

	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	prefix, err := ResolveTarantoolPrefix(&cli)
	require.NoError(t, err)
	assert.Equal(testDir, prefix.Dir)
	assert.Empty(prefix.Warnings)

	// Resolved again in the same command, the prefix is said once, with -V.
	_, err = ResolveTarantoolPrefix(&cli)
	require.NoError(t, err)

	said := 0

	for _, record := range recorder.Records() {
		if strings.HasPrefix(record.Message, "Tarantool prefix path: ") {
			said++
		}
	}

	assert.Equal(1, said)

	failed := errors.New("failed to run luarocks")
	require.Equal(t, failed, explainMissingHeaders(failed, prefix, []string{"make"}))
}

func TestExplainMissingHeaders(t *testing.T) {
	failed := errors.New("failed to run luarocks")
	prefix := tarantool.Prefix{
		Dir: "/usr", Origin: tarantool.PrefixOriginGuess, IncludeDir: "",
		Tried:      []string{"/opt/include/tarantool", "/usr/include/tarantool"},
		Rejected:   map[string]string{"/usr/include/tarantool": "headers of 3.9.0"},
		Executable: "/opt/bin/tarantool", Version: "3.8.1", HeadersVersion: "", Warnings: nil,
	}

	err := explainMissingHeaders(failed, prefix, []string{"install", "--tree", "x", "rock"})
	require.ErrorIs(t, err, failed)
	require.ErrorIs(t, err, ErrNoHeaders)
	assert.Contains(t, err.Error(),
		"no module.h in /opt/include/tarantool; /usr/include/tarantool: headers of 3.9.0")
	assert.Contains(t, err.Error(), tarantool.PrefixEnv)

	// A command that builds nothing is not told of the headers.
	require.Equal(t, failed, explainMissingHeaders(failed, prefix, []string{"search", "rock"}))
}

func TestLuarocksCommand(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"pack", "queue"}, "pack"},
		{[]string{"--tree", "pack", "list"}, "list"},
		{[]string{"--tree=pack", "--verbose", "pack", "queue"}, "pack"},
		{[]string{"--server", "a b", "ZIP=/bin/zip", "install", "pack"}, "install"},
		{[]string{"--verbose"}, ""},
		{[]string{"--only-server"}, ""},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, luarocksCommand(tt.args), tt.args)
	}
}

// TestZipProbe: a run that packs a rock with no zip in PATH and no ZIP of
// its own is given the probe as ZIP; the failure of the run says that zip is
// missing only when LuaRocks ran the probe, and never names the probe.
func TestZipProbe(t *testing.T) {
	withZip := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withZip, zipTool), []byte("#!/bin/sh\n"), 0o755))

	tests := []struct {
		name  string
		path  string
		args  []string
		probe bool
	}{
		{"pack, no zip", t.TempDir(), []string{"pack", "queue"}, true},
		{"--pack-binary-rock, no zip", t.TempDir(), []string{"make", "--pack-binary-rock"}, true},
		{"pack, zip in PATH", withZip, []string{"pack", "queue"}, false},
		{"pack, ZIP assigned", t.TempDir(), []string{"ZIP=/opt/zip", "pack", "queue"}, false},
		{"no pack", t.TempDir(), []string{"install", "pack"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", tt.path)
			t.Setenv("TMPDIR", t.TempDir())

			probe, args := newZipProbe(tt.args)
			defer probe.remove()

			failure := fmt.Errorf("luaEngine: %v exited with code 1", args)

			if !tt.probe {
				require.Nil(t, probe)
				assert.Equal(t, tt.args, args)
				assert.Equal(t, failure, probe.explain(failure))

				return
			}

			require.NotNil(t, probe)
			require.Equal(t, append([]string{"ZIP=" + filepath.Join(probe.dir, zipTool)},
				tt.args...), args)

			// LuaRocks failed before it needed zip: the pack of a rock that
			// is not installed.
			err := probe.explain(failure)
			require.ErrorIs(t, err, failure)
			require.NotErrorIs(t, err, ErrNoZip)
			assert.Equal(t, fmt.Sprintf("luaEngine: %v exited with code 1", tt.args), err.Error())

			// LuaRocks ran zip, as the shell runs it.
			cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c",
				strings.TrimPrefix(args[0], "ZIP=")+" -r rock.zip a b")
			require.Error(t, cmd.Run(), "the probe fails as no zip does")

			err = probe.explain(failure)
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, err, ErrNoZip)
			assert.NotContains(t, err.Error(), probe.dir)

			probe.remove()
			assert.NoDirExists(t, probe.dir)
		})
	}
}
