package build

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/build/backend"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// manifestHead is the part of a manifest every test here shares.
const manifestHead = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']
`

// goManifest is my-app with a Go program built by the go backend, whose
// sources carry a Lua file the default include would lay out.
const goManifest = manifestHead + `
[components.gw]
path = 'gw'
kind = 'executable'

[components.gw.build]
backend = 'go'
package = './cmd/gw'
`

// goFiles is the project of goManifest.
var goFiles = map[string]string{
	"init.lua":           "-- init",
	"gw/go.mod":          "module example.com/gw\n\ngo 1.22\n",
	"gw/cmd/gw/main.go":  "package main\n\nfunc main() {}\n",
	"gw/testdata/x.lua":  "-- a fixture of the Go tests",
	"gw/cmd/gw/doc.lua":  "-- not a module of the package",
	"gw/cmd/gw/data.txt": "not laid out either",
}

// hostProgram returns the path of a program of the host every system has:
// what a shell backend of the tests copies into TT_OUTPUT_DIR.
func hostProgram(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("true")
	require.NoError(t, err)

	prog, err := native.InspectProgram(path)
	if err != nil || !prog.Executable {
		t.Skipf("%s is not a native program", path)
	}

	return path
}

// shellManifest is my-app with an executable component built by a shell
// command.
func shellManifest(command string) string {
	return manifestHead + `
[components.tools]
path = 'tools'
kind = 'executable'

