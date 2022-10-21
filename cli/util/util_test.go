package util

import (
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

type inputValue struct {
	re   *regexp.Regexp
	data string
}

type outputValue struct {
	result map[string]string
}

func TestFindNamedMatches(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[inputValue]outputValue)

	testCases[inputValue{re: regexp.MustCompile("(?P<user>.*):(?P<pass>.*)"), data: "toor:1234"}] =
		outputValue{
			result: map[string]string{
				"user": "toor",
				"pass": "1234",
			},
		}

	testCases[inputValue{re: regexp.MustCompile("(?P<user>.*):(?P<pass>[a-z]+)?"),
		data: "toor:1234"}] =
		outputValue{
			result: map[string]string{
				"user": "toor",
				"pass": "",
			},
		}

	for input, output := range testCases {
		result := FindNamedMatches(input.re, input.data)

		assert.Equal(output.result, result)
	}
}

func TestIsDir(t *testing.T) {
	assert := assert.New(t)

	workDir, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	defer os.RemoveAll(workDir)

	require.True(t, IsDir(workDir))

	tmpFile, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	assert.False(IsDir(tmpFile.Name()))
	assert.False(IsDir("./non-existing-dir"))
}

func TestIsRegularFile(t *testing.T) {
	assert := assert.New(t)

	tmpFile, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	require.True(t, IsRegularFile(tmpFile.Name()))

	workDir, err := ioutil.TempDir("", "")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	assert.False(IsRegularFile(workDir))
	assert.False(IsRegularFile("./non-existing-file"))
}

type prefixInput struct {
	cli     cmdcontext.CliCtx
	cliOpts *config.CliOpts
	data    *[]byte
}

type prefixOutput struct {
	prefix  string
	include string
	err     error
}

func TestSetupTarantoolPrefix(t *testing.T) {
	assert := assert.New(t)
	testDir, err := ioutil.TempDir("/tmp", "tt-unit")
	require.NoError(t, err)

	defer os.RemoveAll(testDir)

	err = os.Mkdir(testDir+"/bin", os.ModePerm)
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
		TarantoolExecutable:    tntBinPath,
	}, data: &tntOkData}] =
		prefixOutput{
			prefix:  "/usr",
			include: "/usr/include/tarantool",
			err:     nil,
		}

	tntBadData0 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -D_FAIL_HERE_=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolExecutable:    tntBinPath,
	}, data: &tntBadData0}] =
		prefixOutput{
			err: fmt.Errorf("Failed to get prefix path: regexp does not match"),
		}

	tntBadData1 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolExecutable:    tntBinPath,
	}, data: &tntBadData1}] =
		prefixOutput{
			err: fmt.Errorf("Failed to get prefix path: expected more data"),
		}

	appOpts := config.AppOpts{IncludeDir: "hdr"}
	cliOpts := config.CliOpts{App: &appOpts}

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: true,
		TarantoolExecutable:    tntBinPath,
	}, cliOpts: &cliOpts, data: &tntOkData}] =
		prefixOutput{
			prefix:  cwd + "/hdr",
			include: cwd + "/hdr/include/tarantool",
			err:     nil,
		}

	for input, output := range testCases {
		tntFile, err := os.Create(tntBinPath)
		require.NoError(t, err)

		_, err = tntFile.Write(*input.data)
		require.NoError(t, err)
		tntFile.Close()

		err = os.Chmod(tntFile.Name(), 0755)
		require.NoError(t, err)

		err = SetupTarantoolPrefix(&input.cli, input.cliOpts)
		if err == nil {
			assert.Nil(err)
			assert.Equal(output.prefix, input.cli.TarantoolInstallPrefix)
			assert.Equal(output.include, input.cli.TarantoolIncludeDir)
		} else {
			assert.Equal(output.err, err)
		}

		os.Remove(tntBinPath)
	}
}

func TestGetAbsPath(t *testing.T) {
	require.Equal(t, GetAbsPath("/base/dir", "/abs/path"), "/abs/path")
	require.Equal(t, GetAbsPath("/base/dir", "./abs/path"),
		"/base/dir/abs/path")
	require.Equal(t, GetAbsPath("/base/dir", "abs/path"),
		"/base/dir/abs/path")
}

func TestGenerateDefaultTtConfig(t *testing.T) {
	cfg := GenerateDefaulTtEnvConfig()
	require.Equal(t, cfg.CliConfig.App.BinDir, "bin")
	require.Equal(t, cfg.CliConfig.App.LogMaxAge, 8)
	require.Equal(t, cfg.CliConfig.App.LogMaxBackups, 10)
	require.Equal(t, cfg.CliConfig.App.LogMaxSize, 100)
	require.Equal(t, cfg.CliConfig.App.DataDir, "var/lib")
	require.Equal(t, cfg.CliConfig.App.LogDir, "var/log")
	require.Equal(t, cfg.CliConfig.App.RunDir, "var/run")
	require.Equal(t, cfg.CliConfig.App.IncludeDir, "include")
	require.Equal(t, cfg.CliConfig.Modules.Directory, "modules")
	require.Equal(t, cfg.CliConfig.Templates[0].Path, "templates")
	require.Equal(t, cfg.CliConfig.Repo.Install, "install")
}
