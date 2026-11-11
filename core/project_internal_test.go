package core

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
)

// projectManifest is the manifest of the project the tests of Project work
// on: a package with one Lua component and no dependencies.
const projectManifest = `manifest_version = '0.1'

# The application of the tests of the core.
[package]
name = 'app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.lua]
path = '.'
`

// testRockspec is the rockspec of a rock of rockServer, with its name and
// version to fill in: a Lua module and a program, nothing to build.
const testRockspec = `rockspec_format = "3.0"
package = %q
version = %q
source = {
   url = "file:///nonexistent/source.tar.gz",
}
description = {
   summary = "A rock of the tests of the core",
   license = "MIT",
}
build = {
   type = "none",
}
deploy = {
   wrap_bin_scripts = false,
}
`

// testRockManifest is the rock_manifest of a rock of rockServer, with its
// name three times and its rockspec's file name to fill in. The checksums
// are not checked on install, which takes its own of what it deploys.
const testRockManifest = `rock_manifest = {
   bin = {
      [%q] = "00000000000000000000000000000000",
   },
   lua = {
      [%q] = "00000000000000000000000000000000",
   },
   [%q] = "00000000000000000000000000000000",
}
`

// rockServer writes a rock server into a new directory and returns it: the
// rocks named in rocks at each of their versions, each a binary rock for
// all platforms with the Lua module <name>.lua and the program bin/<name>,
// and the manifest that indexes them, as luarocks-admin make_manifest
// writes it. No rocks make an empty server.
func rockServer(t *testing.T, rocks map[string][]string) string {
	t.Helper()

	dir := t.TempDir()

	var index strings.Builder

	index.WriteString("commands = {}\nmodules = {}\nrepository = {\n")

	for name, versions := range rocks {
		_, _ = fmt.Fprintf(&index, "   [%q] = {\n", name)

		for _, version := range versions {
			_, _ = fmt.Fprintf(&index, "      [%q] = {\n         { arch = \"all\" },\n      },\n",
				version)

			writeRock(t, filepath.Join(dir, name+"-"+version+".all.rock"), name, version)
		}

		index.WriteString("   },\n")
	}

	index.WriteString("}\n")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest"), []byte(index.String()),
		0o644))

	return dir
}

// writeRock writes the binary rock of name at version to path: a zip of its
// rockspec, its rock_manifest, lua/<name>.lua, which returns the version,
// and the program bin/<name>, which prints it.
func writeRock(t *testing.T, path, name, version string) {
	t.Helper()

	specName := name + "-" + version + ".rockspec"

	entries := []struct {
		name    string
		mode    os.FileMode
		content string
	}{
		{specName, 0o644, fmt.Sprintf(testRockspec, name, version)},
		{"rock_manifest", 0o644, fmt.Sprintf(testRockManifest, name, name+".lua", specName)},
		{"lua/" + name + ".lua", 0o644, "return '" + version + "'\n"},
		{"bin/" + name, 0o755, "#!/bin/sh\necho " + version + "\n"},
	}

	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)

		file, err := writer.CreateHeader(header)
		require.NoError(t, err)

		_, err = io.WriteString(file, entry.content)
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(path, archive.Bytes(), 0o644))
}

// packageProject makes the working directory a project with the manifest
// content, configures tt with a Tarantool and with an empty rock server as
// TT_REGISTRIES, so that nothing is resolved from the network, and returns
// the project directory and the Project of a module.
func packageProject(t *testing.T, content string) (string, sdk.Project) {
	t.Helper()

	restoreProcess(t)

	dir := t.TempDir()
	t.Chdir(dir)

	if content != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
			[]byte(content), 0o644))
	}

	t.Setenv("TT_REGISTRIES", rockServer(t, nil))

	// Resolution runs tarantool for its version and needs its prefix; a
	// script answering --version is enough for rocks with nothing to build.
	executable := filepath.Join(t.TempDir(), "tarantool")
	writeTarantool(t, executable, "Tarantool 3.8.1-0-g1234567")

	ctx := cmd.GetCmdCtxPtr()

	ctx.Cli.TarantoolCli = cmdcontext.TarantoolCli{Executable: executable}
	ctx.Cli.TarantoolPrefix = t.TempDir()

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)

	return dir, svc.Project()
}

