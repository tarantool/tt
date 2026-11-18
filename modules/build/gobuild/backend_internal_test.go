package gobuild

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// A build of go says what it makes, names its program, checks what it needs
// first and downloads the modules it requires ahead into a mirror.
var (
	_ build.Producer     = goBuild{}
	_ build.ProgramNamer = goBuild{}
	_ build.Preflighter  = goBuild{}
	_ build.Vendorer     = goBuild{}
)

// component is the component gw, in the directory gw.
func component() build.Component {
	return build.Component{
		Name: "gw", Path: "gw", Namespace: "", Package: "", Wants: nil,
	}
}

// block is a build block of the fields rest, static as asked.
func block(rest map[string]any, static bool) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: nil, Static: static},
		Incremental: false,
		Fields:      rest,
	}
}

// validate checks the block of rest for the component gw and returns its
// build.
func validate(t *testing.T, rest map[string]any, static bool) build.Build {
	t.Helper()

	bld, err := buildtest.Validate(Backend(), component(), block(rest, static))
	require.NoError(t, err)

	return bld
}

// writeTool writes the shell script body as go in dir. The script is
// written aside and copied with no fork in between, so that starting it is
// not refused while parallel tests start processes.
func writeTool(t *testing.T, dir, body string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), goTool)
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"+body), 0o600))
	require.NoError(t, os.Chmod(src, 0o700))
	require.NoError(t, build.CopyFile(filepath.Join(dir, goTool), src))
}

func TestValidate(t *testing.T) {
	t.Parallel()

	bld := validate(t, map[string]any{
		"package": "./cmd/gw",
		"name":    "gateway",
		"tags":    []any{"prod"},
		"ldflags": []any{"-s", "-X", "main.version=1.0"},
		"cgo":     true,
	}, true)

	namer, ok := bld.(build.ProgramNamer)
	require.True(t, ok)

	names, exhaustive := namer.Programs()
	assert.Equal(t, []string{"gateway"}, names)
	assert.True(t, exhaustive)

	_, ok = bld.(build.Preflighter)
	assert.True(t, ok)
}

func TestProgramsDefaultToTheComponent(t *testing.T) {
	t.Parallel()

	namer, ok := validate(t, nil, false).(build.ProgramNamer)
	require.True(t, ok)

	names, exhaustive := namer.Programs()
	assert.Equal(t, []string{"gw"}, names)
	assert.True(t, exhaustive)

	producer, ok := validate(t, nil, false).(build.Producer)
	require.True(t, ok)
	assert.Equal(t, []build.Result{build.Program}, producer.Produces())

	// A component that wants programs alone builds the same program.
	programs := component()

	programs.Wants = []build.Result{build.Program}

	_, err := buildtest.Validate(Backend(), programs, block(nil, false))
	require.NoError(t, err)
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	module := component()

	module.Wants = []build.Result{build.Module, build.Data}

	tests := []struct {
		name      string
		component build.Component
		rest      map[string]any
		field     string
		msg       string
	}{
		{"a name with a slash", component(), map[string]any{"name": "bin/gw"},
			"name", `"bin/gw" is not a file name`},
		{"a name of a dot", component(), map[string]any{"name": ".."},
			"name", `".." is not a file name`},
		{"two tags in one", component(), map[string]any{"tags": []any{"a,b"}},
			"tags", `"a,b" is not a build tag`},
		{"an empty tag", component(), map[string]any{"tags": []any{""}},
			"tags", `"" is not a build tag`},
		{"an empty ldflag", component(), map[string]any{"ldflags": []any{"-s", ""}},
			"ldflags", "must not contain an empty entry"},
		{"a name of a number", component(), map[string]any{"name": int64(1)},
			"name", "must be a string, not an integer"},
		{"cgo of a string", component(), map[string]any{"cgo": "yes"},
			"cgo", "must be a boolean, not a string"},
		{"tags of a string", component(), map[string]any{"tags": "prod"},
			"tags", "must be an array, not a string"},
		{"a tag of a number", component(), map[string]any{"tags": []any{int64(1)}},
			"tags[0]", "must be a string, not an integer"},
		{"an unknown field", component(), map[string]any{"make_target": "all"},
			"make_target", "unknown field"},
		{"a component of modules", module, nil,
			"", `backend "go" makes nothing the component wants`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildtest.Validate(Backend(), test.component,
				block(test.rest, false))

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
	}
}

func TestBackend(t *testing.T) {
	t.Parallel()

	backend := Backend()

	assert.Equal(t, Name, backend.Name)
	assert.Equal(t, []build.Result{build.Program}, backend.Produces)
	assert.True(t, backend.NoSourceLayout)
	assert.Equal(t, build.OutputNone, backend.OutputFrom)
	assert.False(t, backend.Incremental)
	assert.False(t, backend.Hook)
	assert.True(t, backend.Cross)

	fields, err := backend.Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"package", "name", "tags", "ldflags", "cgo"}, fields)
}

