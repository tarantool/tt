package build

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// requireModuleCC skips the test where there is no cc, and off Linux, where
// the modules it builds are not the ELF ones the test expects.
func requireModuleCC(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("the modules cc builds here are ELF ones")
	}

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not found in PATH")
	}
}

// staticShellManifest is my-app with a module component built by a shell
// command with static = true, and incremental, so that a build that fails
// its check is seen not to be recorded.
func staticShellManifest(command string) string {
	return manifestHead + `
[components.native]
path = 'native'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''` + command + `''']
static = true
incremental = true
`
}

// staticModuleFiles are the sources of the modules of the tests here:
// plain.c needs libc and libm, linked.c a library of its own, extra.c.
var staticModuleFiles = map[string]string{
	"init.lua": "-- init",
	"native/plain.c": "#include <math.h>\n#include <string.h>\n" +
		"double plain(double x, const char *s) { return cos(x) + (double)strlen(s); }\n",
	"native/extra.c":  "int extra(void) { return 1; }\n",
	"native/linked.c": "int extra(void);\nint linked(void) { return extra(); }\n",
}

// TestRun_staticModule builds a static module that needs only the C library:
// the build passes and installs it.
func TestRun_staticModule(t *testing.T) {
	t.Parallel()

	requireModuleCC(t)

	dir := setupProject(t, staticShellManifest(`mkdir -p "$TT_OUTPUT_DIR" && `+
		`cc -shared -fPIC -o "$TT_OUTPUT_DIR/plain.so" plain.c -lm`), staticModuleFiles)

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.FileExists(t, filepath.Join(dir, ".rocks", "lib", "tarantool", "my-app", "plain.so"))

	record, found := readIncremental(filepath.Join(dir, buildDirName), "native")
	require.True(t, found, "a module that passed the check is recorded")
	assert.Equal(t, []string{"plain.so"}, record.Written)
}

// TestRun_staticModuleNeedsALibrary: a static module that needs a library
// but those of the C library fails the build, naming every such file, the
// one the build wrote into TT_OUTPUT_DIR and the one the layout would take
// from the component, and what each needs; a build that failed is not
// recorded, so the next one fails again rather than being skipped.
func TestRun_staticModuleNeedsALibrary(t *testing.T) {
	t.Parallel()

	requireModuleCC(t)

	dir := setupProject(t, staticShellManifest(`mkdir -p "$TT_OUTPUT_DIR" && `+
		`cc -shared -fPIC -o libextra.so -Wl,-soname,libextra.so.1 extra.c && `+
		`cc -shared -fPIC -o "$TT_OUTPUT_DIR/linked.so" linked.c -L. -l:libextra.so && `+
		`cc -shared -fPIC -o insource.so linked.c -L. -l:libextra.so`),
		staticModuleFiles)

	for range 2 {
		err := Run(context.Background(), dryOptions(dir))
		require.ErrorIs(t, err, errNotStaticModule)
		assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))

		msg := err.Error()
		assert.Contains(t, msg, `building component "native"`)
		assert.Contains(t, msg, filepath.Join(packageStage(dir, "default"), "lib", "my-app",
			"linked.so")+": not a static module: it needs libextra.so.1, which a machine "+
			"Tarantool runs on may not have: link it in statically, or load it at run time "+
			"with dlopen")
		assert.Contains(t, msg, filepath.Join(dir, "native", "insource.so")+
			": not a static module: it needs libextra.so.1")
		assert.NotContains(t, msg, "libextra.so:", "the library itself needs libc only")

		_, found := readIncremental(filepath.Join(dir, buildDirName), "native")
		assert.False(t, found, "a module that failed the check is not recorded")
	}

	assert.NoFileExists(t, filepath.Join(dir, ".rocks", "lib", "tarantool", "my-app",
		"linked.so"), "a module that fails the check is not installed")
}

// staticCMakeManifest builds two modules of one CMake project, each a
// component with static = true: plain, and linked, which needs a library the
// project builds.
const staticCMakeManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.plain]
path = 'native'

[components.plain.build]
backend = 'cmake'
targets = ['plain']
output = ['plain.so']
static = true

[components.linked]
path = 'native'

[components.linked.build]
backend = 'cmake'
targets = ['linked']
output = ['linked.so']
static = true
`

// staticCMakeLists builds plain.so, which needs libc and libm, and
// linked.so, which needs libextra.so of the same project.
const staticCMakeLists = `cmake_minimum_required(VERSION 3.15)
project(static C)

