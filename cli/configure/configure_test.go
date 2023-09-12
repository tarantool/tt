package configure

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// Test Tarantool CLI configuration (system and local).
func TestConfigureCli(t *testing.T) {
	assert := assert.New(t)

	cmdCtx := cmdcontext.CmdCtx{}

	// Test system configuration.
	cmdCtx.Cli.IsSystem = true
	assert.Nil(Cli(&cmdCtx))

	// In fact, cmdCtx.Cli.ConfigPath must contain the path, for example
	// /etc/tarantool/tt.yaml on Linux, to the standard configuration file.
	// But, the path to the system configuration file is set at the compilation
	// stage of the application (therefore, we get only the file name `tt.yaml`,
	// not the entire path). We cannot set the path to the file at build time because
	// we run `go test`, which compiles the functions again.
	assert.Equal(cmdCtx.Cli.ConfigPath, ConfigName)

	testDir := t.TempDir()

	// Test local cofniguration.
	cmdCtx.Cli.IsSystem = false
	cmdCtx.Cli.LocalLaunchDir = testDir
	cmdCtx.Cli.ConfigPath = ""

	expectedConfigPath, err := util.JoinAbspath(testDir, ConfigName)
	assert.Nil(err)
	require.NoError(t, os.WriteFile(expectedConfigPath, []byte(`tt:
  env:
    bin_dir: "."
`), 0644))

	// Create local tarantool and check that it is found during configuration.
	expectedTarantoolPath := filepath.Join(cmdCtx.Cli.LocalLaunchDir, "tarantool")
	assert.Nil(os.WriteFile(
		expectedTarantoolPath, []byte("I am [fake] local Tarantool!"), 0777,
	))

	defer os.Remove(expectedTarantoolPath)

	wd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(wd) // Chdir from local launch dir after the Cli call.

	require.NoError(t, Cli(&cmdCtx))
	assert.Equal(cmdCtx.Cli.ConfigPath, expectedConfigPath)
	assert.Equal(cmdCtx.Cli.TarantoolCli.Executable, expectedTarantoolPath)

	// Test default configuration (no flags specified).
	cmdCtx.Cli.LocalLaunchDir = ""
	cmdCtx.Cli.ConfigPath = ""
	dir := t.TempDir()

	// Check if it will go down to the bottom of the directory looking
	// for the tt.yaml configuration file, specifically skip a file
	// in the working directory.
	os.Chdir(dir)
	expectedConfigPath = filepath.Join(filepath.Dir(dir), ConfigName)

	assert.Nil(os.WriteFile(
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
	type args struct {
		filePath    string
		baseDir     string
		defaultPath string
	}

	cwd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name     string
		args     args
		wantPath string
		wantErr  bool
	}{
		{
			name:     "Test empty file path",
			args:     args{"", "/abs/path", "bin"},
			wantPath: "/abs/path/bin",
			wantErr:  false,
		},
		{
			name:     "Test absolute file path",
			args:     args{"/abs/my_bin", "/base/dir", "bin"},
			wantPath: "/abs/my_bin",
			wantErr:  false,
		},
		{
			name:     "Test relative file path",
			args:     args{"./rel/my_bin", "/base/dir", "bin"},
			wantPath: "/base/dir/rel/my_bin",
			wantErr:  false,
		},
		{
			name:     "Test relative file path without dot",
			args:     args{"rel/my_bin", "/base/dir", "bin"},
			wantPath: "/base/dir/rel/my_bin",
			wantErr:  false,
		},
		{
			name:     "Test relative base dir",
			args:     args{"my_bin", "rel/path", "bin"},
			wantPath: filepath.Join(cwd, "rel/path/my_bin"),
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str, err := adjustPathWithConfigLocation(tt.args.filePath, tt.args.baseDir,
				tt.args.defaultPath)
			if tt.wantErr {
				require.Error(t, err)
				return
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, tt.wantPath, str)
		})
	}
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
		{cmdcontext.CliCtx{IsSystem: true, ConfigPath: "/" + ConfigName},
			"you can specify only one of -S(--system) and -с(--cfg) options"},
		{cmdcontext.CliCtx{LocalLaunchDir: "/", ConfigPath: "/" + ConfigName},
			"you can specify only one of -L(--local) and -с(--cfg) options"},
		{cmdcontext.CliCtx{IsSystem: true, LocalLaunchDir: "."},
			"you can specify only one of -L(--local) and -S(--system) options"},
		{cmdcontext.CliCtx{IsSystem: true}, ""},
		{cmdcontext.CliCtx{LocalLaunchDir: "."}, ""},
		{cmdcontext.CliCtx{ConfigPath: ConfigName}, ""},
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
	cliOpts := config.CliOpts{Env: &config.TtEnvOpts{BinDir: "./testdata/bin_dir"}}
	cmdCtx := cmdcontext.CmdCtx{}
	require.NoError(t, detectLocalTarantool(&cmdCtx, &cliOpts))
	expected, err := filepath.Abs("./testdata/bin_dir/tarantool")
	require.NoError(t, err)
	require.Equal(t, expected, cmdCtx.Cli.TarantoolCli.Executable)

	// Chdir to temporary directory to avoid loading tt.yaml from parent directories.
	wd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(t.TempDir())
	require.NoError(t, err)
	defer os.Chdir(wd)

	// Tarantool executable is in PATH.
	cliOpts.Env.BinDir = "./testdata"
	Cli(&cmdCtx)
	require.NoError(t, detectLocalTarantool(&cmdCtx, &cliOpts))
	expected, err = exec.LookPath("tarantool")
	require.NoError(t, err)
	require.Equal(t, expected, cmdCtx.Cli.TarantoolCli.Executable)
}

