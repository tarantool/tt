package search

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	GitRepoTarantool = "https://github.com/tarantool/tarantool.git"
	GitRepoTT        = "https://github.com/tarantool/tt.git"
)

// isMasked function checks that the given version of tarantool is masked.
func isMasked(version version.Version) bool {
	// Mask all versions below 1.10: deprecated.
	if version.Major == 1 && version.Minor < 10 {
		return true
	}

	// Mask all versions below 1.10.11: static build is not supported.
	if version.Major == 1 && version.Minor == 10 && version.Patch < 11 {
		return true
	}

	// Mask all versions below 2.7: static build is not supported.
	if version.Major == 2 && version.Minor < 7 {
		return true
	}

	// Mask 2.10.1 version: https://github.com/orgs/tarantool/discussions/7646.
	if version.Major == 2 && version.Minor == 10 && version.Patch == 1 {
		return true
	}

	// Mask all 2.X.0 below 2.10.0: technical tags.
	if version.Major == 2 && version.Minor < 10 && version.Patch == 0 {
		return true
	}

	return false
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) (version.VersionSlice, error) {
	versions := version.VersionSlice{}

	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "ls-remote", "--tags", "--refs", repo).Output()
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
			return nil, fmt.Errorf("unexpected Data from %s", repo)
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

	sort.Stable(version.VersionSlice(versions))

	return versions, nil
}

// GetCommitFromGitLocal returns hash or pr/ID info from specified local git repo.
func GetCommitFromGitLocal(repo string, input string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("unable to get commits: `git` command is missing")
	}

	isPullRequest, pullRequestID := util.IsPullRequest(input)

	if isPullRequest {
		commandStr := "pull/" + pullRequestID +
			"/head:" + input
		cmd := exec.Command("git", "fetch", "origin", commandStr)
		cmd.Dir = repo
		err := cmd.Run()
		if err != nil {
			return "", err
		}
	}

	cmd := exec.Command("git", "show", input, "--quiet")
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
func GetCommitFromGitRemote(repo string, input string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("unable to get commits: `git` command is missing")
	}

	tempRepoPath, err := os.MkdirTemp("", "tt_install_repo")
	if err != nil {
		return "", fmt.Errorf("failed to get commits from %q: %w", repo, err)
	}

	defer os.RemoveAll(tempRepoPath)

	cmd := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout",
		"--single-branch", repo, tempRepoPath)

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
		return nil, errors.New("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "-C", repo, "tag").Output()
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
func searchVersionsGit(cliOpts *config.CliOpts, program, repo string) (
	version.VersionSlice, error,
) {
	versions, err := GetVersionsFromGitRemote(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get versions from %s: %w", repo, err)
	}

	return append(versions, version.Version{Str: "master"}), nil
}
