package util

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
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

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"golang.org/x/exp/constraints"
	"gopkg.in/yaml.v2"
)

const bufSize int64 = 10000

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
		return nil, err
	}
	defer file.Close()

	fileContent, err := io.ReadAll(file)
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
	if path, err = filepath.Abs(path); err != nil {
		return "", fmt.Errorf("failed to get absolute path: %s", err)
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
func InternalError(format string, f VersionFunc, err ...interface{}) error {
	errorFmt := `whoops! It looks like something is wrong with this version of Tarantool CLI.
Error: %s
Version: %s
Stacktrace:
%s`
	version := f(false, false)

	return fmt.Errorf(errorFmt, fmt.Sprintf(format, err...), version, debug.Stack())
}

// ParseYAML parse yaml file at specified path.
func ParseYAML(path string) (map[string]interface{}, error) {
	fileContent, err := GetFileContentBytes(path)
	if err != nil {
		return nil, fmt.Errorf(`failed to read "%s" file: %s`, path, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(fileContent, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %s", err)
	}

	return raw, nil
}

// GetHomeDir returns current home directory.
func GetHomeDir() (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return usr.HomeDir, nil
}

func readFromPos(readSeeker io.ReadSeeker, pos int64, buf *[]byte) (int, error) {
	if _, err := readSeeker.Seek(pos, io.SeekStart); err != nil {
		return 0, fmt.Errorf("failed to seek: %s", err)
	}

	n, err := readSeeker.Read(*buf)
	if err != nil {
		return n, fmt.Errorf("failed to read: %s", err)
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
		return 0, fmt.Errorf("failed to open file: %s", err)
	}
	defer f.Close()

	var fileSize int64
	if fileInfo, err := os.Stat(filepath); err != nil {
		return 0, fmt.Errorf("failed to get fileinfo: %s", err)
	} else {
		fileSize = fileInfo.Size()
	}

	if fileSize == 0 {
		return 0, nil
	}

	buf := make([]byte, bufSize)

	filePos := fileSize - bufSize
	var lastNewLinePos int64 = 0
	newLinesN := 0

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
		return nil, fmt.Errorf("failed to open file: %s", err)
	}

	if _, err := file.Seek(lastNLinesBeginPos, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek in file: %s", err)
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

// GetOs returns the operating system version of the host.
func GetOs() (OsType, error) {
	out, err := exec.Command("uname", "-s").Output()
	if err != nil {
		return OsUnknown, err
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

// IsURL checks if str is a valid URL.
func IsURL(str string) bool {
	if strings.HasPrefix(str, "unix:") {
		return true
	}
	u, err := url.Parse(str)

	return err == nil && u.Scheme != "" && u.Host != "" && u.Opaque == "" && u.User == nil
}

// removeScheme removes the scheme from the input URL.
func RemoveScheme(inputURL string) (string, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", err
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
		return nil, nil
	}
	if err = os.Chdir(newPath); err != nil {
		return nil, fmt.Errorf("failed to change directory: %s", err)
	}

	// Update PWD environment var.
	if err = os.Setenv("PWD", newPath); err != nil {
		if err = os.Chdir(cwd); err != nil {
			return nil, fmt.Errorf("failed to change directory back: %w", err)
		}
		os.Setenv("PWD", cwd) // Return PWD back.
		return nil, fmt.Errorf("failed to change PWD environment variable: %w", err)
	}

	return func() error {
		if err = os.Chdir(cwd); err != nil {
			return fmt.Errorf("failed to change directory back: %w", err)
		}
		if err = os.Setenv("PWD", cwd); err != nil {
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
		return err
	}
	// Write data to dst.
	return os.WriteFile(dst, data, fs.FileMode(perms))
}

// CopyFilePreserve copies file from source to destination with perms.
func CopyFilePreserve(src, dst string) error {
	// Read all content of src to data.
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Write data to dst.
	err = os.WriteFile(dst, data, info.Mode().Perm())
	return err
}

// CopyFileChangePerms copies file from source to destination with changing perms.
func CopyFileChangePerms(src, dst string, perms int) error {
	// Read all content of src to data.
	_, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Write data to dst.
	err = os.WriteFile(dst, data, fs.FileMode(perms))
	return err
}

// ResolveSymlink resolves symlink path.
func ResolveSymlink(linkPath string) (string, error) {
	resolvedLink, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return "", err
	}

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

	uncompressedStream, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}

	tarReader := tar.NewReader(uncompressedStream)
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
				os.MkdirAll(dir+header.Name[0:pos], 0o755)
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
			return fmt.Errorf("unknown type: %b in %s", header.Typeflag, header.Name)
		}

	}
	return nil
}

// ExecuteCommand executes program with given args in verbose or quiet mode.
func ExecuteCommand(program string, isVerbose bool, writer io.Writer, workDir string,
	args ...string,
) error {
	cmd := exec.Command(program, args...)
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
		return err
	}
	err = cmd.Wait()
	return err
}

// ExecuteCommandStdin executes program with given args in verbose or quiet mode
// and sends stdinData to stdin pipe.
func ExecuteCommandStdin(program string, isVerbose bool, logFile *os.File, workDir string,
	stdinData []byte, args ...string,
) error {
	cmd := exec.Command(program, args...)
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
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	stdin.Write(stdinData)
	stdin.Close()

	err = cmd.Wait()
	return err
}

// CreateSymlink creates newName as a symbolic link to oldName. Overwrites existing if overwrite
// flag is set.
func CreateSymlink(oldName, newName string, overwrite bool) error {
	if _, err := os.Stat(newName); err == nil {
		if !overwrite {
			return fmt.Errorf("symbolic link cannot be created: '%s' already exists", newName)
		} else {
			log.Debugf("Replace existing '%s' with new symlink.", newName)
			if err := os.Remove(newName); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("symbolic link cannot be created: %s", err)
	}

	return os.Symlink(oldName, newName)
}

// IsApp detects if the passed path is an application.
func IsApp(path string) bool {
	entry, err := os.Stat(path)
	if err != nil {
		return false
	}

	if entry.IsDir() {
		// Check if the directory contains init.lua script or instances.yml file.
		for _, fileTocheck := range [...]string{"init.lua", "instances.yml", "instances.yaml"} {
			if fileInfo, err := os.Stat(filepath.Join(path, fileTocheck)); err == nil {
				if !fileInfo.IsDir() {
					return true
				}
			}
		}
	} else {
		if filepath.Ext(entry.Name()) == ".lua" {
			return true
		}
	}

	return false
}

// CheckRequiredBinaries returns an error if some binaries not found in PATH.
func CheckRequiredBinaries(binaries ...string) error {
	missedBinaries := getMissedBinaries(binaries...)

	if len(missedBinaries) > 0 {
		return fmt.Errorf("missed required binaries %s", strings.Join(missedBinaries, ", "))
	}

	return nil
}

// CreateDirectory create a directory with existence and error checks.
func CreateDirectory(dirName string, fileMode os.FileMode) error {
	stat, err := os.Stat(dirName)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		if !stat.IsDir() {
			return fmt.Errorf("'%s' already exists and is not a directory", dirName)
		}
		return nil
	}
	if err = os.MkdirAll(dirName, fileMode); err != nil {
		return err
	}
	return nil
}

// writeYaml writes YAML encoding of object o to fileName.
func WriteYaml(fileName string, o interface{}) error {
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warnf("Failed to close a file '%s': %s", file.Name(), err)
		}
	}()

	if err = yaml.NewEncoder(file).Encode(o); err != nil {
		return err
	}
	return nil
}

