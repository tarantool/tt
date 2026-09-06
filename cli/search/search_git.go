package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

var (
	errGitRequired = errors.New(
		"'git' is required for 'tt search' to work",
	)
	errUnableToGetCommitsGitCommandIsMissing = errors.New(
		"unable to get commits: `git` command is missing",
	)
	errUnexpectedDataFrom = errors.New("unexpected Data from ")
)

const (
	GitRepoTarantool         = "https://github.com/tarantool/tarantool.git"
	GitRepoTT                = "https://github.com/tarantool/tt.git"
	minSupportedMajorVersion = 3
)

// isMasked function checks that the given version of tarantool is masked.
// Tarantool 1.x and 2.x are no longer supported.
func isMasked(version version.Version) bool {
	return version.Major < minSupportedMajorVersion
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	if _, err := exec.LookPath("git"); err != nil {
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
		slashIdx := strings.LastIndex(line, "/")
		if slashIdx == -1 {
			return nil, fmt.Errorf("%w%s", errUnexpectedDataFrom, repo)
		} else {
			slashIdx += 1
		}
		ver := line[slashIdx:]
		version, err := version.Parse(ver)
		if err != nil {
			continue
		}
		if isMasked(version) && repo == GitRepoTarantool {
			continue
		}
		versions = append(versions, version)
	}

	sort.Stable(versions)

	return versions, nil
}

// GetCommitFromGitLocal returns hash or pr/ID info from specified local git repo.
func GetCommitFromGitLocal(repo, input string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	isPullRequest, pullRequestID := util.IsPullRequest(input)

	if isPullRequest {
		commandStr := "pull/" + pullRequestID +
			"/head:" + input
		cmd := exec.CommandContext(context.Background(), "git", "fetch", "origin", commandStr)
		cmd.Dir = repo
		err := cmd.Run()
		if err != nil {
			return "", err
		}
	}

	cmd := exec.CommandContext(context.Background(), "git", "show", input, "--quiet")
	cmd.Dir = repo

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	hash := strings.Split(lines[0], " ")[1]

	return hash, nil
}

// GetCommitFromGitRemote returns hash or pr/ID info from specified remote git repo.
func GetCommitFromGitRemote(repo, input string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errUnableToGetCommitsGitCommandIsMissing
	}

	tempRepoPath, err := os.MkdirTemp("", "tt_install_repo")
	if err != nil {
		return "", fmt.Errorf("failed to get commits from %q: %w", repo, err)
	}

	defer func() {
		_ = os.RemoveAll(tempRepoPath)
	}()

	cmd := exec.CommandContext(context.Background(), "git", "clone",
		"--filter=blob:none", "--no-checkout", repo, tempRepoPath)

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("unable to get commits: git clone failed: %w", err)
	}

	return GetCommitFromGitLocal(tempRepoPath, input)
}

// GetVersionsFromGitLocal returns sorted versions list from specified local git repo.
func GetVersionsFromGitLocal(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	if _, err := exec.LookPath("git"); err != nil {
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

	return append(versions, version.Version{Str: "master"}), nil
}