func TestPreflight(t *testing.T) {
	t.Parallel()

	t.Run("static for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())
		writeTool(t, harness.Context.Dirs.Tools, "")

		harness.Context.Target = build.Target{OS: "darwin", Arch: "arm64"}

		preflighter, ok := validate(t, nil, true).(build.Preflighter)
		require.True(t, ok)

		err := preflighter.Preflight(t.Context(), harness.Context)
		require.ErrorIs(t, err, ErrStaticDarwin)
		assert.Contains(t, err.Error(), "static = true needs a linux build")

		// Without static it passes.
		preflighter, ok = validate(t, nil, false).(build.Preflighter)
		require.True(t, ok)
		require.NoError(t, preflighter.Preflight(t.Context(), harness.Context))
	})

	t.Run("a missing tool", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())

		harness.Context.Env["PATH"] = t.TempDir()

		err := harness.Run(t.Context(), validate(t, nil, false))
		require.ErrorIs(t, err, build.ErrNoTool)
		assert.Contains(t, err.Error(), `the go backend runs "go", which is not in PATH`)
	})

	t.Run("a tool of the project", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())

		harness.Context.Env["PATH"] = t.TempDir()
		writeTool(t, harness.Context.Dirs.Tools, "")

		preflighter, ok := validate(t, nil, false).(build.Preflighter)
		require.True(t, ok)
		require.NoError(t, preflighter.Preflight(t.Context(), harness.Context))
	})
}

// TestRunSelectsTheTarget: go build is run in the working directory with the
// program in the output directory, and the target of the build and cgo are
// set after the env of the block, so that the block cannot build for
// another platform than the one the program is checked against.
func TestRunSelectsTheTarget(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	facts := harness.Context
	log := filepath.Join(facts.Dirs.State, "go")

	writeTool(t, facts.Dirs.Tools, `printf '%s\n' "$PWD" "$@" > "`+log+`.args"
printf '%s\n' "$GOOS" "$GOARCH" "$CGO_ENABLED" "$CC" > "`+log+`.env"
`)

	other := otherTarget()

	harness.SetTarget(other)

	facts.Env["GOOS"] = "darwin"
	facts.Env["CGO_ENABLED"] = "1"
	facts.Env["CC"] = "musl-gcc"

	require.NoError(t, Backend().CheckTarget(other))
	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{
		"package": "./cmd/gw", "name": "gateway",
	}, false)))

	cwd, err := filepath.EvalSymlinks(facts.Dirs.Cwd)
	require.NoError(t, err)

	args, err := os.ReadFile(log + ".args")
	require.NoError(t, err)
	assert.Equal(t, cwd+"\nbuild\n-trimpath\n-o\n"+
		filepath.Join(facts.Dirs.Programs, "gateway")+"\n./cmd/gw\n", string(args))

	env, err := os.ReadFile(log + ".env")
	require.NoError(t, err)
	assert.Equal(t, other.OS+"\n"+other.Arch+"\n0\nmusl-gcc\n", string(env))

	// cgo is for the machine alone.
	harness.SetTarget(build.HostTarget())

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{"cgo": true}, false)))

	env, err = os.ReadFile(log + ".env")
	require.NoError(t, err)
	assert.Equal(t, runtime.GOOS+"\n"+runtime.GOARCH+"\n1\nmusl-gcc\n", string(env))
}

