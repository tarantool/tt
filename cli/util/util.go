package util

import (
	"archive/tar"
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"gopkg.in/yaml.v2"
)

var (
	errAlreadyExistsAndIsNotADirectory = errors.New("already exists and is not a directory")
	errInternal                        = errors.New(
		"whoops! It looks like something is wrong with this version of Tarantool CLI.\nError: ",
	)
	errMissedRequiredBinaries                         = errors.New("missed required binaries ")
	errMoreThanOneYAMLFilesAreFoundAmbiguousSelection = errors.New(
		"more than one YAML files are found",
	)
	errInvalidYAMLFileExtension                 = errors.New("provided file '")
	errSymbolicLinkCannotBeCreatedAlreadyExists = errors.New(
		"symbolic link cannot be created: '",
	)
	errUnknownArchiveEntryType = errors.New("unknown type: ")
)

const bufSize int64 = 10000

const archiveDirectoryMode = 0o755

type OsType uint16

const (
	OsLinux OsType = iota
	OsMacos
	OsUnknown
)

// ArgError represents command line arguments error.
type ArgError struct {
	msg string
}

// Error returns error message.
func (e ArgError) Error() string {
	return e.msg
}

// NewArgError creates and returns new argument error.
func NewArgError(text string) error {
	return &ArgError{text}
}

// VersionFunc is a type of function that return
// string with current Tarantool CLI version.
type VersionFunc func(bool, bool) string

// FileLinesScanner returns scanner for file.
func FileLinesScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Split(bufio.ScanLines)

	return scanner
}

// GetFileContentBytes returns file content as a bytes slice.
func GetFileContentBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open the file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	fileContent, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read %q: %w", path, err)
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

// JoinPaths concat paths.
func JoinPaths(paths ...string) string {
	path := ""

	for _, pathPart := range paths {
		if filepath.IsAbs(pathPart) {
			path = pathPart
		} else {
			path = filepath.Join(path, pathPart)
		}
	}

	return path
}

// JoinAbspath concat paths and makes the resulting path absolute.
func JoinAbspath(paths ...string) (string, error) {
	var err error

	path := JoinPaths(paths...)

	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	return path, nil
}

// Find find index of specified string in the slice.
//
// Deprecated: use [slices.Index] or [slices.Contains] instead.
func Find(src []string, find string) int {
	for i, elem := range src {
		if find == elem {
			return i
		}
	}

	return -1
}

// InternalError shows error information, version of tt and call stack.
func InternalError(format string, f VersionFunc, err ...any) error {
	version := f(false, false)

	return fmt.Errorf("%w%s\nVersion: %s\nStacktrace:\n%s",
		errInternal, fmt.Sprintf(format, err...), version, debug.Stack())
}

// ParseYAML parse yaml file at specified path.
func ParseYAML(path string) (map[string]any, error) {
	fileContent, err := GetFileContentBytes(path)
	if err != nil {
		return nil, fmt.Errorf(`failed to read "%s" file: %w`, path, err)
	}

	var raw map[string]any

	err = yaml.Unmarshal(fileContent, &raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return raw, nil
}

// GetHomeDir returns current home directory.
func GetHomeDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("cannot get the current user: %w", err)
	}

	return usr.HomeDir, nil
}

