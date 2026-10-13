package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/internal/binary"
)

// printLog prints logfile to stdout.
func printLog(logName string) error {
	logs, err := os.ReadFile(logName)
	if err != nil {
		return fmt.Errorf("failed to read log: %w", err)
	}

	_, _ = os.Stdout.Write(logs)

	return nil
}

// Install installs program.
func Install(installCtx InstallCtx) error {
	if installCtx.LocalRepo != "" {
		localRepo, err := filepath.Abs(installCtx.LocalRepo)
		if err != nil {
			return fmt.Errorf("failed to resolve local repository path: %w", err)
		}

		installCtx.LocalRepo = localRepo
	}

	switch installCtx.Program {
	case binary.ProgramTt:
		return installTt(installCtx)
	case binary.ProgramCe:
		return installTarantool(installCtx)
	case binary.ProgramEe, binary.ProgramTcm:
		return installBundleProgram(installCtx)
	case binary.ProgramSDK:
		return DownloadSDK(installCtx)
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, installCtx.Program)
	}
}
