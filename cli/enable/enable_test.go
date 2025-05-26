package enable

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

func TestEnableBase(t *testing.T) {
	tempDir := t.TempDir()

	err := copy.Copy("./testdata", tempDir)
	assert.Nil(t, err)
	cliOpts := &config.CliOpts{
		Env: &config.TtEnvOpts{InstancesEnabled: filepath.Join(tempDir, "in_en")},
	}

	// Test enable application.
	appPath := filepath.Join(tempDir, "test_app")
	err = Enable(appPath, cliOpts)
	assert.Nil(t, err)
	appLink, err := util.ResolveSymlink(filepath.Join(cliOpts.Env.InstancesEnabled, "test_app"))
	assert.Nil(t, err)
	assert.Contains(t, appLink, appPath)

	// Test enable script.
	appPath = filepath.Join(tempDir, "test_script", "test.lua")
	err = Enable(appPath, cliOpts)
	assert.Nil(t, err)
	appLink, err = util.ResolveSymlink(filepath.Join(cliOpts.Env.InstancesEnabled, "test.lua"))
	assert.Nil(t, err)
	assert.Contains(t, appLink, appPath)
}

func TestEnableNoFile(t *testing.T) {
	tempDir := t.TempDir()

	cliOpts := &config.CliOpts{
		Env: &config.TtEnvOpts{InstancesEnabled: filepath.Join(tempDir, "in_en")},
	}

	// Test enable script.
	appPath := filepath.Join(tempDir, "test_script", "test.foo")
	err := Enable(appPath, cliOpts)
	require.ErrorContains(t, err, "cannot get info of")
}

func TestEnableWrongScript(t *testing.T) {
	tempDir := t.TempDir()

	err := copy.Copy("./testdata", tempDir)
	assert.Nil(t, err)
	cliOpts := &config.CliOpts{
		Env: &config.TtEnvOpts{InstancesEnabled: filepath.Join(tempDir, "in_en")},
	}

	// Test enable script.
	appPath := filepath.Join(tempDir, "test_script", "foo.foo")
	err = Enable(appPath, cliOpts)
	require.ErrorContains(t, err, "does not have '.lua' extension")
}

func TestEnableDirNotApp(t *testing.T) {
	tempDir := t.TempDir()

	appPath := filepath.Join(tempDir, "notAppDir")
	err := os.Mkdir(appPath, defaultDirPermissions)
	assert.Nil(t, err)
	cliOpts := &config.CliOpts{
		Env: &config.TtEnvOpts{InstancesEnabled: filepath.Join(tempDir, "in_en")},
	}

	// Test enable empty directory.
	err = Enable(appPath, cliOpts)
	require.ErrorContains(t, err, "is not an application")
}
