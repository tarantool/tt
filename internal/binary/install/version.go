package install

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

var (
	errCouldNotGetInstalledVersionCommitHash = errors.New(
		"could not get commit hash of the versionof an installed ",
	)
	errNoVersionFound        = errors.New("no version found")
	errNoVersionsWereFetched = errors.New("no versions were fetched")
)

const (
	// masterVersion is the version that stands for the master branch.
	masterVersion = "master"
)

type resolvedInstallVersion struct {
	version         string
	versionStr      string
	versionFound    bool
	isPullRequest   bool
	pullRequestID   string
	pullRequestHash string
}

// newResolvedInstallVersion returns the requested version, not resolved yet.
func newResolvedInstallVersion(requested string) resolvedInstallVersion {
	return resolvedInstallVersion{
		version:         requested,
		versionStr:      "",
		versionFound:    false,
		isPullRequest:   false,
		pullRequestID:   "",
		pullRequestHash: "",
	}
}

func matchRequestedInstallVersion(result resolvedInstallVersion, installCtx InstallCtx,
	distfiles, programName, repo string,
) (resolvedInstallVersion, error) {
	if !version.IsVersion(result.version, false) {
		return result, nil
	}

	log.Infof("Searching in versions...")

	versions, err := getVersionsFromRepo(installCtx.Local, distfiles, programName, repo)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	match, err := version.MatchVersion(result.version, versions)
	if err != nil {
		_, isNotFound := errors.AsType[version.NotFoundError](err)
		if !isNotFound {
			return resolvedInstallVersion{}, err
		}

		return result, nil
	}

	result.versionFound = true
	result.version = match

	return result, nil
}

func resolveRequestedInstallVersion(result resolvedInstallVersion, installCtx InstallCtx,
	distfiles, programName, repo string,
) (resolvedInstallVersion, error) {
	if result.version == masterVersion {
		return result, nil
	}

	result, err := matchRequestedInstallVersion(
		result, installCtx, distfiles, programName, repo)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	if result.versionFound {
		return result, nil
	}

	result.isPullRequest, result.pullRequestID = binary.IsPullRequest(result.version)
	if result.isPullRequest {
		log.Infof("Searching in pull-requests...")
	} else {
		log.Infof("Searching in commits...")
	}

	result.version, result.pullRequestHash, err = checkCommit(
		result.version, programName, installCtx, distfiles)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	return result, nil
}

func resolveTtInstallVersion(installCtx InstallCtx, distfiles string) (
	resolvedInstallVersion,
	error,
) {
	result := newResolvedInstallVersion(installCtx.version)

	// Get latest version if it was not specified.
	if result.version == "" {
		log.Infof("Getting latest tt version...")

		versions, err := getVersionsFromRepo(installCtx.Local, distfiles, "tt", search.GitRepoTT)
		if err != nil {
			return resolvedInstallVersion{}, err
		}

		if len(versions) == 0 {
			return resolvedInstallVersion{}, errNoVersionsWereFetched
		}

		result.version = versions[len(versions)-1].Str
	}

	// The tag format in tt is vX.Y.Z, but the user can use X.Y.Z.
	var err error

	result, err = resolveRequestedInstallVersion(
		result, installCtx, distfiles, "tt", search.GitRepoTT)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	switch {
	case result.versionFound:
		result.versionStr = binary.ProgramTt.Exec() + version.FsSeparator + result.version
	case result.isPullRequest:
		result.versionStr = binary.ProgramTt.Exec() + version.FsSeparator + result.pullRequestHash
	default:
		result.versionStr = binary.ProgramTt.Exec() + version.FsSeparator +
			result.version[0:binary.Min(len(result.version), binary.MinCommitHashLength)]
	}

	return result, nil
}

func resolveTarantoolInstallVersion(installCtx InstallCtx, distfiles string) (
	resolvedInstallVersion,
	error,
) {
	result := newResolvedInstallVersion(installCtx.version)

	// Get latest release if it was not specified.
	if result.version == "" {
		log.Infof("Getting latest tarantool version...")

		versions, err := getVersionsFromRepo(
			installCtx.Local, distfiles, "tarantool", search.GitRepoTarantool)
		if err != nil {
			return resolvedInstallVersion{}, err
		}

		result.version = getLatestRelease(versions)
		if result.version == "" {
			return resolvedInstallVersion{}, errNoVersionFound
		}
	}

	var err error

	result, err = resolveRequestedInstallVersion(
		result, installCtx, distfiles, "tarantool", search.GitRepoTarantool)
	if err != nil {
		return resolvedInstallVersion{}, err
	}

	switch {
	case result.versionFound:
		result.versionStr = binary.ProgramCe.String() + version.FsSeparator + result.version
	case result.isPullRequest:
		result.versionStr = binary.ProgramCe.String() + version.FsSeparator + result.pullRequestHash
	default:
		result.versionStr = binary.ProgramCe.String() + version.FsSeparator +
			result.version[0:binary.Min(len(result.version), binary.MinCommitHashLength)]
	}

	return result, nil
}

func getLatestRelease(versions []version.Version) string {
	latestRelease := ""

	for _, candidate := range slices.Backward(versions) {
		if candidate.Release.Type == version.TypeRelease {
			latestRelease = candidate.Str
			break
		}
	}

	return latestRelease
}

// isUpdatePossible func checks if Tt or Tarantool are 'master' version
// and if so, asks user about checking for update to latest commit.
// Function returns boolean variable if update of master is possible or not.
func isUpdatePossible(installCtx InstallCtx,
	pathToBin string,
	program binary.Program,
	progVer,
	distfiles string,
	isBinExecutable bool,
) (bool, error) {
	// We need to make sure that we check newest commits only for
	// production 'master' branch. Also we want to ask if user wants
	// to check for updates.
	if !isBinExecutable || progVer != masterVersion || installCtx.skipMasterUpdate {
		return false, nil
	}

	answer, err := binary.AskConfirm(os.Stdin, "The 'master' version of the "+
		program.String()+" has already been installed.\n"+
		"Would you like to update it if there are newer commits available?")
	if err != nil {
		return false, err
	}

	if !answer {
		return false, nil
	}

	lastCommitHash, err := getCommit(installCtx.Local, distfiles, program.String(), progVer)
	if err != nil {
		return false, err
	}

	var curBinHash string

	switch program {
	case binary.ProgramCe:
		tarantoolBin := cmdcontext.TarantoolCli{
			Executable: pathToBin,
		}

		binVersion, err := tarantoolBin.GetVersion()
		if err != nil {
			return false, err
		}

		// We need to trim first rune to get commit hash
		// from string structure 'g<commitHash>'.
		if len(binVersion.Hash) < 1 {
			return false, fmt.Errorf("%w%s", errCouldNotGetInstalledVersionCommitHash, program)
		}

		curBinHash = binVersion.Hash[1:]
	case binary.ProgramTt:
		ttVer, err := cmdcontext.GetTtVersion(pathToBin)
		if err != nil {
			return false, err
		}

		curBinHash = ttVer.Hash
	case binary.ProgramUnknown, binary.ProgramEe, binary.ProgramTcm, binary.ProgramSDK:
		// Current callers only pass Tarantool CE or tt.
	}

	if strings.HasPrefix(lastCommitHash, curBinHash) {
		return false, nil
	}

	return true, nil
}
