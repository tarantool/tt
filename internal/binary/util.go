package binary

import (
	"archive/tar"
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"gopkg.in/yaml.v2"
)

var (
	errAlreadyExistsAndIsNotADirectory = errors.New(
		"already exists and is not a directory",
	)
	errSymbolicLinkCannotBeCreatedAlreadyExists = errors.New("symbolic link cannot be created: '")
	errUnknownArchiveEntryType                  = errors.New("unknown type: ")
)

const archiveDirectoryMode = 0o755

type OsType uint16

const (
	OsLinux OsType = iota
	OsMacos
	OsUnknown
)

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

// BitHas32 checks if a bit is set in b.
func BitHas32(b, flag uint32) bool { return b&flag != 0 }

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

// Max returns the maximum value.
func Max(x, y int) int {
	if x < y {
		return y
	}

	return x
}

// Min returns minimal of two values.
func Min[T cmp.Ordered](a, b T) T {
	if a < b {
		return a
	}

	return b
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
