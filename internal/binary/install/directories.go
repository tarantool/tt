package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/configure"
	"golang.org/x/sys/unix"
)

var (
	errBinDirNotSet                                = errors.New("bin_dir is not set, check ")
	errIncludeDirIsNotSetCheck                     = errors.New("include_dir is not set, check ")
	errTheDirectoryIsNotWriteableForTheCurrentUser = errors.New("the directory ")
)

const (
	// defaultDirPermissions is rights used to create folders.
	// 0755 - drwxr-xr-x
	// We need to give permission for all to execute
	// read,write for user and only read for others.
	defaultDirPermissions = 0o755
)

// dirIsWritable checks if the current user has the write access to the passed directory.
func dirIsWritable(dir string) bool {
	return unix.Access(dir, unix.W_OK) == nil
}

// subDirIsWritable checks if the passed dir doesn't exist but can be created.
func subDirIsWritable(dir string) bool {
	var err error

	for {
		_, err = os.Stat(dir)
		if os.IsNotExist(err) {
			dir = filepath.Dir(dir)
			continue
		}

		return dirIsWritable(dir)
	}
}

// prepareInstallDirs validates the environment and resolves the destination for headers.
func prepareInstallDirs(installCtx *InstallCtx, cliOpts *config.CliOpts) error {
	err := checkInstallDirs(cliOpts.Env.BinDir, cliOpts.Env.IncludeDir)
	if err != nil {
		return err
	}

	if installCtx.IncDir == "" && installCtx.FromBuild == "" {
		installCtx.IncDir = filepath.Join(cliOpts.Env.IncludeDir, "include")
	}

	return nil
}

// checkInstallDirs validates that binary and include directories are configured and writable.
func checkInstallDirs(binDir, includeDir string) error {
	if binDir == "" {
		return fmt.Errorf("%w%s", errBinDirNotSet, configure.ConfigName)
	}

	if includeDir == "" {
		return fmt.Errorf("%w%s", errIncludeDirIsNotSetCheck, configure.ConfigName)
	}

	for _, dir := range []string{binDir, includeDir} {
		_, err := os.Stat(dir)
		if os.IsNotExist(err) && subDirIsWritable(dir) {
			continue
		}

		if !dirIsWritable(dir) {
			return fmt.Errorf("%w%s is not writeable for the current user.\n"+
				"     Please, update rights to the directory or use 'sudo' for "+
				"successful install",
				errTheDirectoryIsNotWriteableForTheCurrentUser, dir)
		}
	}

	return nil
}
