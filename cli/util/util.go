package util

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"gopkg.in/yaml.v2"
)

const bufSize int64 = 10000

// VersionFunc is a type of function that return
// string with current Tarantool CLI version.
type VersionFunc func(bool, bool) string

// FileLinesScanner returns scanner for file.
func FileLinesScanner(file *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	return scanner
}

// GetFileContentBytes returns file content as a bytes slice.
func GetFileContentBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}

	return fileContent, nil
}

// GetFileContent returns file content as a string.
func GetFileContent(path string) (string, error) {
	fileContentBytes, err := GetFileContentBytes(path)
	if err != nil {
		return "", err
	}

	return string(fileContentBytes), nil
}

// JoinAbspath concat paths and makes the resulting path absolute.
func JoinAbspath(paths ...string) (string, error) {
	var err error

	path := filepath.Join(paths...)
	if path, err = filepath.Abs(path); err != nil {
		return "", fmt.Errorf("Failed to get absolute path: %s", err)
	}

	return path, nil
}

// Find find index of specified string in the slice.
func Find(src []string, find string) int {
	for i, elem := range src {
		if find == elem {
			return i
		}
	}

	return -1
}

// InternalError shows error information, version of tt and call stack.
func InternalError(format string, f VersionFunc, err ...interface{}) error {
	internalErrorFmt :=
		`Whoops! It looks like something is wrong with this version of Tarantool CLI.
Error: %s
Version: %s
Stacktrace:
%s
`
	version := f(false, false)
	return fmt.Errorf(internalErrorFmt, fmt.Sprintf(format, err...), version, debug.Stack())
}

// ParseYAML parse yaml file at specified path.
func ParseYAML(path string) (map[string]interface{}, error) {
	fileContent, err := GetFileContentBytes(path)
	if err != nil {
		return nil, fmt.Errorf(`Failed to read "%s" file: %s`, path, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(fileContent, &raw); err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	return raw, nil
}

// GetHelpCommand returns the help command for the passed cmd argument.
func GetHelpCommand(cmd *cobra.Command) *cobra.Command {
	for _, subcmd := range cmd.Commands() {
		if subcmd.Name() == "help" {
			return subcmd
		}
	}

	return nil
}

// GetHomeDir returns current home directory.
func GetHomeDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return usr.HomeDir, nil
}

func readFromPos(f *os.File, pos int64, buf *[]byte) (int, error) {
	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return 0, fmt.Errorf("Failed to seek: %s", err)
	}

	n, err := f.Read(*buf)
	if err != nil {
		return n, fmt.Errorf("Failed to read: %s", err)
	}

	return n, nil
}

// GetLastNLinesBegin return the position of last lines begin.
func GetLastNLinesBegin(filepath string, lines int) (int64, error) {
	if lines == 0 {
		return 0, nil
	}

	if lines < 0 {
		lines = -lines
	}

	f, err := os.Open(filepath)
	if err != nil {
		return 0, fmt.Errorf("Failed to open file: %s", err)
	}
	defer f.Close()

	var fileSize int64
	if fileInfo, err := os.Stat(filepath); err != nil {
		return 0, fmt.Errorf("Failed to get fileinfo: %s", err)
	} else {
		fileSize = fileInfo.Size()
	}

	if fileSize == 0 {
		return 0, nil
	}

	buf := make([]byte, bufSize)

	var filePos int64 = fileSize - bufSize
	var lastNewLinePos int64 = 0
	var newLinesN int = 0

	// Check last symbol of the last line.

	if _, err := readFromPos(f, fileSize-1, &buf); err != nil {
		return 0, err
	}
	if buf[0] != '\n' {
		newLinesN++
	}

	lastPart := false

Loop:
	for {
		if filePos < 0 {
			filePos = 0
			lastPart = true
		}

		n, err := readFromPos(f, filePos, &buf)
		if err != nil {
			return 0, err
		}

		for i := n - 1; i >= 0; i-- {
			b := buf[i]

			if b == '\n' {
				newLinesN++
			}

			if newLinesN == lines+1 {
				lastNewLinePos = filePos + int64(i+1)
				break Loop
			}
		}

		if lastPart || filePos == 0 {
			break
		}

		filePos -= bufSize
	}

	return lastNewLinePos, nil
}

// GetLastNLines returns the last N lines fromthe file.
func GetLastNLines(filepath string, linesN int) ([]string, error) {
	lastNLinesBeginPos, err := GetLastNLinesBegin(filepath, linesN)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("Failed to open file: %s", err)
	}

	if _, err := file.Seek(lastNLinesBeginPos, io.SeekStart); err != nil {
		return nil, fmt.Errorf("Failed to seek in file: %s", err)
	}

	lines := []string{}

	scanner := FileLinesScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, nil
}

// GetTarantoolVersion returns and caches the tarantool version.
func GetTarantoolVersion(cli *cmdcontext.CliCtx) (string, error) {
	if cli.TarantoolVersion != "" {
		return cli.TarantoolVersion, nil
	}

	output, err := exec.Command(cli.TarantoolExecutable, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("Failed to get tarantool version: %s", err)
	}

	version := strings.Split(string(output), "\n")
	version = strings.Split(version[0], " ")

	if len(version) < 2 {
		return "", fmt.Errorf("Failed to get tarantool version: corrupted data")
	}

	cli.TarantoolVersion = version[len(version)-1]

	return cli.TarantoolVersion, nil
}

// ReadEmbedFile reads content of embed file in string mode.
func ReadEmbedFile(fs embed.FS, path string) (string, error) {
	content, err := ReadEmbedFileBinary(fs, path)
	return string(content), err
}

// ReadEmbedFileBinary reads content of embed file in byte mode.
func ReadEmbedFileBinary(fs embed.FS, path string) ([]byte, error) {
	content, err := fs.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return content, nil
}