// otherTarget is a platform other than the machine of the test: its OS on
// the other architecture.
func otherTarget() build.Target {
	if runtime.GOARCH == "arm64" {
		return build.Target{OS: runtime.GOOS, Arch: "amd64"}
	}

	return build.Target{OS: runtime.GOOS, Arch: "arm64"}
}

// TestPreflightOfAnotherPlatform: a build for another platform than the
// machine is refused with cgo, before go runs, wrapping build.ErrTarget, and
// passes without it.
func TestPreflightOfAnotherPlatform(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	writeTool(t, harness.Context.Dirs.Tools, "exit 1")

	harness.SetTarget(otherTarget())

	err := harness.Run(t.Context(), validate(t, map[string]any{"cgo": true}, false))
	require.ErrorIs(t, err, build.ErrTarget)
	assert.Equal(t, fmt.Sprintf("preflight: the build cannot be made for the target platform: "+
		"cgo = true compiles C with the C compiler of the machine, %s, and the build is for "+
		"%s; build it there, or without cgo", build.HostTarget(), otherTarget()), err.Error())

	preflighter, ok := validate(t, nil, false).(build.Preflighter)
	require.True(t, ok)
	require.NoError(t, preflighter.Preflight(t.Context(), harness.Context))
}

// TestBuildsForAnotherPlatform builds a program with the real go for the
// other architecture of the machine, which go makes without a C toolchain,
// and checks that it is a program of that platform.
func TestBuildsForAnotherPlatform(t *testing.T) {
	t.Parallel()

	harness := goHarness(t, false)
	other := otherTarget()

	harness.SetTarget(other)

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{
		"package": "./cmd/gw",
	}, false)), harness.Printed())

	program := filepath.Join(harness.Context.Dirs.Programs, "gw")

	switch runtime.GOOS {
	case "linux":
		file, err := elf.Open(program)
		require.NoError(t, err)

		defer func() { _ = file.Close() }()

		assert.Equal(t, map[string]elf.Machine{"amd64": elf.EM_X86_64,
			"arm64": elf.EM_AARCH64}[other.Arch], file.Machine)
	case "darwin":
		file, err := macho.Open(program)
		require.NoError(t, err)

		defer func() { _ = file.Close() }()

		assert.Equal(t, map[string]macho.Cpu{"amd64": macho.CpuAmd64,
			"arm64": macho.CpuArm64}[other.Arch], file.Cpu)
	}
}

// TestRunJobs: the jobs of the build are -p of go build, and with none go
// build is given no -p.
func TestRunJobs(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	facts := harness.Context
	log := filepath.Join(facts.Dirs.State, "go.args")
	output := filepath.Join(facts.Dirs.Programs, "gw")

	writeTool(t, facts.Dirs.Tools, `printf '%s\n' "$@" > "`+log+`"`+"\n")

	bld := validate(t, map[string]any{"name": "gw"}, false)

	require.NoError(t, harness.Run(t.Context(), bld))

	args, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, "build\n-trimpath\n-o\n"+output+"\n.\n", string(args))

	harness.SetJobs(5)

	require.NoError(t, harness.Run(t.Context(), bld))

	args, err = os.ReadFile(log)
	require.NoError(t, err)
	assert.Equal(t, "build\n-trimpath\n-o\n"+output+"\n-p\n5\n.\n", string(args))
}

// The go backend is run below on the real toolchain, on a program small
// enough to build in a second or two.

