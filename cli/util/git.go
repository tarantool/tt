package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
)

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

	res, err := NormalizeGitVersion(out.String())
	if err != nil {
		return "", err
	}

	return res, nil
}

// NormalizeGitVersion returns a version string filtered by regular expressions.
func NormalizeGitVersion(versionStr string) (string, error) {
	expr1 := "\\d+\\.\\d+\\.\\d+"
	expr2 := "\\-\\d+\\-"

	regex1, err := regexp.Compile(expr1)
	if err != nil {
		return "", fmt.Errorf("regexp compile failed")
	}
	regex2, err := regexp.Compile(expr2)
	if err != nil {
		return "", fmt.Errorf("regexp compile failed")
	}

	majMinPatch := regex1.FindString(versionStr)
	count := regex2.FindString(versionStr)
	res := majMinPatch + "." + count[1:len(count)-1]

	return res, nil
}
