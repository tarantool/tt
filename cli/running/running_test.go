package running

import (
	"io"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/lib/integrity"
	"golang.org/x/exp/slices"
)

type mockRepository struct{}

func (mock *mockRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (mock *mockRepository) ValidateAll() error {
	return nil
}

func TestCollectInstances(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	applicationsRoot := filepath.Join("testdata", "applications")
	singleAppPath := filepath.Join(applicationsRoot, "single_inst")

	instances, err := collectInstances("single_inst", singleAppPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Equal(t, InstanceCtx{
		AppDir:         "testdata/applications/single_inst",
		AppName:        "single_inst",
		InstName:       "single_inst",
		InstanceScript: "testdata/applications/single_inst/init.lua",
		SingleApp:      true,
		IsFileApp:      false,
	}, instances[0])

	appName := "multi_inst_app"
	appPath := filepath.Join(applicationsRoot, appName)
	instances, err = collectInstances(appName, appPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 3, len(instances))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/applications/multi_inst_app",
		AppName:        appName,
		InstName:       "router",
		InstanceScript: filepath.Join(appPath, "router.init.lua"),
		SingleApp:      false,
		IsFileApp:      false,
	}))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/applications/multi_inst_app",
		AppName:        appName,
		InstName:       "master1",
		InstanceScript: filepath.Join(appPath, "init.lua"),
		SingleApp:      false,
		IsFileApp:      false,
	}))
	_, err = collectInstances("another_app", singleAppPath, integrity.IntegrityCtx{
		Repository: &mockRepository{},
	}, ConfigLoadAll)
	assert.ErrorContains(t, err, `application "another_app" not found`)

	appPath = filepath.Join(t.TempDir(), "script")
	require.NoError(t, os.Mkdir(appPath, 0o755))

	instances, err = collectInstances("script", appPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.ErrorContains(t, err, "require files are missing")
	assert.Equal(t, 0, len(instances))

	err = os.WriteFile(filepath.Join(appPath, "script.lua"),
		[]byte("print(42)"), 0o644)
	require.NoError(t, err)
	instances, err = collectInstances("script", appPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(instances))

	require.NoError(t, os.Chmod(appPath, 0o666))
	instances, err = collectInstances("script", appPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.ErrorContains(t, err, "script.lua: permission denied")
	assert.Equal(t, 1, len(instances))
	require.NoError(t, os.Chmod(appPath, 0o755))
}

func TestCollectInstancesInstanceScript(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "script")
	require.NoError(t, os.Mkdir(appPath, 0o755))

	err := os.WriteFile(filepath.Join(appPath, "script.lua"),
		[]byte("print(42)"), 0o644)
	require.NoError(t, err)

	cases := []struct {
		access os.FileMode
		mode   ConfigLoad
		err    string
	}{
		{
			access: 0o666,
			mode:   ConfigLoadAll,
			err:    "script.lua: permission denied",
		},
		{
			access: 0o666,
			mode:   ConfigLoadScripts,
			err:    "script.lua: permission denied",
		},
		{
			access: 0o755,
			mode:   ConfigLoadSkip,
		},
		{
			access: 0o755,
			mode:   ConfigLoadCluster,
		},
		{
			access: 0o755,
			mode:   ConfigLoadAll,
		},
	}

	for _, tc := range cases {
		t.Run("test", func(t *testing.T) {
			require.NoError(t, os.Chmod(appPath, tc.access))
			instances, err := collectInstances("script", appPath,
				integrity.IntegrityCtx{
					Repository: &mockRepository{},
				}, tc.mode)
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(instances))
			}
			require.NoError(t, os.Chmod(appPath, 0o755))
		})
	}
}

