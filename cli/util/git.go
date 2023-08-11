package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"

	"github.com/hashicorp/go-version"
)

// MinCommitHashLength is the Git default for a short SHA.
const MinCommitHashLength = 7

// CheckVersionFromGit enters the passed path, tries to get a git version
// it is a git repo, parses and returns a normalized string.
func CheckVersionFromGit(basePath string) (string, error) {
	if basePath == "" {
		return "", fmt.Errorf("empty path is passed")
	}
	startPath, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(startPath)
	}()
	err := os.Chdir(basePath)
	if err != nil {
		return "", err
	}

	cmd := exec.Command("git", "describe", "--tags", "--long")
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("no git version found")
	}

	version := strings.TrimSpace(out.String())
	return version, nil
}

// isGitFetchJobsSupported checks if fetchJobs option (-j) is supported by the git version
// passed using gitOutput input parameter.
func isGitFetchJobsSupported(gitOutput string) bool {
	versionStr := strings.TrimFunc(gitOutput, func(r rune) bool {
		return !unicode.IsDigit(r)
	})
	gitVersion, err := version.NewVersion(versionStr)
	if err != nil {
		return false
	}
	fetchJobsStartGitVersion, err := version.NewVersion("2.8")
	if err != nil {
		return false
	}
	return gitVersion.GreaterThanOrEqual(fetchJobsStartGitVersion)
}

// IsGitFetchJobsSupported checks if fetchJobs option (-j) is supported by current git version.
func IsGitFetchJobsSupported() bool {
	cmd := exec.Command("git", "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false
	}
	return isGitFetchJobsSupported(out.String())
}

// IsValidCommitHash checks hash format.
func IsValidCommitHash(hash string) (bool, error) {
	if len(hash) < MinCommitHashLength {
		return false, fmt.Errorf("the hash must contain at least 7 characters")
	}
	return regexp.MatchString(`^[0-9a-f]+$`, hash)
}

// IsPullRequest returns is this pull-request format and pr num.
func IsPullRequest(input string) (bool, string) {
	input = strings.ToLower(input)
	if !strings.HasPrefix(input, "pr/") {
		return false, ""
	}
	_, prNum, _ := strings.Cut(input, "/")
	isPullRequest, _ := regexp.MatchString(`^[0-9]+$`, prNum)
	return isPullRequest, prNum
}
