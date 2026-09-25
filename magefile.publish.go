//go:build mage

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var (
	errFailedToPublishPackageRWSAuthIsNotSet = errors.New(
		"failed to publish package: RWS_AUTH is not set",
	)
	errFailedToPublishPackageRWSURLPartIsNotSet = errors.New(
		"failed to publish package: RWS_URL_PART is not set",
	)
	errUnknownOS = errors.New("unknown OS: ")
)

const distPath = "dist"

const packageName = "tt"

type Distro struct {
	OS   string
	Dist string
}

// The operating systems packages are published for.
const (
	osEL       = "el"
	osFedora   = "fedora"
	osUbuntu   = "ubuntu"
	osDebian   = "debian"
	osLinuxDeb = "linux-deb"
	osLinuxRPM = "linux-rpm"
)

var targetDistros = []Distro{
	{OS: osEL, Dist: "7"},
	{OS: osEL, Dist: "8"},

	{OS: osFedora, Dist: "34"},
	{OS: osFedora, Dist: "35"},
	{OS: osFedora, Dist: "36"},

	{OS: osUbuntu, Dist: "xenial"}, // 16.04
	{OS: osUbuntu, Dist: "bionic"}, // 18.04
	{OS: osUbuntu, Dist: "focal"},  // 20.04
	{OS: osUbuntu, Dist: "jammy"},  // 22.04
	{OS: osUbuntu, Dist: "noble"},  // 24.04

	{OS: osDebian, Dist: "stretch"},  // 9
	{OS: osDebian, Dist: "buster"},   // 10
	{OS: osDebian, Dist: "bullseye"}, // 11
	{OS: osDebian, Dist: "bookworm"}, // 12

	{OS: osLinuxDeb, Dist: "static"},
	{OS: osLinuxRPM, Dist: "static"},
}

// walkMatch walks through directory and collects file paths satisfying patterns.
func walkMatch(root string, patterns []string) ([]string, error) {
	var matches []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		for _, pattern := range patterns {
			matched, err := filepath.Match(pattern, filepath.Base(path))
			if err != nil {
				return fmt.Errorf("failed to match pattern %q: %w", pattern, err)
			}

			if matched {
				matches = append(matches, path)
				return nil
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk %q: %w", root, err)
	}

	return matches, nil
}

// getPatterns returns patterns to select go releaser build artifacts.
func getPatterns(distro Distro) ([]string, error) {
	if distro.OS == osEL || distro.OS == osFedora || distro.OS == osLinuxRPM {
		return []string{"*.rpm"}, nil
	}

	if distro.OS == osUbuntu || distro.OS == osDebian || distro.OS == osLinuxDeb {
		return []string{"*.deb", "*.dsc"}, nil
	}

	return nil, fmt.Errorf("%w%s", errUnknownOS, distro.OS)
}

// PublishRWS puts packages to RWS (Repository Web Service).
func PublishRWS() error {
	_, _ = fmt.Fprintf(os.Stdout, "Publish packages to RWS...\n")

	for _, targetDistro := range targetDistros {
		_, _ = fmt.Fprintf(os.Stdout, "Publish package for %s/%s...\n",
			targetDistro.OS, targetDistro.Dist)

		patterns, err := getPatterns(targetDistro)
		if err != nil {
			return fmt.Errorf("failed to publish package for %s/%s: %w",
				targetDistro.OS, targetDistro.Dist, err)
		}

		files, err := walkMatch(distPath, patterns)
		if err != nil {
			return fmt.Errorf("failed to publish package for %s/%s: %w",
				targetDistro.OS, targetDistro.Dist, err)
		}

		rwsURLPart := os.Getenv("RWS_URL_PART")
		if rwsURLPart == "" {
			return errFailedToPublishPackageRWSURLPartIsNotSet
		}

		flags := []string{
			"-v",
			"-LfsS",
			"-X", "PUT", fmt.Sprintf("%s/%s/%s", rwsURLPart, targetDistro.OS, targetDistro.Dist),
			"-F", "product=" + packageName,
		}

		for _, file := range files {
			flags = append(flags, "-F", fmt.Sprintf("%s=@./%s", filepath.Base(file), file))
		}

		_, _ = fmt.Fprintf(os.Stdout, "curl flags (excluding secrets): %s\n", flags)

		rwsAuth := os.Getenv("RWS_AUTH")
		if rwsAuth == "" {
			return errFailedToPublishPackageRWSAuthIsNotSet
		}

		flags = append(flags, "-u", rwsAuth)

		//nolint:gosec // G702: curl runs without a shell, on arguments from the CI environment.
		cmd := exec.CommandContext(context.Background(), "curl", flags...)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to publish package for %s/%s: %w, %s",
				targetDistro.OS, targetDistro.Dist, err, output)
		}
	}

	return nil
}