// goHarness returns a harness of the component gw whose directory holds a
// Go module of one main package, cmd/gw. With cgo, the program calls into
// C, so that it is a cgo program indeed. It skips the test without go.
func goHarness(t *testing.T, cgo bool) *buildtest.Harness {
	t.Helper()

	_, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not in PATH")
	}

	main := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"gw\") }\n"
	if cgo {
		main = "package main\n\n// int answer(void) { return 42; }\nimport \"C\"\n\n" +
			"import \"fmt\"\n\nfunc main() { fmt.Println(\"gw\", C.answer()) }\n"
	}

	harness := buildtest.New(t, Backend(), component())
	dir := harness.Context.Dirs.Component

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd", "gw"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/gw\n\ngo 1.22\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "gw", "main.go"),
		[]byte(main), 0o600))

	return harness
}

// linkage returns what the ELF program at path needs of the system: its
// loader and its libraries. It skips the test off linux.
func linkage(t *testing.T, path string) (string, []string) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("the linkage of a program is inspected on linux")
	}

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	assert.Equal(t, elf.ET_EXEC, elfType(file), "a program, not a library")

	interpreter := ""

	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			data := make([]byte, prog.Filesz)

			_, err = prog.ReadAt(data, 0)
			require.NoError(t, err)

			interpreter = strings.TrimRight(string(data), "\x00")
		}
	}

	libraries, err := file.ImportedLibraries()
	require.NoError(t, err)

	return interpreter, libraries
}

// elfType is the type of an ELF file, a position-independent program being
// ET_EXEC too.
func elfType(file *elf.File) elf.Type {
	if file.Type == elf.ET_DYN {
		for _, prog := range file.Progs {
			if prog.Type == elf.PT_INTERP {
				return elf.ET_EXEC
			}
		}
	}

	return file.Type
}

// runProgram runs the program at path and returns what it printed.
func runProgram(t *testing.T, path string) string {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), path).Output()
	require.NoError(t, err)

	return strings.TrimSpace(string(out))
}

func TestBuildsAStaticProgram(t *testing.T) {
	t.Parallel()

	harness := goHarness(t, false)

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{
		"package": "./cmd/gw", "name": "gateway",
	}, false)))

	program := filepath.Join(harness.Context.Dirs.Programs, "gateway")
	assert.Equal(t, "gw", runProgram(t, program))

	interpreter, libraries := linkage(t, program)
	assert.Empty(t, interpreter, "a Go program without cgo is static")
	assert.Empty(t, libraries)
}

func TestBuildsAStaticCgoProgram(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("a static program is built for linux only")
	}

	requireStaticLibc(t)

	harness := goHarness(t, true)

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{
		"package": "./cmd/gw", "cgo": true,
	}, true)))

	program := filepath.Join(harness.Context.Dirs.Programs, "gw")
	assert.Equal(t, "gw 42", runProgram(t, program))

	interpreter, libraries := linkage(t, program)
	assert.Empty(t, interpreter)
	assert.Empty(t, libraries)
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

// TestCgoIsDynamicWithoutStatic shows what the static check of the build is
// for: cgo alone links the C library dynamically.
func TestCgoIsDynamicWithoutStatic(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the loader is checked on linux")
	}

	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler in PATH for cgo")
	}

	harness := goHarness(t, true)

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{
		"package": "./cmd/gw", "cgo": true,
	}, false)))

	interpreter, _ := linkage(t, filepath.Join(harness.Context.Dirs.Programs, "gw"))
	assert.NotEmpty(t, interpreter)
}

func TestFailureIsAnError(t *testing.T) {
	t.Parallel()

	harness := goHarness(t, false)

	require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Component, "cmd", "gw",
		"broken.go"), []byte("package main\n\nfunc broken( {}\n"), 0o600))

	err := harness.Run(t.Context(), validate(t, map[string]any{"package": "./cmd/gw"}, false))
	require.Error(t, err)
	assert.Contains(t, harness.Printed(), "broken.go")
}

