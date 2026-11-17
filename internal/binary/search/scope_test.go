package search_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log/logtest"
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

var errTestListing = errors.New("connection reset")

// prebuiltTestSource is a source of one product: tarantool lists its
// builds for the host, as rws does, and tt the tags of its releases, as
// the releases of tt on GitHub do.
type prebuiltTestSource struct {
	product   string
	artifacts []bundle.Artifact
	releases  []string
	err       error
	// queries are what the source was asked.
	queries []bundle.Query
}

func (*prebuiltTestSource) Name() string { return "test-source" }

func (s *prebuiltTestSource) Products() []string { return []string{s.product} }

func (s *prebuiltTestSource) List(_ context.Context, query bundle.Query) (
	[]bundle.Artifact, error,
) {
	s.queries = append(s.queries, query)

	return s.artifacts, s.err
}

func (*prebuiltTestSource) Fetch(context.Context, bundle.Artifact, io.Writer) error {
	return nil
}

// releaseTestSource is a prebuiltTestSource that lists the tags of its
// releases.
type releaseTestSource struct {
	*prebuiltTestSource
}

func (s releaseTestSource) Releases(context.Context) ([]string, error) {
	return s.releases, s.err
}

// prebuiltTestSources are a source of tarantool with a release tt no longer
// installs, three for the host and a debug build, and one of tt.
func prebuiltTestSources() (*prebuiltTestSource, releaseTestSource) {
	tarantool := &prebuiltTestSource{
		product: binary.ProgramCe.String(),
		artifacts: []bundle.Artifact{
			{Version: "2.11.5"}, {Version: "3.7.1"}, {Version: "3.9.0"}, {Version: "3.8.0"},
			{Version: "3.9.0", Flavor: []string{"debug"}},
		},
	}
	tt := releaseTestSource{&prebuiltTestSource{
		product:  binary.ProgramTt.String(),
		releases: []string{"v3.0.0-alpha.2", "nightly", "v2.15.2", "v2.15.1"},
	}}

	return tarantool, tt
}

// searchWithoutGit runs the search of program in the project scope of the
// current directory with an empty PATH, and returns what it prints and the
// warnings it logs.
func searchWithoutGit(t *testing.T, program binary.Program) (string, []string, error) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())

	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx := search.NewSearchCtx()

	ctx.Program = program
	ctx.Scope = binary.ScopeProject

	output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })

	var warnings []string

	for _, record := range recorder.Records() {
		if record.Level == slog.LevelWarn {
			warnings = append(warnings, record.Message)
		}
	}

	return output, warnings, err
}

// TestSearchVersionsWithoutGit lists the releases the source of prebuilt
// builds has for the host on a host without git, marked installed and
// active as the Git tags are, and warns that they are all it lists.
func TestSearchVersionsWithoutGit(t *testing.T) {
	platform := runtime.GOOS + "/" + runtime.GOARCH

	for _, test := range []struct {
		program binary.Program
		want    string
	}{
		{program: binary.ProgramCe, want: "3.7.1 [installed]\n3.8.0 [active]\n3.9.0\n"},
		{program: binary.ProgramTt, want: "v2.15.1\nv2.15.2 [active]\nv3.0.0-alpha.2\n"},
	} {
		t.Run(test.program.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			tarantool, tt := prebuiltTestSources()
			useBundleSources(t, tarantool, tt)

			if test.program == binary.ProgramCe {
				addSearchVersion(t, root, test.program, "3.7.1")
				activateSearchVersion(t, root, test.program,
					addSearchVersion(t, root, test.program, "3.8.0"))
			} else {
				activateSearchVersion(t, root, test.program,
					addSearchVersion(t, root, test.program, "v2.15.2"))
			}

			output, warnings, err := searchWithoutGit(t, test.program)
			require.NoError(t, err)
			require.Equal(t, test.want, output)
			require.Equal(t, []string{"git is not found, so only the releases of " +
				test.program.String() + " with a prebuilt build in test-source for " +
				platform + " are listed; install git to list every tag"}, warnings)

			if test.program == binary.ProgramCe {
				require.Equal(t, []bundle.Query{binary.NewBundleQuery(test.program, false, "")},
					tarantool.queries, "every build for the host is listed")
			}
		})
	}
}

// TestSearchVersionsWithoutGitFails says that git is required, the reason
// the source of prebuilt builds does not do, and to install git, when the
// source has nothing for the host, fails or is not there.
func TestSearchVersionsWithoutGitFails(t *testing.T) {
	const required = "'git' is required for 'tt binary search' to list the versions of "

	for _, test := range []struct {
		name    string
		program binary.Program
		change  func(tarantool, tt *prebuiltTestSource)
		// noSources is a build of tt without sources of prebuilt builds.
		noSources bool
		want      string
	}{
		{
			name: "no build for the host", program: binary.ProgramCe,
			change: func(tarantool, _ *prebuiltTestSource) { tarantool.artifacts = nil },
			want: required + "tarantool: test-source has no prebuilt tarantool for " +
				runtime.GOOS + "/" + runtime.GOARCH + "; install git",
		},
		{
			name: "listing failure", program: binary.ProgramCe,
			change: func(tarantool, _ *prebuiltTestSource) { tarantool.err = errTestListing },
			want: required + "tarantool: list tarantool archives in test-source: " +
				"connection reset; install git",
		},
		{
			name: "tt releases failure", program: binary.ProgramTt,
			change: func(_, tt *prebuiltTestSource) { tt.err = errTestListing },
			want:   required + "tt: test-source: connection reset; install git",
		},
		{
			name: "no source", program: binary.ProgramCe, noSources: true,
			want: required + "tarantool: install git",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			tarantool, tt := prebuiltTestSources()
			if test.change != nil {
				test.change(tarantool, tt.prebuiltTestSource)
			}

			if test.noSources {
				useBundleSources(t)
			} else {
				useBundleSources(t, tarantool, tt)
			}

			output, warnings, err := searchWithoutGit(t, test.program)
			require.EqualError(t, err, test.want)
			require.Empty(t, output, "a failed search prints no versions")
			require.Empty(t, warnings)
		})
	}
}

// TestSearchVersionsWithGit lists the Git tags and master where git is,
// as before, and does not ask the source of prebuilt builds.
func TestSearchVersionsWithGit(t *testing.T) {
	t.Chdir(t.TempDir())

	commands := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(commands, "git"), []byte(`#!/bin/sh
if [ "$1 $2 $3" != "ls-remote --tags --refs" ]; then exit 1; fi
printf 'abcdef0\trefs/tags/%s\n' 2.11.5 3.7.1 3.9.1-entrypoint 3.9.1
`), 0o755))
	t.Setenv("PATH", commands)

	tarantool, tt := prebuiltTestSources()
	useBundleSources(t, tarantool, tt)

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramCe
	ctx.Scope = binary.ScopeProject

	output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "3.7.1\n3.9.1\nmaster\n", output,
		"a release with no prebuilt build is listed, a development tag is not")
	require.Empty(t, tarantool.queries)
}

// TestSearchVersionsLocalWithoutGit says what --local-repo needs git for:
// its tags have no other reader.
func TestSearchVersionsLocalWithoutGit(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())

	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, "tarantool"), 0o755))

	ctx := search.NewSearchCtx()

	ctx.Program = binary.ProgramCe
	ctx.Scope = binary.ScopeProject
	ctx.LocalRepo = repo

	_, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
	require.EqualError(t, err, "failed to search local versions for tarantool: 'git' is "+
		"required for 'tt binary search' to read the tags of "+filepath.Join(repo, "tarantool")+
		": install git")
}
