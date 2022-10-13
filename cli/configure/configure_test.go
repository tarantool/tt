package configure

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// cleanupTempDir cleanups temp directory after test.
func cleanupTempDir(tempDir string) {
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		os.RemoveAll(tempDir)
	}
}

// Test Tarantool CLI configuration (system and local).
func TestConfigureCli(t *testing.T) {
	assert := assert.New(t)

	cmdCtx := cmdcontext.CmdCtx{}

	// Test system configuration.
	cmdCtx.Cli.IsSystem = true
	assert.Nil(Cli(&cmdCtx))

	// In fact, cmdCtx.Cli.ConfigPath must contain the path, for example
	// /etc/tarantool/tarantool.yaml on Linux, to the standard configuration file.
	// But, the path to the system configuration file is set at the compilation
	// stage of the application (therefore, we get only the file name `tarantool.yaml`,
	// not the entire path). We cannot set the path to the file at build time because
	// we run `go test`, which compiles the functions again.
	assert.Equal(cmdCtx.Cli.ConfigPath, "tarantool.yaml")

	testDir, err := ioutil.TempDir(os.TempDir(), "tarantool_tt_")
	t.Cleanup(func() { cleanupTempDir(testDir) })
	assert.Nil(err)
	// Test local cofniguration.
	cmdCtx.Cli.IsSystem = false
	cmdCtx.Cli.LocalLaunchDir = testDir
	cmdCtx.Cli.ConfigPath = ""

	expectedConfigPath, err := util.JoinAbspath(testDir, "tarantool.yaml")
	assert.Nil(err)
	ioutil.WriteFile(expectedConfigPath, []byte("tt:\n  app:\n    bin_dir: \".\""), 0755)

	// Create local tarantool and check that it is found during configuration.
	expectedTarantoolPath := filepath.Join(cmdCtx.Cli.LocalLaunchDir, "tarantool")
	assert.Nil(ioutil.WriteFile(
		expectedTarantoolPath, []byte("I am [fake] local Tarantool!"), 0777,
	))

	defer os.Remove(expectedTarantoolPath)

	wd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(wd) // Chdir from local launch dir after the Cli call.

	require.NoError(t, Cli(&cmdCtx))
	assert.Equal(cmdCtx.Cli.ConfigPath, expectedConfigPath)
	assert.Equal(cmdCtx.Cli.TarantoolExecutable, expectedTarantoolPath)

	// Test default configuration (no flags specified).
	cmdCtx.Cli.LocalLaunchDir = ""
	cmdCtx.Cli.ConfigPath = ""
	dir, err := ioutil.TempDir(testDir, "temp")
	assert.Nil(err)

	// Check if it will go down to the bottom of the directory looking
	// for the tarantool.yaml configuration file, specifically skip a file
	// in the working directory.
	os.Chdir(dir)
	expectedConfigPath = filepath.Join(filepath.Dir(dir), "tarantool.yaml")

	assert.Nil(ioutil.WriteFile(
		expectedConfigPath, []byte("tt:\n  app:"), 0755,
	))

	defer os.Remove(expectedConfigPath)

	assert.Nil(Cli(&cmdCtx))
	// I don't know why, but go tests run in /private folder (when running on MacOS).
	if runtime.GOOS == "darwin" {
		expectedConfigPath = filepath.Join("/private", expectedConfigPath)
	}

	assert.Equal(cmdCtx.Cli.ConfigPath, expectedConfigPath)
}

func TestAdjustPathWithConfigLocation(t *testing.T) {
	require.Equal(t, adjustPathWithConfigLocation("", "/config/dir", "bin"),
		"/config/dir/bin")
	require.Equal(t, adjustPathWithConfigLocation("/bin_dir", "/config/dir", "bin"),
		"/bin_dir")
	require.Equal(t, adjustPathWithConfigLocation("./bin_dir", "/config/dir", "bin"),
		"/config/dir/bin_dir")
}

