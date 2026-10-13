package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errInvalidDirectory                     = errors.New("directory ")
	errTarantoolBinaryWasNotFoundInThePaths = errors.New(
		"tarantool binary was not found in the paths",
	)
)

// searchTarantoolHeaders searches tarantool headers.
// First, it checks the specified includeDir,
// in case of failure, it checks the default one.
func searchTarantoolHeaders(buildDir, includeDir string) (string, error) {
	var err error

	if includeDir != "" {
		includeDir, err = filepath.Abs(includeDir)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path: %w", err)
		}

		if !binary.IsDir(includeDir) {
			return "", fmt.Errorf("%w%v doesn't exist, or isn't a directory",
				errInvalidDirectory, includeDir)
		}

		return includeDir, nil
	}

	// Check the default path.
	defaultIncPath := filepath.Join(buildDir, "tarantool-prefix", "include", "tarantool")
	if binary.IsDir(defaultIncPath) {
		return defaultIncPath, nil
	}

	return "", nil
}

// installTarantoolFromBuild links Tarantool from a local build directory.
func installTarantoolFromBuild(installCtx InstallCtx, artifactRoot string) error {
	ttBinDir := filepath.Join(artifactRoot, binary.BinDirName)
	ttIncludeDir := filepath.Join(artifactRoot, binary.IncDirName)
	includeDir := installCtx.IncDir

	// Validate build directory.
	buildDir, err := filepath.Abs(installCtx.FromBuild)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	if !binary.IsDir(buildDir) {
		return fmt.Errorf("%w%v doesn't exist, or isn't directory",
			errInvalidDirectory, buildDir)
	}

	checkedBinaryPaths := make([]string, 0)

	// Searching for tarantool binary.
	for _, binaryRelPath := range [...]string{
		"src/tarantool",
		"tarantool/src/tarantool",
		"tarantool-prefix/bin/tarantool",
	} {
		binaryPath := filepath.Join(buildDir, binaryRelPath)

		var isExecOwner bool

		isExecOwner, err = binary.IsExecOwner(binaryPath)

		if err != nil || !isExecOwner || binary.IsDir(binaryPath) {
			checkedBinaryPaths = append(checkedBinaryPaths, binaryPath)
			continue
		}

		includeDir, err = searchTarantoolHeaders(buildDir, includeDir)
		if err != nil {
			return err
		}

		// Create the scope's directories only after validating the build and headers.
		err = binary.CreateDirectory(ttBinDir, defaultDirPermissions)
		if err != nil {
			return err
		}

		err = binary.CreateDirectory(ttIncludeDir, defaultDirPermissions)
		if err != nil {
			return err
		}

		log.Infof("Changing symlinks...")

		err = binary.CreateSymlink(binaryPath, filepath.Join(ttBinDir, "tarantool"), true)
		if err != nil {
			return err
		}

		tarantoolIncludeSymlink := filepath.Join(ttIncludeDir, "tarantool")
		// Remove old symlink to the tarantool headers.
		// RemoveAll is used to perform deletion even if the file is not a symlink.
		err = os.RemoveAll(tarantoolIncludeSymlink)
		if err != nil {
			return fmt.Errorf("failed to remove the headers symlink: %w", err)
		}

		if includeDir == "" {
			log.Warn("Tarantool headers location was not specified. " +
				"`tt package build`, `tt rocks` may not work properly.\n" +
				"  To specify include files location use --include-dir option.")
		} else {
			err = binary.CreateSymlink(includeDir, tarantoolIncludeSymlink, true)
			if err != nil {
				return err
			}

			log.Infof("tarantool headers directory set as %v.", includeDir)
		}

		log.Infof("Done.")

		return nil
	}

	return fmt.Errorf("%w:\n%s",
		errTarantoolBinaryWasNotFoundInThePaths, strings.Join(checkedBinaryPaths, "\n"))
}