func TestCollectInstancesEtcdNotAvailable(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	appPath := filepath.Join("testdata", "applications", "config_load")

	cases := []struct {
		mode ConfigLoad
		err  string
	}{
		{
			mode: ConfigLoadAll,
			err:  "etcd",
		},
		{
			mode: ConfigLoadCluster,
			err:  "etcd",
		},
		{
			mode: ConfigLoadScripts,
		},
		{
			mode: ConfigLoadSkip,
		},
	}

	for _, tc := range cases {
		t.Run(tc.err, func(t *testing.T) {
			_, err := collectInstances("config_load", appPath,
				integrity.IntegrityCtx{
					Repository: &mockRepository{},
				}, tc.mode)
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_collectAppDirFiles(t *testing.T) {
	tmpdir := t.TempDir()

	_, err := collectAppDirFiles(tmpdir)
	require.NoError(t, err)

	expectedDefaultScript := filepath.Join(tmpdir, "init.lua")
	expectedInstancesConfig := filepath.Join(tmpdir, "instances.yml")
	expectedClusterConfig := filepath.Join(tmpdir, "config.yml")

	// Cluster config exists, but no instances config.
	os.Create(expectedClusterConfig)
	appDirFiles, err := collectAppDirFiles(tmpdir)
	require.NoError(t, err)
	require.Equal(t, expectedClusterConfig, appDirFiles.clusterCfgPath)
	require.Equal(t, "", appDirFiles.defaultLuaPath)
	require.Equal(t, "", appDirFiles.instCfgPath)

	// Cluster config and default instance script exist, but no instances config.
	os.Create(expectedDefaultScript)
	appDirFiles, err = collectAppDirFiles(tmpdir)
	require.NoError(t, err)
	require.Equal(t, expectedClusterConfig, appDirFiles.clusterCfgPath)
	require.Equal(t, expectedDefaultScript, appDirFiles.defaultLuaPath)
	require.Equal(t, "", appDirFiles.instCfgPath)

	// All files exist.
	os.Create(expectedInstancesConfig)
	appDirFiles, err = collectAppDirFiles(tmpdir)
	require.NoError(t, err)
	require.Equal(t, expectedClusterConfig, appDirFiles.clusterCfgPath)
	require.Equal(t, expectedDefaultScript, appDirFiles.defaultLuaPath)
	require.Equal(t, expectedInstancesConfig, appDirFiles.instCfgPath)

	// No default script.
	os.Remove(expectedDefaultScript)
	appDirFiles, err = collectAppDirFiles(tmpdir)
	require.NoError(t, err)
	require.Equal(t, expectedClusterConfig, appDirFiles.clusterCfgPath)
	require.Equal(t, "", appDirFiles.defaultLuaPath)
	require.Equal(t, expectedInstancesConfig, appDirFiles.instCfgPath)

	// Only instances config.
	os.Remove(expectedClusterConfig)
	appDirFiles, err = collectAppDirFiles(tmpdir)
	require.NoError(t, err)
	require.Equal(t, "", appDirFiles.clusterCfgPath)
	require.Equal(t, "", appDirFiles.defaultLuaPath)
	require.Equal(t, expectedInstancesConfig, appDirFiles.instCfgPath)
}

func TestCollectInstancesForApp(t *testing.T) {
	appName := "cluster_app"
	applicationsRoot, err := filepath.Abs("./testdata/applications")
	require.NoError(t, err)
	appLocation := filepath.Join(applicationsRoot, appName)
	cliOpts := configure.GetDefaultCliOpts()
	instances, err := CollectInstancesForApp(appName, cliOpts, appLocation,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)

	comparisonsCount := 0
	for _, inst := range instances {
		switch inst.InstName {
		case "instance-001":
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-001"),
				inst.WalDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-001"),
				inst.VinylDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-001"),
				inst.MemtxDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-001"),
				inst.RunDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-001", "tt.pid"),
				inst.PIDFile)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-001",
				"tarantool.control"), inst.ConsoleSocket)
			assert.Equal(t, filepath.Join(appLocation, "var", "log", "instance-001", "tt.log"),
				inst.Log)
			assert.Equal(t, filepath.Join(appLocation, "config.yml"), inst.ClusterConfigPath)
			comparisonsCount++

		case "instance-002":
			assert.Contains(t, inst.WalDir, filepath.Join(appLocation, "instance-002_wal_dir"))
			assert.Contains(t, inst.ConsoleSocket, filepath.Join(appLocation,
				"instance-002.control"))
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-002"),
				inst.VinylDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-002"),
				inst.MemtxDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-002"),
				inst.RunDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-002", "tt.pid"),
				inst.PIDFile)
			assert.Equal(t, filepath.Join(appLocation, "instance-002.control"), inst.ConsoleSocket)
			comparisonsCount++

		case "instance-003":
			assert.Contains(t, inst.MemtxDir, filepath.Join(appLocation, "instance-003_snap_dir"))
			assert.Contains(t, inst.VinylDir, filepath.Join(appLocation, "instance-003_vinyl_dir"))
			assert.Equal(t, filepath.Join(appLocation, "var", "lib", "instance-003"),
				inst.WalDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-003"),
				inst.RunDir)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-003", "tt.pid"),
				inst.PIDFile)
			assert.Equal(t, filepath.Join(appLocation, "var", "run", "instance-003",
				"tarantool.control"), inst.ConsoleSocket)
			comparisonsCount++

		default:
			t.Fatalf("unknown %q", inst.InstName)
		}
	}
	require.Equal(t, 3, comparisonsCount)
}

func TestIsAbleToStartInstances(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "tnt.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 3.0.0"`),
		0o755)
	require.NoError(t, err)

	canStart, _ := IsAbleToStartInstances([]InstanceCtx{
		{
			InstanceScript: "init.lua",
		},
	}, &cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			TarantoolCli: cmdcontext.TarantoolCli{
				Executable: filepath.Join(tmpDir, "tnt.sh"),
			},
		},
	})
	assert.True(t, canStart)

	err = os.WriteFile(filepath.Join(tmpDir, "tnt_non_executable.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 3.0.0"`), 0o644)
	require.NoError(t, err)
	canStart, reason := IsAbleToStartInstances([]InstanceCtx{
		{
			InstanceScript: "init.lua",
		},
	}, &cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			TarantoolCli: cmdcontext.TarantoolCli{
				Executable: filepath.Join(tmpDir, "tnt_non_executable.sh"),
			},
		},
	})
	assert.False(t, canStart)
	assert.Contains(t, reason, "permission denied")
}

