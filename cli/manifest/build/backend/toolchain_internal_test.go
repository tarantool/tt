package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// The go and cargo backends are run here on real toolchains, on a program
// small enough to build in a second or two. go is there wherever these tests
// run; cargo may not be, and its tests are skipped then.

// writeFiles writes files, a map of a slash path under dir to its content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// goModule writes a Go module of one main package into a new directory and
// returns it. With cgo, the program calls into C, so that it is a cgo
// program indeed.
func goModule(t *testing.T, cgo bool) string {
	t.Helper()

	main := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"gw\") }\n"
	if cgo {
		main = "package main\n\n// int answer(void) { return 42; }\nimport \"C\"\n\n" +
			"import \"fmt\"\n\nfunc main() { fmt.Println(\"gw\", C.answer()) }\n"
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":         "module example.com/gw\n\ngo 1.22\n",
		"cmd/gw/main.go": main,
	})

	return dir
}

// inspectOutput inspects the program the build put into out under name.
func inspectOutput(t *testing.T, out, name string) native.Program {
	t.Helper()

	prog, err := native.InspectProgram(filepath.Join(out, name))
	require.NoError(t, err)
	assert.True(t, prog.Executable)
	assert.Equal(t, []native.Platform{native.Host()}, prog.Platforms)

	return prog
}

// runProgram runs the program at path and returns what it printed.
func runProgram(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), path).Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(out))
}

func TestGoBackendBuildsAStaticProgram(t *testing.T) {
	t.Parallel()

	cwd := goModule(t, false)
	out := filepath.Join(t.TempDir(), ".rocks", "bin")

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Name: "gateway"}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gateway")
	assert.True(t, prog.Static(), "a Go program without cgo is static: %+v", prog)
	assert.Equal(t, "gw", runProgram(t, filepath.Join(out, "gateway")))
}

func TestGoBackendBuildsAStaticCgoProgram(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}

	requireStaticLibc(t)

	cwd := goModule(t, true)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Cgo: on(), Static: on()}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gw")
	assert.True(t, prog.Static(), "%+v", prog)
	assert.Equal(t, "gw 42", runProgram(t, filepath.Join(out, "gw")))
}

// requireStaticLibc skips the test when there is no C compiler in PATH, or
// no static C library for it to link a static cgo program with: the cc
// driver prints the bare name of a library it cannot find.
func requireStaticLibc(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler in PATH for cgo")
	}

	out, err := exec.CommandContext(context.Background(),
		"cc", "-print-file-name=libc.a").Output()
	if err != nil || !filepath.IsAbs(strings.TrimSpace(string(out))) {
		t.Skip("no static C library for cc")
	}
}

// TestGoBackendCgoIsDynamicWithoutStatic shows what the static check of the
// build is for: cgo alone links the C library dynamically.
func TestGoBackendCgoIsDynamicWithoutStatic(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the loader is checked on linux")
	}

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler in PATH for cgo")
	}

	cwd := goModule(t, true)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw", Cgo: on()}
	env := Env{OutputDir: out, Component: "gw"}

	require.NoError(t, goBackend{}.Run(context.Background(), build, cwd, env))

	prog := inspectOutput(t, out, "gw")
	assert.False(t, prog.Static(), "%+v", prog)
	assert.NotEmpty(t, prog.Interpreter)
}

func TestGoBackendFailureIsAnError(t *testing.T) {
	t.Parallel()

	cwd := goModule(t, false)
	writeFiles(t, cwd, map[string]string{"cmd/gw/broken.go": "package main\n\nfunc broken( {}\n"})

	build := manifest.Build{Backend: BackendGo, Package: "./cmd/gw"}
	env := Env{OutputDir: t.TempDir(), Component: "gw"}

	require.Error(t, goBackend{}.Run(context.Background(), build, cwd, env))
}

// requireCargo skips the test when cargo is not in PATH, and, when target is
// set, when the Rust target is not installed.
func requireCargo(t *testing.T, target string) {
	t.Helper()

	_, err := exec.LookPath(cargoTool)
	if err != nil {
		t.Skip("cargo is not in PATH")
	}

	if target == "" {
		return
	}

	sysroot, err := exec.CommandContext(context.Background(),
		"rustc", "--print", "sysroot").Output()
	if err != nil {
		t.Skipf("rustc cannot tell its sysroot: %s", err)
	}

	lib := filepath.Join(strings.TrimSpace(string(sysroot)), "lib", "rustlib", target)

	_, err = os.Stat(lib)
	if err != nil {
		t.Skipf("the Rust target %s is not installed", target)
	}
}

// cargoCrate writes a crate without dependencies, which builds offline, and
// returns its directory.
func cargoCrate(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Cargo.toml": "[package]\nname = \"agent-crate\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[[bin]]\nname = \"agent\"\npath = \"src/main.rs\"\n\n" +
			"[dependencies]\n",
		"src/main.rs": "fn main() { println!(\"agent\"); }\n",
	})

	return dir
}

func TestCargoBackendBuildsTheBinary(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	cwd := cargoCrate(t)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendCargo, Bin: "agent", Profile: "dev"}
	env := Env{OutputDir: out, Component: "agent-component"}

	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))

	inspectOutput(t, out, "agent")
	assert.Equal(t, "agent", runProgram(t, filepath.Join(out, "agent")))
}

func TestCargoBackendBuildsAStaticBinary(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}

	arch, ok := cargoArches[runtime.GOARCH]
	if !ok {
		t.Skipf("no cargo target for %s", runtime.GOARCH)
	}

	requireCargo(t, arch+"-unknown-linux-musl")

	cwd := cargoCrate(t)
	out := t.TempDir()

	build := manifest.Build{Backend: BackendCargo, Static: on()}
	env := Env{OutputDir: out, Component: "agent"}

	require.NoError(t, cargoBackend{}.Run(context.Background(), build, cwd, env))

	// A static-pie program has a dynamic segment of its own, and is still
	// static: no loader, no library.
	prog := inspectOutput(t, out, "agent")
	assert.True(t, prog.Static(), "%+v", prog)
	assert.Equal(t, "agent", runProgram(t, filepath.Join(out, "agent")))
}

func TestCargoBackendUnknownBinIsAnError(t *testing.T) {
	t.Parallel()
	requireCargo(t, "")

	build := manifest.Build{Backend: BackendCargo, Profile: "dev"}
	env := Env{OutputDir: t.TempDir(), Component: "missing"}

	require.Error(t, cargoBackend{}.Run(context.Background(), build, cargoCrate(t), env))
}
