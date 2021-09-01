package configure

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/util"
)

// Test Tarantool CLI configuration (system and local).
func TestConfigureCli(t *testing.T) {
	assert := assert.New(t)

	ctx := context.Ctx{}

	// Test system configuration.
	ctx.Cli.IsSystem = true
	assert.Nil(Cli(&ctx))

	// In fact, ctx.Cli.ConfigPath must contain the path, for example
	// /etc/tarantool/tarantool.yaml on Linux, to the standard configuration file.
	// But, the path to the system configuration file is set at the compilation
	// stage of the application (therefore, we get only the file name `tarantool.yaml`,
	// not the entire path). We cannot set the path to the file at build time because
	// we run `go test`, which compiles the functions again.
	assert.Equal(ctx.Cli.ConfigPath, "tarantool.yaml")

	// Test local cofniguration.
	ctx.Cli.IsSystem = false
	ctx.Cli.LocalLaunchDir = os.TempDir()
	ctx.Cli.ConfigPath = ""

	expectedConfigPath, err := util.JoinAbspath(os.TempDir(), "tarantool.yaml")
	assert.Nil(err)
	ioutil.WriteFile(expectedConfigPath, []byte("config-file"), 0755)

	// Create local tarantool and check that it is found during configuration.
	expectedTarantoolPath := filepath.Join(ctx.Cli.LocalLaunchDir, "tarantool")
	assert.Nil(ioutil.WriteFile(
		expectedTarantoolPath, []byte("I am [fake] local Tarantool!"), 0777,
	))

	defer os.Remove(expectedTarantoolPath)

	assert.Nil(Cli(&ctx))
	assert.Equal(ctx.Cli.ConfigPath, expectedConfigPath)
	assert.Equal(ctx.Cli.TarantoolExecutable, expectedTarantoolPath)

	// Test default configuration (no flags specified).
	ctx.Cli.LocalLaunchDir = ""
	ctx.Cli.ConfigPath = ""
	dir, err := ioutil.TempDir(os.TempDir(), "temp")
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

	assert.Nil(Cli(&ctx))
	// I don't know why, but go tests run in /private folder (when running on MacOS).
	if runtime.GOOS == "darwin" {
		expectedConfigPath = filepath.Join("/private", expectedConfigPath)
	}

	assert.Equal(ctx.Cli.ConfigPath, expectedConfigPath)
}
