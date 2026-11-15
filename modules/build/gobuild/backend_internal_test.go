package gobuild

import (
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
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

// writeTool writes the shell script body as the program name in dir. The
// script is written aside and copied with no fork in between, so that
// starting it is not refused while parallel tests start processes.
func writeTool(t *testing.T, dir, name, body string) {
	t.Helper()

	src := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"+body), 0o600))
	require.NoError(t, os.Chmod(src, 0o700))
	require.NoError(t, build.CopyFile(filepath.Join(dir, name), src))
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

	fields, err := backend.Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{"package", "name", "tags", "ldflags", "cgo"}, fields)
}

func TestPreflight(t *testing.T) {
	t.Parallel()

	t.Run("static for darwin", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, Backend(), component())
		writeTool(t, harness.Context.Dirs.Tools, "go", "")

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
		writeTool(t, harness.Context.Dirs.Tools, "go", "")

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

	writeTool(t, facts.Dirs.Tools, "go", `printf '%s\n' "$PWD" "$@" > "`+log+`.args"
printf '%s\n' "$GOOS" "$GOARCH" "$CGO_ENABLED" "$CC" > "`+log+`.env"
`)

	facts.Target = build.Target{OS: "linux", Arch: "arm64"}
	facts.Env["GOOS"] = "darwin"
	facts.Env["CGO_ENABLED"] = "1"
	facts.Env["CC"] = "musl-gcc"

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
	assert.Equal(t, "linux\narm64\n0\nmusl-gcc\n", string(env))

	require.NoError(t, harness.Run(t.Context(), validate(t, map[string]any{"cgo": true}, false)))

	env, err = os.ReadFile(log + ".env")
	require.NoError(t, err)
	assert.Equal(t, "linux\narm64\n1\nmusl-gcc\n", string(env))
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
