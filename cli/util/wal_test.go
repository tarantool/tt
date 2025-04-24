package util_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/util"
)

// createTestFiles creates directories and files structure.
func createTestFiles(t *testing.T, tstDir string, filesMap map[string][]string) {
	t.Helper()

	for dir, files := range filesMap {
		dest := tstDir
		allowedDir := true
		if dir[0] == '/' {
			if dir[len(dir)-1] == '-' {
				dir = dir[:len(dir)-1]
				allowedDir = false
			}
			dest = filepath.Join(tstDir, dir)
			require.NoError(t, os.MkdirAll(dest, 0o755))
		} else {
			require.Empty(t, files,
				"Is it a Directory or File? Missed '/' prefix for dir %q", dir)
			files = append(files, dir)
		}

		for _, file := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dest, file), []byte(file), 0o644))
		}

		if !allowedDir {
			// Remove permission to read directory.
			require.NoError(t, os.Chmod(dest, 0o000))
		}
	}
}

// restorePermissions restores permissions for directories.
// It is needed to avoid permission issues at cleanup phase after tests done.
func restorePermissions(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			require.NoError(t, os.Chmod(p, 0o755))
		}
		return nil
	}))
}

func TestCollectWalFiles_recursive(t *testing.T) {
	tstDir := t.TempDir()

	testFilesMap := map[string][]string{
		"01.xlog": {},
		"02.xlog": {},
		"01.snap": {},
		"02.snap": {},
		"/data1": {
			"14.xlog",
			"13.xlog",
			"13.snap",
			"14.snap",
		},
		"/no_perm-": { // Suffix '-' remove permission to read dir.
			"p14.xlog",
			"p13.xlog",
			"p13.snap",
			"p14.snap",
		},
		"/data1/empty_dir": {},
		"/data2.xlog": {
			"21.xlog",
			"22.xlog",
			"not_wal.log",
		},
		"/data2.snap": {
			"21.snap",
			"22.snap",
			"not_wal.txt",
		},
		"/data2/logs": {
			"01.xlog",
			"01.snap",
			"02.xlog",
			"02.snap",
		},
		"/.xlog": {
			"314.xlog",
			"313.xlog",
		},
		"/.snap": {
			"314.snap",
			"313.snap",
		},
		"/not_wal_files": {
			".xlog",
			".snap",
			"not.xlog.txt",
			".snap.bin",
		},
		"/empty_dir": {},
	}

	createTestFiles(t, tstDir, testFilesMap)

	// j is wrapper to join test names with temporary directory name.
	j := func(f string) string {
		return filepath.Join(tstDir, f)
	}

	tests := map[string]struct {
		input     []string
		recursive bool
		output    []string
		errMsg    string
		logMsg    string
	}{
		"all files": {
			input:     []string{"."},
			recursive: true,
			output: []string{
				j("01.snap"), j("02.snap"),
				j(".snap/313.snap"), j(".snap/314.snap"),
				j("data1/13.snap"), j("data1/14.snap"),
				j("data2.snap/21.snap"), j("data2.snap/22.snap"),
				j("data2/logs/01.snap"), j("data2/logs/02.snap"),

				j("01.xlog"), j("02.xlog"),
				j(".xlog/313.xlog"), j(".xlog/314.xlog"),
				j("data1/13.xlog"), j("data1/14.xlog"),
				j("data2.xlog/21.xlog"), j("data2.xlog/22.xlog"),
				j("data2/logs/01.xlog"), j("data2/logs/02.xlog"),
			},
			logMsg: fmt.Sprintf("warn Skipping %q due to error during walk", j("no_perm")),
		},

		"no file": {
			input:  []string{},
			output: []string{},
		},

		"no wal files": {
			input:  []string{j("not_wal_files")},
			output: []string{},
			logMsg: fmt.Sprintf("No WAL files found at %q", j("not_wal_files")),
		},

		"not existing file": {
			input:  []string{j("not-exists-file")},
			errMsg: "not-exists-file: no such file or directory",
		},

		"try no permission without recursion": {
			input:     []string{j("no_perm")},
			recursive: false,
			output:    []string{},
			logMsg:    fmt.Sprintf("warn Failed to read directory %q:", j("no_perm")),
		},

		"one relative file": {
			input:  []string{"01.snap"},
			output: []string{j("01.snap")},
		},

		"relative directory": {
			input: []string{"data1"},
			output: []string{
				j("data1/13.snap"),
				j("data1/14.snap"),
				j("data1/13.xlog"),
				j("data1/14.xlog"),
			},
		},

		"absolute directory": {
			input: []string{j("data2/logs")},
			output: []string{
				j("data2/logs/01.snap"),
				j("data2/logs/02.snap"),
				j("data2/logs/01.xlog"),
				j("data2/logs/02.xlog"),
			},
		},

		"recursive directory": {
			input:     []string{"data2"},
			recursive: true,
			output: []string{
				j("data2/logs/01.snap"),
				j("data2/logs/02.snap"),
				j("data2/logs/01.xlog"),
				j("data2/logs/02.xlog"),
			},
		},

		"remove duplicates": {
			input: []string{
				"data1/14.snap",
				"data1",
				j("data1/13.xlog"),
			},
			recursive: true,
			output: []string{
				j("data1/13.snap"),
				j("data1/14.snap"),
				j("data1/13.xlog"),
				j("data1/14.xlog"),
			},
		},
	}

	wd, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, os.Chdir(wd))
	}()

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if len(test.input) == 0 || test.input[0][0] != '/' {
				// If the first element of input is relative path,
				// we need to change the working directory to the test directory.
				require.NoError(t, os.Chdir(tstDir))
			} else {
				// If the first element of input is absolute path,
				// run test from current directory, not in temp.
				require.NoError(t, os.Chdir(wd))
			}

			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer func() {
				log.SetOutput(os.Stderr)
			}()

			result, err := util.CollectWalFiles(test.input, test.recursive)

			if test.errMsg != "" {
				assert.ErrorContains(t, err, test.errMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.output, result)
				if buf.Len() > 0 {
					logStr := buf.String()
					if test.logMsg != "" {
						assert.Contains(t, logStr, test.logMsg)
					} else {
						t.Log(logStr)
					}
				}
			}
		})
	}

	restorePermissions(t, tstDir)
}