func readFromPos(readSeeker io.ReadSeeker, pos int64, buf *[]byte) (int, error) {
	_, err := readSeeker.Seek(pos, io.SeekStart)
	if err != nil {
		return 0, fmt.Errorf("failed to seek: %w", err)
	}

	n, err := readSeeker.Read(*buf)
	if err != nil {
		return n, fmt.Errorf("failed to read: %w", err)
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

	file, err := os.Open(filepath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	fileInfo, err := os.Stat(filepath)
	if err != nil {
		return 0, fmt.Errorf("failed to get fileinfo: %w", err)
	}

	fileSize := fileInfo.Size()

	if fileSize == 0 {
		return 0, nil
	}

	buf := make([]byte, bufSize)

	filePos := fileSize - bufSize

	var lastNewLinePos int64 = 0

	newLinesN := 0

	// Check last symbol of the last line.

	_, err = readFromPos(file, fileSize-1, &buf)
	if err != nil {
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

		n, err := readFromPos(file, filePos, &buf)
		if err != nil {
			return 0, err
		}

		for pos := n - 1; pos >= 0; pos-- {
			if buf[pos] == '\n' {
				newLinesN++
			}

			if newLinesN == lines+1 {
				lastNewLinePos = filePos + int64(pos+1)
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
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	_, err = file.Seek(lastNLinesBeginPos, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek in file: %w", err)
	}

	lines := []string{}

	scanner := FileLinesScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, nil
}

// AskConfirm asks the user for confirmation and returns true if yes.
func AskConfirm(ioReader io.Reader, question string) (bool, error) {
	reader := bufio.NewReader(ioReader)

	for {
		_, _ = fmt.Fprintf(os.Stdout, "%s [y/n]: ", question)

		resp, err := reader.ReadString('\n')

		resp = strings.ToLower(strings.TrimSpace(resp))

		if err != nil {
			return false, fmt.Errorf("cannot read the answer: %w", err)
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
	out, err := exec.CommandContext(context.Background(), "uname", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("cannot run uname -m: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// GetOs returns the operating system version of the host.
func GetOs() (OsType, error) {
	out, err := exec.CommandContext(context.Background(), "uname", "-s").Output()
	if err != nil {
		return OsUnknown, fmt.Errorf("cannot run uname -s: %w", err)
	}

	osStr := strings.TrimSpace(string(out))
	switch osStr {
	case "Linux":
		return OsLinux, nil
	case "Darwin":
		return OsMacos, nil
	}

	return OsUnknown, nil
}

// AtoiUint64 converts string to uint64.
func AtoiUint64(str string) (uint64, error) {
	res, err := strconv.ParseUint(str, 10, 64)
	if err != nil {
		// The *strconv.NumError names the input, and callers match its text exactly.
		return 0, err //nolint:wrapcheck // A thin alias of strconv.ParseUint.
	}

	return res, nil
}

// FindNamedMatches processes regexp with named capture groups
// and transforms output to a map. If capture group is optional
// and was not found, map value is empty string.
func FindNamedMatches(pattern *regexp.Regexp, str string) map[string]string {
	match := pattern.FindStringSubmatch(str)
	res := map[string]string{}

	for i, value := range match {
		if i == 0 { // Skip input string.
			continue
		}

		res[pattern.SubexpNames()[i]] = value
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
		_, err := exec.LookPath(binary)
		if err != nil {
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

// IsDir checks if filePath is a directory. Returns true if the directory exists.
func IsDir(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.IsDir()
}

// IsRegularFile checks if filePath is a regular file. Returns true if the file exists
// and it is a regular file.
func IsRegularFile(filePath string) bool {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	return fileInfo.Mode().IsRegular()
}

// IsURL checks if str is a valid URL.
func IsURL(str string) bool {
	if strings.HasPrefix(str, "unix:") {
		return true
	}

	u, err := url.Parse(str)

	return err == nil && u.Scheme != "" && u.Host != "" && u.Opaque == "" && u.User == nil
}

// RemoveScheme removes the scheme from the input URL.
func RemoveScheme(inputURL string) (string, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", fmt.Errorf("cannot remove the scheme: %w", err)
	}

	if parsedURL.Scheme == "unix" {
		return inputURL, nil
	}

	parsedURL.Scheme = ""

	result := strings.Replace(parsedURL.String(), "//", "", 1)

	return result, nil
}

// Chdir changes current directory and updates PWD environment var accordingly.
// This can be useful for some scripts, which use getenv('PWD') to get working directory.
func Chdir(newPath string) (func() error, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	err = os.Chdir(newPath)
	if err != nil {
		return nil, fmt.Errorf("failed to change directory: %w", err)
	}

	// Update PWD environment var.
	err = os.Setenv("PWD", newPath)
	if err != nil {
		err = os.Chdir(cwd)
		if err != nil {
			return nil, fmt.Errorf("failed to change directory back: %w", err)
		}

		_ = os.Setenv("PWD", cwd) // Return PWD back.

		return nil, fmt.Errorf("failed to change PWD environment variable: %w", err)
	}

	return func() error {
		err = os.Chdir(cwd)
		if err != nil {
			return fmt.Errorf("failed to change directory back: %w", err)
		}

		err = os.Setenv("PWD", cwd)
		if err != nil {
			return fmt.Errorf("failed to change PWD environment variable: %w", err)
		}

		return nil
	}, nil
}

// BitHas32 checks if a bit is set in b.
func BitHas32(b, flag uint32) bool { return b&flag != 0 }

// FsCopyFileChangePerms copies file from the certain FS with changing perms.
func FsCopyFileChangePerms(fsys fs.FS, src, dst string, perms int) error {
	// Read data from src.
	data, err := fs.ReadFile(fsys, src)
	if err != nil {
		return fmt.Errorf("cannot read the source file: %w", err)
	}

	// Write data to dst.
	//nolint:gosec // perms is a file mode chosen by the caller, not external input.
	err = os.WriteFile(dst, data, fs.FileMode(perms))
	if err != nil {
		return fmt.Errorf("cannot write the destination file: %w", err)
	}

	return nil
}

// CopyFilePreserve copies file from source to destination with perms.
func CopyFilePreserve(src, dst string) error {
	// Read all content of src to data.
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("cannot stat the source file: %w", err)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("cannot read the source file: %w", err)
	}

	// Write data to dst.
	err = os.WriteFile(dst, data, info.Mode().Perm()) //nolint:gosec // dst is chosen by the caller.
	if err != nil {
		return fmt.Errorf("cannot write the destination file: %w", err)
	}

	return nil
}

// CopyFileChangePerms copies file from source to destination with changing perms.
func CopyFileChangePerms(src, dst string, perms int) error {
	// Read all content of src to data.
	_, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("cannot stat the source file: %w", err)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("cannot read the source file: %w", err)
	}

	// Write data to dst.
	//nolint:gosec // perms is a file mode chosen by the caller, not external input.
	err = os.WriteFile(dst, data, fs.FileMode(perms))
	if err != nil {
		return fmt.Errorf("cannot write the destination file: %w", err)
	}

	return nil
}

// ResolveSymlink resolves symlink path.
func ResolveSymlink(linkPath string) (string, error) {
	resolvedLink, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		// Callers test the result with os.IsNotExist, which does not unwrap.
		return "", err //nolint:wrapcheck // The *fs.PathError must reach callers as is.
	}

	if !filepath.IsAbs(resolvedLink) {
		resolvedLink = path.Join(path.Dir(linkPath), resolvedLink)
	}

	return resolvedLink, nil
}

// RunCommandAndGetOutput returns output of command.
func RunCommandAndGetOutput(program string, args ...string) (string, error) {
	out, err := exec.CommandContext(context.Background(), program, args...).Output()
	if err != nil {
		return "", fmt.Errorf("cannot run %s: %w", program, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// ExtractTar extracts tar archive.
func ExtractTar(tarName string) error {
	path, err := filepath.Abs(tarName)
	if err != nil {
		return fmt.Errorf("cannot get the absolute path of %q: %w", tarName, err)
	}

	dir := filepath.Dir(path) + "/"

	archive, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open the archive: %w", err)
	}

	defer func() {
		_ = archive.Close()
	}()

	uncompressedStream, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("cannot decompress %q: %w", path, err)
	}

	tarReader := tar.NewReader(uncompressedStream)

	for {
		header, err := tarReader.Next()

		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("cannot read %q: %w", path, err)
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

			_, err := os.Stat(dir + header.Name[0:pos])
			if os.IsNotExist(err) {
				// 0755:
				//    user:   read/write/execute
				//    group:  read/execute
				//    others: read/execute
				_ = os.MkdirAll(dir+header.Name[0:pos], archiveDirectoryMode)
			}

			outFile, err := os.Create(dir + header.Name)
			if err != nil {
				_ = outFile.Close()
				return fmt.Errorf("cannot create an extracted file: %w", err)
			}

			//nolint:gosec // The archive is a tt bundle; its size is not limited.
			_, err = io.Copy(outFile, tarReader)
			if err != nil {
				_ = outFile.Close()
				return fmt.Errorf("cannot extract %q: %w", header.Name, err)
			}

			_ = outFile.Close()

		default:
			return fmt.Errorf("%w%b in %s",
				errUnknownArchiveEntryType, header.Typeflag, header.Name)
		}
	}

	return nil
}

// ExecuteCommand executes program with given args in verbose or quiet mode.
func ExecuteCommand(program string, isVerbose bool, writer io.Writer, workDir string,
	args ...string,
) error {
	cmd := exec.CommandContext(context.Background(), program, args...)
	if isVerbose {
		log.Infof("Run: %s\n", cmd)
	}

	if isVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = writer
		cmd.Stderr = writer
	}

	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	cmd.Dir = workDir

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("cannot start %s: %w", program, err)
	}

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("%s failed: %w", program, err)
	}

	return nil
}

// ExecuteCommandStdin executes program with given args in verbose or quiet mode
// and sends stdinData to stdin pipe.
func ExecuteCommandStdin(program string, isVerbose bool, logFile *os.File, workDir string,
	stdinData []byte, args ...string,
) error {
	cmd := exec.CommandContext(context.Background(), program, args...)
	if isVerbose {
		log.Infof("Run: %s\n", cmd)
	}

	if isVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		if logFile != nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		} else {
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
		}
	}

	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	cmd.Dir = workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("cannot open the stdin of %s: %w", program, err)
	}

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("cannot start %s: %w", program, err)
	}

	_, _ = stdin.Write(stdinData)
	_ = stdin.Close()

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("%s failed: %w", program, err)
	}

	return nil
}

// CreateSymlink creates newName as a symbolic link to oldName. Overwrites existing if overwrite
// flag is set.
func CreateSymlink(oldName, newName string, overwrite bool) error {
	_, err := os.Stat(newName)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("symbolic link cannot be created: %w", err)
	}

	if !os.IsNotExist(err) {
		if !overwrite {
			return fmt.Errorf("%w%s' already exists",
				errSymbolicLinkCannotBeCreatedAlreadyExists, newName)
		}

		log.Debugf("Replace existing '%s' with new symlink.", newName)

		err = os.Remove(newName)
		if err != nil {
			return fmt.Errorf("cannot replace the existing file: %w", err)
		}
	}

	err = os.Symlink(oldName, newName)
	if err != nil {
		return fmt.Errorf("symbolic link cannot be created: %w", err)
	}

	return nil
}