func TestCollectInstancesForFileApp(t *testing.T) {
	appName := "script"
	appDir := filepath.Join(t.TempDir(), appName)
	require.NoError(t, os.Mkdir(appDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, appName+".lua"),
		[]byte("print(42)"), 0o644))

	cliOpts := configure.GetDefaultCliOpts()
	instances, err := CollectInstancesForApp(appName+".lua", cliOpts, appDir,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))

	inst := instances[0]
	assert.Equal(t, filepath.Join(appDir, "var", "lib", appName), inst.WalDir)
	assert.Equal(t, filepath.Join(appDir, "var", "lib", appName), inst.VinylDir)
	assert.Equal(t, filepath.Join(appDir, "var", "lib", appName), inst.MemtxDir)
	assert.Equal(t, filepath.Join(appDir, "var", "run", appName), inst.RunDir)
	assert.Equal(t, filepath.Join(appDir, "var", "run", appName, "tt.pid"), inst.PIDFile)
	assert.Equal(t, filepath.Join(appDir, "var", "run", appName, "tarantool.control"),
		inst.ConsoleSocket)
	assert.Equal(t, filepath.Join(appDir, "var", "log", appName), inst.LogDir)
	assert.Equal(t, filepath.Join(appDir, "var", "log", appName, "tt.log"), inst.Log)
	assert.Equal(t, "", inst.ClusterConfigPath)
	assert.Equal(t, appDir, inst.AppDir)
	assert.Equal(t, filepath.Join(appDir, appName+".lua"), inst.InstanceScript)
}

func Test_getInstanceName(t *testing.T) {
	for _, tc := range []struct {
		fullInstanceName  string
		isClusterInstance bool
		expected          string
	}{
		{"master", false, "master"},
		{"app.master", false, "master"},
		{"app-master", false, "app-master"},
		{"app.inst-001", false, "inst-001"},
		{"app-master", true, "app-master"},
		{"app.inst-001", true, "app.inst-001"},
	} {
		actual := getInstanceName(tc.fullInstanceName, tc.isClusterInstance)
		assert.Equal(t, tc.expected, actual)
	}
}

func TestGetAppPath(t *testing.T) {
	assert.Equal(t, "/path/to/app/init.lua", GetAppPath(InstanceCtx{
		InstanceScript: "/path/to/app/init.lua",
		AppDir:         "/path/to/app/",
		SingleApp:      true,
		IsFileApp:      true,
	}))
	assert.Equal(t, "/path/to/app/init.lua", GetAppPath(InstanceCtx{
		InstanceScript: "/path/to/app/init.lua",
		AppDir:         "/path/to/app/",
		SingleApp:      false,
		IsFileApp:      true,
	}))
	assert.Equal(t, "/path/to/app/", GetAppPath(InstanceCtx{
		InstanceScript: "/path/to/app/init.lua",
		AppDir:         "/path/to/app/",
		SingleApp:      true,
	}))
	assert.Equal(t, "/path/to/app/", GetAppPath(InstanceCtx{
		InstanceScript: "/path/to/app/init.lua",
		AppDir:         "/path/to/app/",
		SingleApp:      false,
	}))
}

func TestGetClusterConfigPath(t *testing.T) {
	applicationsRoot := filepath.Join("testdata", "applications")
	cases := []struct {
		name        string
		ttConfigDir string
		mustExist   bool
		expected    string
		wantErr     bool
	}{
		{
			name:        "yml config",
			ttConfigDir: filepath.Join(applicationsRoot, "cluster_app"),
			mustExist:   true,
			expected:    filepath.Join(applicationsRoot, "cluster_app", "config.yml"),
		},
		{
			name:        "yaml config",
			ttConfigDir: filepath.Join(applicationsRoot, "cluster_app_yaml_config_extension"),
			mustExist:   true,
			expected: filepath.Join(applicationsRoot, "cluster_app_yaml_config_extension",
				"config.yaml"),
		},
		{
			name:        "missing config",
			ttConfigDir: filepath.Join(applicationsRoot, "single_inst"),
			mustExist:   true,
			wantErr:     true,
		},
		{
			name:        "default config path",
			ttConfigDir: filepath.Join(applicationsRoot, "single_inst"),
			mustExist:   false,
			expected:    filepath.Join(applicationsRoot, "single_inst", "config.yml"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := GetClusterConfigPath(tc.ttConfigDir, tc.mustExist)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
