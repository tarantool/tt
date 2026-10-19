package install

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/binary"
)

var errCouldNotGetInstalledVersionCommitHash = errors.New(
	"could not get commit hash of the versionof an installed ",
)

// isUpdatePossible asks whether to check master and compares its head with the installed commit.
func isUpdatePossible(installCtx InstallCtx, pathToBin string, kind versionKind,
	isBinExecutable bool,
) (bool, error) {
	if !isBinExecutable || kind != versionKindMaster || installCtx.skipMasterUpdate {
		return false, nil
	}

	answer, err := binary.AskConfirm(os.Stdin, "The 'master' version of the "+
		installCtx.Program.String()+" has already been installed.\n"+
		"Would you like to update it if there are newer commits available?")
	if err != nil {
		return false, err
	}

	if !answer {
		return false, nil
	}

	latestCommit, err := getCommit(installCtx, masterVersion)
	if err != nil {
		return false, err
	}

	installedCommit, err := installedCommitHash(installCtx.Program, pathToBin)
	if err != nil {
		return false, err
	}

	return !strings.HasPrefix(latestCommit, installedCommit), nil
}

// installedCommitHash reads the source commit from the selected program's version output.
func installedCommitHash(program binary.Program, binPath string) (string, error) {
	switch program {
	case binary.ProgramCe:
		return installedTarantoolCommit(binPath)
	case binary.ProgramTt:
		return installedTtCommit(binPath)
	default:
		return "", fmt.Errorf("%w%s", binary.ErrUnknownApplication, program)
	}
}

func installedTarantoolCommit(binPath string) (string, error) {
	tarantoolBin := cmdcontext.TarantoolCli{Executable: binPath}

	binVersion, err := tarantoolBin.GetVersion()
	if err != nil {
		return "", err
	}

	if binVersion.Hash == "" {
		return "", fmt.Errorf("%w%s", errCouldNotGetInstalledVersionCommitHash, binary.ProgramCe)
	}

	return strings.TrimPrefix(binVersion.Hash, "g"), nil
}

func installedTtCommit(binPath string) (string, error) {
	binVersion, err := cmdcontext.GetTtVersion(binPath)
	if err != nil {
		return "", err
	}

	return binVersion.Hash, nil
}

func prepareExistingProgram(installCtx *InstallCtx, binPath, incPath string,
	resolved resolvedInstallVersion,
) (bool, error) {
	if installCtx.Reinstall {
		return false, nil
	}

	log.Infof("Checking existing...")

	if !binary.IsRegularFile(binPath) || incPath != "" && !binary.IsDir(incPath) {
		return false, nil
	}

	isBinExecutable, err := binary.IsExecOwner(binPath)
	if err != nil {
		return false, err
	}

	updatePossible, err := isUpdatePossible(*installCtx, binPath, resolved.kind, isBinExecutable)
	if err != nil {
		return false, err
	}

	if updatePossible {
		log.Infof("Found newest commit of %s in master", installCtx.Program)

		installCtx.Reinstall = true

		return false, nil
	}

	log.Infof("%s version of %s already exists.",
		resolved.installName(installCtx.Program), installCtx.Program)

	return true, nil
}