// TestCollectWalFiles just a copy of the old test, to keep backward compatibility.
func TestCollectWalFiles(t *testing.T) {
	srcDir := t.TempDir()
	// DEBUG: А нужно ли чистить временную директорию?
	require.NoError(t, os.RemoveAll(srcDir))
	require.NoError(t, os.MkdirAll(srcDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "1.xlog"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "2.xlog"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "1.snap"), []byte{}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "2.snap"), []byte{}, 0o644))
	snap1 := fmt.Sprintf("%s/%s", srcDir, "1.snap")
	snap2 := fmt.Sprintf("%s/%s", srcDir, "2.snap")
	xlog1 := fmt.Sprintf("%s/%s", srcDir, "1.xlog")
	xlog2 := fmt.Sprintf("%s/%s", srcDir, "2.xlog")

	tests := []struct {
		name           string
		input          []string
		output         []string
		expectedErrMsg string
	}{
		{
			name:   "no_file",
			input:  []string{},
			output: []string{},
		},
		{
			name:           "incorrect_file",
			input:          []string{"file"},
			expectedErrMsg: "stat file: no such file or directory",
		},
		{
			name:   "one_file",
			input:  []string{xlog1},
			output: []string{xlog1},
		},
		{
			name:   "directory",
			input:  []string{srcDir},
			output: []string{snap1, snap2, xlog1, xlog2},
		},
		{
			name:   "mix",
			input:  []string{srcDir, "util_test.go"},
			output: []string{snap1, snap2, xlog1, xlog2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := util.CollectWalFiles(test.input, false)

			if test.expectedErrMsg != "" {
				assert.ErrorContains(t, err, test.expectedErrMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.output, result)
			}
		})
	}
}
