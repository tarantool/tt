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

// prepareTarantoolFromSource builds binary and headers in the caller's temporary directory.
func prepareTarantoolFromSource(installCtx InstallCtx, tempDir string,
	resolved resolvedInstallVersion,
) (tarantoolArtifacts, error) {
	log.Infof("Installing tarantool=%s", resolved.ref)

	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err := programDependenciesInstalled(binary.ProgramCe)
		if err != nil {
			return tarantoolArtifacts{}, err
		}
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return tarantoolArtifacts{}, fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = logFile.Close()
		_ = os.Remove(logFile.Name())
	}()

	err = downloadTarantoolSource(installCtx, tempDir, resolved, logFile)
	if err != nil {
		_ = printLog(logFile.Name())

		return tarantoolArtifacts{}, err
	}

	log.Infof("Building tarantool...")

	buildDir, err := buildTarantool(tempDir, installCtx, logFile)
	if err != nil {
		_ = printLog(logFile.Name())

		return tarantoolArtifacts{}, err
	}

	if resolved.kind == versionKindPullRequest {
		log.Debugf("Binary name is %s", resolved.name)
	}

	return tarantoolArtifacts{
		executable: filepath.Join(buildDir, "tarantool-prefix", "bin", "tarantool"),
		headers:    filepath.Join(buildDir, "tarantool-prefix", "include", "tarantool"),
	}, nil
}

func downloadTarantoolSource(installCtx InstallCtx, destination string,
	resolved resolvedInstallVersion, logFile *os.File,
) error {
	if installCtx.LocalRepo != "" {
		log.Infof("Checking local files...")

		localRepoPath := filepath.Join(installCtx.LocalRepo, installCtx.Program.String())

		return copyLocalTarantool(localRepoPath, destination, resolved.ref,
			installCtx.Verbose, logFile)
	}

	log.Infof("Downloading tarantool...")

	return downloadGitVersion(installCtx.Program.GitRepo(), destination, resolved,
		installCtx.Verbose, logFile)
}

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
func copyLocalTarantool(localPath, buildPath, ref string,
	verbose bool, logFile *os.File,
) error {
	if !binary.IsDir(localPath) {
		return errDistfilesDirectoryNotFound
	}

	log.Infof("Local files found, installing from them...")

	err := copy.Copy(localPath, buildPath)
	if err != nil {
		return fmt.Errorf("failed to copy local tarantool sources: %w", err)
	}

	return checkoutGitVersion(buildPath, ref, verbose, logFile)
}
