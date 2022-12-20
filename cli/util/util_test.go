package util

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/pack/test_helpers"
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

type prefixInput struct {
	cli     cmdcontext.CliCtx
	cliOpts *config.CliOpts
	data    *[]byte
}

type prefixOutput struct {
	prefix  string
	include string
	err     error
}

func TestSetupTarantoolPrefix(t *testing.T) {
	assert := assert.New(t)
	testDir, err := ioutil.TempDir("/tmp", "tt-unit")
	require.NoError(t, err)

	defer os.RemoveAll(testDir)

	err = os.Mkdir(testDir+"/bin", os.ModePerm)
	require.NoError(t, err)

	tntBinPath := testDir + "/bin/tarantool"

	cwd, err := os.Getwd()
	require.NoError(t, err)

	testCases := make(map[prefixInput]prefixOutput)

	tntOkData := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -DCMAKE_INSTALL_PREFIX=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolExecutable:    tntBinPath,
	}, data: &tntOkData}] =
		prefixOutput{
			prefix:  "/usr",
			include: "/usr/include/tarantool",
			err:     nil,
		}

	tntBadData0 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n" +
		"Build options: cmake . -D_FAIL_HERE_=/usr -DENABLE_BACKTRACE=yes'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolExecutable:    tntBinPath,
	}, data: &tntBadData0}] =
		prefixOutput{
			err: fmt.Errorf("failed to get prefix path: regexp does not match"),
		}

	tntBadData1 := []byte("#!/bin/sh\n" +
		"echo 'Tarantool 2.10.2-0-gb924f0b\n" +
		"Target: Linux-x86_64-RelWithDebInfo\n'")

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: false,
		TarantoolExecutable:    tntBinPath,
	}, data: &tntBadData1}] =
		prefixOutput{
			err: fmt.Errorf("failed to get prefix path: expected more data"),
		}

	appOpts := config.AppOpts{IncludeDir: "hdr"}
	cliOpts := config.CliOpts{App: &appOpts}

	testCases[prefixInput{cli: cmdcontext.CliCtx{
		IsTarantoolBinFromRepo: true,
		TarantoolExecutable:    tntBinPath,
	}, cliOpts: &cliOpts, data: &tntOkData}] =
		prefixOutput{
			prefix:  cwd + "/hdr",
			include: cwd + "/hdr/include/tarantool",
			err:     nil,
		}

	for input, output := range testCases {
		tntFile, err := os.Create(tntBinPath)
		require.NoError(t, err)

		_, err = tntFile.Write(*input.data)
		require.NoError(t, err)
		tntFile.Close()

		err = os.Chmod(tntFile.Name(), 0755)
		require.NoError(t, err)

		err = SetupTarantoolPrefix(&input.cli, input.cliOpts)
		if err == nil {
			assert.Nil(err)
			assert.Equal(output.prefix, input.cli.TarantoolInstallPrefix)
			assert.Equal(output.include, input.cli.TarantoolIncludeDir)
		} else {
			assert.Equal(output.err, err)
		}

		os.Remove(tntBinPath)
	}
}

func TestCreateDirectory(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(tempDir, "dir1"), 0750))

	// Existing dir.
	assert.NoError(t, CreateDirectory(filepath.Join(tempDir, "dir1"), 0750))
	// Non-existent dir.
	assert.NoError(t, CreateDirectory(filepath.Join(tempDir, "dir2"), 0750))

	f, err := os.Create(filepath.Join(tempDir, "file"))
	require.NoError(t, err)
	defer f.Close()
	assert.Error(t, CreateDirectory(f.Name(), 0750))

	// Permissions denied.
	require.NoError(t, os.Chmod(tempDir, 0444))
	defer os.Chmod(tempDir, 0777)
	assert.Error(t, CreateDirectory(filepath.Join(tempDir, "dir3"), 0750))
}