// fakeGo writes a go into the tools directory of harness. go env -json prints
// the go.mod of the working directory, no workspace, FAKE_GOMODCACHE and
// FAKE_GOPROXY; go mod edit -json prints a go.mod of one requirement; go mod
// download records its arguments at <state>/go.download.args and its
// GOMODCACHE and GOPROXY at <state>/go.download.env, adds a hash to the
// go.sum beside its -modfile and fills cache/download of GOMODCACHE: the
// files of the module example.com/answer, its lock and its list of versions
// among them, the go.mod of example.com/num, a toolchain and a record of the
// checksum database, and a module directory it makes read-only, as go does;
// go build records its arguments at <state>/go.args and its GOPROXY, GOSUMDB
// and GONOPROXY at <state>/go.env, and fails when FAKE_FAIL is set.
func fakeGo(t *testing.T, harness *buildtest.Harness) string {
	t.Helper()

	log := filepath.Join(harness.Context.Dirs.State, "go")

	writeTool(t, harness.Context.Dirs.Tools, `case "$1 $2" in
"env -json")
	printf '{"GOMOD": "%s", "GOWORK": "", "GOMODCACHE": "%s", "GOPROXY": "%s"}\n' \
		"$PWD/go.mod" "${FAKE_GOMODCACHE:-/home/u/go/pkg/mod}" \
		"${FAKE_GOPROXY:-https://proxy.golang.org,direct}"
	exit 0;;
"mod edit")
	echo '{"Require": [{"Path": "example.com/answer", "Version": "v1.0.0"}]}'
	exit 0;;
"mod download")
	printf '%s\n' "$@" > "`+log+`.download.args"
	printf '%s\n' "$GOMODCACHE" "$GOPROXY" > "`+log+`.download.env"
	modfile=${3#-modfile=}
	echo 'example.com/num v1.0.0/go.mod h1:added=' >> "$(dirname "$modfile")/go.sum"
	d=$GOMODCACHE/cache/download
	mkdir -p "$d/example.com/answer/@v" "$d/example.com/num/@v" "$d/golang.org/toolchain/@v" \
		"$d/sumdb/sum.golang.org/lookup" "$GOMODCACHE/example.com/answer@v1.0.0"
	for ext in info mod zip ziphash lock; do
		echo "answer $ext" > "$d/example.com/answer/@v/v1.0.0.$ext"
	done
	echo v1.0.0 > "$d/example.com/answer/@v/list"
	echo 'num mod' > "$d/example.com/num/@v/v1.0.0.mod"
	echo toolchain > "$d/golang.org/toolchain/@v/v0.0.1-go1.99.0.linux-amd64.zip"
	echo sum > "$d/sumdb/sum.golang.org/lookup/example.com!answer@v1.0.0"
	echo answer > "$GOMODCACHE/example.com/answer@v1.0.0/answer.go"
	chmod 555 "$GOMODCACHE/example.com/answer@v1.0.0"
	exit 0;;
esac
[ -n "$FAKE_FAIL" ] && exit 1
printf '%s\n' "$@" > "`+log+`.args"
printf '%s\n' "$GOPROXY" "$GOSUMDB" "$GONOPROXY" > "`+log+`.env"
`)

	return log
}

