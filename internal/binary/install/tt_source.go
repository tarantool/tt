package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// downloadTtSource prepares the selected tt sources in the temporary build directory.
func downloadTtSource(installCtx InstallCtx, buildPath string,
	resolved resolvedInstallVersion, logFile *os.File,
) error {
	if installCtx.Local {
		localRepoPath := filepath.Join(installCtx.localRepoDir, installCtx.Program.String())

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