[components.tools.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''` + command + `''']
`
}

// readRecord reads the record of programs of the project dir.
func readRecord(t *testing.T, dir string) map[string][]string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, buildDirName, executablesFileName))
	require.NoError(t, err)

	var record executablesRecord

	require.NoError(t, json.Unmarshal(data, &record))

	return record.Components
}

func TestRun_executableGo(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, goManifest, goFiles)

	result, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)

	program := filepath.Join(dir, ".rocks", "bin", "gw")

	prog, err := native.InspectProgram(program)
	require.NoError(t, err)
	assert.True(t, prog.Executable)
	assert.True(t, prog.Static(), "%+v", prog)
	assert.Equal(t, []native.Platform{native.Host()}, prog.Platforms)

	assert.Equal(t, map[string][]string{"gw": {"gw"}}, result.Executables)
	assert.Equal(t, map[string][]string{"gw": {"gw"}}, readRecord(t, dir))

	// An executable component lays nothing out under the namespace.
	share := filepath.Join(dir, ".rocks", "share", "tarantool", "my-app")
	assert.True(t, exists(t, filepath.Join(share, "init.lua")))
	assert.False(t, exists(t, filepath.Join(share, "testdata")))
	assert.False(t, exists(t, filepath.Join(share, "cmd")))
	assert.False(t, exists(t, filepath.Join(dir, ".rocks", "share", "tarantool", "gw")))
}

// TestRun_executableShellKeepsItsRecord: a make or shell build owns what it
// created in bin/, and still owns it when it is up to date and writes
// nothing.
func TestRun_executableShellKeepsItsRecord(t *testing.T) {
	t.Parallel()

	program := hostProgram(t)

	dir := setupProject(t, shellManifest(
		`[ -e "$TT_OUTPUT_DIR/helper" ] || cp `+program+` "$TT_OUTPUT_DIR/helper"`),
		map[string]string{"init.lua": "-- init", "tools/x": "x"})

	// A rock's console script was there before: it is not the package's.
	writeFile(t, filepath.Join(dir, ".rocks", "bin", "luatest"), "#!/bin/sh\n")

	for range 2 {
		result, err := RunResult(context.Background(), dryOptions(dir))
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{"tools": {"helper"}}, result.Executables)
	}

	assert.Equal(t, map[string][]string{"tools": {"helper"}}, readRecord(t, dir))
}

func TestRun_executableNotAProgram(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, shellManifest(
		`printf '#!/bin/sh\n' > "$TT_OUTPUT_DIR/run.sh"; chmod +x "$TT_OUTPUT_DIR/run.sh"`),
		map[string]string{"init.lua": "-- init", "tools/x": "x"})

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, errNotProgram)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.Contains(t, err.Error(), "run.sh")
	assert.Contains(t, err.Error(), `building component "tools"`)
}

func TestRun_executableBuildsNothing(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, shellManifest("true"),
		map[string]string{"init.lua": "-- init", "tools/x": "x"})

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, errNoProgram)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
}

// TestRun_componentBuildKeepsTheOthersRecord: building one component keeps
// what the record says of the others, and a component no longer executable
// leaves the record.
func TestRun_componentBuildKeepsTheOthersRecord(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, goManifest, goFiles)

	_, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)

	opts := dryOptions(dir)

	opts.Component = "lua"

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"gw": {"gw"}}, result.Executables)

	// gw is a Lua component now.
	writeFile(t, filepath.Join(dir, manifestFileName),
		manifestHead+"\n[components.gw]\npath = 'gw'\n")

	result, err = RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)
	assert.Empty(t, result.Executables)
}

// TestRun_missingToolFailsFirst: a go component without go in PATH fails the
// build before anything is resolved or built.
func TestRun_missingToolFailsFirst(t *testing.T) {
	dir := setupProject(t, goManifest, goFiles)

	t.Setenv("PATH", t.TempDir())

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, backend.ErrNoTool)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.Contains(t, err.Error(), `"go"`)
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
}

// elfExecutable returns the header of a little-endian 64-bit Linux ELF
// executable for machine, which is all the platform check reads of it.
func elfExecutable(t *testing.T, machine elf.Machine) []byte {
	t.Helper()

	hdr := elf.Header64{
		Type: uint16(elf.ET_EXEC), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
		Ehsize: 64, Phentsize: 56, Shentsize: 64,
	}

	copy(hdr.Ident[:], elf.ELFMAG)

	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.LittleEndian, &hdr))

	return buf.Bytes()
}

func TestCheckPrograms(t *testing.T) {
	t.Parallel()

	linuxAmd64 := native.Platform{OS: "linux", Arch: "amd64"}

	dir := t.TempDir()

	write := func(name string, data []byte, perm os.FileMode) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, perm))
	}

	write("amd64", elfExecutable(t, elf.EM_X86_64), 0o755)
	write("arm64", elfExecutable(t, elf.EM_AARCH64), 0o755)
	write("plain", elfExecutable(t, elf.EM_X86_64), 0o644)
	write("script", []byte("#!/bin/sh\n"), 0o755)

	require.NoError(t, checkPrograms(dir, []string{"amd64"}, linuxAmd64, false))
	// A program without a loader or a library is static.
	require.NoError(t, checkPrograms(dir, []string{"amd64"}, linuxAmd64, true))

	err := checkPrograms(dir, []string{"amd64", "arm64", "plain", "script", "gone"},
		linuxAmd64, false)
	require.ErrorIs(t, err, errNotProgram)

	msg := err.Error()
	assert.Contains(t, msg, "arm64: not a program of the build platform: it runs on "+
		"linux-arm64, but the build is for linux-amd64")
	assert.Contains(t, msg, "plain: not a program of the build platform: it is not executable")
	assert.Contains(t, msg, "script: not a program of the build platform: it is neither")
	assert.Contains(t, msg, "gone: not a program of the build platform")
	assert.NotContains(t, msg, "amd64: ")
}

// TestCheckProgramsStatic: a dynamic program fails a static build, naming
// what it needs.
func TestCheckProgramsStatic(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the loader is read of ELF programs")
	}

	program := hostProgram(t)

	prog, err := native.InspectProgram(program)
	require.NoError(t, err)

	if prog.Static() {
		t.Skipf("%s is static here", program)
	}

	err = checkPrograms(filepath.Dir(program), []string{filepath.Base(program)},
		native.Host(), true)
	require.ErrorIs(t, err, errNotStatic)
	assert.Contains(t, err.Error(), "it needs the loader "+prog.Interpreter)
}
