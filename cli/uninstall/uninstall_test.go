package uninstall

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/version"
)

const testDirName = "uninstall-test-dir"

func TestGetList(t *testing.T) {
	assert := assert.New(t)
	workDir, err := ioutil.TempDir("", testDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	binDir := filepath.Join(workDir, "bin")
	err = os.Mkdir(binDir, os.ModePerm)
	require.NoError(t, err)

	cfgData := []byte("tt:\n  app:\n    bin_dir: " + binDir)
	cfgPath := filepath.Join(workDir, "tt.yaml")

	err = os.WriteFile(cfgPath, cfgData, 0400)
	require.NoError(t, err)

	files := []string{
		"tt" + version.FsSeparator + "1.2.3",
		"tarantool" + version.FsSeparator + "1.2.10",
		"tarantool-ee" + version.FsSeparator + "master",
	}
	for _, file := range files {
		f, err := os.Create(filepath.Join(binDir, file))
		require.NoError(t, err)
		f.Close()
	}

	expected := []string{
		"tt" + version.CliSeparator + "1.2.3",
		"tarantool" + version.CliSeparator + "1.2.10",
		"tarantool-ee" + version.CliSeparator + "master",
	}

	cliOpts, _, err := configure.GetCliOpts(cfgPath)
	require.NoError(t, err)
	result := GetList(cliOpts)

	assert.ElementsMatch(result, expected)
}
