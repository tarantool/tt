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
	// Local is a flag. If it is set,
	// install will do local installation.
	Local bool
	// localRepoDir contains product source repositories and local installation archives.
	// It is initialized from repo.distfiles when installation starts.
	localRepoDir string
	// Destination is the directory to save downloaded SDK bundles.
	Destination string
	// BuildInDocker is set if tarantool must be built in docker container.
	BuildInDocker bool
	// Version of the program to install. Specified by user.
	requestedVersion string
	// Dynamic flag enables dynamic linking.
	Dynamic bool
	// FromBuild is an existing Tarantool build directory to link into the environment.
	FromBuild string
	// IncDir is the directory, where the Tarantool headers are located for a local build.
	// Or the directory, where the headers should be installed from a bundle.
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
