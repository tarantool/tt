package configure

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// writeExecutable writes a shell script that prints its name to path,
// creating the directories above it.
func writeExecutable(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho "+path+"\n"), 0o755))
}

// writeProject makes dir a project: a directory holding a manifest.
func writeProject(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
		[]byte("manifest_version = '0.1'\n"), 0o644))
}

// isolate keeps the user and system roots, PATH and the tarantool choice of
// the environment out of a test.
func isolate(t *testing.T) {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")
	t.Setenv("PATH", t.TempDir())
}

func TestValidateCliOpts(t *testing.T) {
	for _, testCase := range []struct {
		input     cmdcontext.CliCtx
		errString string
	}{
		{
			cmdcontext.CliCtx{IntegrityCheckPeriod: 1},
			"need to specify public key in --integrity-check to use --integrity-check-period",
		},
		{
			cmdcontext.CliCtx{IntegrityCheck: "public.pem", IntegrityCheckPeriod: -1},
			"--integrity-check-period must take non-negative value",
		},
		{cmdcontext.CliCtx{IntegrityCheck: "public.pem", IntegrityCheckPeriod: 1}, ""},
		{cmdcontext.CliCtx{}, ""},
	} {
		if testCase.errString != "" {
			require.EqualError(t, ValidateCliOpts(&testCase.input), testCase.errString)
		} else {
			require.NoError(t, ValidateCliOpts(&testCase.input))
		}
	}
}

// TestProjectDir checks that the project directory is the project the
// working directory is in, and the working directory outside a project or
// when the project cannot be looked for.
func TestProjectDir(t *testing.T) {
	isolate(t)

	root := t.TempDir()
	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "a", "b")

	writeProject(t, project)
	require.NoError(t, os.MkdirAll(nested, 0o755))

	for _, strict := range []bool{false, true} {
		t.Chdir(nested)

		dir, err := projectDir(strict)
		require.NoError(t, err)
		assert.Equal(t, project, dir)

		t.Chdir(root)

		dir, err = projectDir(strict)
		require.NoError(t, err)
		assert.Equal(t, root, dir)
	}

	// A relative root fails the project search, which no command but those
	// acting on the project has to stop for; the integrity checks, which
	// check the project, do.
	t.Chdir(nested)
	t.Setenv("TT_USER_ROOT", "relative")

	dir, err := projectDir(false)
	require.NoError(t, err)
	assert.Equal(t, nested, dir)

	_, err = projectDir(true)
	require.ErrorContains(t, err, "failed to find the project: ")
}

// TestCliIntegrityRoot checks that the integrity checks, enabled by a public
// key, need the project to be found.
func TestCliIntegrityRoot(t *testing.T) {
	isolate(t)
	t.Chdir(t.TempDir())
	t.Setenv("TT_USER_ROOT", "relative")

	cmdCtx := cmdcontext.CmdCtx{}
	require.NoError(t, Cli(&cmdCtx))

	cmdCtx.Cli.IntegrityCheck = "public.pem"
	require.ErrorContains(t, Cli(&cmdCtx), "failed to find the project: ")
}

// TestCliIgnoresTTYaml checks that tt.yaml configures nothing: neither a
// broken one nor the bin_dir of a valid one, whose tarantool, tt and tcm are
// not taken.
func TestCliIgnoresTTYaml(t *testing.T) {
	isolate(t)

	dir := t.TempDir()
	t.Chdir(dir)

	for _, name := range []string{"tarantool", "tt", "tcm"} {
		writeExecutable(t, filepath.Join(dir, "bin", name))
	}

	for _, content := range []string{"[broken", "env:\n  bin_dir: bin\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tt.yaml"), []byte(content),
			0o644))

		var cmdCtx cmdcontext.CmdCtx

		require.NoError(t, Cli(&cmdCtx))
		assert.Equal(t, dir, cmdCtx.Cli.ProjectDir)
		assert.Empty(t, cmdCtx.Cli.TarantoolCli.Executable)
		assert.Empty(t, cmdCtx.Cli.TarantoolPrefix)
		assert.Empty(t, cmdCtx.Cli.TcmCli.Executable)
	}
}

// TestDetectTarantool checks that the tarantool is the one of the project
// scope, whose root is its prefix, and the one from PATH otherwise.
func TestDetectTarantool(t *testing.T) {
	isolate(t)

	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	writeExecutable(t, filepath.Join(pathDir, "tarantool"))

	project := t.TempDir()
	writeProject(t, project)

	installed := filepath.Join(project, ".tarantool", "versions", "tarantool",
		"tarantool_3.8.1", "tarantool")
	writeExecutable(t, installed)

	link := filepath.Join(project, ".tarantool", "bin", "tarantool")

	// Without the project link, the tarantool from PATH.
	cli := cmdcontext.CliCtx{ProjectDir: project}
	detectTarantool(&cli)
	assert.Equal(t, filepath.Join(pathDir, "tarantool"), cli.TarantoolCli.Executable)
	assert.Empty(t, cli.TarantoolPrefix)

	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(installed, link))

	cli = cmdcontext.CliCtx{ProjectDir: project}
	detectTarantool(&cli)
	assert.Equal(t, installed, cli.TarantoolCli.Executable)
	assert.Equal(t, filepath.Join(project, ".tarantool"), cli.TarantoolPrefix)

	// TT_USE_SYSTEM_TARANTOOL takes the one from PATH in a project too.
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "1")

	cli = cmdcontext.CliCtx{ProjectDir: project}
	detectTarantool(&cli)
	assert.Equal(t, filepath.Join(pathDir, "tarantool"), cli.TarantoolCli.Executable)
	assert.Empty(t, cli.TarantoolPrefix)

	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	// A link that selects nothing leaves no tarantool, and no error: tt
	// binary, which repairs the link, must still run.
	require.NoError(t, os.Remove(installed))

	cli = cmdcontext.CliCtx{ProjectDir: project}
	detectTarantool(&cli)
	assert.Empty(t, cli.TarantoolCli.Executable)
	assert.Empty(t, cli.TarantoolPrefix)
}

// TestDetectTcm checks that tcm is the one of the project scope, else the one
// from PATH, else none.
func TestDetectTcm(t *testing.T) {
	isolate(t)

	project := t.TempDir()
	assert.Empty(t, detectTcm(project))

	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	writeExecutable(t, filepath.Join(pathDir, "tcm"))

	expected, err := exec.LookPath("tcm")
	require.NoError(t, err)
	assert.Equal(t, expected, detectTcm(project))

	local := filepath.Join(project, ".tarantool", "bin", "tcm")
	writeExecutable(t, local)
	assert.Equal(t, local, detectTcm(project))
}
