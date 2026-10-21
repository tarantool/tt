package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

func TestGetTarPackageName(t *testing.T) {
	testDir, err := filepath.Abs(".")
	require.NoErrorf(t, err, "failed to get the test directory absolute path")

	arch, err := util.GetArch()
	require.NoError(t, err)

	testCases := []struct {
		name          string
		packCtx       *PackCtx
		opts          *config.CliOpts
		expectedName  string
		expectedError error
	}{
		{
			name:    "No parameters in context",
			packCtx: &PackCtx{Type: Tgz},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: testDir},
			},
			expectedName: filepath.Base(testDir) + "-0.1.0.0." + arch + ".tar.gz",
		},
		{
			name: "Set package name, without version",
			packCtx: &PackCtx{
				Type: Tgz,
				Name: "test",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: testDir},
			},
			expectedName: "test-0.1.0.0." + arch + ".tar.gz",
		},
		{
			name: "Set package name and version",
			packCtx: &PackCtx{
				Type:    Tgz,
				Name:    "test",
				Version: "2.1.1",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: testDir},
			},
			expectedName: "test-2.1.1." + arch + ".tar.gz",
		},
		{
			name: "Set package full filename",
			packCtx: &PackCtx{
				Type:     Tgz,
				FileName: "test",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: testDir},
			},
			expectedName: "test",
		},
		{
			name: "Set package full filename, package name and version",
			packCtx: &PackCtx{
				Type:     Tgz,
				FileName: "test",
				Name:     "unused",
				Version:  "unused",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: testDir},
			},
			expectedName: "test",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			suffix, _ := getTgzSuffix()
			packageName, err := getPackageFileName(testCase.packCtx, testCase.opts, suffix, true)
			require.ErrorIs(t, err, testCase.expectedError)
			require.Equalf(t, testCase.expectedName, packageName,
				"Got wrong package name, expected: %s, got: %s",
				testCase.expectedName, packageName)
		})
	}
}

func TestGenerateVersionFile(t *testing.T) {
	bundlePath := t.TempDir()
	manifestPath := filepath.Join(bundlePath, rocksManifestPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(manifestPath), 0o755))
	require.NoError(t, os.WriteFile(manifestPath, []byte(`commands = {}
dependencies = {
   app_name = {
      ["scm-1"] = {}
   },
   stat = {
      ["0.3.2-1"] = {}
   },
   vshard = {
      ["0.1.37-1"] = {}
   }
}
modules = {}
repository = {}
`), 0o644))

	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			TarantoolCli: cmdcontext.TarantoolCli{
				Executable: "testdata/single_app/bin/tarantool",
			},
		},
	}
	packCtx := PackCtx{Name: "app_name", Version: "1.2.3"}
	require.NoError(t, generateVersionFile(bundlePath, &cmdCtx, &packCtx))

	content, err := os.ReadFile(filepath.Join(bundlePath, versionFileName))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	require.Equal(t, []string{"app_name=1.2.3", "TARANTOOL=3.2.0-0-g19607a903"}, lines[:2])
	require.ElementsMatch(t, []string{"stat=0.3.2-1", "vshard=0.1.37-1"}, lines[2:])
}
