//go:build integration

package cc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// minTarantoolMajor is the lowest Tarantool major version tt v3 supports. The
// c / lua-c integration tests compile against its C module headers, so an
// older Tarantool is skipped rather than exercised.
const minTarantoolMajor = 3

// requireHostHeaders resolves the host build prerequisites of the c / lua-c
// integration tests - a C compiler, a supported Tarantool binary and its C
// module headers - and returns the directory of the headers, the include
// directory of the toolchain of the build. When a prerequisite is missing it
// skips the calling test with the reason, so the suite stays green on a host
// without the native toolchain while still exercising it wherever it is.
func requireHostHeaders(t *testing.T) string {
	t.Helper()

	// The compiler the core takes: $CC, else cc.
	ccName := os.Getenv("CC")
	if ccName == "" {
		ccName = "cc"
	}

	compiler, err := exec.LookPath(ccName)
	if err != nil {
		t.Skipf("C compiler %q not found in PATH: %v", ccName, err)
	}

	tarantoolBin, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skipf("tarantool not found in PATH: %v", err)
	}

	version, major, err := tarantoolVersion(tarantoolBin)
	if err != nil {
		t.Skipf("cannot determine tarantool version from %q: %v", tarantoolBin, err)
	}

	if major < minTarantoolMajor {
		t.Skipf("tarantool %s is too old: need >= %d.0", version, minTarantoolMajor)
	}

	includeDir := tarantoolIncludeDir(tarantoolBin)
	if includeDir == "" {
		t.Skipf("tarantool C module headers (module.h) not found for %q", tarantoolBin)
	}

	t.Logf("host toolchain: cc=%s tarantool=%s (%s) include=%s",
		compiler, tarantoolBin, version, includeDir)

	return includeDir
}

// tarantoolVersion runs tarantool --version and returns the version, the
// last word of its first line, as tt reads it, and its major number.
func tarantoolVersion(tarantoolBin string) (string, int, error) {
	out, err := exec.Command(tarantoolBin, "--version").Output() //nolint:noctx // A test probe.
	if err != nil {
		return "", 0, err
	}

	first, _, _ := strings.Cut(string(out), "\n")
	words := strings.Fields(first)

	if len(words) == 0 {
		return "", 0, strconv.ErrSyntax
	}

	version := words[len(words)-1]
	majorText, _, _ := strings.Cut(version, ".")

	major, err := strconv.Atoi(majorText)
	if err != nil {
		return version, 0, err
	}

	return version, major, nil
}

// tarantoolIncludeDir locates the directory holding Tarantool's module.h for
// the given tarantool binary, or "" when none is found. It derives the
// install prefix from the binary path - both as found on PATH and with
// symlinks resolved, to cover layouts like Homebrew's bin -> Cellar - and
// probes <prefix>/include/tarantool, as tt does.
func tarantoolIncludeDir(tarantoolBin string) string {
	seen := make(map[string]struct{})

	for _, bin := range candidateBinPaths(tarantoolBin) {
		prefix := filepath.Dir(filepath.Dir(bin))
		if _, dup := seen[prefix]; dup {
			continue
		}

		seen[prefix] = struct{}{}

		includeDir := filepath.Join(prefix, "include", "tarantool")

		_, err := os.Stat(filepath.Join(includeDir, "module.h"))
		if err == nil {
			return includeDir
		}
	}

	return ""
}

// candidateBinPaths returns the binary path as given, plus its
// symlink-resolved form when that differs. The as-given path is tried first
// on purpose: Homebrew's headers live under the symlink prefix
// (/opt/homebrew), not the resolved Cellar path bin/tarantool points into.
func candidateBinPaths(bin string) []string {
	paths := []string{bin}

	resolved, err := filepath.EvalSymlinks(bin)
	if err == nil && resolved != bin {
		paths = append(paths, resolved)
	}

	return paths
}
