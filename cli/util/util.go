package util

import (
	"archive/tar"
	"bufio"
	"embed"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/apex/log"
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
		return nil, fmt.Errorf("Failed to parse YAML: %s", err)
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

// GetLastNLines returns the last N lines from the file.
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

// AskConfirm asks the user for confirmation and returns true if yes.
func AskConfirm(question string) (bool, error) {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", question)

		resp, err := reader.ReadString('\n')
		resp = strings.ToLower(strings.TrimSpace(resp))
		if err != nil {
			return false, err
		}

		if resp == "y" || resp == "yes" {
			return true, nil
		}

		if resp == "n" || resp == "no" {
			return false, nil
		}
	}
}

// GetArch returns Architecture of machine.
func GetArch() (string, error) {
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// AtoiUint64 converts string to uint64.
func AtoiUint64(str string) (uint64, error) {
	res, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		return 0, err
	}

	return res, nil
}

// FindNamedMatches processes regexp with named capture groups
// and transforms output to a map. If capture group is optional
// and was not found, map value is empty string.
func FindNamedMatches(re *regexp.Regexp, str string) map[string]string {
	match := re.FindStringSubmatch(str)
	res := map[string]string{}

	for i, value := range match {
		if i == 0 { // Skip input string.
			continue
		}

		res[re.SubexpNames()[i]] = value
	}

	return res
}

// Max returns the maximum value.
func Max(x, y int) int {
	if x < y {
		return y
	}

	return x
}

// getMissedBinaries returns list of binaries not found in PATH.
func getMissedBinaries(binaries ...string) []string {
	var missedBinaries []string

	for _, binary := range binaries {
		if _, err := exec.LookPath(binary); err != nil {
			missedBinaries = append(missedBinaries, binary)
		}
	}

	return missedBinaries
}

// CheckRecommendedBinaries warns if some binaries not found in PATH.
func CheckRecommendedBinaries(binaries ...string) {
	missedBinaries := getMissedBinaries(binaries...)

	if len(missedBinaries) > 0 {
		log.Warnf("Missed recommended binaries %s", strings.Join(missedBinaries, ", "))
	}
}

// isRegularFile checks if filePath is a directory. Returns true if the directory exists.
func IsDir(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.IsDir()
}

// isRegularFile checks if filePath is a regular file. Returns true if the file exists
// and it is a regular file.
func IsRegularFile(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.Mode().IsRegular()
}

// Chdir changes current directory and updates PWD environment var accordingly.
// This can be useful for some scripts, which use getenv('PWD') to get working directory.
func Chdir(newPath string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil
	}
	if err = os.Chdir(newPath); err != nil {
		return "", fmt.Errorf("failed to change directory: %s", err)
	}

	// Update PWD environment var.
	if err = os.Setenv("PWD", newPath); err != nil {
		if err = os.Chdir(cwd); err != nil {
			return "", fmt.Errorf("failed to change directory back: %s", err)
		}
		return "", fmt.Errorf("failed to change PWD environment variable: %s", err)
	}

	return cwd, nil
}

// BitHas32 checks if a bit is set in b.
func BitHas32(b, flag uint32) bool { return b&flag != 0 }

// IsDeprecated checks whether version of program is below 1.10.0.
func IsDeprecated(version string) bool {
	splittedVersion := strings.Split(version, ".")
	if len(splittedVersion) < 2 {
		return false
	}
	if splittedVersion[0] == "1" && len(splittedVersion[1]) < 2 {
		return true
	}
	return false
}

// ResolveSymlink resolves symlink path.
func ResolveSymlink(linkPath string) (string, error) {
	resolvedLink, err := os.Readlink(linkPath)
	if err != nil {
		return "", err
	}
	// Output of os.Readlink is OS-dependent, so need to check if path is absolute.
	if !filepath.IsAbs(resolvedLink) {
		resolvedLink = path.Join(path.Dir(linkPath), resolvedLink)
	}
	return resolvedLink, nil
}

// RunCommandAndGetOutput returns output of command.
func RunCommandAndGetOutput(program string, args ...string) (string, error) {
	out, err := exec.Command(program, args...).Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// ExtractTar extracts tar archive.
func ExtractTar(tarName string) error {

	path, err := filepath.Abs(tarName)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path) + "/"
	archive, err := os.Open(path)
	if err != nil {
		return err
	}
	defer archive.Close()

	tarReader := tar.NewReader(archive)
	if err != nil {
		return err
	}
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			var pos int
			// Some archives have strange order of objects,
			// so we check that all folders exist before
			// creating a file.
			pos = strings.LastIndex(header.Name, "/")
			if pos == -1 {
				pos = 0
			}
			if _, err := os.Stat(dir + header.Name[0:pos]); os.IsNotExist(err) {
				// 0755:
				//    user:   read/write/execute
				//    group:  read/execute
				//    others: read/execute
				os.MkdirAll(dir+header.Name[0:pos], 0755)
			}
			outFile, err := os.Create(dir + header.Name)
			if err != nil {
				outFile.Close()
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()

		default:
			return fmt.Errorf("Unknown type: %b in %s", header.Typeflag, header.Name)
		}

	}
	return nil
}