// readProject parses the manifest and the lock of the project in dir.
func readProject(t *testing.T, dir string) ([]byte, *manifest.Manifest, *manifest.Lock) {
	t.Helper()

	source, err := os.ReadFile(filepath.Join(dir, "app.manifest.toml"))
	require.NoError(t, err)

	man, _, err := manifest.ParseManifest(source)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "app.manifest.lock"))
	require.NoError(t, err)

	lock, err := manifest.ParseLock(data)
	require.NoError(t, err)

	return source, man, lock
}

// lockedVersions maps the rocks of closure to their locked versions.
func lockedVersions(closure []manifest.LockDependency) map[string]string {
	versions := map[string]string{}
	for _, dep := range closure {
		versions[dep.Name] = dep.Version
	}

	return versions
}

// TestProjectAddDependency checks that a module declares a dependency as tt
// package add and tt emmylua prepare do: a long-form dev dependency from a
// rock server, which a second call leaves as it is, and a short-form one,
// whose constraint a second call rewrites, the lock re-resolved each time
// with what it held kept.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectAddDependency(t *testing.T) {
	registry := rockServer(t, map[string][]string{
		"tool": {"1.0.0-1", "1.1.0-1"},
		"lib":  {"2.0.0-1", "2.1.0-1"},
	})

	dir, project := packageProject(t, projectManifest)

	tool := sdk.Dependency{
		Name: "tool", Constraint: "<1.1", Registry: registry, Dev: true, Registries: nil,
	}

	change, err := project.AddDependency(t.Context(), tool)
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{
		Existed:  false,
		Previous: "",
		Moves:    []sdk.DependencyMove{{Name: "tool", From: "", To: "1.0.0-1"}},
	}, change)

	source, man, lock := readProject(t, dir)
	assert.Contains(t, string(source), "# The application of the tests of the core.",
		"the rest of the manifest is kept")
	assert.Equal(t, manifest.Dependency{Source: "registry", Version: "<1.1", Registry: registry},
		man.DevDependencies["tool"])
	assert.NotContains(t, man.Dependencies, "tool")
	assert.Equal(t, map[string]string{"tool": "1.0.0-1"}, lockedVersions(lock.DevDependencies))

	// A declaration with a registry is someone's choice: it stays.
	tool.Constraint = ">=1.1"

	change, err = project.AddDependency(t.Context(), tool)
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{
		Existed:  true,
		Previous: "<1.1",
		Moves:    []sdk.DependencyMove{},
	}, change)

	unchanged, _, _ := readProject(t, dir)
	assert.Equal(t, string(source), string(unchanged))

	// The short form resolves from the servers given, and keeps what the lock
	// holds: tool stays at 1.0.0-1.
	lib := sdk.Dependency{
		Name: "lib", Constraint: "", Registry: "", Dev: false, Registries: []string{registry},
	}

	change, err = project.AddDependency(t.Context(), lib)
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{
		Existed:  false,
		Previous: "",
		Moves:    []sdk.DependencyMove{{Name: "lib", From: "", To: "2.1.0-1"}},
	}, change)

	_, man, lock = readProject(t, dir)
	assert.Equal(t, manifest.Dependency{Source: "registry", Version: "*"}, man.Dependencies["lib"])
	assert.Equal(t, map[string]string{"tool": "1.0.0-1"}, lockedVersions(lock.DevDependencies))

	// Without a registry a declaration has its constraint rewritten.
	lib.Constraint = "<2.1"

	change, err = project.AddDependency(t.Context(), lib)
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{
		Existed:  true,
		Previous: "*",
		Moves:    []sdk.DependencyMove{{Name: "lib", From: "2.1.0-1", To: "2.0.0-1"}},
	}, change)

	_, man, _ = readProject(t, dir)
	assert.Equal(t, manifest.Dependency{Source: "registry", Version: "<2.1"},
		man.Dependencies["lib"])

	// Without servers of its own the resolution asks TT_REGISTRIES, the
	// empty server, which has none of the short-form rocks. The declaration
	// is on disk already, and the error says so.
	_, err = project.AddDependency(t.Context(), sdk.Dependency{Name: "other"})
	require.ErrorIs(t, err, deps.ErrManifestEdited)
	require.ErrorContains(t, err, "rock not found on any server")

	_, man, _ = readProject(t, dir)
	assert.Contains(t, man.Dependencies, "other")
}