// IsApp detects if the passed path is an application.
func IsApp(path string) bool {
	entry, err := os.Stat(path)
	if err != nil {
		return false
	}

	if entry.IsDir() {
		// Check if the directory contains init.lua script or instances.yml file.
		for _, fileToCheck := range [...]string{"init.lua", "instances.yml", "instances.yaml"} {
			fileInfo, err := os.Stat(filepath.Join(path, fileToCheck))
			if err == nil && !fileInfo.IsDir() {
				return true
			}
		}
	} else if filepath.Ext(entry.Name()) == ".lua" {
		return true
	}

	return false
}

// CheckRequiredBinaries returns an error if some binaries not found in PATH.
func CheckRequiredBinaries(binaries ...string) error {
	missedBinaries := getMissedBinaries(binaries...)

	if len(missedBinaries) > 0 {
		return fmt.Errorf("%w%s", errMissedRequiredBinaries, strings.Join(missedBinaries, ", "))
	}

	return nil
}

// CreateDirectory create a directory with existence and error checks.
func CreateDirectory(dirName string, fileMode os.FileMode) error {
	stat, err := os.Stat(dirName)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot stat the directory: %w", err)
		}
	} else {
		if !stat.IsDir() {
			return fmt.Errorf("'%s' %w", dirName, errAlreadyExistsAndIsNotADirectory)
		}

		return nil
	}

	err = os.MkdirAll(dirName, fileMode)
	if err != nil {
		return fmt.Errorf("cannot create the directory: %w", err)
	}

	return nil
}

