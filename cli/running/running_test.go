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
	"github.com/tarantool/tt/cli/config"
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

func Test_CollectInstances(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	instancesEnabledPath := filepath.Join("testdata", "instances_enabled")

	instances, err := CollectInstances("script", instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Equal(t, InstanceCtx{
		AppDir:         "testdata/instances_enabled/script",
		AppName:        "script",
		InstName:       "script",
		InstanceScript: "testdata/instances_enabled/script.lua",
		SingleApp:      true,
		IsFileApp:      true,
	}, instances[0])

	instances, err = CollectInstances("single_inst", instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Equal(t, InstanceCtx{
		AppDir:         "testdata/instances_enabled/single_inst",
		AppName:        "single_inst",
		InstName:       "single_inst",
		InstanceScript: "testdata/instances_enabled/single_inst/init.lua",
		SingleApp:      true,
		IsFileApp:      false,
	}, instances[0])

	appName := "multi_inst_app"
	appPath := filepath.Join(instancesEnabledPath, appName)
	instances, err = CollectInstances(appName, instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 3, len(instances))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "router",
		InstanceScript: filepath.Join(appPath, "router.init.lua"),
		SingleApp:      false,
		IsFileApp:      false,
	}))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "master1",
		InstanceScript: filepath.Join(appPath, "init.lua"),
		SingleApp:      false,
		IsFileApp:      false,
	}))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "stateboard",
		InstanceScript: filepath.Join(appPath, "stateboard.init.lua"),
		SingleApp:      false,
		IsFileApp:      false,
	}))

	// Error cases.
	tmpDir := t.TempDir()
	instancesEnabledPath = filepath.Join(tmpDir, "instances.enabled")
	require.NoError(t, os.Mkdir(instancesEnabledPath, 0755))

	instances, err = CollectInstances("script", instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.ErrorContains(t, err, "script\" doesn't exist or not a directory")
	assert.Equal(t, 0, len(instances))

	err = os.WriteFile(filepath.Join(instancesEnabledPath, "script.lua"),
		[]byte("print(42)"), 0644)
	require.NoError(t, err)
	instances, err = CollectInstances("script", instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(instances))

	require.NoError(t, os.Chmod(instancesEnabledPath, 0666))
	instances, err = CollectInstances("script", instancesEnabledPath,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	assert.ErrorContains(t, err, "script.lua: permission denied")
	assert.Equal(t, 1, len(instances))
	require.NoError(t, os.Chmod(instancesEnabledPath, 0755))
}

func Test_CollectInstancesInstanceScript(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	tmpDir := t.TempDir()
	instancesEnabledPath := filepath.Join(tmpDir, "instances.enabled")
	require.NoError(t, os.Mkdir(instancesEnabledPath, 0755))

	err := os.WriteFile(filepath.Join(instancesEnabledPath, "script.lua"),
		[]byte("print(42)"), 0644)
	require.NoError(t, err)

	cases := []struct {
		access os.FileMode
		mode   ConfigLoad
		err    string
	}{
		{
			access: 0666,
			mode:   ConfigLoadAll,
			err:    "script.lua: permission denied",
		},
		{
			access: 0666,
			mode:   ConfigLoadScripts,
			err:    "script.lua: permission denied",
		},
		{
			access: 0755,
			mode:   ConfigLoadSkip,
		},
		{
			access: 0755,
			mode:   ConfigLoadCluster,
		},
		{
			access: 0755,
			mode:   ConfigLoadAll,
		},
	}

	for _, tc := range cases {
		t.Run("test", func(t *testing.T) {
			require.NoError(t, os.Chmod(instancesEnabledPath, tc.access))
			instances, err := CollectInstances("script", instancesEnabledPath,
				integrity.IntegrityCtx{
					Repository: &mockRepository{},
				}, tc.mode)
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(instances))
			}
			require.NoError(t, os.Chmod(instancesEnabledPath, 0755))
		})
	}
}

