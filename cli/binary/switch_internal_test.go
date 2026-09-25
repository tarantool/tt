package binary

import (
	"path/filepath"
	"testing"

	"github.com/fatih/color"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/search"
	"github.com/tarantool/tt/v3/cli/util"
)

func TestCleanString(t *testing.T) {
	testStrings := []string{
		util.Bold(color.GreenString("3.0.0")), color.BlueString("2.1.1"),
		"1.10", util.Bold("2.1.3"),
	}
	expectedStrings := []string{"3.0.0", "2.1.1", "1.10", "2.1.3"}

	for i := range testStrings {
		assert.Equal(t, cleanString(testStrings[i]), expectedStrings[i])
	}
}

func TestSwitchTarantool(t *testing.T) {
	tempDir := t.TempDir()
	err := copy.Copy("./testdata/switch_test", tempDir)
	require.NoError(t, err)

	var testCtx SwitchCtx

	testCtx.IncDir = filepath.Join(tempDir, "include")
	testCtx.BinDir = filepath.Join(tempDir, "bin")
	testCtx.Program, err = search.ParseProgram("tarantool")
	require.NoError(t, err)

	testCtx.Version = "2.10.3"
	err = Switch(&testCtx)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(testCtx.BinDir, "tarantool"))
	assert.FileExists(t, filepath.Join(testCtx.IncDir, "include/tarantool"))

	binLink, err := util.ResolveSymlink(filepath.Join(testCtx.BinDir, "tarantool"))
	require.NoError(t, err)
	assert.Contains(t, binLink, "tarantool_2.10.3")

	incLink, err := util.ResolveSymlink(filepath.Join(testCtx.IncDir, "include/tarantool"))
	require.NoError(t, err)
	assert.Contains(t, incLink, "tarantool_2.10.3")
}

func TestSwitchUnknownProgram(t *testing.T) {
	var (
		err     error
		testCtx SwitchCtx
	)

	testCtx.IncDir = filepath.Join(".", "include")
	testCtx.BinDir = filepath.Join(".", "bin")
	testCtx.Program, err = search.ParseProgram("tarantool-foo")
	require.Error(t, err)

	testCtx.Version = "2.10.3"
	err = Switch(&testCtx)
	assert.Contains(t, err.Error(), "unknown application: unknown(0)")
}

func TestSwitchNotInstalledVersion(t *testing.T) {
	var (
		err     error
		testCtx SwitchCtx
	)

	testCtx.IncDir = filepath.Join(".", "include")
	testCtx.BinDir = filepath.Join(".", "bin")
	testCtx.Program, err = search.ParseProgram("tarantool")
	require.NoError(t, err)

	testCtx.Version = "2.10.3"
	err = Switch(&testCtx)
	assert.Contains(t, err.Error(), "tarantool_2.10.3 is not installed in current environment")
}
