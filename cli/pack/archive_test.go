package pack

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
			suffix, err := getTgzSuffix()
			packageName, err := getPackageName(testCase.packCtx, testCase.opts, suffix, true)
			require.ErrorIs(t, err, testCase.expectedError)
			require.Equalf(t, testCase.expectedName, packageName,
				"Got wrong package name, expected: %s, got: %s",
				testCase.expectedName, packageName)
		})
	}
}
