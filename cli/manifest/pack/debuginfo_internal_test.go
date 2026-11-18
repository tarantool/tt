package pack

import (
	"bytes"
	"context"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// lookPathOf is a lookPath that finds the programs of found, at /bin/<name>.
func lookPathOf(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(found, name) {
			return "/bin/" + name, nil
		}

		return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
	}
}

// TestStripCommand pins the tool that strips the debug information: strip
// -S on darwin, strip --strip-debug elsewhere, else objcopy, and an error
// naming strip when there is none.
func TestStripCommand(t *testing.T) {
	t.Parallel()

	command, err := stripCommand("linux", lookPathOf("strip", "objcopy"))
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/strip", "--strip-debug", "-o", "out", "in"},
		command("in", "out"))

	command, err = stripCommand("linux", lookPathOf("objcopy"))
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/objcopy", "--strip-debug", "in", "out"}, command("in", "out"))

	command, err = stripCommand("darwin", lookPathOf("strip", "objcopy"))
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/strip", "-S", "-o", "out", "in"}, command("in", "out"))

	for _, goos := range []string{"linux", "darwin"} {
		_, err = stripCommand(goos, lookPathOf())
		require.ErrorIs(t, err, errNoStrip, goos)
		require.ErrorIs(t, err, exec.ErrNotFound, goos)
		assert.Contains(t, err.Error(), "--debuginfo strip needs strip", goos)
	}

	_, err = stripCommand("darwin", lookPathOf("objcopy"))
	require.ErrorIs(t, err, errNoStrip, "objcopy does not strip Mach-O")
}

// TestStripCommandOf pins the tool that strips the files of a build: that of
// the machine for the machine, and llvm-strip for another platform, whose
// files the strip of the machine may not read, and an error naming it when
// there is none.
func TestStripCommandOf(t *testing.T) {
	t.Parallel()

	host, err := stripCommand(runtime.GOOS, lookPathOf("strip"))
	require.NoError(t, err)

	for _, target := range []native.Platform{native.Host(), {OS: "", Arch: ""}} {
		command, err := stripCommandOf(target, lookPathOf("strip", "llvm-strip"))
		require.NoError(t, err)
		assert.Equal(t, host("in", "out"), command("in", "out"))
	}

	other := native.Platform{OS: "linux", Arch: "arm64"}
	if other == native.Host() {
		other.Arch = "amd64"
	}

	command, err := stripCommandOf(other, lookPathOf("strip", "llvm-strip"))
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/llvm-strip", "--strip-debug", "-o", "out", "in"},
		command("in", "out"))

	_, err = stripCommandOf(other, lookPathOf("strip", "objcopy"))
	require.ErrorIs(t, err, errNoStrip)
	require.ErrorIs(t, err, exec.ErrNotFound)
	assert.Contains(t, err.Error(), "--debuginfo strip needs strip: a pack for "+
		other.String()+", another platform than the machine, strips with llvm-strip")
}

// TestCheckDebugInfo pins the values of --debuginfo.
func TestCheckDebugInfo(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", DebugInfoKeep} {
		require.NoError(t, checkDebugInfo(Options{DebugInfo: mode}), mode)
	}

	err := checkDebugInfo(Options{DebugInfo: "split"})
	require.ErrorIs(t, err, errDebugInfo)
	assert.Contains(t, err.Error(), `unknown --debuginfo "split": want keep or strip`)
}

// debugProgram compiles a program with debug information into dir and
// returns it, skipping the test where there is no cc, no strip, or the
// program is not ELF.
func debugProgram(t *testing.T, dir string) string {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("the debug information is checked in ELF")
	}

	for _, tool := range []string{"cc", "strip"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found in PATH: %v", tool, err)
		}
	}

	source := filepath.Join(dir, "helper.c")
	program := filepath.Join(dir, "helper")

	require.NoError(t, os.WriteFile(source, []byte("int main(void) { return 0; }\n"), 0o600))

	printed, err := exec.CommandContext(t.Context(), "cc", "-g", "-o", program, source).
		CombinedOutput()
	require.NoError(t, err, string(printed))
	require.True(t, hasDebugInfo(t, program), "cc -g writes the debug information")

	return program
}

