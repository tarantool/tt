package search_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestSearchVersionsLocalGit(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramCe, binary.ProgramTt} {
		t.Run(program.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())

			// Both repositories live under a path containing "tarantool".
			// Only the CE product should have its older versions masked.
			localRepo := "tarantool-sources"
			repo := filepath.Join(localRepo, program.String())
			require.NoError(t, os.MkdirAll(repo, 0o755))

			commands := [][]string{
				{"init", "-q"},
				{"-c", "user.email=test@example.com", "-c", "user.name=Test", "commit",
					"-q", "--allow-empty", "-m", "test"},
				{"tag", "v3.1.0"}, {"tag", "v3.0.0"}, {"tag", "v2.11.0"},
			}
			for _, args := range commands {
				cmd := exec.CommandContext(t.Context(), "git", args...)

				cmd.Dir = repo

				output, err := cmd.CombinedOutput()
				require.NoError(t, err, string(output))
			}

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			active := addSearchVersion(t, root, program, "v3.0.0")
			activateSearchVersion(t, root, program, active)
			addSearchVersion(t, root, program, "master")

			ctx := search.SearchCtx{
				Scope:     binary.ScopeProject,
				Program:   program,
				LocalRepo: localRepo,
			}
			output, err := captureSearch(t, func() error { return search.SearchVersionsLocal(ctx) })
			require.NoError(t, err)

			expected := "v3.0.0 [active]\nv3.1.0\nmaster [installed]\n"
			if program == binary.ProgramTt {
				expected = "v2.11.0\n" + expected
			}

			require.Equal(t, expected, output)
		})
	}
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
	require.Equal(t, "debug-gc64-3.3.2-0-r59\ngc64-3.3.2-0-r59\n", output)
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

func TestSearchVersionsRemoteScopeLabels(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_EE_USERNAME", testingUsername)
	t.Setenv("TT_CLI_EE_PASSWORD", testingPassword)

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	active := addSearchVersion(t, root, binary.ProgramTcm, "1.3.0-0-g3857712a")
	activateSearchVersion(t, root, binary.ProgramTcm, active)
	addSearchVersion(t, root, binary.ProgramTcm, "1.3.1-0-g074b5ffa")

	platform := platformInfo{arch: "x86_64", os: binary.OsLinux}
	doer := mockDoer{
		t: t,
		content: map[string][]string{
			"1.3": {
				"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
				"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
			},
		},
		query: "tarantool-cluster-manager/release/linux/amd64",
	}
	ctx := search.NewSearchCtx(&platform, &doer)

	ctx.Program = binary.ProgramTcm
	ctx.Scope = binary.ScopeProject

	output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })
	require.NoError(t, err)
	require.Equal(t, "1.3.0-0-g3857712a [active]\n1.3.1-0-g074b5ffa [installed]\n", output)
}

func TestSearchVersionsCredentialsFile(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		t.Run(program.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("TT_CLI_EE_USERNAME", "ignored-user")
			t.Setenv("TT_CLI_EE_PASSWORD", "ignored-password")

			credentials := "credentials"
			require.NoError(t, os.WriteFile(credentials,
				[]byte(testingUsername+"\n"+testingPassword+"\n"), 0o600))

			tarball := "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"
			release := "1.3"
			expected := "1.3.1-0-g074b5ffa\n"
			query := "tarantool-cluster-manager/release/linux/amd64"

			if program == binary.ProgramEe {
				tarball = "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz"
				release = "3.4"
				expected = "gc64-3.4.0-0-r60\n"
				query = "enterprise/release/linux/x86_64"
			}

			platform := platformInfo{arch: "x86_64", os: binary.OsLinux}
			doer := mockDoer{
				t:       t,
				content: map[string][]string{release: {tarball}},
				query:   query,
			}
			ctx := search.NewSearchCtx(&platform, &doer)

			ctx.Program = program
			ctx.Scope = binary.ScopeProject
			ctx.CredentialsFile = credentials

			output, err := captureSearch(t, func() error { return search.SearchVersions(ctx) })
			require.NoError(t, err)
			require.Equal(t, expected, output)
		})
	}
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
