package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/exp/slices"
)

func Test_CollectInstances(t *testing.T) {
	instancesEnabledPath := filepath.Join("testdata", "instances_enabled")

	instances, err := CollectInstances("script", instancesEnabledPath)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Equal(t, InstanceCtx{
		AppName:        "script",
		InstName:       "script",
		InstanceScript: "testdata/instances_enabled/script.lua",
		SingleApp:      true,
	}, instances[0])

	instances, err = CollectInstances("single_inst", instancesEnabledPath)
	require.NoError(t, err)
	require.Equal(t, 1, len(instances))
	require.Equal(t, InstanceCtx{
		AppDir:         "testdata/instances_enabled/single_inst",
		AppName:        "single_inst",
		InstName:       "single_inst",
		InstanceScript: "testdata/instances_enabled/single_inst/init.lua",
		SingleApp:      true,
	}, instances[0])

	appName := "multi_inst_app"
	appPath := filepath.Join(instancesEnabledPath, appName)
	instances, err = CollectInstances(appName, instancesEnabledPath)
	require.NoError(t, err)
	require.Equal(t, 3, len(instances))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "router",
		InstanceScript: filepath.Join(appPath, "router.init.lua"),
		SingleApp:      false,
	}))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "master1",
		InstanceScript: filepath.Join(appPath, "init.lua"),
		SingleApp:      false,
	}))
	assert.True(t, slices.Contains(instances, InstanceCtx{
		AppDir:         "testdata/instances_enabled/multi_inst_app",
		AppName:        appName,
		InstName:       "stateboard",
		InstanceScript: filepath.Join(appPath, "stateboard.init.lua"),
		SingleApp:      false,
	}))

	// Error cases.
	tmpDir := t.TempDir()
	instancesEnabledPath = filepath.Join(tmpDir, "instances.enabled")
	require.NoError(t, os.Mkdir(instancesEnabledPath, 0755))

	instances, err = CollectInstances("script", instancesEnabledPath)
	assert.ErrorContains(t, err, "script\" doesn't exist or not a directory")
	assert.Equal(t, 0, len(instances))

	err = os.WriteFile(filepath.Join(instancesEnabledPath, "script.lua"),
		[]byte("print(42)"), 0644)
	require.NoError(t, err)
	instances, err = CollectInstances("script", instancesEnabledPath)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(instances))

	require.NoError(t, os.Chmod(instancesEnabledPath, 0666))
	instances, err = CollectInstances("script", instancesEnabledPath)
	assert.ErrorContains(t, err, "script.lua: permission denied")
	assert.Equal(t, 0, len(instances))
	require.NoError(t, os.Chmod(instancesEnabledPath, 0755))
}

func Test_collectAppDirFiles(t *testing.T) {
	tmpdir := t.TempDir()

	_, err := collectAppDirFiles(tmpdir)
	require.Error(t, err)

	expectedDefaultScript := filepath.Join(tmpdir, "init.lua")
	expectedInstancesConfig := filepath.Join(tmpdir, "instances.yml")
	expectedClusterConfig := filepath.Join(tmpdir, "config.yml")

	// Cluster config exists, but no instances config.
	os.Create(expectedClusterConfig)
	appDirFiles, err := collectAppDirFiles(tmpdir)
	require.Error(t, err)
	require.Equal(t, expectedClusterConfig, appDirFiles.clusterCfgPath)
	require.Equal(t, "", appDirFiles.defaultLuaPath)
	require.Equal(t, "", appDirFiles.instCfgPath)

	// Cluster config and default instance script exist, but no instances config.
	os.Create(expectedDefaultScript)
	appDirFiles, err = collectAppDirFiles(tmpdir)
	require.Error(t, err)
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
	clusterAppName := "cluster_app"
	apps := []util.AppListEntry{
		{
			Name:     "cluster_app",
			Location: "./testdata/instances_enabled/cluster_app",
		},
	}
	cfgDir := "/etc/tarantool/"
	cliOpts := configure.GetDefaultCliOpts()
	cliOpts.Env.InstancesEnabled = "./testdata/instances_enabled/"
	instances, err := collectInstancesForApps(apps, cliOpts, "/etc/tarantool/")
	require.NoError(t, err)
	assert.Equal(t, 3, len(instances))

	comparisonsCount := 0
	for _, inst := range instances {
		switch inst.InstName {
		case "instance-001":
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-001"),
				inst.WalDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-001"),
				inst.VinylDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-001"),
				inst.MemtxDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "run", clusterAppName, "instance-001"),
				inst.RunDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "run", clusterAppName, "instance-001",
				"instance-001.control"),
				inst.ConsoleSocket)
			assert.Equal(t, "testdata/instances_enabled/cluster_app/config.yml",
				inst.ClusterConfigPath)
			comparisonsCount++

		case "instance-002":
			assert.Contains(t, inst.WalDir,
				"testdata/instances_enabled/cluster_app/instance-002_wal_dir")
			assert.Contains(t, inst.ConsoleSocket,
				"testdata/instances_enabled/cluster_app/instance-002.control")
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-002"),
				inst.VinylDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-002"),
				inst.MemtxDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "run", clusterAppName, "instance-002"),
				inst.RunDir)
			comparisonsCount++

		case "instance-003":
			assert.Contains(t, inst.MemtxDir,
				"testdata/instances_enabled/cluster_app/instance-003_snap_dir")
			assert.Contains(t, inst.VinylDir,
				"testdata/instances_enabled/cluster_app/instance-003_vinyl_dir")
			assert.Equal(t, filepath.Join(cfgDir, "var", "lib", clusterAppName, "instance-003"),
				inst.WalDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "run", clusterAppName, "instance-003"),
				inst.RunDir)
			assert.Equal(t, filepath.Join(cfgDir, "var", "run", clusterAppName, "instance-003",
				"instance-003.control"),
				inst.ConsoleSocket)
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
