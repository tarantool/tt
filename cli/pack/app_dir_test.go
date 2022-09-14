package pack

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/pack/test_helpers"
	"path/filepath"
	"testing"
)

func TestCreateAppDir(t *testing.T) {
	testPackageDir := t.TempDir()
	testBundleDir := t.TempDir()
	dirsToCreate := []string{
		"bundle",
	}
	filesToCreate := []string{
		"bundle/app1.lua",
	}

	err := test_helpers.CreateDirs(testBundleDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test dirs: %v", err)

	err = test_helpers.CreateFiles(testBundleDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test files: %v", err)

	testRootPrefix := "/usr/share/"
	prefixedTestPackageDir := filepath.Join(testPackageDir, testRootPrefix)
	err = copyBundleDir(prefixedTestPackageDir, testBundleDir)
	assert.NoErrorf(t, err, "failed to create a directory with bundle of applications")
	require.FileExistsf(t, filepath.Join(testPackageDir, testRootPrefix,
		"bundle", "app1.lua"), "")
}
