package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
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

	var err error

	switch {
	case installCtx.Program == binary.ProgramTt:
		err = installTt(installCtx)
	case installCtx.Program == binary.ProgramCe:
		err = installTarantool(installCtx)
	case installCtx.Program.BundleSource() != binary.ProgramUnknown:
		err = installBundleProgram(installCtx)
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, installCtx.Program)
	}

	if err != nil {
		return err
	}

	err = prepareProjectEnvironment(installCtx.Scope)
	if err != nil {
		return err
	}

	if installCtx.Scope == binary.ScopeProject {
		_, _ = fmt.Fprintf(os.Stdout, "\nTo activate the installed binaries, run:\n\n  %s\n\n",
			color.GreenString("source .env/activate.sh"))
	}

	return nil
}