// readLines reads the lines of the file at path.
func readLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// writeFiles writes files, a map of a slash path under dir to its content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// TestVendorRunsGoModDownload: the modules the go.mod requires are downloaded
// with go mod download, given copies of go.mod and go.sum, into a module
// cache of its own, with the module cache of the user before GOPROXY; their
// files are copied into the mirror in the layout of a module proxy, keeping
// what is there, without the locks and the lists of the module cache, the
// toolchains and the checksum database. The sources are not written, and the
// module cache of the download is removed.
func TestVendorRunsGoModDownload(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	log := fakeGo(t, harness)
	cwd := harness.Context.Dirs.Cwd
	dir := filepath.Join(t.TempDir(), "mirror", Name)
	sources := map[string]string{
		"go.mod": "module example.com/gw\n\ngo 1.22\n\nrequire example.com/answer v1.0.0\n",
		"go.sum": "example.com/answer v1.0.0 h1:x=\nexample.com/answer v1.0.0/go.mod h1:y=\n",
	}

	writeFiles(t, cwd, sources)
	writeFiles(t, dir, map[string]string{"example.com/answer/@v/v1.0.0.info": "kept"})

	harness.Context.Env["FAKE_GOMODCACHE"] = "/home/u/go/pkg/mod"
	harness.Context.Env["FAKE_GOPROXY"] = "https://goproxy.example.com"

	require.NoError(t, harness.Vendor(t.Context(), validate(t, nil, false), dir))

	args := readLines(t, log+".download.args")
	require.Len(t, args, 3)
	assert.Equal(t, []string{"mod", "download"}, args[:2])

	modfile, ok := strings.CutPrefix(args[2], "-modfile=")
	require.True(t, ok, args[2])
	assert.Equal(t, "go.mod", filepath.Base(modfile))

	env := readLines(t, log+".download.env")
	scratch := filepath.Dir(filepath.Dir(modfile))
	assert.Equal(t, []string{
		filepath.Join(scratch, "cache"),
		"file:///home/u/go/pkg/mod/cache/download,https://goproxy.example.com",
	}, env)
	assert.NoDirExists(t, scratch, "the module cache of the download is removed")

	for name, content := range sources {
		data, err := os.ReadFile(filepath.Join(cwd, name))
		require.NoError(t, err)
		assert.Equal(t, content, string(data), "%s is not written", name)
	}

	var files []string

	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			rel, _ := filepath.Rel(dir, path)

			files = append(files, filepath.ToSlash(rel))
		}

		return err
	}))
	assert.Equal(t, []string{
		"example.com/answer/@v/v1.0.0.info",
		"example.com/answer/@v/v1.0.0.mod",
		"example.com/answer/@v/v1.0.0.zip",
		"example.com/answer/@v/v1.0.0.ziphash",
		"example.com/num/@v/v1.0.0.mod",
	}, files)

	data, err := os.ReadFile(filepath.Join(dir, "example.com", "answer", "@v", "v1.0.0.info"))
	require.NoError(t, err)
	assert.Equal(t, "kept", string(data))

	info, err := os.Stat(filepath.Join(dir, "example.com", "answer", "@v", "v1.0.0.zip"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestVendorOfNothingToDownload: a module that requires no module, or only
// modules it replaces by directories, downloads nothing, and so does one
// with a vendor directory, which the build takes the modules from, and a
// package of no module; a module that requires modules and has no go.sum
// pins nothing to download, and fails it.
func TestVendorOfNothingToDownload(t *testing.T) {
	t.Parallel()

	const required = "module example.com/gw\n\ngo 1.22\n\nrequire example.com/answer v1.0.0\n"

	tests := []struct {
		name     string
		files    map[string]string
		noModule bool
		err      error
	}{
		{"no requirement", nil, false, nil},
		{"a requirement of a directory", map[string]string{
			"go.mod": required + "\nreplace example.com/answer => ../answer\n",
		}, false, nil},
		{"a vendor directory", map[string]string{
			"go.mod":             required,
			"vendor/modules.txt": "# example.com/answer v1.0.0\n## explicit\nexample.com/answer\n",
		}, false, nil},
		{"no module", nil, true, nil},
		{"no go.sum", map[string]string{"go.mod": required}, false, ErrNoGoSum},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := goHarness(t, false)
			dir := filepath.Join(t.TempDir(), "mirror", Name)
			cwd := harness.Context.Dirs.Cwd

			writeFiles(t, cwd, test.files)

			if test.noModule {
				require.NoError(t, os.Remove(filepath.Join(cwd, "go.mod")))
			}

			err := harness.Vendor(t.Context(), validate(t, nil, false), dir)
			if test.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.err)
				assert.Contains(t, err.Error(), ": its go.mod requires modules, and the module "+
					"pins nothing to download; run go mod tidy there, and keep go.sum with the "+
					"sources")
			}

			assert.NoDirExists(t, dir)
		})
	}
}

