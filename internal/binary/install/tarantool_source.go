package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	cmakeExec = "cmake"
	makeExec  = "make"
)

// prepareCmakeOpts prepares cmake command line options for tarantool building.
func prepareCmakeOpts(buildPath string,
	installCtx InstallCtx,
) []string {
	cmakeOpts := []string{".."}

	cmakeOpts = append(cmakeOpts, `-DCMAKE_TARANTOOL_ARGS=-DCMAKE_BUILD_TYPE=RelWithDebInfo;`+
		`-DENABLE_WERROR=OFF;-DENABLE_BACKTRACE=ON`)

	if installCtx.Dynamic {
		cmakeOpts = append(cmakeOpts, "-DCMAKE_INSTALL_PREFIX="+filepath.Join(buildPath,
			"tarantool-prefix"))
	} else {
		cmakeOpts = append(cmakeOpts, "-DCMAKE_INSTALL_PREFIX="+buildPath)
	}

	return cmakeOpts
}

// prepareMakeOpts prepares make command line options for tarantool building.
func prepareMakeOpts(installCtx InstallCtx) []string {
	makeOpts := []string{}
	if installCtx.Dynamic {
		makeOpts = append(makeOpts, "install")
	}

	if _, isMakeFlagsSet := os.LookupEnv("MAKEFLAGS"); !isMakeFlagsSet {
		maxThreads := strconv.Itoa(runtime.NumCPU())

		makeOpts = append(makeOpts, "-j", maxThreads)
	}

	return makeOpts
}

// buildTarantool builds tarantool from source. Returns a path, where build artifacts are placed.
func buildTarantool(srcPath string,
	installCtx InstallCtx, logFile *os.File,
) (string, error) {
	buildPath := filepath.Join(srcPath, "/static-build/build")
	if installCtx.Dynamic {
		buildPath = filepath.Join(srcPath, "/dynamic-build")
	}

	err := os.MkdirAll(buildPath, defaultDirPermissions)
	if err != nil {
		return "", fmt.Errorf("failed to create build directory: %w", err)
	}

	cmakeOpts := prepareCmakeOpts(buildPath, installCtx)

	err = binary.ExecuteCommand(cmakeExec, installCtx.Verbose, logFile, buildPath, cmakeOpts...)
	if err != nil {
		return "", err
	}

	makeOpts := prepareMakeOpts(installCtx)

	return buildPath, binary.ExecuteCommand(makeExec, installCtx.Verbose, logFile, buildPath,
		makeOpts...)
}

// copyLocalTarantool finds and copies local tarantool folder to tmp folder.
func copyLocalTarantool(distfiles, path, tarVersion string,
	installCtx InstallCtx, logFile *os.File,
) error {
	var err error

	if binary.IsDir(filepath.Join(distfiles, "tarantool")) {
		log.Infof("Local files found, installing from them...")

		localPath, _ := binary.JoinAbspath(distfiles, "tarantool")

		err = copy.Copy(localPath, path)
		if err != nil {
			return fmt.Errorf("failed to copy local tarantool sources: %w", err)
		}

		err = gitCheckout(path, tarVersion, installCtx.Verbose, logFile)
	} else {
		return errDistfilesDirectoryNotFound
	}

	return err
}
