package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	// errGitRequired reports git missing from PATH for a lookup in a Git
	// repository that nothing else can do.
	errGitRequired = errors.New("'git' is required for 'tt binary search'")

	errUnableToGetCommitsGitCommandIsMissing = errors.New(
		"unable to get commits: `git` command is missing",
	)
	errUnexpectedDataFrom = errors.New("unexpected Data from ")
)

const gitExec = "git"

// isMasked reports whether a version of program is no longer offered for
// installation, as binary.IsMasked does for archives.
func isMasked(program binary.Program, ver version.Version) bool {
	return binary.IsMasked(program, ver)
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	_, err := exec.LookPath(gitExec)
	if err != nil {
		return nil, fmt.Errorf("%w to list the tags of %s: install git", errGitRequired, repo)
	}

	output, err := exec.CommandContext(
		context.Background(), "git", "ls-remote", "--tags", "--refs", repo).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %w", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		_, ver, found := strings.CutLast(line, "/")
		if !found {
			return nil, fmt.Errorf("%w%s", errUnexpectedDataFrom, repo)
		}

		version, err := version.Parse(ver)
		if err != nil {
			continue
		}

		if repo == binary.ProgramCe.GitRepo() && isMasked(binary.ProgramCe, version) {
			continue
		}

		versions = append(versions, version)
	}

	sort.Stable(versions)

	return versions, nil
}

// GetCommitFromGitLocal reads the revision reported by git show without fetching.
// For an annotated tag object, git show reports the tag name before the commit.
//
// A branch the repository has only as the remote-tracking branch of one
// remote is read from there, as a clone has every branch of its remote but
// the one it checked out so: git checkout, which prepares a build from the
// repository, takes that branch for the name too (see checkoutGitVersion of
// the install).
func GetCommitFromGitLocal(repo, ref string) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	output, err := gitShow(repo, ref)
	if err != nil {
		tracking, found := remoteTrackingBranch(repo, ref)
		if !found {
			return "", fmt.Errorf("failed to find commit %s: %w", ref, err)
		}

		output, err = gitShow(repo, tracking)
		if err != nil {
			return "", fmt.Errorf("failed to find commit %s: %w", ref, err)
		}
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	hash := strings.Split(lines[0], " ")[1]

	return hash, nil
}

// gitShow returns what git show reports of ref in repo, without the diff.
func gitShow(repo, ref string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "show", ref, "--quiet")

	cmd.Dir = repo

	output, err := cmd.Output()

	return string(output), err
}

// remoteTrackingBranch returns refs/remotes/<remote>/<branch> of the only remote
// of repo that has branch, as git checkout of a branch the repository does not
// have finds one: a name that no remote or several have finds none.
func remoteTrackingBranch(repo, branch string) (string, bool) {
	cmd := exec.CommandContext(context.Background(), "git", "remote")

	cmd.Dir = repo

	output, err := cmd.Output()
	if err != nil {
		return "", false
	}

	var found []string

	for remote := range strings.FieldsSeq(string(output)) {
		ref := "refs/remotes/" + remote + "/" + branch

		cmd := exec.CommandContext(context.Background(), "git", "show-ref", "--verify",
			"--quiet", ref)

		cmd.Dir = repo

		if cmd.Run() == nil {
			found = append(found, ref)
		}
	}

	if len(found) != 1 {
		return "", false
	}

	return found[0], true
}

// GetPullRequestCommitFromGitLocal fetches the pull request and resolves its head commit.
func GetPullRequestCommitFromGitLocal(repo, pullRequestID string) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	err = binary.GitFetchPullRequest(repo, pullRequestID, false, nil)
	if err != nil {
		return "", err
	}

	return GetCommitFromGitLocal(repo, "pr/"+pullRequestID)
}

// GetCommitFromGitRemote clones the remote repository and resolves a commit, tag or branch.
func GetCommitFromGitRemote(repo, ref string) (string, error) {
	tempRepoPath, err := cloneGitRepoForLookup(repo)
	if err != nil {
		return "", err
	}

	defer func() {
		_ = os.RemoveAll(tempRepoPath)
	}()

	return GetCommitFromGitLocal(tempRepoPath, ref)
}

// GetPullRequestCommitFromGitRemote clones the repository and fetches the pull request's head.
func GetPullRequestCommitFromGitRemote(repo, pullRequestID string) (string, error) {
	tempRepoPath, err := cloneGitRepoForLookup(repo)
	if err != nil {
		return "", err
	}

	defer func() {
		_ = os.RemoveAll(tempRepoPath)
	}()

	return GetPullRequestCommitFromGitLocal(tempRepoPath, pullRequestID)
}

// cloneGitRepoForLookup clones the remote bare and without the contents of the
// files. Every branch of the remote is a branch of a bare clone, where a clone
// with a work tree has only the default branch of the remote, so that any
// branch resolves by its name: master of tarantool/tt, whose default branch
// is not master, too.
func cloneGitRepoForLookup(repo string) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	tempRepoPath, err := os.MkdirTemp("", "tt_install_repo")
	if err != nil {
		return "", fmt.Errorf("failed to get commits from %q: %w", repo, err)
	}

	binary.RemoveOnInterrupt(tempRepoPath)

	cmd := exec.CommandContext(context.Background(), "git", "clone", "--bare",
		"--filter=blob:none", repo, tempRepoPath)

	err = cmd.Run()
	if err != nil {
		_ = os.RemoveAll(tempRepoPath)

		return "", fmt.Errorf("unable to get commits: git clone failed: %w", err)
	}

	return tempRepoPath, nil
}

// GetVersionsFromGitLocal returns sorted versions list from specified local git repo.
func GetVersionsFromGitLocal(repo string, program binary.Program) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	_, err := exec.LookPath(gitExec)
	if err != nil {
		return nil, fmt.Errorf("%w to read the tags of %s: install git", errGitRequired, repo)
	}

	output, err := exec.CommandContext(context.Background(), "git", "-C", repo, "tag").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %w", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		version, err := version.Parse(line)
		if err != nil {
			continue
		}

		if isMasked(program, version) {
			continue
		}

		versions = append(versions, version)
	}

	sort.Stable(versions)

	return versions, nil
}

// searchVersionsRepo lists the versions of program, which has a Git
// repository: its tags and master with git, the releases the source of its
// prebuilt builds has for the host without it (searchVersionsPrebuilt).
func searchVersionsRepo(program binary.Program) (version.VersionSlice, error) {
	_, err := exec.LookPath(gitExec)
	if err != nil {
		return searchVersionsPrebuilt(context.Background(), program)
	}

	return searchVersionsGit(program.GitRepo())
}

// searchVersionsGit handles searching versions from a remote Git repository.
func searchVersionsGit(repo string) (
	version.VersionSlice, error,
) {
	versions, err := GetVersionsFromGitRemote(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %w", repo, err)
	}

	return append(versions, newMasterVersion()), nil
}

// masterVersion returns the version that stands for the master branch.
func newMasterVersion() version.Version {
	var master version.Version

	master.Str = "master"

	return master
}
