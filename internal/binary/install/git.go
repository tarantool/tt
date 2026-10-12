package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

var (
	errDistfilesDirectoryNotFound = errors.New("can't find distfiles directory")
	errHashHasAWrongFormat        = errors.New("hash has a wrong format")
	errUnableToGetHashInfo        = errors.New(": unable to get hash info")
	errUnableToGetPullRequestInfo = errors.New(": unable to get pull-request info")
)

const (
	gitCloneArgsCapacity = 10
	gitExec              = "git"
)

// getVersionsFromRepo returns all available versions from github repository.
func getVersionsFromRepo(local bool, distfiles, program string,
	repoLink string,
) ([]version.Version, error) {
	if local {
		return search.GetVersionsFromGitLocal(filepath.Join(distfiles, program))
	}

	return search.GetVersionsFromGitRemote(repoLink)
}

// getCommit resolves a commit or pull request in the product repository.
func getCommit(local bool, distfiles, programName, ref string) (string, error) {
	program, err := binary.ParseProgram(programName)
	if err != nil {
		return "", err
	}

	isPullRequest, pullRequestID := binary.IsPullRequest(ref)

	switch {
	case isPullRequest && local:
		return search.GetPullRequestCommitFromGitLocal(
			filepath.Join(distfiles, programName), pullRequestID)
	case isPullRequest:
		return search.GetPullRequestCommitFromGitRemote(program.GitRepo(), pullRequestID)
	case local:
		return search.GetCommitFromGitLocal(filepath.Join(distfiles, programName), ref)
	default:
		return search.GetCommitFromGitRemote(program.GitRepo(), ref)
	}
}

// downloadRepo downloads git repository.
func downloadRepo(repoLink, tag, dst string, logFile *os.File, verbose bool) error {
	gitCloneArgs := make([]string, 0, gitCloneArgsCapacity)
	if tag == masterVersion {
		gitCloneArgs = append(gitCloneArgs, "clone", repoLink,
			"--recursive", dst)
	} else {
		gitCloneArgs = append(gitCloneArgs, "clone", "-b", tag, "--depth=1", repoLink,
			"--recursive", dst)
	}

	if binary.IsGitFetchJobsSupported() {
		gitCloneArgs = append(gitCloneArgs, "-j", "19") // 19 - Tarantool submodules count.
	}

	return binary.ExecuteCommand(gitExec, verbose, logFile, dst, gitCloneArgs...)
}

// checkCommit checks the existence of a commit by hash.
func checkCommit(input, programName string, installCtx InstallCtx,
	distfiles string,
) (string, string, error) {
	pullRequestHash := ""
	isPullRequest, pullRequestID := binary.IsPullRequest(input)

	if isPullRequest {
		pullRequestHash, err := getCommit(installCtx.Local, distfiles,
			programName, input)
		if err != nil {
			return input, pullRequestHash,
				fmt.Errorf("%s%w", input, errUnableToGetPullRequestInfo)
		}

		pullRequestHash = pullRequestHash[0:binary.MinCommitHashLength]

		return "pr/" + pullRequestID, pullRequestHash, nil
	}

	isRightFormat, err := binary.IsValidCommitHash(input)
	if err != nil {
		return input, pullRequestHash, err
	}

	if !isRightFormat {
		return input, pullRequestHash, errHashHasAWrongFormat
	}

	returnedHash, err := getCommit(installCtx.Local, distfiles, programName, input)
	if err != nil {
		return input, pullRequestHash, fmt.Errorf("%s%w", input, errUnableToGetHashInfo)
	}

	return returnedHash, pullRequestHash, nil
}

// gitCheckout switches to commit/tag, initializes and updates submodules.
func gitCheckout(repoDir, checkout string, verbose bool, logWriter io.Writer) error {
	err := binary.ExecuteCommand(gitExec, verbose, logWriter, repoDir, "checkout", checkout)
	if err != nil {
		return fmt.Errorf("failed to checkout: %w", err)
	}

	err = binary.ExecuteCommand(gitExec, verbose, logWriter, repoDir, "submodule", "update",
		"--init", "--recursive")
	if err != nil {
		return fmt.Errorf("failed to update submodules: %w", err)
	}

	return nil
}
