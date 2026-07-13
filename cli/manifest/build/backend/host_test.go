//go:build integration

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/version"
)

// minTarantoolMajor is the lowest Tarantool major version tt v3 supports. The
// cc / lua-c integration tests compile against its C module headers, so an
// older Tarantool is skipped rather than exercised.
const minTarantoolMajor = 3

// hostToolchain is the set of native build prerequisites the cc / lua-c
// integration tests need, resolved and validated by requireHostToolchain.
type hostToolchain struct {
	// CC is the resolved C compiler (honors $CC, else "cc").
	CC string
	// Tarantool is the resolved tarantool binary.
	Tarantool string
	// Version is the parsed tarantool version.
	Version version.Version
	// IncludeDir is the directory holding module.h (the C module API header) —
	// the -I passed to cc.
	IncludeDir string
}

// requireHostToolchain resolves and validates the host build prerequisites for
// the cc / lua-c integration tests: a C compiler, a supported Tarantool binary,
// and its C module headers. When any prerequisite is missing it SKIPS the
// calling test with a specific reason (it never fails), so the integration suite
// stays green on a host without the native toolchain while still exercising it
// wherever it is present. The returned toolchain carries the real Tarantool
// include dir, so callers compile against the actual headers, not a stub.
func requireHostToolchain(t *testing.T) hostToolchain {
	t.Helper()

	// Resolve the compiler the same way DeriveFlags does: $CC, else "cc".
	ccName := os.Getenv("CC")
	if ccName == "" {
		ccName = "cc"
	}

	cc, err := exec.LookPath(ccName)
	if err != nil {
		t.Skipf("C compiler %q not found in PATH: %v", ccName, err)
	}

	tarantoolBin, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skipf("tarantool not found in PATH: %v", err)
	}

	// Reuse tt's own version parsing (tarantool --version, last token of line 1).
	tntCli := cmdcontext.TarantoolCli{Executable: tarantoolBin}

	ver, err := tntCli.GetVersion()
	if err != nil {
		t.Skipf("cannot determine tarantool version from %q: %v", tarantoolBin, err)
	}

	if ver.Major < minTarantoolMajor {
		t.Skipf("tarantool %s is too old: need >= %d.0", ver.Str, minTarantoolMajor)
	}

	includeDir := tarantoolIncludeDir(tarantoolBin)
	if includeDir == "" {
		t.Skipf("tarantool C module headers (module.h) not found for %q", tarantoolBin)
	}

	t.Logf("host toolchain: cc=%s tarantool=%s (%s) include=%s",
		cc, tarantoolBin, ver.Str, includeDir)

	return hostToolchain{
		CC:         cc,
		Tarantool:  tarantoolBin,
		Version:    ver,
		IncludeDir: includeDir,
	}
}

// tarantoolIncludeDir locates the directory holding Tarantool's module.h for the
// given tarantool binary, or "" when none is found. It derives the install
// prefix from the binary path — both as found on PATH and with symlinks
// resolved, to cover layouts like Homebrew's bin -> Cellar — and probes
// <prefix>/include/tarantool, matching tt's TarantoolInfo.IncludeDir.
func tarantoolIncludeDir(tarantoolBin string) string {
	seen := make(map[string]struct{})

	for _, bin := range candidateBinPaths(tarantoolBin) {
		prefix := filepath.Dir(filepath.Dir(bin))
		if _, dup := seen[prefix]; dup {
			continue
		}

		seen[prefix] = struct{}{}

		includeDir := filepath.Join(prefix, "include", "tarantool")
		if _, err := os.Stat(filepath.Join(includeDir, "module.h")); err == nil {
			return includeDir
		}
	}

	return ""
}

// candidateBinPaths returns the binary path as given, plus its symlink-resolved
// form when that differs, so prefix derivation works for both direct installs
// and symlinked layouts. The as-given path is tried first on purpose: Homebrew's
// headers live under the symlink prefix (/opt/homebrew), not the resolved Cellar
// path bin/tarantool points into.
func candidateBinPaths(bin string) []string {
	paths := []string{bin}

	if resolved, err := filepath.EvalSymlinks(bin); err == nil && resolved != bin {
		paths = append(paths, resolved)
	}

	return paths
}
