package util

import (
	"io/ioutil"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inputValue struct {
	re   *regexp.Regexp
	data string
}

type outputValue struct {
	result map[string]string
}

func TestFindNamedMatches(t *testing.T) {
	assert := assert.New(t)

	testCases := make(map[inputValue]outputValue)

	testCases[inputValue{re: regexp.MustCompile("(?P<user>.*):(?P<pass>.*)"), data: "toor:1234"}] =
		outputValue{
			result: map[string]string{
				"user": "toor",
				"pass": "1234",
			},
		}

	testCases[inputValue{re: regexp.MustCompile("(?P<user>.*):(?P<pass>[a-z]+)?"),
		data: "toor:1234"}] =
		outputValue{
			result: map[string]string{
				"user": "toor",
				"pass": "",
			},
		}

	for input, output := range testCases {
		result := FindNamedMatches(input.re, input.data)

		assert.Equal(output.result, result)
	}
}

func TestIsDir(t *testing.T) {
	assert := assert.New(t)

	workDir, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	defer os.RemoveAll(workDir)

	require.True(t, IsDir(workDir))

	tmpFile, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	assert.False(IsDir(tmpFile.Name()))
	assert.False(IsDir("./non-existing-dir"))
}

func TestIsRegularFile(t *testing.T) {
	assert := assert.New(t)

	tmpFile, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	require.True(t, IsRegularFile(tmpFile.Name()))

	workDir, err := ioutil.TempDir("", "")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	assert.False(IsRegularFile(workDir))
	assert.False(IsRegularFile("./non-existing-file"))
}
