package binary

import (
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
	"syscall"

	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/untar"
)

var (
	errAlreadyExistsAndIsNotADirectory = errors.New(
		"already exists and is not a directory",
	)
	errSymbolicLinkCannotBeCreatedAlreadyExists = errors.New("symbolic link cannot be created: '")
)

type OsType uint16

const (
	OsLinux OsType = iota
	OsMacos
	OsUnknown
)

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
	err = execfile.WriteFile(dst, data, fs.FileMode(perms))
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
	err = execfile.WriteFile(dst, data, info.Mode().Perm())
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
	_, err := os.Lstat(newName)
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
	return ExecuteCommandEnv(program, nil, isVerbose, writer, workDir, args...)
}

// ExecuteCommandEnv is ExecuteCommand that runs program in the environment
// environ, key=value entries; a nil one is the environment of tt.
func ExecuteCommandEnv(program string, environ []string, isVerbose bool, writer io.Writer,
	workDir string, args ...string,
) error {
	ctx, done := commandContext()
	defer done()

	cmd := exec.CommandContext(ctx, program, args...)

	// An interrupt stops the command with SIGTERM, which a make passes on to
	// the compilers it runs, and kills it if it does not exit in time.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = commandWaitDelay
	cmd.Env = environ

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

// ExtractTar extracts a tar.gz archive next to it: its directories and its
// regular files, with 0666. Any other entry, or one outside the directory of
// the archive, is an error.
func ExtractTar(tarName string) error {
	path, err := filepath.Abs(tarName)
	if err != nil {
		return fmt.Errorf("cannot get the absolute path of %q: %w", tarName, err)
	}

	return untar.ExtractTarGz(path, filepath.Dir(path), untar.Options{
		Symlinks: false, EntryModes: false,
	})
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