// WriteYaml writes YAML encoding of object obj to fileName.
func WriteYaml(fileName string, obj any) error {
	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("cannot create the YAML file: %w", err)
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil {
			log.Warnf("Failed to close a file '%s': %s", file.Name(), closeErr)
		}
	}()

	err = yaml.NewEncoder(file).Encode(obj)
	if err != nil {
		return fmt.Errorf("cannot write YAML to %q: %w", fileName, err)
	}

	return nil
}

// ConcatBuffers appends sources content to dest.
func ConcatBuffers(dest *bytes.Buffer, sources ...*bytes.Buffer) error {
	for _, src := range sources {
		_, err := io.Copy(dest, src)
		if err != nil {
			return fmt.Errorf("cannot append a buffer: %w", err)
		}
	}

	return nil
}

// MergeFiles creates a file that is a concatenation of srcFilePaths.
func MergeFiles(destFilePath string, srcFilePaths ...string) error {
	destFile, err := os.Create(destFilePath)
	if err != nil {
		_ = os.Remove(destFilePath)
		return fmt.Errorf("failed to create result file %s: %w", destFilePath, err)
	}

	defer func() {
		_ = destFile.Close()
	}()

	for _, srcFilePath := range srcFilePaths {
		srcFile, err := os.Open(srcFilePath)
		if err != nil {
			_ = os.Remove(destFilePath)
			return fmt.Errorf("failed to open source file %s: %w", srcFilePath, err)
		}

		_, err = io.Copy(destFile, srcFile)
		_ = srcFile.Close()

		if err != nil {
			return fmt.Errorf("failed to copy source file %s: %w", srcFilePath, err)
		}
	}

	return nil
}

