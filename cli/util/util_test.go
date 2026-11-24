package util_test

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
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

// TestGetHomeDir checks that the home directory is $HOME, whatever the user
// database says, and the one the database has without HOME: a container may
// run tt as a uid the database does not know.
func TestGetHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := util.GetHomeDir()
	require.NoError(t, err)
	assert.Equal(t, home, got)

	require.NoError(t, os.Unsetenv("HOME"))

	got, err = util.GetHomeDir()

	current, currentErr := user.Current()
	if currentErr != nil {
		require.ErrorContains(t, err, currentErr.Error())

		return
	}

	require.NoError(t, err)
	assert.Equal(t, current.HomeDir, got)
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

// TestParseYamlScalars pins how ParseYAML reads the scalars of a file: the
// YAML 1.1 booleans are booleans and a timestamp stays its text, which the
// string fields of the structures decoded from the result take.
func TestParseYamlScalars(t *testing.T) {
	dir := t.TempDir()

	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		return path
	}

	got, err := util.ParseYAML(write("scalars.yml", "description: 2024-01-02\nflag: on\n"+
		"quoted: 'on'\nport: 0x10\nvars:\n  - name: x\n    default: yes\n    re: 2024-01-02\n"+
		"1: one\nempty:\nnested: {1: a, n: off}\n"))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"description": "2024-01-02",
		"flag":        true,
		"quoted":      "on",
		"port":        16,
		"vars":        []any{map[any]any{"name": "x", "default": true, "re": "2024-01-02"}},
		"1":           "one",
		"empty":       nil,
		"nested":      map[any]any{1: "a", false: false},
	}, got)

	got, err = util.ParseYAML(write("empty.yml", ""))
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = util.ParseYAML(write("null.yml", "---\n...\n"))
	require.NoError(t, err)
	assert.Nil(t, got)

	_, err = util.ParseYAML(write("seq.yml", "- a\n- b\n"))
	require.ErrorContains(t, err, "failed to parse YAML: yaml: unmarshal errors:\n"+
		"  line 1: cannot unmarshal !!seq into map[string]interface {}")

	_, err = util.ParseYAML(write("keys.yml", "[1]: a\nb: c\n"))
	require.EqualError(t, err, "failed to parse YAML: yaml: unmarshal errors:\n"+
		"  line 1: cannot unmarshal !!seq into string")
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
