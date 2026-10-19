package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// prepareTtFromSource builds the executable in the caller's temporary directory.
func prepareTtFromSource(installCtx InstallCtx, tempDir string,
	resolved resolvedInstallVersion,
) (programArtifact, error) {
	log.Infof("Installing tt=%s", resolved.ref)

	if !installCtx.Force {
		log.Infof("Checking dependencies...")

		err := programDependenciesInstalled(binary.ProgramTt)
		if err != nil {
			return programArtifact{}, err
		}
	}

	logFile, err := os.CreateTemp("", "tt_install")
	if err != nil {
		return programArtifact{}, fmt.Errorf("failed to create log file: %w", err)
	}

	defer func() {
		_ = logFile.Close()
		_ = os.Remove(logFile.Name())
	}()

	err = downloadTtSource(installCtx, tempDir, resolved, logFile)
	if err != nil {
		_ = printLog(logFile.Name())

		return programArtifact{}, err
	}

	log.Infof("Building tt...")

	err = binary.ExecuteCommand(mageExec, installCtx.Verbose, logFile, tempDir, "build")
	if err != nil {
		_ = printLog(logFile.Name())

		return programArtifact{}, err
	}

	if resolved.kind == versionKindPullRequest {
		log.Debugf("Binary name is %s", resolved.name)
	}

	return programArtifact{
		program:    binary.ProgramTt,
		executable: filepath.Join(tempDir, binary.ProgramTt.Exec()), headers: "",
	}, nil
}

// downloadTtSource prepares the selected tt sources in the temporary build directory.
func downloadTtSource(installCtx InstallCtx, buildPath string,
	resolved resolvedInstallVersion, logFile *os.File,
) error {
	if installCtx.LocalRepo != "" {
		localRepoPath := filepath.Join(installCtx.LocalRepo, installCtx.Program.String())

		return copyLocalTtSource(localRepoPath, buildPath, resolved.ref,
			installCtx.Verbose, logFile)
	}

	log.Infof("Downloading tt...")

	return downloadGitVersion(installCtx.Program.GitRepo(), buildPath, resolved,
		installCtx.Verbose, logFile)
}

// copyLocalTtSource copies the local repository and checks out the requested revision.
func copyLocalTtSource(localPath, buildPath, ref string, verbose bool,
	logFile *os.File,
) error {
	if !binary.IsDir(localPath) {
		return errDistfilesDirectoryNotFound
	}

	log.Infof("Local files found, installing from them...")

	err := copy.Copy(localPath, buildPath)
	if err != nil {
		return fmt.Errorf("failed to copy local tt sources: %w", err)
	}

	return checkoutGitVersion(buildPath, ref, verbose, logFile)
}
