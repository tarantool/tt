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
	errGitRequired = errors.New(
		"'git' is required for 'tt binary search' to work",
	)
	errUnableToGetCommitsGitCommandIsMissing = errors.New(
		"unable to get commits: `git` command is missing",
	)
	errUnexpectedDataFrom = errors.New("unexpected Data from ")
)

const minSupportedMajorVersion = 3

// isMasked function checks that the given version of tarantool is masked.
// Tarantool 1.x and 2.x are no longer supported.
func isMasked(version version.Version) bool {
	return version.Major < minSupportedMajorVersion
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	_, err := exec.LookPath("git")
	if err != nil {
		return nil, errGitRequired
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

		if isMasked(version) && repo == binary.ProgramCe.GitRepo() {
			continue
		}

		versions = append(versions, version)
	}

	sort.Stable(versions)

	return versions, nil
}

// GetCommitFromGitLocal reads the revision reported by git show without fetching.
// For an annotated tag object, git show reports the tag name before the commit.
func GetCommitFromGitLocal(repo, ref string) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	cmd := exec.CommandContext(context.Background(), "git", "show", ref, "--quiet")

	cmd.Dir = repo

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to find commit %s: %w", ref, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	hash := strings.Split(lines[0], " ")[1]

	return hash, nil
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

// cloneGitRepoForLookup clones the remote without checking out source files.
func cloneGitRepoForLookup(repo string) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	tempRepoPath, err := os.MkdirTemp("", "tt_install_repo")
	if err != nil {
		return "", fmt.Errorf("failed to get commits from %q: %w", repo, err)
	}

	cmd := exec.CommandContext(context.Background(), "git", "clone",
		"--filter=blob:none", "--no-checkout", repo, tempRepoPath)

	err = cmd.Run()
	if err != nil {
		_ = os.RemoveAll(tempRepoPath)

		return "", fmt.Errorf("unable to get commits: git clone failed: %w", err)
	}

	return tempRepoPath, nil
}

// GetVersionsFromGitLocal returns sorted versions list from specified local git repo.
func GetVersionsFromGitLocal(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	_, err := exec.LookPath("git")
	if err != nil {
		return nil, errGitRequired
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

		if isMasked(version) && strings.Contains(repo, "tarantool") {
			continue
		}

		versions = append(versions, version)
	}

	sort.Stable(versions)

	return versions, nil
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