// ConcatBuffers appends sources content to dest.
func ConcatBuffers(dest *bytes.Buffer, sources ...*bytes.Buffer) error {
	for _, src := range sources {
		if _, err := io.Copy(dest, src); err != nil {
			return err
		}
	}

	return nil
}

// MergeFiles creates a file that is a concatenation of srcFilePaths.
func MergeFiles(destFilePath string, srcFilePaths ...string) error {
	destFile, err := os.Create(destFilePath)
	if err != nil {
		_ = os.Remove(destFilePath)
		return fmt.Errorf("failed to create result file %s: %s", destFilePath, err)
	}
	defer destFile.Close()

	for _, srcFilePath := range srcFilePaths {
		srcFile, err := os.Open(srcFilePath)
		if err != nil {
			_ = os.Remove(destFilePath)
			return fmt.Errorf("failed to open source file %s: %s", srcFilePath, err)
		}

		_, err = io.Copy(destFile, srcFile)
		srcFile.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

// GetYamlFileName searches for file with .yaml or .yml extension, based on the file name provided.
// If mustExist flag is set and no yaml files are found, ErrNotExists error is returned,
// passed fileName is returned otherwise.
func GetYamlFileName(fileName string, mustExist bool) (string, error) {
	fileBaseName := fileName
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
		return "", fmt.Errorf("provided file '%s' has no .yaml/.yml extension", fileName)
	}
	foundYamlFiles := []string{}
	if foundFiles, err := filepath.Glob(fmt.Sprintf("%s.y*ml", fileBaseName)); err == nil {
		for _, fileName := range foundFiles {
			switch filepath.Ext(fileName) {
			case ".yaml", ".yml":
				foundYamlFiles = append(foundYamlFiles, fileName)
			}
		}
	} else {
		return "", err
	}
	yamlFilesCount := len(foundYamlFiles)
	if yamlFilesCount > 1 {
		return "", fmt.Errorf("more than one YAML files are found:\n%s\nAmbiguous selection",
			strings.Join(foundYamlFiles, ", "))
	} else if yamlFilesCount == 1 {
		return foundYamlFiles[0], nil
	} else if !mustExist {
		return "", nil
	}

	return "", os.ErrNotExist
}

// InstantiateFileFromTemplate accepts the path to file,
// template content and parameters for its filling.
func InstantiateFileFromTemplate(templatePath, templateContent string, params any) error {
	file, err := os.Create(templatePath)
	if err != nil {
		return err
	}
	defer file.Close()

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
		return fmt.Errorf("error parsing %s: %s", templatePath, err)
	}
	parsedTemplate.Option("missingkey=error") // Treat missing variable as error.

	_, err = file.WriteString(unitContent)
	if err != nil {
		removeErr := os.Remove(templatePath)
		if removeErr != nil {
			log.Warnf("Failed to remove a file %s", templatePath)
		}
		return err
	}
	return nil
}