// hasDebugInfo reports whether the ELF file path has a .debug_info section.
func hasDebugInfo(t *testing.T, path string) bool {
	t.Helper()

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	return file.Section(".debug_info") != nil
}

// hasDebugInfoData is hasDebugInfo of the content of a file.
func hasDebugInfoData(t *testing.T, data string) bool {
	t.Helper()

	file, err := elf.NewFile(bytes.NewReader([]byte(data)))
	require.NoError(t, err)

	return file.Section(".debug_info") != nil
}

// TestPackStripsDebugInfo checks that --debuginfo strip packs the program
// the package built without its debug information, leaves the one of the
// project's .rocks as it is, and that keep, the default, packs it as built.
func TestPackStripsDebugInfo(t *testing.T) {
	t.Parallel()

	helper := debugProgram(t, t.TempDir())

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName: fmt.Sprintf(executableManifest, helper),
		versionFileName:  "1.0.0\n",
		"init.lua":       "return 1\n",
		"helper/README":  "the helper",
	})

	pack := func(mode string) *Result {
		result, err := Run(context.Background(), Options{
			ProjectDir:  src,
			WithoutDeps: true,
			DebugInfo:   mode,
			OutputDir:   filepath.Join(src, "_out", mode),
			Build: build.Options{
				TtVersion: "tt test",
				Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
			},
		})
		require.NoError(t, err, mode)

		return result
	}

	stripped := readArchive(t, pack(DebugInfoStrip).Path)
	require.Contains(t, stripped, ".rocks/bin/helper")
	assert.False(t, hasDebugInfoData(t, stripped[".rocks/bin/helper"]), "stripped")
	assert.True(t, hasDebugInfo(t, filepath.Join(src, ".rocks", "bin", "helper")),
		"the program of the project is as it was built")

	kept := readArchive(t, pack("").Path)
	assert.True(t, hasDebugInfoData(t, kept[".rocks/bin/helper"]), "kept by default")

	// The rock is packed from the stripped copy of the stage, and rocks.lua
	// is left out of it by its path in the stage, which keeps it.
	opts := packOptions(src, filepath.Join(src, "_out", "rock"), FormatRock)

	opts.DebugInfo = DebugInfoStrip

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	rock := zipEntries(t, result.Rock)
	assert.False(t, hasDebugInfoData(t, rock["bin/helper"]), "stripped in the rock")
	assert.Contains(t, rock, "lua/exec-app/version.lua")
	assert.NotContains(t, rock, "lua/exec-app/rocks.lua")
	assert.FileExists(t,
		filepath.Join(src, buildDirName, "rock", "default", "lua", "exec-app", "rocks.lua"),
		"the stage keeps it")
}

// TestStrippedStage checks that the stage of a rock is stripped in a copy,
// the native files of the copy without their debug information, and the
// stage as it was.
func TestStrippedStage(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	program := debugProgram(t, stage)

	require.NoError(t, os.MkdirAll(filepath.Join(stage, "lua"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(stage, "lua", "init.lua"), []byte("x"), 0o600))

	copied, cleanup, err := strippedStage(t.Context(), t.TempDir(), stage, native.Host())
	require.NoError(t, err)

	assert.False(t, hasDebugInfo(t, filepath.Join(copied, "helper")))
	assert.FileExists(t, filepath.Join(copied, "lua", "init.lua"))
	assert.True(t, hasDebugInfo(t, program), "the stage is as it was")

	info, err := os.Stat(filepath.Join(copied, "helper"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the mode is kept")

	cleanup()

	_, err = os.Stat(copied)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
