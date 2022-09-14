package pack

import (
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"path/filepath"
	"testing"
)

func TestGetTarPackageName(t *testing.T) {
	testDir, err := filepath.Abs(".")
	require.NoErrorf(t, err, "failed to get the test directory absolute path")

	testCases := []struct {
		name          string
		packCtx       *cmdcontext.PackCtx
		expectedName  string
		expectedError error
	}{
		{
			name: "No parameters in context",
			packCtx: &cmdcontext.PackCtx{
				App: &config.AppOpts{InstancesEnabled: testDir},
			},
			expectedName: filepath.Base(testDir) + "_0.1.0.0.tar.gz",
		},
		{
			name: "Set package name, without version",
			packCtx: &cmdcontext.PackCtx{
				Name: "test",
				App:  &config.AppOpts{InstancesEnabled: testDir},
			},
			expectedName: "test_0.1.0.0.tar.gz",
		},
		{
			name: "Set package name and version",
			packCtx: &cmdcontext.PackCtx{
				Name:    "test",
				Version: "2.1.1",
				App:     &config.AppOpts{InstancesEnabled: testDir},
			},
			expectedName: "test_2.1.1.tar.gz",
		},
		{
			name: "Set package full filename",
			packCtx: &cmdcontext.PackCtx{
				FileName: "test",
				App:      &config.AppOpts{InstancesEnabled: testDir},
			},
			expectedName: "test",
		},
		{
			name: "Set package full filename, package name and version",
			packCtx: &cmdcontext.PackCtx{
				FileName: "test",
				Name:     "unused",
				Version:  "unused",
				App:      &config.AppOpts{InstancesEnabled: testDir},
			},
			expectedName: "test",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			packageName, err := getTarPackageName(testCase.packCtx)
			require.ErrorIs(t, err, testCase.expectedError)
			require.Equalf(t, testCase.expectedName, packageName,
				"Got wrong package name, expected: %s, got: %s",
				testCase.expectedName, packageName)
		})
	}
}
