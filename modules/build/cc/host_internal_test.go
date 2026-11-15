//go:build integration

package cc

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// minTarantoolMajor is the lowest Tarantool major version tt v3 supports. The
// c / lua-c integration tests compile against its C module headers, so an
// older Tarantool is skipped rather than exercised.
const minTarantoolMajor = 3

// prefixEnv names the prefix of a Tarantool, the directory with its headers
// in include/tarantool, as tt takes it.
const prefixEnv = "TT_CLI_TARANTOOL_PREFIX"

// requireHostHeaders resolves the host build prerequisites of the c / lua-c
// integration tests - a C compiler, a supported Tarantool binary and its C
// module headers - and returns the directory of the headers, the include
// directory of the toolchain of the build, and the Tarantool. When a
// prerequisite is missing it skips the calling test with the reason, so the
// suite stays green on a host without the native toolchain while still
// exercising it wherever it is.
//
// The headers are those of TT_CLI_TARANTOOL_PREFIX when it is set, in
// <prefix>/include/tarantool, as tt takes them, and the Tarantool is the one
// in PATH, else <prefix>/bin/tarantool. Without it the Tarantool is the one
// in PATH and the headers are under its prefix.
func requireHostHeaders(t *testing.T) (string, string) {
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

	prefix := os.Getenv(prefixEnv)

	tarantoolBin, err := exec.LookPath("tarantool")
	if err != nil && prefix != "" {
		tarantoolBin, err = exec.LookPath(filepath.Join(prefix, "bin", "tarantool"))
	}

	if err != nil {
		t.Skipf("tarantool not found in PATH, nor in <%s>/bin (%s=%q): %v",
			prefixEnv, prefixEnv, prefix, err)
	}

	version, major, err := tarantoolVersion(tarantoolBin)
	if err != nil {
		t.Skipf("cannot determine tarantool version from %q: %v", tarantoolBin, err)
	}

	if major < minTarantoolMajor {
		t.Skipf("tarantool %s is too old: need >= %d.0", version, minTarantoolMajor)
	}

	includeDir, probed := tarantoolIncludeDir(tarantoolBin, prefix)
	if includeDir == "" {
		t.Skipf("no Tarantool C module headers: no module.h in %s; set %s to a "+
			"prefix with include/tarantool", strings.Join(probed, ", "), prefixEnv)
	}

	t.Logf("host toolchain: cc=%s tarantool=%s (%s) include=%s",
		compiler, tarantoolBin, version, includeDir)

	return includeDir, tarantoolBin
}

// tarantoolVersion runs tarantool --version and returns the version, the
// last word of its first line, as tt reads it, and its major number.
func tarantoolVersion(tarantoolBin string) (string, int, error) {
	// A test probe of the Tarantool the developer runs the tests with.
	out, err := exec.Command(tarantoolBin, "--version").Output() //nolint:noctx,gosec // See above.
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

// tarantoolIncludeDir locates the directory holding Tarantool's module.h -
// <prefix>/include/tarantool, as tt probes it - and returns it, or "", with
// the directories it probed. The prefix is envPrefix when it is set, the
// value of TT_CLI_TARANTOOL_PREFIX; otherwise it is derived from the path of
// the tarantool binary - both as found on PATH and with symlinks resolved,
// to cover layouts like Homebrew's bin -> Cellar.
func tarantoolIncludeDir(tarantoolBin, envPrefix string) (string, []string) {
	var prefixes []string

	if envPrefix != "" {
		prefixes = []string{envPrefix}
	} else {
		for _, bin := range candidateBinPaths(tarantoolBin) {
			prefixes = append(prefixes, filepath.Dir(filepath.Dir(bin)))
		}
	}

	var probed []string

	for _, prefix := range prefixes {
		includeDir := filepath.Join(prefix, "include", "tarantool")
		if slices.Contains(probed, includeDir) {
			continue
		}

		probed = append(probed, includeDir)

		_, err := os.Stat(filepath.Join(includeDir, "module.h"))
		if err == nil {
			return includeDir, probed
		}
	}

	return "", probed
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
