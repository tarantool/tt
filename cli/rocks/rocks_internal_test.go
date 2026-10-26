package rocks

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
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
	testDir := t.TempDir()
	err := os.Mkdir(testDir+"/bin", os.ModePerm)
	require.NoError(t, err)

	tntBinPath := testDir + "/bin/tarantool"

	testCases := make(map[prefixInput]prefixOutput)

	tntOkData := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntOkData}] = prefixOutput{
		prefix: "/usr",
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
		err: errPrefixPathRegexpMismatch,
	}

	tntBadData1 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		TarantoolCli: cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntBadData1}] = prefixOutput{
		err: errPrefixPathExpectedMoreData,
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
		_ = os.Unsetenv(tarantoolPrefixEnvVarName)

		tntFile, err := os.Create(tntBinPath)
		require.NoError(t, err)

		_, err = tntFile.Write(*input.data)
		require.NoError(t, err)

		_ = tntFile.Close()

		err = os.Chmod(tntFile.Name(), 0o755)
		require.NoError(t, err)

		if input.tntPrefixEnv != "" {
			t.Setenv(tarantoolPrefixEnvVarName, input.tntPrefixEnv)
		}

		tarantoolPrefix, err := GetTarantoolPrefix(&input.cli)

		_ = os.Unsetenv(tarantoolPrefixEnvVarName)

		if err == nil {
			require.NoError(t, err)
			assert.Equal(output.prefix, tarantoolPrefix)
		} else {
			assert.Equal(output.err, err)
		}

		_ = os.Remove(tntBinPath)
	}
}