func TestExcludeArgs(t *testing.T) {
	type argsData struct {
		input, expected []string
	}
	testArgsData := []argsData{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"a", "b", "-L"}, []string{"a", "b"}},
		{[]string{"a", "b", "-L", "/dir"}, []string{"a", "b"}},
		{[]string{"a", "-L", "/dir", "b"}, []string{"a", "b"}},
		{[]string{"a", "--local", "/dir", "b"}, []string{"a", "b"}},
	}

	for _, testData := range testArgsData {
		require.Equal(t, excludeArgumentsForChildTt(testData.input), testData.expected)
	}
}

func TestValidateCliOpts(t *testing.T) {
	type cliCtxTest struct {
		input     cmdcontext.CliCtx
		errString string
	}
	testData := []cliCtxTest{
		{cmdcontext.CliCtx{IsSystem: true, ConfigPath: "/tarantool.yaml"},
			"You can specify only one of -S(--system) and -с(--cfg) options"},
		{cmdcontext.CliCtx{LocalLaunchDir: "/", ConfigPath: "/tarantool.yaml"},
			"You can specify only one of -L(--local) and -с(--cfg) options"},
		{cmdcontext.CliCtx{IsSystem: true, LocalLaunchDir: "."},
			"You can specify only one of -L(--local) and -S(--system) options"},
		{cmdcontext.CliCtx{IsSystem: true}, ""},
		{cmdcontext.CliCtx{LocalLaunchDir: "."}, ""},
		{cmdcontext.CliCtx{ConfigPath: "tarantool.yaml"}, ""},
	}

	for _, cliCtxTestData := range testData {
		if cliCtxTestData.errString != "" {
			require.EqualError(t, ValidateCliOpts(&cliCtxTestData.input), cliCtxTestData.errString)
		} else {
			require.NoError(t, ValidateCliOpts(&cliCtxTestData.input))
		}
	}
}

func TestDetectLocalTarantool(t *testing.T) {
	// Tarantool executable is in bin_dir.
	cliOpts := config.CliOpts{App: &config.AppOpts{BinDir: "./testdata/bin_dir"}}
	cmdCtx := cmdcontext.CmdCtx{}
	require.NoError(t, detectLocalTarantool(&cmdCtx, &cliOpts))
	expected, err := filepath.Abs("./testdata/bin_dir/tarantool")
	require.NoError(t, err)
	require.Equal(t, expected, cmdCtx.Cli.TarantoolExecutable)

	// Tarantool executable is in PATH.
	cliOpts.App.BinDir = "./testdata"
	Cli(&cmdCtx)
	require.NoError(t, detectLocalTarantool(&cmdCtx, &cliOpts))
	expected, err = exec.LookPath("tarantool")
	require.NoError(t, err)
	require.Equal(t, expected, cmdCtx.Cli.TarantoolExecutable)
}

func TestDetectLocalTt(t *testing.T) {
	cliOpts := config.CliOpts{App: &config.AppOpts{BinDir: "./testdata/bin_dir"}}
	localTt, err := detectLocalTt(&cliOpts)
	require.NoError(t, err)
	expected, err := filepath.Abs("./testdata/bin_dir/tt")
	require.NoError(t, err)
	require.Equal(t, expected, localTt)

	cliOpts.App.BinDir = "./testdata"
	localTt, err = detectLocalTt(&cliOpts)
	require.NoError(t, err)
	require.Equal(t, "", localTt)
}

func TestGetSystemConfigPath(t *testing.T) {
	require.Equal(t, filepath.Join(defaultConfigPath, ConfigName), getSystemConfigPath())
	os.Setenv(systemConfigDirEnvName, "/system_config_dir")
	defer os.Unsetenv(getSystemConfigPath())
	require.Equal(t, filepath.Join("/system_config_dir", ConfigName), getSystemConfigPath())
}