func TestDetectLocalTt(t *testing.T) {
	cliOpts := config.CliOpts{Env: &config.TtEnvOpts{BinDir: "./testdata/bin_dir"}}
	localTt, err := detectLocalTt(&cliOpts)
	require.NoError(t, err)
	expected, err := filepath.Abs("./testdata/bin_dir/tt")
	require.NoError(t, err)
	require.Equal(t, expected, localTt)

	cliOpts.Env.BinDir = "./testdata"
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

func TestGetConfigPath(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "a", "b"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a", ConfigName), []byte("tt:"),
		0664))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "a", "tt.yml"), []byte("tt:"),
		0664))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "tt.yaml"), []byte("tt:"),
		0664))

	if wd, err := os.Getwd(); err == nil {
		require.NoError(t, os.Chdir(filepath.Join(tempDir, "a", "b")))
		defer os.Chdir(wd)
	}
	workdir, _ := os.Getwd()
	workdir = strings.TrimSuffix(workdir, "/a/b")

	configName, err := getConfigPath(ConfigName)
	assert.Equal(t, "", configName)
	assert.True(t, strings.Contains(err.Error(), "more than one YAML files are found"))

	require.NoError(t, os.Remove(filepath.Join(tempDir, "a", ConfigName)))

	configName, err = getConfigPath(ConfigName)

	assert.Equal(t, filepath.Join(workdir, "a", "tt.yml"), configName)
	assert.NoError(t, err)
}

func TestUpdateCliOpts(t *testing.T) {
	cliOpts := config.CliOpts{
		App: &config.AppOpts{
			RunDir:   "/var/run",
			LogDir:   "var/log",
			WalDir:   "./var/lib/wal",
			VinylDir: "./var/lib/vinyl",
			MemtxDir: "./var/lib/snap",
		},
		Env: &config.TtEnvOpts{
			IncludeDir: "../include_dir",
			LogMaxAge:  42,
			LogMaxSize: 200,
		},
	}
	configDir := "/etc/tarantool"

	err := updateCliOpts(&cliOpts, configDir)
	require.NoError(t, err)
	assert.Equal(t, "/var/run", cliOpts.App.RunDir)
	assert.Equal(t, filepath.Join(configDir, "var", "log"), cliOpts.App.LogDir)
	assert.Equal(t, filepath.Join(configDir, "var", "lib", "wal"), cliOpts.App.WalDir)
	assert.Equal(t, filepath.Join(configDir, "var", "lib", "vinyl"), cliOpts.App.VinylDir)
	assert.Equal(t, filepath.Join(configDir, "var", "lib", "snap"), cliOpts.App.MemtxDir)
	assert.Equal(t, filepath.Join(configDir, "..", "include_dir"), cliOpts.Env.IncludeDir)
	assert.Equal(t, filepath.Join(configDir, ModulesPath), cliOpts.Modules.Directory)
	assert.Equal(t, ".", cliOpts.Env.InstancesEnabled)
	assert.Equal(t, 42, cliOpts.Env.LogMaxAge)
	assert.Equal(t, logMaxBackups, cliOpts.Env.LogMaxBackups)
	assert.Equal(t, 200, cliOpts.Env.LogMaxSize)
}
