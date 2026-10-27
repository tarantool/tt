package util_test

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/util"
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

	iv := inputValue{re: regexp.MustCompile("(?P<user>.*):(?P<pass>.*)"), data: "toor:1234"}

	testCases[iv] = outputValue{
		result: map[string]string{
			"user": "toor",
			"pass": "1234",
		},
	}

	testCases[inputValue{
		re:   regexp.MustCompile("(?P<user>.*):(?P<pass>[a-z]+)?"),
		data: "toor:1234",
	}] = outputValue{
		result: map[string]string{
			"user": "toor",
			"pass": "",
		},
	}

	for input, output := range testCases {
		result := util.FindNamedMatches(input.re, input.data)

		assert.Equal(output.result, result)
	}
}

func TestIsDir(t *testing.T) {
	assert := assert.New(t)

	workDir := t.TempDir()

	require.True(t, util.IsDir(workDir))

	tmpFile, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)

	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	assert.False(util.IsDir(tmpFile.Name()))
	assert.False(util.IsDir("./non-existing-dir"))
}

func TestIsRegularFile(t *testing.T) {
	assert := assert.New(t)

	tmpFile, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)

	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	require.True(t, util.IsRegularFile(tmpFile.Name()))

	workDir := t.TempDir()
	assert.False(util.IsRegularFile(workDir))
	assert.False(util.IsRegularFile("./non-existing-file"))
}

func TestCreateDirectory(t *testing.T) {
	currentUser, err := user.Current()
	if err == nil && currentUser.Uid == "0" {
		t.Skip("Skipping the test, it shouldn't run as root")
	}

	tempDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tempDir, "dir1"), 0o750))

	// Existing dir.
	assert.NoError(t, util.CreateDirectory(filepath.Join(tempDir, "dir1"), 0o750))
	// Non-existent dir.
	assert.NoError(t, util.CreateDirectory(filepath.Join(tempDir, "dir2"), 0o750))

	file, err := os.Create(filepath.Join(tempDir, "file"))
	require.NoError(t, err)

	defer func() {
		_ = file.Close()
	}()

	require.Error(t, util.CreateDirectory(file.Name(), 0o750))

	// Permissions denied.
	require.NoError(t, os.Chmod(tempDir, 0o444))

	defer func() {
		_ = os.Chmod(tempDir, 0o777)
	}()

	assert.Error(t, util.CreateDirectory(filepath.Join(tempDir, "dir3"), 0o750))
}

func TestAskConfirm(t *testing.T) {
	// Confirmed.
	confirmed, err := util.AskConfirm(strings.NewReader("Y\n"), "Yes?")
	require.NoError(t, err)
	require.True(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("y\n"), "Yes?")
	require.NoError(t, err)
	require.True(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("yes\n"), "Yes?")
	require.NoError(t, err)
	require.True(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("YES\n"), "Yes?")
	require.NoError(t, err)
	require.True(t, confirmed)

	// Negative.
	confirmed, err = util.AskConfirm(strings.NewReader("N\n"), "Yes?")
	require.NoError(t, err)
	require.False(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("n\n"), "Yes?")
	require.NoError(t, err)
	require.False(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("No\n"), "Yes?")
	require.NoError(t, err)
	require.False(t, confirmed)

	confirmed, err = util.AskConfirm(strings.NewReader("NO\n"), "Yes?")
	require.NoError(t, err)
	require.False(t, confirmed)

	// Unknown.
	_, err = util.AskConfirm(strings.NewReader("Wat?\n"), "Yes?")
	require.ErrorIs(t, err, io.EOF)
}

