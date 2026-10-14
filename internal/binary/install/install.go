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

	switch {
	case installCtx.Program == binary.ProgramTt:
		return installTt(installCtx)
	case installCtx.Program == binary.ProgramCe:
		return installTarantool(installCtx)
	case installCtx.Program.BundleSource() != binary.ProgramUnknown:
		return installBundleProgram(installCtx)
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, installCtx.Program)
	}
}
