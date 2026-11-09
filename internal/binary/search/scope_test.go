package search_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

func captureSearch(t *testing.T, run func() error) (string, error) {
	t.Helper()

	output, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)

	defer func() { require.NoError(t, output.Close()) }()

	original := os.Stdout

	os.Stdout = output

	defer func() { os.Stdout = original }()

	searchErr := run()

	_, err = output.Seek(0, io.SeekStart)
	require.NoError(t, err)

	content, err := io.ReadAll(output)
	require.NoError(t, err)

	return string(content), searchErr
}

func addSearchVersion(t *testing.T, root string, program binary.Program, version string) string {
	t.Helper()

	binaryPath := filepath.Join(root, "versions", program.String(),
		program.String()+"_"+version, program.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(binaryPath), 0o755))
	require.NoError(t, os.WriteFile(binaryPath, nil, 0o755))

	return binaryPath
}

func activateSearchVersion(t *testing.T, root string, program binary.Program, target string) {
	t.Helper()

	activePath := filepath.Join(root, "bin", program.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(activePath), 0o755))
	require.NoError(t, os.Symlink(target, activePath))
}

func TestSearchVersionsLocalScopes(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	projectRoot, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	userRoot, err := binary.ScopeUser.ArtifactPath()
	require.NoError(t, err)

	repo := filepath.Join(project, "archives")
	require.NoError(t, os.MkdirAll(repo, 0o755))

	versions := []string{"1.2.0-0-gaaaaaaa", "1.3.0-0-gbbbbbbb", "1.4.0-0-gccccccc"}
	for _, version := range versions {
		filename := "tcm-" + version + ".linux.amd64.tar.gz"
		require.NoError(t, os.WriteFile(filepath.Join(repo, filename), nil, 0o644))
	}

	addSearchVersion(t, projectRoot, binary.ProgramTcm, versions[0])

	active := addSearchVersion(t, projectRoot, binary.ProgramTcm, versions[1])
	relative, err := filepath.Rel(filepath.Join(projectRoot, "bin"), active)
	require.NoError(t, err)
	activateSearchVersion(t, projectRoot, binary.ProgramTcm, relative)
	addSearchVersion(t, projectRoot, binary.ProgramTcm, "9.9.9-0-gddddddd")

	active = addSearchVersion(t, userRoot, binary.ProgramTcm, versions[0])
	activateSearchVersion(t, userRoot, binary.ProgramTcm, active)
	addSearchVersion(t, userRoot, binary.ProgramTcm, versions[2])

	ctx := search.SearchCtx{
		Scope:     binary.ScopeProject,
		Program:   binary.ProgramTcm,
		LocalRepo: "archives",
	}
	output, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t,
		versions[0]+" [installed]\n"+versions[1]+" [active]\n"+versions[2]+"\n", output)

	ctx.Scope = binary.ScopeUser
	output, err = captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t,
		versions[0]+" [active]\n"+versions[1]+"\n"+versions[2]+" [installed]\n", output)

	child := filepath.Join(project, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	t.Chdir(child)

	ctx.Scope = binary.ScopeProject
	ctx.LocalRepo = "../archives"
	output, err = captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, strings.Join(versions, "\n")+"\n", output)
	require.NoDirExists(t, filepath.Join(child, ".tarantool"))
}

func TestSearchVersionsLocalPreservesBundleFilters(t *testing.T) {
	t.Chdir(t.TempDir())

	repo := t.TempDir()
	for _, name := range []string{
		"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
		"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), nil, 0o644))
	}

	ctx := search.SearchCtx{
		Scope:          binary.ScopeProject,
		Program:        binary.ProgramEe,
		LocalRepo:      repo,
		Filter:         search.SearchRelease,
		ReleaseVersion: "99.0",
		DevBuilds:      true,
	}
	output, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, "dev_debug-gc64-3.3.2-0-r59\ndev_gc64-3.3.2-0-r59\n", output)
}

func TestSearchVersionsLocalEmpty(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramCe, binary.ProgramTt, binary.ProgramEe, binary.ProgramTcm,
	} {
		t.Run(program.String(), func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)

			ctx := search.SearchCtx{
				Scope:     binary.ScopeProject,
				Program:   program,
				LocalRepo: "missing",
			}
			output, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
			require.NoError(t, err)
			require.Empty(t, output)
			require.NoDirExists(t, filepath.Join(project, ".tarantool"))

			ctx.LocalRepo = ""
			require.ErrorContains(t, search.SearchVersionsLocal(ctx),
				"local repository directory is required")
		})
	}
}

func TestSearchVersionsInvalidScope(t *testing.T) {
	ctx := search.SearchCtx{
		Scope:     binary.ScopeUnknown,
		Program:   binary.ProgramEe,
		LocalRepo: t.TempDir(),
	}
	require.ErrorIs(t, search.SearchVersions(ctx), binary.ErrUnknownScope)
	require.ErrorIs(t, search.SearchVersionsLocal(ctx), binary.ErrUnknownScope)
}

// scopeTestSource lists the archives it holds and records the queries.
type scopeTestSource struct {
	artifacts []bundle.Artifact
	queries   []bundle.Query
}

