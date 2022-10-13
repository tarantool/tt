package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

	return out.String(), nil
}
