package rocks

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

func TestAddLuarocksRepoOpts(t *testing.T) {
	type args struct {
		cliOpts *config.CliOpts
		args    []string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{
			"Rocks repo is not specified",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "",
					},
				},
				[]string{},
			},
			[]string{},
			false,
		},
		{
			"Nil repo config",
			args{
				&config.CliOpts{
					Repo: nil,
				},
				[]string{},
			},
			[]string{},
			false,
		},
		{
			"Rock repo is specified",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "local_path",
					},
				},
				[]string{},
			},
			[]string{"--server", "local_path"},
			false,
		},
		{
			"Rock repo is specified and --only-server opt is provided",
			args{
				&config.CliOpts{
					Repo: &config.RepoOpts{
						Rocks: "local_path",
					},
				},
				[]string{"--only-server", "/other/repo"},
			},
			[]string{"--only-server", "/other/repo"}, // No --server option is added.
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := addLuarocksRepoOpts(tt.args.cliOpts, tt.args.args)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, got, tt.want)
		})
	}
}

func TestGetRocksRepoPath(t *testing.T) {
	assert.EqualValues(t, "./testdata/repo", getRocksRepoPath("./testdata/repo"))
	assert.EqualValues(t, "./testdata/emptyrepo", getRocksRepoPath("./testdata/emptyrepo"))

	os.Setenv(repoRocksPathEnvVarName, "./other_repo")
	// If env var is set, return it if manifets is missing in passed repo.
	assert.EqualValues(t, "./other_repo", getRocksRepoPath("./testdata/emptyrepo"))
	// Return passed repo path, since manifest exists. Env var is ignored.
	assert.EqualValues(t, "./testdata/repo", getRocksRepoPath("./testdata/repo"))
	os.Unsetenv(repoRocksPathEnvVarName)
}

func TestSetupTarantoolPrefix(t *testing.T) {
	type prefixInput struct {
		cli          cmdcontext.CliCtx
		cliOpts      *config.CliOpts
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

	cwd, err := os.Getwd()
	require.NoError(t, err)

	testCases := make(map[prefixInput]prefixOutput)

	tntOkData := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolCli:           cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntOkData}] =
		prefixOutput{
			prefix: "/usr",
			err:    nil,
		}

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolCli:           cmdcontext.TarantoolCli{Executable: tntBinPath},
	},
		data:         &tntOkData,
		tntPrefixEnv: "/tnt/prefix"}] =
		prefixOutput{
			prefix: "/tnt/prefix",
			err:    nil,
		}

	tntBadData0 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -D_FAIL_HERE_=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolCli:           cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntBadData0}] =
		prefixOutput{
			err: fmt.Errorf("failed to get prefix path: regexp does not match"),
		}

	tntBadData1 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolCli:           cmdcontext.TarantoolCli{Executable: tntBinPath},
	}, data: &tntBadData1}] =
		prefixOutput{
			err: fmt.Errorf("failed to get prefix path: expected more data"),
		}

	appOpts := config.AppOpts{IncludeDir: "hdr"}
	cliOpts := config.CliOpts{App: &appOpts}

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: true,
		TarantoolCli:           cmdcontext.TarantoolCli{Executable: tntBinPath},
	},
		cliOpts:      &cliOpts,
		data:         &tntOkData,
		tntPrefixEnv: "/tnt/prefix"}] =
		prefixOutput{
			prefix: cwd + "/hdr",
			err:    nil,
		}

	for input, output := range testCases {
		tntFile, err := os.Create(tntBinPath)
		require.NoError(t, err)

		_, err = tntFile.Write(*input.data)
		require.NoError(t, err)
		tntFile.Close()

		err = os.Chmod(tntFile.Name(), 0755)
		require.NoError(t, err)

		if input.tntPrefixEnv != "" {
			os.Setenv(tarantoolPrefixEnvVarName, input.tntPrefixEnv)
		}
		tarantoolPrefix, err := GetTarantoolPrefix(&input.cli, input.cliOpts)
		os.Unsetenv(tarantoolPrefixEnvVarName)
		if err == nil {
			assert.Nil(err)
			assert.Equal(output.prefix, tarantoolPrefix)
		} else {
			assert.Equal(output.err, err)
		}

		os.Remove(tntBinPath)
	}
}