func (*scopeTestSource) Name() string { return "test-source" }

func (*scopeTestSource) Products() []string {
	return []string{
		binary.ProgramEe.String(), binary.ProgramSDK.String(), binary.ProgramTcm.String(),
	}
}

func (s *scopeTestSource) List(_ context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	s.queries = append(s.queries, query)

	return s.artifacts, nil
}

func (*scopeTestSource) Fetch(context.Context, bundle.Artifact, io.Writer) error {
	return nil
}

// useBundleSources makes sources those of the process for the test.
func useBundleSources(t *testing.T, sources ...bundle.Source) {
	t.Helper()

	previous := binary.BundleSources()

	binary.SetBundleSources(sources)
	t.Cleanup(func() { binary.SetBundleSources(previous) })
}

func TestSearchVersionsRemoteScopeLabels(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	active := addSearchVersion(t, root, binary.ProgramTcm, "1.3.0-0-g3857712a")
	activateSearchVersion(t, root, binary.ProgramTcm, active)
	addSearchVersion(t, root, binary.ProgramTcm, "1.3.1-0-g074b5ffa")

	source := &scopeTestSource{artifacts: []bundle.Artifact{
		{Version: "1.3.1-0-g074b5ffa", Name: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"},
		{Version: "1.3.0-0-g3857712a", Name: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz"},
	}}
	useBundleSources(t, source)

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramTcm
	ctx.Scope = binary.ScopeProject
	ctx.ReleaseVersion = "1.3"
	ctx.DevBuilds = true

	output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "dev_1.3.0-0-g3857712a\ndev_1.3.1-0-g074b5ffa\n", output,
		"a development build is not the installed release")
	require.Equal(t, []bundle.Query{{
		Product: "tcm", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Channel: bundle.ChannelDev, Version: "1.3",
	}}, source.queries)

	ctx.DevBuilds = false

	output, err = captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "1.3.0-0-g3857712a [active]\n1.3.1-0-g074b5ffa [installed]\n", output)
}

func TestSearchVersionsRemoteFlavors(t *testing.T) {
	t.Chdir(t.TempDir())

	useBundleSources(t, &scopeTestSource{artifacts: []bundle.Artifact{
		{Version: "3.4.0-0-r60", Flavor: []string{"gc64"}},
		{Version: "3.4.0-0-r60", Flavor: []string{"debug", "gc64"}},
		{Version: "2.11.1-0-r563", Flavor: []string{"gc64"}},
		{Version: "not-a-version"},
	}})

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramEe
	ctx.Scope = binary.ScopeProject

	output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "gc64-3.4.0-0-r60\n", output,
		"release builds of Tarantool 3 only, named with their flavours")

	ctx.Filter = search.SearchDebug

	output, err = captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "debug-gc64-3.4.0-0-r60\n", output)
}

func TestSearchVersionsRemoteNothingFound(t *testing.T) {
	t.Chdir(t.TempDir())
	useBundleSources(t, &scopeTestSource{artifacts: nil})

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramTcm
	ctx.Scope = binary.ScopeProject

	require.ErrorContains(t, search.SearchVersions(ctx),
		"failed to fetch bundle info for tcm: no packages found for this OS or release version")
}

func TestSearchVersionsWithoutSource(t *testing.T) {
	t.Chdir(t.TempDir())
	useBundleSources(t)

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramEe
	ctx.Scope = binary.ScopeProject

	err := search.SearchVersions(ctx)
	require.ErrorIs(t, err, binary.ErrNoBundleSource)
	require.ErrorContains(t, err, "use --local-dir")
}

func TestSearchVersionsLocalActiveIdentity(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	repo := t.TempDir()
	version := "gc64-3.3.2-0-r59"
	archive := "tarantool-enterprise-sdk-" + version + ".linux.x86_64.tar.gz"
	require.NoError(t, os.WriteFile(filepath.Join(repo, archive), nil, 0o644))

	installedPath := addSearchVersion(t, root, binary.ProgramEe, "3.3.2")
	cePath := addSearchVersion(t, root, binary.ProgramCe, version)
	activateSearchVersion(t, root, binary.ProgramCe, cePath)

	ctx := search.SearchCtx{
		Scope:     binary.ScopeProject,
		Program:   binary.ProgramEe,
		LocalRepo: repo,
	}
	output, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, version+" [installed]\n", output,
		"CE and EE executable basenames must not identify the same installation")

	otherPath := addSearchVersion(t, t.TempDir(), binary.ProgramEe, "3.3.2")
	require.NoError(t, os.Remove(filepath.Join(root, "bin", "tarantool")))
	activateSearchVersion(t, root, binary.ProgramEe, otherPath)

	output, err = captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, version+" [installed]\n", output,
		"the same version in another scope must not activate this installation")

	require.NoError(t, os.Remove(otherPath))

	output, err = captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, version+" [installed]\n", output, "a dangling link is inactive")

	require.NoError(t, os.Remove(installedPath))
	require.NoError(t, os.Mkdir(installedPath, 0o755))

	output, err = captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.NoError(t, err)
	require.Equal(t, version+"\n", output, "a directory is not an installed executable")
}
