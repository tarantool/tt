package util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	errEmptyPathIsPassed  = errors.New("empty path is passed")
	errNoGitVersionFound  = errors.New("no git version found")
	errCommitHashTooShort = errors.New("the hash must contain at least 7 characters")
)

// MinCommitHashLength is the Git default for a short SHA.
const MinCommitHashLength = 7

// commitHashPattern matches a full or abbreviated lowercase commit hash.
var commitHashPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// CheckVersionFromGit enters the passed path, tries to get a git version
// it is a git repo, parses and returns a normalized string.
func CheckVersionFromGit(basePath string) (string, error) {
	if basePath == "" {
		return "", errEmptyPathIsPassed
	}

	startPath, _ := os.Getwd()

	defer func() {
		_ = os.Chdir(startPath)
	}()

	err := os.Chdir(basePath)
	if err != nil {
		return "", fmt.Errorf("cannot enter %q: %w", basePath, err)
	}

	cmd := exec.CommandContext(context.Background(), "git", "describe", "--tags", "--long")

	var out bytes.Buffer

	cmd.Stdout = &out

	err = cmd.Run()
	if err != nil {
		return "", errNoGitVersionFound
	}

	version := strings.TrimSpace(out.String())

	return version, nil
}

// IsValidCommitHash checks hash format.
func IsValidCommitHash(hash string) (bool, error) {
	if len(hash) < MinCommitHashLength {
		return false, errCommitHashTooShort
	}

	return commitHashPattern.MatchString(hash), nil
}
