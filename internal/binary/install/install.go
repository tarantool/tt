package install

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/v3/cli/config"
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
func Install(installCtx InstallCtx, cliOpts *config.CliOpts) error {
	if cliOpts != nil && cliOpts.Repo != nil {
		installCtx.localRepoDir = cliOpts.Repo.Install
	}

	switch installCtx.Program {
	case binary.ProgramTt:
		return installTt(installCtx)
	case binary.ProgramCe:
		return installTarantool(installCtx, cliOpts)
	case binary.ProgramEe, binary.ProgramTcm:
		return installBundleProgram(installCtx, cliOpts)
	case binary.ProgramSDK:
		return DownloadSDK(installCtx, cliOpts)
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, installCtx.Program)
	}
}
