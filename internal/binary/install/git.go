package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// errGitRequired reports git missing from PATH for a lookup in the Git
	// repository of a product.
	errGitRequired = errors.New("'git' is required for 'tt binary install'")
)

const (
	gitExec          = "git"
	gitSubmoduleJobs = "19" // Tarantool submodules count.
)

// requireGit fails when git is not on PATH, saying that lookup, in the Git
// repository of a product, needs it and what to do: install git or, when
// the source of prebuilt builds has missed the version (miss), ask for a
// release that source has. A miss that is nil is a lookup no source of
// prebuilt builds could do: a commit, a pull request, a build from source.
func requireGit(lookup string, miss *prebuiltMiss) error {
	_, err := exec.LookPath(gitExec)
	if err == nil {
		return nil
	}

	if miss == nil {
		return fmt.Errorf("%w to %s: install git", errGitRequired, lookup)
	}

	advice := "install git"
	if miss.suggestion != "" {
		advice += fmt.Sprintf(", or ask for a version %s has, such as %s", miss.source,
			miss.suggestion)
	}

	return fmt.Errorf("%w to %s: %w; %s", errGitRequired, lookup, miss.err, advice)
}

// getVersionsFromRepo returns available versions from the selected product's repository.
func getVersionsFromRepo(installCtx InstallCtx) ([]version.Version, error) {
	programName := installCtx.Program.String()
	if installCtx.LocalRepo != "" {
		return search.GetVersionsFromGitLocal(filepath.Join(installCtx.LocalRepo, programName),
			installCtx.Program)
	}

	return search.GetVersionsFromGitRemote(installCtx.Program.GitRepo())
}

// getCommit resolves a Git revision in the selected product's repository.
func getCommit(installCtx InstallCtx, ref string) (string, error) {
	programName := installCtx.Program.String()
	if installCtx.LocalRepo != "" {
		return search.GetCommitFromGitLocal(
			filepath.Join(installCtx.LocalRepo, programName), ref)
	}

	return search.GetCommitFromGitRemote(installCtx.Program.GitRepo(), ref)
}

// getPullRequestCommit resolves a pull request in the selected product's repository.
func getPullRequestCommit(installCtx InstallCtx, pullRequestID string) (string, error) {
	if installCtx.LocalRepo != "" {
		repoPath := filepath.Join(installCtx.LocalRepo, installCtx.Program.String())

		return search.GetPullRequestCommitFromGitLocal(repoPath, pullRequestID)
	}

	return search.GetPullRequestCommitFromGitRemote(installCtx.Program.GitRepo(), pullRequestID)
}

// gitCloneRepository clones the full repository with its submodules.
func gitCloneRepository(repoLink, dst string, verbose bool, logWriter io.Writer) error {
	args := []string{"clone", "--recursive", repoLink, dst}
	if binary.IsGitFetchJobsSupported() {
		args = append(args, "-j", gitSubmoduleJobs)
	}

	return binary.ExecuteCommand(gitExec, verbose, logWriter, dst, args...)
}

// gitCloneTag clones only the selected tag and its submodules.
func gitCloneTag(repoLink, tag, dst string, verbose bool, logWriter io.Writer) error {
	args := []string{"clone", "-b", tag, "--depth=1", "--recursive", repoLink, dst}
	if binary.IsGitFetchJobsSupported() {
		args = append(args, "-j", gitSubmoduleJobs)
	}

	return binary.ExecuteCommand(gitExec, verbose, logWriter, dst, args...)
}

// downloadGitVersion prepares a resolved revision in a cloned repository.
func downloadGitVersion(repoLink, buildPath string, resolved resolvedInstallVersion,
	verbose bool, logFile *os.File,
) error {
	switch resolved.kind {
	case versionKindTag:
		return gitCloneTag(repoLink, resolved.ref, buildPath, verbose, logFile)
	case versionKindCommit, versionKindMaster:
		return cloneAndCheckout(repoLink, resolved.ref, buildPath, verbose, logFile)
	case versionKindPullRequest:
		return downloadGitPullRequest(repoLink, resolved.ref, buildPath, verbose, logFile)
	default:
		return fmt.Errorf("%w: %d", errUnknownVersionKind, resolved.kind)
	}
}

// cloneAndCheckout clones the repository and prepares a commit or the master branch.
func cloneAndCheckout(repoLink, ref, buildPath string, verbose bool, logWriter io.Writer) error {
	err := gitCloneRepository(repoLink, buildPath, verbose, logWriter)
	if err != nil {
		return err
	}

	return checkoutGitVersion(buildPath, ref, verbose, logWriter)
}

// downloadGitPullRequest clones the repository and prepares the pull request's branch.
func downloadGitPullRequest(repoLink, ref, buildPath string, verbose bool,
	logWriter io.Writer,
) error {
	err := gitCloneRepository(repoLink, buildPath, verbose, logWriter)
	if err != nil {
		return err
	}

	_, pullRequestID := binary.IsPullRequest(ref)

	err = binary.GitFetchPullRequest(buildPath, pullRequestID, verbose, logWriter)
	if err != nil {
		return err
	}

	return checkoutGitVersion(buildPath, ref, verbose, logWriter)
}

// checkoutGitVersion switches to the revision and updates its submodules.
func checkoutGitVersion(repoDir, ref string, verbose bool, logWriter io.Writer) error {
	err := gitCheckout(repoDir, ref, verbose, logWriter)
	if err != nil {
		return err
	}

	return gitUpdateSubmodules(repoDir, verbose, logWriter)
}

// gitCheckout switches to a commit, tag or branch.
func gitCheckout(repoDir, checkout string, verbose bool, logWriter io.Writer) error {
	err := binary.ExecuteCommand(gitExec, verbose, logWriter, repoDir, "checkout", checkout)
	if err != nil {
		return fmt.Errorf("failed to checkout: %w", err)
	}

	return nil
}

// gitUpdateSubmodules initializes and updates the revision's submodules.
func gitUpdateSubmodules(repoDir string, verbose bool, logWriter io.Writer) error {
	err := binary.ExecuteCommand(gitExec, verbose, logWriter, repoDir, "submodule", "update",
		"--init", "--recursive")
	if err != nil {
		return fmt.Errorf("failed to update submodules: %w", err)
	}

	return nil
}