// CollectAppList collects all the supposed applications in passed appsPath directory.
func CollectAppList(baseDir, appsPath string, verbose bool) ([]string, error) {
	if appsPath == "." {
		// Check whether base directory is application.
		if IsApp(baseDir) {
			return []string{filepath.Base(baseDir)}, nil
		}
		// Instances enabled is '.', if base directory is not an application,
		// consider base directory as directory containing a set of applications.
		appsPath = baseDir
	}
	dirEntries, err := os.ReadDir(appsPath)
	if err != nil {
		return nil, err
	}

	apps := make([]string, 0)
	for _, entry := range dirEntries {
		dirItem := filepath.Join(appsPath, entry.Name())
		if IsApp(dirItem) {
			apps = append(apps, entry.Name())
		} else if verbose {
			log.Debugf("Skipping %s: the source is not an application.", entry.Name())
		}
	}

	return apps, nil
}

// RelativeToCurrentWorkingDir returns a path relative to current working dir.
// In case of error, fullpath is returned.
func RelativeToCurrentWorkingDir(fullpath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fullpath
	}
	relPath, err := filepath.Rel(cwd, fullpath)
	if err != nil {
		return fullpath
	}
	return relPath
}

// Min returns minimal of two values.
func Min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

// HandleCmdErr handles an error returned by command implementation.
// If received error is of an ArgError type, usage help is printed.
func HandleCmdErr(cmd *cobra.Command, err error) {
	if err != nil {
		var argError *ArgError
		if errors.As(err, &argError) {
			log.Error(argError.Error())
			cmd.Usage()
			os.Exit(1)
		}
		if errors.Is(err, ErrCmdAbort) {
			os.Exit(1)
		}
		log.Fatalf(err.Error())
	}
}

// CopyFileDeep copies a file resolving symlinks.
func CopyFileDeep(src, dst string) error {
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return copy.Copy(src, dst)
}

// StringToTimestamp transforms string with number or RFC339Nano time
// to <sec.nanosec> timestamp string.
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
		// Incorrect input, trigger an error.
		return "", err
	}
	tsSec := rfc3339NanoTs.Unix()
	tsNanoSec := rfc3339NanoTs.Nanosecond()
	ts := fmt.Sprintf("%s.%s", strconv.FormatInt(tsSec, 10),
		strconv.FormatInt(int64(tsNanoSec), 10))

	return ts, nil
}
