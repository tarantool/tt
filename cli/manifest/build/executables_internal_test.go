package build

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/version"
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

// exeManifest is my-app with a program built by exe, a backend of programs
// that lays out no sources of its component (buildregtest), whose sources
// carry a Lua file the default include would lay out. The component says no
// kind: it makes what its build makes.
const exeManifest = manifestHead + `
[components.gw]
path = 'gw'

[components.gw.build]
backend = 'exe'
`

// exeFiles is the project of exeManifest: the sources of a Go program.
var exeFiles = map[string]string{
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

// shellManifest is my-app with a component of the deprecated
// kind = "executable" built by a shell command, which is told to put its
// programs into TT_OUTPUT_DIR.
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

// recordOf reads the record of what the components of the project dir
// built, strictly.
func recordOf(t *testing.T, dir string) map[string]madeFiles {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, buildDirName, resultsFileName))
	require.NoError(t, err)

	var record resultsRecord

	require.NoError(t, json.Unmarshal(data, &record))

	return record.Components
}

// readRecord reads the programs of the record of the project dir, by
// component, of the components that made any.
func readRecord(t *testing.T, dir string) map[string][]string {
	t.Helper()

	return programsOf(recordOf(t, dir))
}

// TestRun_programBackend: the program a backend of programs makes is laid
// out into bin/ and recorded, and the sources of its component are not laid
// out.
func TestRun_programBackend(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, exeManifest, exeFiles)

	result, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)

	program := filepath.Join(dir, ".rocks", "bin", "gw")

	prog, err := native.InspectProgram(program)
	require.NoError(t, err)
	assert.True(t, prog.Executable)
	assert.Equal(t, []native.Platform{native.Host()}, prog.Platforms)

	assert.Equal(t, map[string][]string{"gw": {"gw"}}, result.Programs)
	assert.Equal(t, map[string][]string{"gw": {"gw"}}, readRecord(t, dir))
	assert.Equal(t, madeFiles{Programs: []string{"gw"}}, recordOf(t, dir)["gw"])

	// The sources of the component are not laid out under the namespace.
	share := filepath.Join(dir, ".rocks", "share", "tarantool", "my-app")
	assert.True(t, exists(t, filepath.Join(share, "init.lua")))
	assert.False(t, exists(t, filepath.Join(share, "testdata")))
	assert.False(t, exists(t, filepath.Join(share, "cmd")))
	assert.False(t, exists(t, filepath.Join(dir, ".rocks", "share", "tarantool", "gw")))
}

// TestRun_includeOfAProgramBackendLaysOut: what the include of a component
// of a backend that lays out no sources names is laid out, the rest of its
// sources not: the include of a component is its own whatever its backend.
func TestRun_includeOfAProgramBackendLaysOut(t *testing.T) {
	t.Parallel()

	files := maps.Clone(exeFiles)

	files["gw/lua/client.lua"] = "return {}"

	dir := setupProject(t, manifestHead+`
[components.gw]
path = 'gw'
include = ['lua/*.lua']
namespace = 'gw'

[components.gw.build]
backend = 'exe'
`, files)

	result, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"gw": {"gw"}}, result.Programs)

	share := filepath.Join(dir, ".rocks", "share", "tarantool", "gw")
	assert.True(t, exists(t, filepath.Join(share, "lua", "client.lua")))
	assert.False(t, exists(t, filepath.Join(share, "testdata")))
	assert.False(t, exists(t, filepath.Join(share, "cmd")))
	assert.True(t, exists(t, filepath.Join(dir, ".rocks", "bin", "gw")))
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
		assert.Equal(t, map[string][]string{"tools": {"helper"}}, result.Programs)
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

	dir := setupProject(t, exeManifest, exeFiles)

	_, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)

	opts := dryOptions(dir)

	opts.Component = "lua"

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"gw": {"gw"}}, result.Programs)

	// gw is a Lua component now.
	writeFile(t, filepath.Join(dir, manifestFileName),
		manifestHead+"\n[components.gw]\npath = 'gw'\n")

	result, err = RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)
	assert.Empty(t, result.Programs)
}

// TestRun_missingToolFailsFirst: a component whose build needs a tool not in
// PATH fails the build before anything is resolved or built.
func TestRun_missingToolFailsFirst(t *testing.T) {
	dir := setupProject(t, exeManifest, exeFiles)

	t.Setenv("PATH", t.TempDir())

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, sdkbuild.ErrNoTool)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.Contains(t, err.Error(), `the exe backend runs "sh"`)
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
}

// TestRun_toolOnlyInThePathOfTt: a PATH that [build].env sets is the whole
// PATH of the build. A tool that is in the PATH of tt only fails the
// preflight of a build that looks for it, and a command of a build that
// runs it fails to start, rather than run the program of tt's PATH.
func TestRun_toolOnlyInThePathOfTt(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("sh")
	require.NoError(t, err)

	empty := t.TempDir()

	dir := setupProject(t, manifestHead+`
[components.gw]
path = 'gw'

[components.gw.build]
backend = 'exe'
tool = 'sh'
env = { PATH = '`+empty+`' }
`, exeFiles)

	err = Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, sdkbuild.ErrNoTool)
	assert.Contains(t, err.Error(), `the exe backend runs "sh"`)

	dir = setupProject(t, manifestHead+`
[components.gen]
path = 'gen'

[components.gen.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'true']
env = { PATH = '`+empty+`' }
`, map[string]string{"gen/init.lua": "return {}\n"})

	err = Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, exec.ErrNotFound)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
}

// TestPreflightFindsTheProjectBin: a toolchain in .tarantool/bin of the
// project being built, and nowhere else, passes the preflight, as the build
// finds it there; and so does one in the PATH of [build].env, the project's
// bin directory still first. A path dependency is checked as the project it
// is, its own directory the root.
func TestPreflightFindsTheProjectBin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	toolManifest := func(tool, env string) string {
		return manifestHead + `
[components.gw]
path = 'gw'

[components.gw.build]
backend = 'exe'
tool = '` + tool + `'
` + env
	}

	// The package and a neighbour it is built as a path dependency of.
	for _, dir := range []string{
		setupProject(t, toolManifest("fake-go", ""), exeFiles),
		setupProject(t, toolManifest("fake-cmake", "env = { PATH = '/nonexistent' }\n"),
			exeFiles),
	} {
		man, err := readManifest(dir, Options{})
		require.NoError(t, err)

		proj := project{dir: dir, stateDir: t.TempDir(), man: man, product: "default"}
		components := slices.Sorted(maps.Keys(man.Components))
		adapter := rocks.New(rocks.BuildConfig(rocks.TarantoolInfo{}, rocks.ConfigOptions{
			Tree: filepath.Join(dir, rocksDirName), WorkingDir: dir,
		}))
		builder := newBuilder(Options{ProjectDir: dir, Backends: buildregtest.Registry()}, proj,
			adapter, packageStage(dir, "default"), version.Version{})

		require.ErrorIs(t, builder.preflight(t.Context(), components), sdkbuild.ErrNoTool)

		bin := ProjectBinDir(dir)
		for _, tool := range []string{"fake-go", "fake-cmake"} {
			writeFile(t, filepath.Join(bin, tool), "#!/bin/sh\n")
			require.NoError(t, os.Chmod(filepath.Join(bin, tool), 0o755))
		}

		require.NoError(t, builder.preflight(t.Context(), components))
	}
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
