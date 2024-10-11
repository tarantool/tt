//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const distPath = "dist"

const packageName = "tt"

type Distro struct {
	OS   string
	Dist string
}

var targetDistros = []Distro{
	{OS: "el", Dist: "7"},

	{OS: "fedora", Dist: "34"},
	{OS: "fedora", Dist: "35"},
	{OS: "fedora", Dist: "36"},

	{OS: "ubuntu", Dist: "xenial"}, // 16.04
	{OS: "ubuntu", Dist: "bionic"}, // 18.04
	{OS: "ubuntu", Dist: "focal"},  // 20.04
	{OS: "ubuntu", Dist: "jammy"},  // 22.04
	{OS: "ubuntu", Dist: "noble"},  // 24.04

	{OS: "debian", Dist: "stretch"},  // 9
	{OS: "debian", Dist: "buster"},   // 10
	{OS: "debian", Dist: "bullseye"}, // 11

	{OS: "linux-deb", Dist: "static"},
	{OS: "linux-rpm", Dist: "static"},
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
			if matched, err := filepath.Match(pattern, filepath.Base(path)); err != nil {
				return err
			} else if matched {
				matches = append(matches, path)
				return nil
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return matches, nil
}

// getPatterns returns patterns to select goreleaser build artifacts.
func getPatterns(distro Distro) ([]string, error) {

	if distro.OS == "el" || distro.OS == "fedora" || distro.OS == "linux-rpm" {
		return []string{"*.rpm"}, nil
	}

	if distro.OS == "ubuntu" || distro.OS == "debian" || distro.OS == "linux-deb"{
		return []string{"*.deb", "*.dsc"}, nil
	}

	return nil, fmt.Errorf("Unknown OS: %s", distro.OS)
}

// PublishRWS puts packages to RWS (Repository Web Service).
func PublishRWS() error {
	fmt.Printf("Publish packages to RWS...\n")

	for _, targetDistro := range targetDistros {
		fmt.Printf("Publish package for %s/%s...\n", targetDistro.OS, targetDistro.Dist)

		patterns, perr := getPatterns(targetDistro)
		if perr != nil {
			return fmt.Errorf("Failed to publish package for %s/%s: %s",
				targetDistro.OS, targetDistro.Dist, perr)
		}

		files, ferr := walkMatch(distPath, patterns)
		if ferr != nil {
			return fmt.Errorf("Failed to publish package for %s/%s: %s",
				targetDistro.OS, targetDistro.Dist, ferr)
		}

		rwsUrlPart := os.Getenv("RWS_URL_PART")
		if rwsUrlPart == "" {
			return fmt.Errorf("Failed to publish package: RWS_URL_PART is not set")
		}

		flags := []string{
			"-v",
			"-LfsS",
			"-X", "PUT", fmt.Sprintf("%s/%s/%s", rwsUrlPart, targetDistro.OS, targetDistro.Dist),
			"-F", fmt.Sprintf("product=%s", packageName),
		}

		for _, file := range files {
			flags = append(flags, "-F", fmt.Sprintf("%s=@./%s", filepath.Base(file), file))
		}

		fmt.Printf("curl flags (excluding secrets): %s\n", flags)

		rwsAuth := os.Getenv("RWS_AUTH")
		if rwsAuth == "" {
			return fmt.Errorf("Failed to publish package: RWS_AUTH is not set")
		}
		flags = append(flags, "-u", rwsAuth)

		cmd := exec.Command("curl", flags...)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed to publish package for %s/%s: %s, %s",
				targetDistro.OS, targetDistro.Dist, err, output)
		}
	}

	return nil
}