// GetYamlFileName searches for file with .yaml or .yml extension, based on the file name provided.
// If mustExist flag is set and no yaml files are found, ErrNotExists error is returned,
// passed fileName is returned otherwise.
func GetYamlFileName(fileName string, mustExist bool) (string, error) {
	var fileBaseName string

	switch filepath.Ext(fileName) {
	case ".yaml":
		fileBaseName = strings.TrimSuffix(fileName, ".yaml")
	case ".yml":
		fileBaseName = strings.TrimSuffix(fileName, ".yml")
	case ".":
		fileBaseName = strings.TrimSuffix(fileName, ".")
	case "":
		fileBaseName = fileName
	default:
		return "", fmt.Errorf("%w%s' has no .yaml/.yml extension",
			errInvalidYAMLFileExtension, fileName)
	}

	foundYamlFiles := []string{}

	foundFiles, err := filepath.Glob(fileBaseName + ".y*ml")
	if err != nil {
		return "", fmt.Errorf("cannot search for %q: %w", fileBaseName+".y*ml", err)
	}

	for _, fileName := range foundFiles {
		switch filepath.Ext(fileName) {
		case ".yaml", ".yml":
			foundYamlFiles = append(foundYamlFiles, fileName)
		}
	}

	yamlFilesCount := len(foundYamlFiles)
	switch {
	case yamlFilesCount > 1:
		return "", fmt.Errorf("%w:\n%s\nAmbiguous selection",
			errMoreThanOneYAMLFilesAreFoundAmbiguousSelection, strings.Join(foundYamlFiles, ", "))
	case yamlFilesCount == 1:
		return foundYamlFiles[0], nil
	case !mustExist:
		return "", nil
	}

	return "", os.ErrNotExist
}

// InstantiateFileFromTemplate accepts the path to file,
// template content and parameters for its filling.
func InstantiateFileFromTemplate(templatePath, templateContent string, params any) error {
	file, err := os.Create(templatePath)
	if err != nil {
		return fmt.Errorf("cannot create the file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	unitContent, err := GetTextTemplatedStr(&templateContent, params)
	if err != nil {
		removeErr := os.Remove(templatePath)
		if removeErr != nil {
			log.Warnf("Failed to remove a file %s", templatePath)
		}

		return err
	}

	parsedTemplate, err := template.New(templatePath).Parse(unitContent)
	if err != nil {
		return fmt.Errorf("error parsing %s: %w", templatePath, err)
	}

	// spell-checker:ignore missingkey
	parsedTemplate.Option("missingkey=error") // Treat missing variable as error.

	_, err = file.WriteString(unitContent)
	if err != nil {
		removeErr := os.Remove(templatePath)
		if removeErr != nil {
			log.Warnf("Failed to remove a file %s", templatePath)
		}

		return fmt.Errorf("cannot write %q: %w", templatePath, err)
	}

	return nil
}

// RelativeToCurrentWorkingDir returns a path relative to current working dir.
// In case of error, fullPath is returned.
func RelativeToCurrentWorkingDir(fullPath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fullPath
	}

	relPath, err := filepath.Rel(cwd, fullPath)
	if err != nil {
		return fullPath
	}

	return relPath
}

// Min returns minimal of two values.
func Min[T cmp.Ordered](a, b T) T {
	if a < b {
		return a
	}

	return b
}

// CopyFileDeep copies a file resolving symlinks.
func CopyFileDeep(src, dst string) error {
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("cannot resolve the source path: %w", err)
	}

	err = copy.Copy(src, dst)
	if err != nil {
		return fmt.Errorf("cannot copy %q to %q: %w", src, dst, err)
	}

	return nil
}

// StringToTimestamp transforms string with number or RFC339Nano time
// to <sec.nanosecond> timestamp string.
func StringToTimestamp(input string) (string, error) {
	if input == "" {
		// Default value.
		return strconv.FormatUint(math.MaxUint64, 10), nil
	}

	floatTimestamp, err := strconv.ParseFloat(input, 64)
	if err == nil {
		return strconv.FormatFloat(floatTimestamp, 'f', -1, 64), nil
	}

	// The RFC3339Nano layout also successfully parses the RFC3339 layout.
	rfc3339NanoTs, err := time.Parse(time.RFC3339Nano, input)
	if err != nil {
		// Incorrect input, trigger an error. Callers and tests match the
		// *time.ParseError text right after their own prefix.
		return "", err //nolint:wrapcheck // The error names the input already.
	}

	tsSec := rfc3339NanoTs.Unix()
	tsNanoSec := rfc3339NanoTs.Nanosecond()
	ts := fmt.Sprintf("%s.%s", strconv.FormatInt(tsSec, 10),
		strconv.FormatInt(int64(tsNanoSec), 10))

	return ts, nil
}
