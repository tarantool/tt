package install

import (
	"fmt"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/binary"
)

// InstallCtx contains information for program installation.
type InstallCtx struct {
	binary.BinaryContext

	// Reinstall is a flag. If it is set,
	// target application will be reinstalled if already exists.
	Reinstall bool
	// Force is a flag which disables dependency check if enabled.
	Force bool
	// KeepTemp is a flag. If it is set,
	// install will don't remove tmp files.
	KeepTemp bool
	// LocalRepo is the source directory: --local-repo for Git repositories,
	// or --local-dir for SDK/EE/TCM archives. An empty path selects remote installation.
	LocalRepo string
	// CredentialsFile contains customer-zone credentials for remote EE, TCM, and SDK downloads.
	CredentialsFile string
	// BuildInDocker is set if tarantool must be built in docker container.
	BuildInDocker bool
	// Version of the program to install. Specified by user.
	requestedVersion string
	// Dynamic flag enables dynamic linking.
	Dynamic bool
	// FromBuild is an existing Tarantool build directory to link into the selected scope.
	FromBuild string
	// IncDir is the source header directory for --from-build.
	IncDir string
	// Install development build.
	DevBuild bool
	// skipMasterUpdate is set if user doesn't want to check for latest master
	// version and update for it if master version already exists. It inherits
	// the --no-prompt flag from global context.
	skipMasterUpdate bool
}

func FillCtx(cmdCtx *cmdcontext.CmdCtx, installCtx *InstallCtx, args []string) error {
	var err error

	installCtx.Program, err = binary.ParseProgram(cmdCtx.CommandName)
	if err != nil {
		return fmt.Errorf("failed create context: %w", err)
	}

	installCtx.Verbose = installCtx.Verbose || cmdCtx.Cli.Verbose
	installCtx.skipMasterUpdate = cmdCtx.Cli.NoPrompt

	if len(args) == 1 {
		installCtx.requestedVersion = args[0]
	}

	return nil
}
