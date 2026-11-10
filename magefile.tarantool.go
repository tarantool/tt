//go:build mage

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// The variables of the tests that run Tarantool.
//
// TT_TEST_TARANTOOL picks the Tarantool CE they run against: a version or a
// series as tt binary install takes it (3.3, 3.6.1), min or latest, which
// ci/tarantool-versions.env sets as CI does, master, or the path of a
// tarantool executable. tt installs a version once, each into a root of its
// own in the test cache, so the versions stay side by side. Unset, the tests
// run against the tarantool in PATH.
//
// TT_TEST_CACHE is the test cache, tt-test in the cache directory of the user
// when it is not set.
const (
	testTarantoolEnv   = "TT_TEST_TARANTOOL"
	testCacheEnv       = "TT_TEST_CACHE"
	tarantoolPrefixEnv = "TT_CLI_TARANTOOL_PREFIX"
	userRootEnv        = "TT_USER_ROOT"
)

var (
	errNotAnExecutable      = errors.New(" is not an executable file")
	errIsNotATarantoolToTry = errors.New(" is not a Tarantool version, a series, min, latest, " +
		"master or the path of an executable")

	// tarantoolVersionRe is what ci/tarantool-version.sh may print: a version,
	// a series or master. It names a directory of the cache.
	tarantoolVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]*$`)
)

// testCacheDir is where the tests keep what they install and download.
func testCacheDir() (string, error) {
	if dir := os.Getenv(testCacheEnv); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("cannot resolve %s=%s: %w", testCacheEnv, dir, err)
		}

		return abs, nil
	}

	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the cache directory of the user: %w", err)
	}

	return filepath.Join(dir, "tt-test"), nil
}

// useTestTarantool puts the Tarantool TT_TEST_TARANTOOL picks first in PATH,
// where the tests look for tarantool, and points TT_CLI_TARANTOOL_PREFIX at
// its headers, as tt binary install --scope project and .env/activate.sh do.
func useTestTarantool() error {
	requested := os.Getenv(testTarantoolEnv)
	if requested == "" {
		return nil
	}

	if strings.ContainsRune(requested, os.PathSeparator) {
		return useTarantoolExecutable(requested)
	}

	version, err := sh.Output("ci/tarantool-version.sh", "ce", requested)
	if err != nil {
		return fmt.Errorf("cannot resolve %s=%s: %w", testTarantoolEnv, requested, err)
	}

	if !tarantoolVersionRe.MatchString(version) {
		return fmt.Errorf("%s=%q%w", testTarantoolEnv, requested, errIsNotATarantoolToTry)
	}

	cache, err := testCacheDir()
	if err != nil {
		return err
	}

	root := filepath.Join(cache, "tarantool", version)
	executable := filepath.Join(root, "bin", "tarantool")

	if !isExecutableFile(executable) {
		err = installTestTarantool(root, version)
		if err != nil {
			return err
		}
	}

	return activateTarantool(executable, root)
}

// installTestTarantool installs Tarantool CE version into root, a user scope
// of its own, with the tt mage build builds.
func installTestTarantool(root, version string) error {
	if !isExecutableFile(ttExecutableName) {
		mg.Deps(Build.Release)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Installing Tarantool %s into %s...\n", version, root)

	err := sh.RunWithV(map[string]string{userRootEnv: root}, ttExecutableName,
		"binary", "install", "--scope", "user", "tarantool", version)
	if err != nil {
		return fmt.Errorf("cannot install Tarantool %s for the tests: %w", version, err)
	}

	return nil
}

// useTarantoolExecutable puts the directory of executable first in PATH. The
// headers are those of the prefix the executable is in, if it has any.
func useTarantoolExecutable(path string) error {
	executable, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve %s=%s: %w", testTarantoolEnv, path, err)
	}

	if !isExecutableFile(executable) {
		return fmt.Errorf("%s=%s: %q%w", testTarantoolEnv, path, executable, errNotAnExecutable)
	}

	prefix := filepath.Dir(filepath.Dir(executable))

	info, err := os.Stat(filepath.Join(prefix, "include", "tarantool"))
	if err != nil || !info.IsDir() {
		prefix = ""
	}

	return activateTarantool(executable, prefix)
}

// activateTarantool makes executable the tarantool of the tests and prefix,
// unless it is empty, the prefix of its headers.
func activateTarantool(executable, prefix string) error {
	dir := filepath.Dir(executable)

	err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err != nil {
		return fmt.Errorf("cannot set PATH: %w", err)
	}

	if prefix != "" {
		err = os.Setenv(tarantoolPrefixEnv, prefix)
		if err != nil {
			return fmt.Errorf("cannot set %s: %w", tarantoolPrefixEnv, err)
		}
	}

	version, err := sh.Output(executable, "--version")
	if err != nil {
		return fmt.Errorf("cannot run %s: %w", executable, err)
	}

	firstLine, _, _ := strings.Cut(version, "\n")

	_, _ = fmt.Fprintf(os.Stdout, "Testing against %s (%s)\n", firstLine, executable)

	return nil
}

// isExecutableFile reports whether path is a regular file someone may run.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
