package configure

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/cmdcontext"
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
	ioutil.WriteFile(expectedConfigPath, []byte("config-file"), 0755)

	// Create local tarantool and check that it is found during configuration.
	expectedTarantoolPath := filepath.Join(cmdCtx.Cli.LocalLaunchDir, "tarantool")
	assert.Nil(ioutil.WriteFile(
		expectedTarantoolPath, []byte("I am [fake] local Tarantool!"), 0777,
	))

	defer os.Remove(expectedTarantoolPath)

	assert.Nil(Cli(&cmdCtx))
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
		expectedConfigPath, []byte("Tarantool CLI configuration file"), 0755,
	))

	defer os.Remove(expectedConfigPath)

	assert.Nil(Cli(&cmdCtx))
	// I don't know why, but go tests run in /private folder (when running on MacOS).
	if runtime.GOOS == "darwin" {
		expectedConfigPath = filepath.Join("/private", expectedConfigPath)
	}

	assert.Equal(cmdCtx.Cli.ConfigPath, expectedConfigPath)
}