func TestWriteYaml(t *testing.T) {
	type book struct {
		Title  string
		Author string
		Pages  int
	}

	type library struct {
		Books []*book
	}

	lib := library{Books: []*book{
		{"title1", "author1", 100},
		{"title2", "author2", 200},
	}}

	tempDir := t.TempDir()
	require.NoError(t, WriteYaml(filepath.Join(tempDir, "library"), &lib))
	f, err := os.Open(filepath.Join(tempDir, "library"))
	require.NoError(t, err)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan()
	require.True(t, strings.Contains(scanner.Text(), "books:"))
	scanner.Scan()
	require.Equal(t, scanner.Text(), "- title: title1")
	scanner.Scan()
	require.Equal(t, scanner.Text(), "  author: author1")
	scanner.Scan()
	require.Equal(t, scanner.Text(), "  pages: 100")
	scanner.Scan()
	require.Equal(t, scanner.Text(), "- title: title2")
	scanner.Scan()
	require.Equal(t, scanner.Text(), "  author: author2")
	scanner.Scan()
	require.Equal(t, scanner.Text(), "  pages: 200")
}

func TestAskConfirm(t *testing.T) {
	// Confirmed.
	confirmed, err := AskConfirm(strings.NewReader("Y\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, true)
	confirmed, err = AskConfirm(strings.NewReader("y\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, true)
	confirmed, err = AskConfirm(strings.NewReader("yes\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, true)
	confirmed, err = AskConfirm(strings.NewReader("YES\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, true)

	// Negative.
	confirmed, err = AskConfirm(strings.NewReader("N\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, false)
	confirmed, err = AskConfirm(strings.NewReader("n\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, false)
	confirmed, err = AskConfirm(strings.NewReader("No\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, false)
	confirmed, err = AskConfirm(strings.NewReader("NO\n"), "Yes?")
	require.NoError(t, err)
	require.Equal(t, confirmed, false)

	// Unknown.
	_, err = AskConfirm(strings.NewReader("Wat?\n"), "Yes?")
	require.ErrorIs(t, err, io.EOF)
}

func TestCreateSymlink(t *testing.T) {
	tempDir := t.TempDir()
	targetFile, err := os.Create(filepath.Join(tempDir, "tgtFile.txt"))
	require.NoError(t, err)
	targetFile.Close()

	// No overwrite.
	require.NoError(t, CreateSymlink(targetFile.Name(), filepath.Join(tempDir, "first_link"),
		false))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))
	targetPath, err := os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite flag is set, but symlink does not exist.
	require.NoError(t, CreateSymlink(targetFile.Name(), filepath.Join(tempDir, "second_link"),
		true))
	assert.FileExists(t, filepath.Join(tempDir, "second_link"))
	targetPath, err = os.Readlink(filepath.Join(tempDir, "second_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite existing symlink.
	require.NoError(t, CreateSymlink("./tgtFile.txt", filepath.Join(tempDir, "first_link"),
		true))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))
	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)

	// Don't overwrite existing.
	require.Error(t, CreateSymlink("./some_file", filepath.Join(tempDir, "first_link"),
		false))
	// Check existing link is not updated.
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))
	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)
}

func TestIsApp(t *testing.T) {
	testCases := []struct {
		testName   string
		createFunc func() (string, error)
		isApp      bool
	}{
		{
			testName: "Application is directory",
			createFunc: func() (string, error) {
				baseDir := t.TempDir()
				filePath := filepath.Join(baseDir, "init.lua")
				_, err := os.Create(filePath)
				if err != nil {
					return "", err
				}
				return baseDir, nil
			},
			isApp: true,
		},
		{
			testName: "Application is file",
			createFunc: func() (string, error) {
				baseDir := t.TempDir()
				filePath := filepath.Join(baseDir, "app.lua")
				_, err := os.Create(filePath)
				if err != nil {
					return "", err
				}
				return filePath, nil
			},
			isApp: true,
		},
		{
			testName: "Empty directory",
			createFunc: func() (string, error) {
				baseDir := t.TempDir()
				return baseDir, nil
			},
			isApp: false,
		},
		{
			testName: "Non lua file",
			createFunc: func() (string, error) {
				baseDir := t.TempDir()
				filePath := filepath.Join(baseDir, "app.py")
				_, err := os.Create(filePath)
				if err != nil {
					return "", err
				}
				return filePath, nil
			},
			isApp: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			path, err := testCase.createFunc()
			require.NoError(t, err, "no error expected")
			assert.Equal(t, testCase.isApp, IsApp(path), "Unexpected result of application check")
		})
	}
}

func TestGetYamlFileName(t *testing.T) {
	tempDir := t.TempDir()

	// Create tarantool.yaml file.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "tarantool.yaml"), []byte("tt:"),
		0664))
	fileName, err := GetYamlFileName(filepath.Join(tempDir, "tarantool.yml"), true)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "tarantool.yaml"), fileName)

	// Create tarantool.yml file. File selection ambiguity.
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "tarantool.yml"), []byte("tt:"),
		0664))
	fileName, err = GetYamlFileName(filepath.Join(tempDir, "tarantool.yml"), true)
	assert.Error(t, err)
	assert.Equal(t, "", fileName)

	// Remove tarantool.yaml file.
	require.NoError(t, os.Remove(filepath.Join(tempDir, "tarantool.yaml")))
	fileName, err = GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), true)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "tarantool.yml"), fileName)

	// Pass file with .txt extension as a parameter.
	fileName, err = GetYamlFileName(filepath.Join(tempDir, "tarantool.txt"), true)
	assert.EqualError(t, err, fmt.Sprintf("provided file '%s' has no .yaml/.yml extension",
		filepath.Join(tempDir, "tarantool.txt")))
	assert.Equal(t, "", fileName)

	// Remove tarantool.yaml file.
	require.NoError(t, os.Remove(filepath.Join(tempDir, "tarantool.yml")))
	fileName, err = GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), true)
	assert.ErrorIs(t, os.ErrNotExist, err)
	assert.Equal(t, "", fileName)

	// Get file name for new file.
	fileName, err = GetYamlFileName(filepath.Join(tempDir, "tarantool.yaml"), false)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(tempDir, "tarantool.yaml"), fileName)
}