func TestGetYamlFileName(t *testing.T) {
	tempDir := t.TempDir()

	// Create tarantool.yaml file.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "tarantool.yaml"), []byte("tt:"),
		0o664))

	fileName, err := util.GetYamlFileName(filepath.Join(tempDir, "tarantool.yml"), true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "tarantool.yaml"), fileName)

	// Create tarantool.yml file. File selection ambiguity.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "tarantool.yml"), []byte("tt:"),
		0o664))

	fileName, err = util.GetYamlFileName(filepath.Join(tempDir, "tarantool.yml"), true)
	require.Error(t, err)
	assert.Empty(t, fileName)

	// Remove tarantool.yaml file.
	require.NoError(t, os.Remove(filepath.Join(tempDir, "tarantool.yaml")))

	fileName, err = util.GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "tarantool.yml"), fileName)

	// Pass file with .txt extension as a parameter.
	fileName, err = util.GetYamlFileName(filepath.Join(tempDir, "tarantool.txt"), true)
	require.EqualError(t, err, fmt.Sprintf("provided file '%s' has no .yaml/.yml extension",
		filepath.Join(tempDir, "tarantool.txt")))
	assert.Empty(t, fileName)

	// Remove tarantool.yaml file.
	require.NoError(t, os.Remove(filepath.Join(tempDir, "tarantool.yml")))

	fileName, err = util.GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), true)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, fileName)

	// Get file name for new file.
	fileName, err = util.GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), false)
	require.NoError(t, err)
	assert.Empty(t, fileName)
}

func TestParseYaml(t *testing.T) {
	type args struct {
		yamlFilePath string
	}

	tests := []struct {
		name    string
		args    args
		want    map[string]any
		wantErr bool
	}{
		{
			name: "Existing file name",
			args: args{
				yamlFilePath: "testdata/instances.yml",
			},
			want: map[string]any{
				"router": nil,
				"master": nil,
				"replica": map[any]any{
					"path": "filename",
				},
			},
			wantErr: false,
		},
		{
			name: "File does not exist",
			args: args{
				yamlFilePath: "testdata/instance.yml",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Invalid yaml",
			args: args{
				yamlFilePath: "testdata/bad.yml",
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.ParseYAML(tt.args.yamlFilePath)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestJoinAbspath(t *testing.T) {
	type args struct {
		paths []string
	}

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			"Absolute path",
			args{
				[]string{"/root"},
			},
			"/root",
			false,
		},
		{
			"Absolute path + relative",
			args{
				[]string{"/root", "../home/user"},
			},
			"/home/user",
			false,
		},
		{
			"Absolute path + multiple relative",
			args{
				[]string{"/root", "../home/user", "./docs"},
			},
			"/home/user/docs",
			false,
		},
		{
			"Absolute path not first",
			args{
				[]string{"/root", "../home/user", "/var"},
			},
			"/var",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.JoinAbspath(tt.args.paths...)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJoinPaths(t *testing.T) {
	type args struct {
		paths []string
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"Absolute path",
			args{
				[]string{"/root"},
			},
			"/root",
		},
		{
			"Absolute path + relative",
			args{
				[]string{"/root", "../home/user"},
			},
			"/home/user",
		},
		{
			"Absolute path + multiple relative",
			args{
				[]string{"/root", "../home/user", "./docs"},
			},
			"/home/user/docs",
		},
		{
			"Absolute path not first",
			args{
				[]string{"/root", "../home/user", "/var"},
			},
			"/var",
		},
		{
			"Relative",
			args{
				[]string{"../home/user"},
			},
			"../home/user",
		},
		{
			"Multiple relatives",
			args{
				[]string{"../home", "./user"},
			},
			"../home/user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := util.JoinPaths(tt.args.paths...)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStringToTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		output  string
		wantErr bool
	}{
		// spell-checker:disable
		{
			"clean",
			"",
			strconv.FormatUint(math.MaxUint64, 10),
			false,
		},
		{
			"int_number",
			"123456",
			"123456",
			false,
		},
		{
			"float_number",
			"123456.123",
			"123456.123",
			false,
		},
		{
			"RFC339",
			"2022-04-28T07:22:12+00:00",
			"1651130532.0",
			false,
		},
		{
			"RFC339Nano",
			"2022-04-28T07:22:12.1534+00:00",
			"1651130532.153400000",
			false,
		},
		{
			"RFC339Nano_Zone",
			"2022-04-28T07:22:12.1534Z",
			"1651130532.153400000",
			false,
		},
		{
			"not_number",
			"acbdef",
			"",
			true,
		},
		{
			"RFC339Nano_random_string",
			"2022-0_-28A07:22:12.1534abc",
			"",
			true,
		},
		{
			"RFC339Nano_incorrect",
			"2022-04-28T07:22:12.1534abc",
			"",
			true,
		},
		{
			"RFC339_incorrect",
			"2022-04-28T07:22abc",
			"",
			true,
		},
		// spell-checker:enable
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StringToTimestamp(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.output, got)
			}
		})
	}
}