// TestProjectInstall checks that a module installs the locked rocks into
// .rocks/ as tt package fetch does, from the servers it gives, and that a
// project with no lock is an error.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectInstall(t *testing.T) {
	registry := rockServer(t, map[string][]string{
		"tool": {"1.0.0-1", "1.1.0-1"},
		"lib":  {"2.0.0-1"},
	})

	dir, project := packageProject(t, projectManifest)

	err := project.Install(t.Context(), sdk.InstallOptions{Registries: nil})
	require.ErrorContains(t, err, "app.manifest.lock")

	_, err = project.AddDependency(t.Context(), sdk.Dependency{
		Name: "tool", Constraint: "<1.1", Registry: registry, Dev: true, Registries: nil,
	})
	require.NoError(t, err)

	_, err = project.AddDependency(t.Context(), sdk.Dependency{
		Name: "lib", Constraint: "", Registry: "", Dev: false, Registries: []string{registry},
	})
	require.NoError(t, err)

	// The short-form lib is on no server TT_REGISTRIES names.
	err = project.Install(t.Context(), sdk.InstallOptions{Registries: nil})
	require.ErrorContains(t, err, "installing lib 2.0.0-1")

	mirror := sdk.InstallOptions{Registries: []string{registry}}
	require.NoError(t, project.Install(t.Context(), mirror))

	program := filepath.Join(dir, ".rocks", "bin", "tool")
	content, err := os.ReadFile(program)
	require.NoError(t, err, "the program of the dev dependency is installed")
	assert.Equal(t, "#!/bin/sh\necho 1.0.0-1\n", string(content), "at the locked version")
	assert.FileExists(t, filepath.Join(dir, ".rocks", "bin", "lib"),
		"the dependency of the product is installed")

	// Again, from the lock as it is.
	require.NoError(t, project.Install(t.Context(), mirror))
	assert.FileExists(t, program)

	// Into a tree cleared, by a tt with no Tarantool, which rocks that build
	// nothing do not need.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, ".rocks")))

	cmd.GetCmdCtxPtr().Cli.TarantoolCli = cmdcontext.TarantoolCli{Executable: ""}

	require.NoError(t, project.Install(t.Context(), mirror))

	content, err = os.ReadFile(program)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho 1.0.0-1\n", string(content))
}

// TestProjectErrors checks the failures of a project with no manifest and of
// a tt with no Tarantool.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectErrors(t *testing.T) {
	dir, project := packageProject(t, "")

	dep := sdk.Dependency{Name: "tool", Constraint: "", Registry: "", Dev: false, Registries: nil}

	_, err := project.AddDependency(t.Context(), dep)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "app.manifest.toml")
	require.NotErrorIs(t, err, sdk.ErrNotFound)

	err = project.Install(t.Context(), sdk.InstallOptions{Registries: nil})
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "app.manifest.toml")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
		[]byte(projectManifest), 0o644))

	// The error tt package add gives, which is sdk.ErrNotFound too.
	cmd.GetCmdCtxPtr().Cli.TarantoolCli = cmdcontext.TarantoolCli{Executable: ""}

	_, err = project.AddDependency(t.Context(), dep)
	require.ErrorIs(t, err, cmd.ErrNoTarantool)
	require.ErrorIs(t, err, sdk.ErrNotFound)
	require.EqualError(t, err, cmd.ErrNoTarantool.Error())

	// Install, as tt package fetch, goes without a Tarantool: it fails on
	// the lock that is not there.
	err = project.Install(t.Context(), sdk.InstallOptions{Registries: nil})
	require.NotErrorIs(t, err, cmd.ErrNoTarantool)
	require.NotErrorIs(t, err, sdk.ErrNotFound)
	require.ErrorContains(t, err, "app.manifest.lock")

	source, err := os.ReadFile(filepath.Join(dir, "app.manifest.toml"))
	require.NoError(t, err)
	assert.Equal(t, projectManifest, string(source), "nothing is declared")
}
