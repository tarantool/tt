//go:build integration

package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/cli/manifest"
)

// integrationFlags derives real toolchain flags with includeDir wired in so the
// header preflight passes and cc receives -I<includeDir>. includeDir comes from
// requireHostToolchain, so it points at the host's real Tarantool headers.
func integrationFlags(includeDir string) lrbuild.Flags {
	cfg := luarocks.Config{}
	cfg.Tarantool.IncludeDir = includeDir

	return lrbuild.DeriveFlags(cfg)
}

// writeSource writes body to name under dir, failing the test on error.
func writeSource(t *testing.T, dir, name, body string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
}

func TestCcCompilesSingleSource(t *testing.T) {
	tc := requireHostToolchain(t)

	cwd := t.TempDir()
	out := t.TempDir()

	writeSource(t, cwd, "answer.c", "int answer(void) { return 42; }\n")

	flags := integrationFlags(tc.IncludeDir)
	b := manifest.Build{Backend: BackendC, Module: "answer", Sources: []string{"answer.c"}}

	err := ccBackend{flags: flags}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "answer"+flags.Ext))
}

func TestCcCompilesMultiSource(t *testing.T) {
	tc := requireHostToolchain(t)

	cwd := t.TempDir()
	out := t.TempDir()

	writeSource(t, cwd, "part_a.c", "int a(void) { return 1; }\n")
	// part_b references a from part_a, so a passing link proves both objects
	// end up in one .so; the prototype avoids an implicit-declaration error.
	writeSource(t, cwd, "part_b.c", "int a(void);\nint b(void) { return a() + 1; }\n")

	flags := integrationFlags(tc.IncludeDir)
	b := manifest.Build{
		Backend: BackendLuaC,
		Module:  "parts",
		Sources: []string{"part_a.c", "part_b.c"},
	}

	err := ccBackend{flags: flags}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "parts"+flags.Ext))
}

// TestCcCompilesAgainstTarantoolHeaders proves the include-dir wiring works
// against the real Tarantool C module API: the source includes <module.h> and
// exports a luaopen_ entry point, so it compiles only when -I<IncludeDir>
// actually resolves the headers requireHostToolchain validated. This is the
// payoff of checking the headers exist — a compile a stub source cannot do.
func TestCcCompilesAgainstTarantoolHeaders(t *testing.T) {
	tc := requireHostToolchain(t)

	cwd := t.TempDir()
	out := t.TempDir()

	src := "#include <module.h>\n" +
		"int luaopen_headercheck(lua_State *L) { (void)L; return 0; }\n"
	writeSource(t, cwd, "headercheck.c", src)

	flags := integrationFlags(tc.IncludeDir)
	b := manifest.Build{
		Backend: BackendLuaC,
		Module:  "headercheck",
		Sources: []string{"headercheck.c"},
	}

	err := ccBackend{flags: flags}.Run(context.Background(), b, cwd, Env{OutputDir: out})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(out, "headercheck"+flags.Ext))
}
