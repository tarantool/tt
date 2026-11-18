package cargo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// otherArch is the architecture of Linux tt is not running on: arm64 on
// amd64, amd64 otherwise.
func otherArch() string {
	if runtime.GOARCH == "amd64" {
		return "arm64"
	}

	return "amd64"
}

func TestZigTriple(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "aarch64-linux-gnu", zigTriple("aarch64-unknown-linux-gnu"))
	assert.Equal(t, "x86_64-linux-musl", zigTriple("x86_64-unknown-linux-musl"))
}

func TestEnvKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "AARCH64_UNKNOWN_LINUX_GNU", envKey("aarch64-unknown-linux-gnu", true))
	assert.Equal(t, "x86_64_unknown_linux_musl", envKey("x86_64-unknown-linux-musl", false))
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `'/opt/zig/zig'`, shellQuote("/opt/zig/zig"))
	assert.Equal(t, `'/it'\''s/zig'`, shellQuote("/it's/zig"))
}

// TestTriples: the Rust targets a build names are those of its module and
// of its program, each once, and none for a build for the host.
func TestTriples(t *testing.T) {
	t.Parallel()

	host := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	other := build.Target{OS: linuxOS, Arch: otherArch()}
	arch := cargoArches[other.Arch]
	both := cargoBuild{module: true, bin: "agent", static: false}
	static := cargoBuild{module: true, bin: "agent", static: true}

	assert.Empty(t, both.triples(host))
	assert.Equal(t, []string{arch + "-unknown-linux-gnu"}, both.triples(other))
	assert.Equal(t, []string{arch + "-unknown-linux-gnu", arch + "-unknown-linux-musl"},
		static.triples(other))
	assert.Equal(t, []string{arch + "-unknown-linux-musl"},
		cargoBuild{module: false, bin: "agent", static: true}.triples(other))
}

// runScript runs the script at path with args, the zig it runs a script
// that prints its arguments a line each, and returns them.
func runScript(t *testing.T, path string, args ...string) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), path, args...).CombinedOutput()
	require.NoError(t, err, string(out))

	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// TestWriteScripts: the script of cc runs zig cc for the target without the
// arguments zig does not take, the start files of musl only for musl, and
// the script of ar zig ar; a script that is there as it would be written is
// not written again.
func TestWriteScripts(t *testing.T) {
	t.Parallel()

	tools := t.TempDir()
	zig := filepath.Join(tools, "it's zig")
	writeProgram(t, tools, filepath.Base(zig), `printf '%s\n' "$@"`)

	dir := filepath.Join(t.TempDir(), crossDirName)
	args := []string{
		"--target=aarch64-unknown-linux-gnu", "-Wl,--fix-cortex-a53-843419", "-Wl,-O1",
		"/rust/lib/self-contained/crt1.o", "-Wl,--as-needed", "a b.o", "-o", "out",
	}

	compiler, archiver, err := writeScripts(dir, zig, "aarch64-unknown-linux-gnu")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "aarch64-linux-gnu-cc"), compiler)
	assert.Equal(t, filepath.Join(dir, "aarch64-linux-gnu-ar"), archiver)

	assert.Equal(t, []string{
		"cc", "-target", "aarch64-linux-gnu",
		"/rust/lib/self-contained/crt1.o", "-Wl,--as-needed", "a b.o", "-o", "out",
	}, runScript(t, compiler, args...))
	assert.Equal(t, []string{"ar", "crs", "lib.a", "a.o"},
		runScript(t, archiver, "crs", "lib.a", "a.o"))

	musl, _, err := writeScripts(dir, zig, "aarch64-unknown-linux-musl")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"cc", "-target", "aarch64-linux-musl", "-Wl,--as-needed", "a b.o", "-o", "out",
	}, runScript(t, musl, args...))

	// The script is not written again.
	past := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(compiler, past, past))

	_, _, err = writeScripts(dir, zig, "aarch64-unknown-linux-gnu")
	require.NoError(t, err)

	info, err := os.Stat(compiler)
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(past), "the script was written again")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 4, "nothing but the scripts is left in the directory")
}