func Test_CollectInstancesEtcdNotAvailable(t *testing.T) {
	if user, err := user.Current(); err == nil && user.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}
	instancesEnabledPath := filepath.Join("testdata", "instances_enabled")

	cases := []struct {
		mode ConfigLoad
		err  string
	}{
		{
			mode: ConfigLoadAll,
			err:  "unable to connect to etcd",
		},
		{
			mode: ConfigLoadCluster,
			err:  "unable to connect to etcd",
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
			_, err := CollectInstances("config_load", instancesEnabledPath,
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

func Test_collectInstancesForApps(t *testing.T) {
	appName := "cluster_app"
	instancesEnabled, err := filepath.Abs("./testdata/instances_enabled")
	require.NoError(t, err)
	appLocation := filepath.Join(instancesEnabled, appName)
	apps := []string{appName}
	cliOpts := configure.GetDefaultCliOpts()
	cliOpts.Env.InstancesEnabled = instancesEnabled
	instances, err := CollectInstancesForApps(apps, cliOpts, "/etc/tarantool/",
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Contains(t, instances, appName)

	comparisonsCount := 0
	for _, inst := range instances[appName] {
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
echo "Tarantool 2.11.0"`),
		0755)
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

	canStart, reason := IsAbleToStartInstances([]InstanceCtx{
		{
			InstanceScript:    "init.lua",
			ClusterConfigPath: "config.yml",
		},
	}, &cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			TarantoolCli: cmdcontext.TarantoolCli{
				Executable: filepath.Join(tmpDir, "tnt.sh"),
			},
		},
	})
	assert.False(t, canStart)
	assert.Contains(t, reason, "supported by Tarantool starting from version 3.0")

	err = os.WriteFile(filepath.Join(tmpDir, "tnt_non_executable.sh"),
		[]byte(`#!/bin/bash
echo "Tarantool 2.11.0"`), 0644)
	require.NoError(t, err)
	canStart, reason = IsAbleToStartInstances([]InstanceCtx{
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

func Test_collectInstancesForSingleInstApp(t *testing.T) {
	appName := "script"
	instancesEnabled, err := filepath.Abs("./testdata/instances_enabled")
	require.NoError(t, err)
	apps := []string{appName + ".lua"}
	appDir := filepath.Join(instancesEnabled, appName)
	cliOpts := configure.GetDefaultCliOpts()
	cliOpts.Env.InstancesEnabled = instancesEnabled
	instances, err := CollectInstancesForApps(apps, cliOpts, "/etc/tarantool/",
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Contains(t, instances, appName)

	inst := instances[appName][0]
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
	assert.Equal(t, filepath.Join(instancesEnabled, appName), inst.AppDir)
}

func Test_collectInstancesSingleInstanceTntCtlLayout(t *testing.T) {
	appName := "script"
	instancesEnabled, err := filepath.Abs("./testdata/instances_enabled")
	require.NoError(t, err)
	apps := []string{appName + ".lua"}
	cliOpts := configure.GetDefaultCliOpts()
	cliOpts.Env.InstancesEnabled = instancesEnabled
	cliOpts.Env.TarantoolctlLayout = true
	cfgDir := "/etc/tarantool/"
	instances, err := CollectInstancesForApps(apps, cliOpts, cfgDir,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, ConfigLoadAll)
	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Contains(t, instances, appName)
	require.Len(t, instances[appName], 1)

	inst := instances[appName][0]
	assert.Equal(t, filepath.Join(cfgDir, "var", "lib", appName), inst.WalDir)
	assert.Equal(t, filepath.Join(cfgDir, "var", "lib", appName), inst.VinylDir)
	assert.Equal(t, filepath.Join(cfgDir, "var", "lib", appName), inst.MemtxDir)
	assert.Equal(t, filepath.Join(cfgDir, "var", "run"), inst.RunDir)
	assert.Equal(t, filepath.Join(cfgDir, "var", "run", appName+".pid"), inst.PIDFile)
	assert.Equal(t, filepath.Join(cfgDir, "var", "run", appName+".control"),
		inst.ConsoleSocket)
	assert.Equal(t, filepath.Join(cfgDir, "var", "log"), inst.LogDir)
	assert.Equal(t, filepath.Join(cfgDir, "var", "log", appName+".log"), inst.Log)
	assert.Equal(t, "", inst.ClusterConfigPath)
	assert.Equal(t, filepath.Join(instancesEnabled, appName), inst.AppDir)
}

func Test_getInstanceName(t *testing.T) {
	for _, tc := range []struct {
		fullInstanceName  string
		isClusterInstance bool
		expected          string
	}{
		{"master", false, "master"},
		{"app.master", false, "master"},
		{"app-stateboard", false, "stateboard"},
		{"app-master", false, "app-master"},
		{"app.inst-001", false, "inst-001"},
		{"app-master", true, "app-master"},
		{"app-stateboard", true, "app-stateboard"},
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
	instEnabled := filepath.Join("testdata", "instances_enabled")
	defaultCliOpts := &config.CliOpts{Env: &config.TtEnvOpts{InstancesEnabled: instEnabled}}
	cases := []struct {
		cliOpts     *config.CliOpts
		ttConfigDir string
		app         string
		mustExist   bool
		expected    string
		wantErr     bool
	}{
		{
			cliOpts:   defaultCliOpts,
			app:       "cluster_app",
			mustExist: true,
			expected:  filepath.Join(instEnabled, "cluster_app", "config.yml"),
		},
		{
			cliOpts:   &config.CliOpts{Env: &config.TtEnvOpts{InstancesEnabled: instEnabled}},
			app:       "cluster_app_yaml_config_extension",
			mustExist: true,
			expected: filepath.Join(instEnabled, "cluster_app_yaml_config_extension",
				"config.yaml"),
		},
		{
			cliOpts:   defaultCliOpts,
			app:       "single_inst",
			mustExist: true,
			wantErr:   true,
		},
		{
			cliOpts:   defaultCliOpts,
			app:       "single_inst",
			mustExist: false,
			expected:  filepath.Join(instEnabled, "single_inst", "config.yml"),
		},
		{
			cliOpts: &config.CliOpts{
				Env: &config.TtEnvOpts{
					InstancesEnabled: ".",
				},
			},
			ttConfigDir: filepath.Join(instEnabled, "cluster_app"),
			app:         "cluster_app",
			mustExist:   true,
			expected:    filepath.Join(instEnabled, "cluster_app", "config.yml"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.app, func(t *testing.T) {
			actual, err := GetClusterConfigPath(tc.cliOpts, tc.ttConfigDir, tc.app, tc.mustExist)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
