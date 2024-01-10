package binary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fatih/color"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/util"
)

func TestCleanString(t *testing.T) {
	testStrings := []string{util.Bold(color.GreenString("3.0.0")), color.BlueString("2.1.1"),
		"1.10", util.Bold("2.1.3")}
	expectedStrings := []string{"3.0.0", "2.1.1", "1.10", "2.1.3"}
	for i := range testStrings {
		assert.Equal(t, cleanString(testStrings[i]), expectedStrings[i])
	}
}

func TestSwitchTarantool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "switch_test")
	defer os.RemoveAll(tempDir)
	assert.Nil(t, err)
	err = copy.Copy("./testdata/switch_test", tempDir)
	assert.Nil(t, err)

	var testCtx SwitchCtx
	testCtx.IncDir = filepath.Join(tempDir, "include")
	testCtx.BinDir = filepath.Join(tempDir, "bin")
	testCtx.ProgramName = "tarantool"
	testCtx.Version = "2.10.3"
	err = Switch(testCtx)
	assert.Nil(t, err)
	assert.FileExists(t, filepath.Join(testCtx.BinDir, "tarantool"))
	assert.FileExists(t, filepath.Join(testCtx.IncDir, "include/tarantool"))
	binLink, err := util.ResolveSymlink(filepath.Join(testCtx.BinDir, "tarantool"))
	assert.Nil(t, err)
	assert.Contains(t, binLink, "tarantool_2.10.3")
	incLink, err := util.ResolveSymlink(filepath.Join(testCtx.IncDir, "include/tarantool"))
	assert.Nil(t, err)
	assert.Contains(t, incLink, "tarantool_2.10.3")
}

func TestSwitchUnknownProgram(t *testing.T) {
	var testCtx SwitchCtx
	testCtx.IncDir = filepath.Join(".", "include")
	testCtx.BinDir = filepath.Join(".", "bin")
	testCtx.ProgramName = "tarantool-foo"
	testCtx.Version = "2.10.3"
	err := Switch(testCtx)
	assert.Equal(t, err.Error(), "unknown application: tarantool-foo")
}

func TestSwitchNotInstalledVersion(t *testing.T) {
	var testCtx SwitchCtx
	testCtx.IncDir = filepath.Join(".", "include")
	testCtx.BinDir = filepath.Join(".", "bin")
	testCtx.ProgramName = "tarantool"
	testCtx.Version = "2.10.3"
	err := Switch(testCtx)
	assert.Equal(t, err.Error(), "tarantool 2.10.3 is not installed in current environment")
}