func TestInstantiateFileFromTemplate(t *testing.T) {
	testDir := t.TempDir()
	templatePath := filepath.Join(testDir, "template.txt")
	templateContent := "{{ .TestName }}"
	type args struct {
		unitPath     string
		unitTemplate string
		ctx          map[string]interface{}
	}
	tests := []struct {
		name            string
		args            args
		wantErr         assert.ErrorAssertionFunc
		expectedContent string
	}{
		{
			name: "Sample template file",
			args: args{
				unitPath:     templatePath,
				unitTemplate: templateContent,
				ctx: map[string]interface{}{
					"TestName": 1,
				},
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err != nil
			},
			expectedContent: "1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, InstantiateFileFromTemplate(tt.args.unitPath,
				tt.args.unitTemplate, tt.args.ctx),
				fmt.Sprintf("InstantiateFileFromTemplate(%v, %v, %v)",
					tt.args.unitPath, tt.args.unitTemplate, tt.args.ctx))

			content, err := ioutil.ReadFile(tt.args.unitPath)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedContent, string(content))
		})
	}
}

func TestCollectAppList(t *testing.T) {
	testDir := t.TempDir()
	var defaultPaths = []string{
		"var",
		"log",
		"run",
		"lib",
		"env",
		filepath.Join("env", "bin"),
		filepath.Join("env", "modules"),
	}

	apps := map[string]bool{
		"app1.lua": true,
		"app2":     true,
	}

	dirsToCreate := []string{
		"app2",
		".rocks",
	}
	dirsToCreate = append(dirsToCreate, defaultPaths...)

	filesToCreate := []string{
		"app1.lua",
		"somefile",
		"app2/init.lua",
	}

	err := test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to initialize a directory structure: %v", err)

	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to initialize a directory structure: %v", err)

	collected, err := CollectAppList("", testDir)
	assert.Nilf(t, err, "failed to collect an app list: %v", err)

	require.Equalf(t, len(apps), len(collected), "wrong count applications collected,"+
		" expected: %d, got %d", len(apps), len(collected))

	for _, item := range collected {
		require.Truef(t, apps[item.Name], "wrong item got collected in app list: %s", item)
	}
}