add_library(extra SHARED extra.c)
add_library(plain MODULE plain.c)
target_link_libraries(plain PRIVATE m)
add_library(linked MODULE linked.c)
target_link_libraries(linked PRIVATE extra)
set_target_properties(plain linked PROPERTIES PREFIX "" SUFFIX ".so")
`

// TestRun_staticCMakeModules checks the static modules of cmake builds: the
// module that needs libc only builds, the one that needs a library of its
// project fails the build, naming it.
func TestRun_staticCMakeModules(t *testing.T) {
	t.Parallel()

	requireCMake(t)
	requireModuleCC(t)

	files := map[string]string{"native/CMakeLists.txt": staticCMakeLists}
	maps.Copy(files, staticModuleFiles)

	dir := setupProject(t, staticCMakeManifest, files)

	opts := dryOptions(dir)

	opts.Component = "plain"

	require.NoError(t, Run(context.Background(), opts))

	opts.Component = ""

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, errNotStaticModule)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.Contains(t, err.Error(), `building component "linked"`)
	assert.Contains(t, err.Error(), filepath.Join("lib", "my-app", "linked.so")+
		": not a static module: it needs libextra.so, which")
	assert.NotContains(t, err.Error(), "plain.so")
}

// TestCheckStaticModuleSkipsWhatIsNotAModule: a Lua file, a missing file
// and a symbolic link are not looked at.
func TestCheckStaticModuleSkipsWhatIsNotAModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lua := filepath.Join(dir, "init.lua")
	writeFile(t, lua, "return {}\n")

	link := filepath.Join(dir, "gone.so")
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere.so"), link))

	err := checkStaticModule(Options{}, "native", []string{
		lua, link, filepath.Join(dir, "missing.so"),
	})
	require.NoError(t, err)
}

// TestCheckStaticModuleGlibc: a module that needs a newer glibc than the
// Tarantool it is built for passes with a warning. The true of the system
// stands for Tarantool; whether the module needs a newer glibc than it
// depends on the compiler and the glibc of the host.
func TestCheckStaticModuleGlibc(t *testing.T) {
	t.Parallel()

	requireModuleCC(t)

	tarantool := hostProgram(t)
	dir := t.TempDir()
	module := filepath.Join(dir, "m.so")
	src := filepath.Join(dir, "m.c")

	writeFile(t, src, "#include <stdlib.h>\n#include <string.h>\n"+
		"long m(const char *s) { return strtol(s, 0, 10) + (long)strlen(s); }\n")

	out, err := exec.CommandContext(t.Context(), "cc", "-shared", "-fPIC", "-o", module,
		src).CombinedOutput()
	require.NoError(t, err, string(out))

	moduleProg, err := native.InspectProgram(module)
	require.NoError(t, err)

	tarantoolProg, err := native.InspectProgram(tarantool)
	require.NoError(t, err)

	var warnings []string

	opts := Options{
		Tarantool: rocks.TarantoolInfo{Executable: tarantool},
		Warn:      func(msg string) { warnings = append(warnings, msg) },
	}

	require.NoError(t, checkStaticModule(opts, "native", []string{module}))

	if moduleProg.Glibc == "" || tarantoolProg.Glibc == "" ||
		native.CompareGlibc(moduleProg.Glibc, tarantoolProg.Glibc) <= 0 {
		assert.Empty(t, warnings)

		return
	}

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], module+" needs glibc "+moduleProg.Glibc)
	assert.Contains(t, warnings[0], tarantool+", the Tarantool it is built for, needs "+
		tarantoolProg.Glibc)
}

func TestNewerGlibc(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `component "m": /p/m.so needs glibc 2.38, and /usr/bin/tarantool, `+
		`the Tarantool it is built for, needs 2.17: the module does not load where `+
		`Tarantool runs on a glibc older than 2.38; build it with an older glibc for it `+
		`to load there`, newerGlibc("m", "/p/m.so", "2.38", "/usr/bin/tarantool", "2.17"))

	for _, versions := range [][2]string{
		{"2.17", "2.17"}, {"2.2.5", "2.17"}, {"", "2.17"}, {"2.38", ""},
	} {
		assert.Empty(t, newerGlibc("m", "/p/m.so", versions[0], "tarantool", versions[1]),
			versions)
	}
}

func TestDescribeExtras(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "it needs libz.so.1, which a machine Tarantool runs on may not have: "+
		"link it in statically, or load it at run time with dlopen",
		describeExtras([]string{"libz.so.1"}))
	assert.Equal(t, "it needs libz.so.1, libssl.so.3 and libsasl2.so.2, which a machine "+
		"Tarantool runs on may not have: link them in statically, or load them at run time "+
		"with dlopen", describeExtras([]string{"libz.so.1", "libssl.so.3", "libsasl2.so.2"}))
}