// TestRunFromAMirror: a build given a mirror takes the modules from it as its
// only GOPROXY, over the env of the block, with no checksum database, no
// private module and -mod=readonly, and a failure says where the modules were
// taken from; a module with a vendor directory builds from it as it does
// without a mirror.
func TestRunFromAMirror(t *testing.T) {
	t.Parallel()

	harness := buildtest.New(t, Backend(), component())
	log := fakeGo(t, harness)
	mirror := filepath.Join(t.TempDir(), "mirror", Name)
	output := filepath.Join(harness.Context.Dirs.Programs, "gw")
	bld := validate(t, nil, false)

	harness.Context.Vendored = mirror
	harness.Context.Env["GOPROXY"] = "https://goproxy.example.com"
	harness.Context.Env["GOPRIVATE"] = "example.com"

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.Equal(t, []string{"build", "-trimpath", "-o", output, "-mod=readonly", "."},
		readLines(t, log+".args"))
	assert.Equal(t, []string{"file://" + filepath.ToSlash(mirror), "off", ","},
		readLines(t, log+".env"))

	harness.Context.Env["FAKE_FAIL"] = "1"

	err := harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "; the build takes the modules from "+mirror+", offline, "+
		"which tt package download fills from the go.sum of the module")

	delete(harness.Context.Env, "FAKE_FAIL")
	writeFiles(t, harness.Context.Dirs.Cwd, map[string]string{"vendor/modules.txt": ""})

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.Equal(t, []string{"build", "-trimpath", "-o", output, "."}, readLines(t, log+".args"))
	assert.Equal(t, []string{"https://goproxy.example.com", "", ""}, readLines(t, log+".env"))

	harness.Context.Env["FAKE_FAIL"] = "1"

	err = harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "tt package download")
}

// runIn runs name with args in dir with env over the environment of the
// test, and fails the test when it fails.
func runIn(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...)

	cmd.Dir = dir

	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// writeModule writes the module path at version of files, its go.mod among
// them, into proxy, a directory in the layout of a module proxy, as GOPROXY
// takes it with file://.
func writeModule(t *testing.T, proxy, path, version string, files map[string]string) {
	t.Helper()

	dir := filepath.Join(proxy, filepath.FromSlash(path), "@v")

	writeFiles(t, dir, map[string]string{
		version + ".mod":  files["go.mod"],
		version + ".info": `{"Version":"` + version + `","Time":"2026-01-01T00:00:00Z"}`,
		"list":            version + "\n",
	})

	var buf bytes.Buffer

	archive := zip.NewWriter(&buf)

	for _, name := range slices.Sorted(maps.Keys(files)) {
		writer, err := archive.Create(path + "@" + version + "/" + name)
		require.NoError(t, err)

		_, err = writer.Write([]byte(files[name]))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, version+".zip"), buf.Bytes(), 0o600))
}

