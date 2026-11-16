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
	"github.com/tarantool/tt/v3/internal/cmakepolicy"
)

const (
	cmakeExec = "cmake"
	makeExec  = "make"
)

// prepareTarantoolFromSource builds binary and headers in the caller's temporary directory.
func prepareTarantoolFromSource(installCtx InstallCtx, tempDir string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	log.Infof("Installing tarantool=%s", resolved.ref)

	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err := programDependenciesInstalled(binary.ProgramCe)
		if err != nil {
			return programArtifact{}, err
		}
	}

	logFile, err := os.CreateTemp("", "tarantool_install")
	if err != nil {
		return programArtifact{}, fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = logFile.Close()
		_ = os.Remove(logFile.Name())
	}()

	err = downloadTarantoolSource(installCtx, tempDir, resolved, logFile)
	if err != nil {
		_ = printLog(logFile.Name())

		return programArtifact{}, err
	}

	log.Infof("Building tarantool...")

	buildDir, err := buildTarantool(tempDir, installCtx, logFile)
	if err != nil {
		_ = printLog(logFile.Name())

		return programArtifact{}, err
	}

	if resolved.kind == versionKindPullRequest {
		log.Debugf("Binary name is %s", resolved.name)
	}

	prefix := filepath.Join(buildDir, "tarantool-prefix")

	// The install keeps the Lua files of Tarantool in share/tarantool, and
	// the source tree has the license files.
	return programArtifact{
		program:    binary.ProgramCe,
		executable: filepath.Join(prefix, "bin", "tarantool"),
		headers:    filepath.Join(prefix, "include", "tarantool"),
		share:      findShare(prefix),
		licenses:   findLicenses(tempDir),
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
		cmakeOpts = append(cmakeOpts, "-DENABLE_DIST=ON")
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

	// The make configures build trees too: those of the projects the
	// static build adds, Tarantool among them.
	environ := tarantoolBuildEnviron()
	cmakeOpts := prepareCmakeOpts(buildPath, installCtx)

	err = binary.ExecuteCommandEnv(cmakeExec, environ, installCtx.Verbose, logFile, buildPath,
		cmakeOpts...)
	if err != nil {
		return "", err
	}

	makeOpts := prepareMakeOpts(installCtx)

	return buildPath, binary.ExecuteCommandEnv(makeExec, environ, installCtx.Verbose, logFile,
		buildPath, makeOpts...)
}

// tarantoolBuildEnviron is the environment of the cmake and make that build
// Tarantool from source: the environment of tt, with the policy version CMake
// 4 needs to configure a commit of 2024 or older, unless the user has set one
// (cmakepolicy).
func tarantoolBuildEnviron() []string {
	return cmakepolicy.Environ(os.Environ())
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
