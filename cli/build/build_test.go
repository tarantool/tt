package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
)

const testDirName = "build-test-dir"

func TestFillCtx(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0o750))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx BuildCtx

	workDir, _ = os.Getwd()

	appDir := filepath.Join(workDir, "app1")

	cliOpts := &config.CliOpts{
		Env: &config.TtEnvOpts{InstancesEnabled: configure.InstancesEnabledDirName},
	}

	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{"app1"}))
	assert.Equal(t, buildCtx.BuildDir, appDir)

	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{"./app1"}))
	assert.Equal(t, buildCtx.BuildDir, appDir)

	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{}))
	assert.Equal(t, buildCtx.BuildDir, workDir)

	require.EqualError(t, FillCtx(&buildCtx, cliOpts, []string{"app1", "app2"}), "too many args")

	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxInstancesEnabledSupport(t *testing.T) {
	workDir := t.TempDir()

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx BuildCtx

	instancesEnabled, err := filepath.Abs(configure.InstancesEnabledDirName)
	require.NoError(t, err)
	cliOpts := &config.CliOpts{Env: &config.TtEnvOpts{InstancesEnabled: instancesEnabled}}
	require.EqualError(t, FillCtx(&buildCtx, cliOpts, []string{"app2"}),
		fmt.Sprintf("lstat %s: no such file or directory", instancesEnabled))
	require.NoError(t, os.Mkdir(instancesEnabled, 0o750))
	require.EqualError(t, FillCtx(&buildCtx, cliOpts, []string{"app2"}),
		fmt.Sprintf("lstat %s/app2: no such file or directory", instancesEnabled))

	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "subdir", "app2"), 0o750))
	require.NoError(t, os.Symlink("../subdir/app2",
		filepath.Join(instancesEnabled, "app2")))
	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{filepath.Join(workDir, "app2")}))
	assert.True(t, strings.HasSuffix(buildCtx.BuildDir,
		filepath.Join(workDir, "subdir", "app2")))
	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{"app2"}))
	assert.True(t, strings.HasSuffix(buildCtx.BuildDir,
		filepath.Join(workDir, "subdir", "app2")))

	// Create ./app2 directory. It has a priority over app2 from instances enabled.
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "app2"), 0o750))
	require.NoError(t, FillCtx(&buildCtx, cliOpts, []string{"app2"}))
	assert.True(t, strings.HasSuffix(buildCtx.BuildDir, filepath.Join(workDir, "app2")))
}

func TestFillCtxAbsoluteAppPath(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "app1"), 0o750))

	var buildCtx BuildCtx
	require.NoError(t, FillCtx(&buildCtx,
		&config.CliOpts{
			Env: &config.TtEnvOpts{InstancesEnabled: configure.InstancesEnabledDirName},
		},
		[]string{filepath.Join(workDir, "app1")}))
	assert.Equal(t, buildCtx.BuildDir, filepath.Join(workDir, "app1"))
}

func TestFillCtxAppPathIsFile(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app1"), []byte("text"), 0o664))

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer os.Chdir(wd)
	var buildCtx BuildCtx
	workDir, _ = os.Getwd()

	appDir := filepath.Join(workDir, "app1")

	require.EqualError(t, FillCtx(&buildCtx,
		&config.CliOpts{
			Env: &config.TtEnvOpts{InstancesEnabled: configure.InstancesEnabledDirName},
		},
		[]string{"app1"}),
		fmt.Sprintf("%s is not a directory", appDir))
}

func TestFillCtxMultipleArgs(t *testing.T) {
	var buildCtx BuildCtx
	require.EqualError(t, FillCtx(&buildCtx,
		&config.CliOpts{Env: &config.TtEnvOpts{
			InstancesEnabled: configure.InstancesEnabledDirName,
		}},
		[]string{"app1", "app2"}), "too many args")
}
