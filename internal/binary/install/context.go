package install

import (
	"errors"
	"fmt"
	"strings"

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
	// or --local-dir for SDK/EE/TCM archives. An empty path selects the source
	// of this build of tt with the archives of the product.
	LocalRepo string
	// BuildInDocker is set if tarantool must be built in docker container.
	BuildInDocker bool
	// SourceBuild always compiles Community releases instead of using prebuilt packages.
	SourceBuild bool
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
	// Confirm asks the user to confirm what question asks: whether to build
	// on an OS whose dependencies tt cannot check, and whether to update an
	// installed master. It keeps the rules of the confirmations of tt, --yes
	// and --no-prompt among them, and fails when it cannot ask. nil asks
	// nothing and confirms nothing.
	Confirm func(question string) (bool, error)
}

// errNoConfirmation is the error of a confirmation with no Confirm to ask.
var errNoConfirmation = errors.New("no confirmation can be asked")

// confirm asks installCtx.Confirm to confirm what question asks.
func (installCtx InstallCtx) confirm(question string) (bool, error) {
	if installCtx.Confirm == nil {
		return false, errNoConfirmation
	}

	return installCtx.Confirm(question)
}

func FillCtx(cmdCtx *cmdcontext.CmdCtx, installCtx *InstallCtx, args []string) error {
	requested := ""
	if len(args) == 1 {
		requested = args[0]
	}

	request, err := binary.ParseVersionRequest(cmdCtx.CommandName, requested)
	if err != nil {
		return fmt.Errorf("failed create context: %w", err)
	}

	installCtx.Program = request.Program
	installCtx.requestedVersion = request.Version

	if request.Program == binary.ProgramSDK {
		installCtx.requestedVersion = strings.TrimPrefix(installCtx.requestedVersion,
			binary.SDKVersionPrefix)
	}

	if request.Program == binary.ProgramEe || request.Program == binary.ProgramSDK ||
		request.Program == binary.ProgramTcm {
		if source, dev := strings.CutPrefix(installCtx.requestedVersion,
			binary.DevVersionPrefix); dev {
			installCtx.requestedVersion = source
			installCtx.DevBuild = true
		}
	}

	installCtx.Verbose = installCtx.Verbose || cmdCtx.Cli.Verbose

	return nil
}