// TestBuildsOfflineFromTheMirror vendors a module whose requirement requires
// another, served by a module proxy on disk, then removes the proxy, vendors
// again from the module cache of the user alone with GOPROXY=off, and builds
// from the mirror with an empty module cache, a GOPROXY and a proxy that
// refuse every connection and the modules private, which go would download
// from their repositories: there is nothing to download from, so the build
// downloaded nothing. Without the mirror the same build fails.
func TestBuildsOfflineFromTheMirror(t *testing.T) {
	t.Parallel()

	harness := goHarness(t, false)
	dirs := harness.Context.Dirs
	proxy := t.TempDir()

	writeModule(t, proxy, "example.com/num", "v1.0.0", map[string]string{
		"go.mod": "module example.com/num\n\ngo 1.22\n",
		"num.go": "package num\n\nconst Value = 40\n",
	})
	writeModule(t, proxy, "example.com/answer", "v1.0.0", map[string]string{
		"go.mod": "module example.com/answer\n\ngo 1.22\n\nrequire example.com/num v1.0.0\n",
		"answer.go": "package answer\n\nimport \"example.com/num\"\n\n" +
			"func Answer() int { return num.Value + 2 }\n",
	})
	writeFiles(t, dirs.Component, map[string]string{
		"go.mod": "module example.com/gw\n\ngo 1.22\n\nrequire example.com/answer v1.0.0\n",
		"cmd/gw/main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/answer\"\n)\n\n" +
			"func main() { fmt.Println(answer.Answer()) }\n",
	})

	// The user's module cache, which go mod tidy fills as it writes go.sum,
	// and go mod download with the versions of the modules: go mod download
	// takes a module from a proxy by its version, which go mod tidy leaves
	// out.
	userCache := t.TempDir()
	env := map[string]string{
		"GOPROXY": fileURL(proxy), "GOSUMDB": "off", "GOMODCACHE": t.TempDir(),
		"GOFLAGS": "-modcacherw", "GOTOOLCHAIN": "local",
	}
	setup := []string{
		"GOPROXY=" + env["GOPROXY"], "GOSUMDB=off", "GOMODCACHE=" + userCache,
		"GOFLAGS=-modcacherw -mod=mod", "GOTOOLCHAIN=local",
	}

	runIn(t, dirs.Component, setup, goTool, "mod", "tidy")
	runIn(t, dirs.Component, setup, goTool, "mod", "download")

	maps.Copy(harness.Context.Env, env)

	sum, err := os.ReadFile(filepath.Join(dirs.Component, "go.sum"))
	require.NoError(t, err)

	mirror := filepath.Join(t.TempDir(), "mirror", Name)
	bld := validate(t, map[string]any{"package": "./cmd/gw"}, false)

	require.NoError(t, harness.Vendor(t.Context(), bld, mirror), harness.Printed())

	for _, module := range []string{"answer", "num"} {
		for _, ext := range []string{".info", ".mod", ".zip", ".ziphash"} {
			assert.FileExists(t, filepath.Join(mirror, "example.com", module, "@v", "v1.0.0"+ext))
		}
	}

	zipFile := filepath.Join(mirror, "example.com", "answer", "@v", "v1.0.0.zip")

	before, err := os.Stat(zipFile)
	require.NoError(t, err)

	// Nothing to download from but the module cache of the user, and what
	// is in the mirror stays.
	require.NoError(t, os.RemoveAll(proxy))

	harness.Context.Env["GOPROXY"] = "off"
	harness.Context.Env["GOMODCACHE"] = userCache

	require.NoError(t, harness.Vendor(t.Context(), bld, mirror), harness.Printed())

	after, err := os.Stat(zipFile)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())

	// Nothing to download from.
	require.NoError(t, os.RemoveAll(userCache))

	maps.Copy(harness.Context.Env, map[string]string{
		"GOMODCACHE": t.TempDir(), "GOPROXY": "http://127.0.0.1:9", "GOPRIVATE": "example.com",
		"HTTP_PROXY": "http://127.0.0.1:9", "HTTPS_PROXY": "http://127.0.0.1:9",
		"GOSUMDB": "sum.golang.org",
	})

	require.Error(t, harness.Run(t.Context(), bld), "a build of no mirror downloads")

	harness.Context.Vendored = mirror

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, "42", runProgram(t, filepath.Join(dirs.Programs, "gw")))

	data, err := os.ReadFile(filepath.Join(dirs.Component, "go.sum"))
	require.NoError(t, err)
	assert.Equal(t, string(sum), string(data), "go.sum is not written")
}
