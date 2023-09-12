package install

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type patchRange_1_0_to_2_9 struct {
	defaultPatchApplier
}

func (patchRange_1_0_to_2_9) isApplicable(ver version.Version) bool {
	return (ver.Major == 2 && ver.Minor < 10) || ver.Major == 1
}

func (applier patchRange_1_0_to_2_9) apply(srcPath string, verbose bool, logFile *os.File) error {
	err := util.ExecuteCommandStdin("patch", verbose, logFile,
		srcPath, applier.patch)

	return err
}

type patcherInput struct {
	ver version.Version
}

type patcherOutput struct {
	result *[]byte
}

func TestPatcher(t *testing.T) {
	assert := assert.New(t)
	testDir := t.TempDir()

	targetData := []byte("aaa\n")
	patchData := []byte("--- testdata.old	2022-10-11 10:32:28.011753821 +0300\n" +
		"+++ testdata	2022-10-11 10:32:34.811754169 +0300\n" + "@@ -1 +1,2 @@\n aaa\n+bbb\n")
	targetPath := testDir + "/testdata"

	testCases := make(map[patcherInput]patcherOutput)

	patchResult0 := []byte("aaa\nbbb\n")
	testCases[patcherInput{version.Version{Major: 1, Minor: 2}}] = patcherOutput{&patchResult0}

	patchResult1 := []byte("aaa\n")
	testCases[patcherInput{version.Version{Major: 3, Minor: 0}}] = patcherOutput{&patchResult1}

	for input, output := range testCases {
		targetFile, err := os.Create(targetPath)
		require.NoError(t, err)

		_, err = targetFile.Write(targetData)
		require.NoError(t, err)
		targetFile.Close()

		patch := patchRange_1_0_to_2_9{defaultPatchApplier{patchData}}

		if patch.isApplicable(input.ver) {
			patch.apply(testDir, false, nil)
		}

		patchedData, err := os.ReadFile(targetPath)
		require.NoError(t, err)

		assert.Equal(*output.result, patchedData)

		os.Remove(targetPath)
	}
}
