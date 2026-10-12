package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestResolveInstallVersionSources(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramTt, binary.ProgramCe} {
		t.Run(program.String(), func(t *testing.T) {
			localRepoDir := t.TempDir()
			repository := filepath.Join(localRepoDir, program.String())
			require.NoError(t, os.Mkdir(repository, 0o755))

			git := func(args ...string) string {
				t.Helper()

				cmd := exec.CommandContext(t.Context(), "git", args...)

				cmd.Dir = repository

				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)

				return string(output)
			}
			commit := func(contents string) string {
				t.Helper()

				require.NoError(t, os.WriteFile(filepath.Join(repository, "README"),
					[]byte(contents), 0o644))
				git("add", "README")
				git("-c", "user.name=Version Test", "-c", "user.email=version@example.com",
					"commit", "-m", contents)

				return strings.TrimSpace(git("rev-parse", "HEAD"))
			}

			git("init", "--initial-branch=master")

			releaseCommit := commit("release\n")
			tag := "3.8.1"
			annotatedTag := "3.8.0"
			prereleaseTag := "4.0.0-rc1"

			if program == binary.ProgramTt {
				tag = "v" + tag
				annotatedTag = "v" + annotatedTag
				prereleaseTag = "v" + prereleaseTag
			}

			git("tag", tag)
			git("-c", "user.name=Version Test", "-c", "user.email=version@example.com",
				"tag", "-a", annotatedTag, "-m", "annotated release")

			annotatedTagHash := strings.TrimSpace(git("rev-parse", annotatedTag))

			var numericCommit, numericContents string

			// Generate an actual numeric short SHA to exercise the version-mask ambiguity.
			for index := range 1000 {
				contents := fmt.Sprintf("numeric commit %d\n", index)
				candidate := commit(contents)

				_, err := strconv.ParseUint(candidate[:7], 10, 32)
				if err == nil && candidate[:7] != "0000000" {
					numericCommit, numericContents = candidate, contents
					break
				}
			}

			require.NotEmpty(t, numericCommit)

			commit("prerelease\n")
			git("tag", prereleaseTag)

			masterCommit := commit("master\n")
			pullRequestCommit := commit("pull request\n")
			git("update-ref", "refs/pull/42/head", pullRequestCommit)
			git("reset", "--hard", masterCommit)
			git("branch", "feature")
			git("remote", "add", "origin", repository)

			// Route remote Git operations to this fixture instead of the public repository.
			gitConfig := filepath.Join(t.TempDir(), "gitconfig")
			git("config", "--file", gitConfig, "url.file://"+repository+".insteadOf",
				program.GitRepo())
			t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)

			latestTag := tag
			latestContents := "release\n"

			if program == binary.ProgramTt {
				latestTag = prereleaseTag
				latestContents = "prerelease\n"
			}

			cases := []struct {
				name      string
				requested string
				kind      versionKind
				ref       string
				id        string
				contents  string
			}{
				{
					name: "tag", requested: "3.8.1", kind: versionKindTag,
					ref: tag, id: tag, contents: "release\n",
				},
				{
					name: "version mask", requested: "3", kind: versionKindTag,
					ref: tag, id: tag, contents: "release\n",
				},
				{
					name: "annotated tag", requested: "3.8.0", kind: versionKindTag,
					ref: annotatedTag, id: annotatedTag, contents: "release\n",
				},
				{
					name: "annotated tag object SHA", requested: annotatedTagHash,
					kind: versionKindCommit, ref: annotatedTag, id: annotatedTag,
					contents: "release\n",
				},
				{
					name: "numeric commit", requested: numericCommit[:7], kind: versionKindCommit,
					ref: numericCommit, id: numericCommit[:7], contents: numericContents,
				},
				{
					name: "commit", requested: releaseCommit[:7], kind: versionKindCommit,
					ref: releaseCommit, id: releaseCommit[:7], contents: "release\n",
				},
				{
					name: "master", requested: masterVersion, kind: versionKindMaster,
					ref: masterVersion, id: masterVersion, contents: "master\n",
				},
				{
					name: "pull request", requested: "pr/42", kind: versionKindPullRequest,
					ref: "pr/42", id: pullRequestCommit[:7], contents: "pull request\n",
				},
				{
					name: "uppercase pull request", requested: "PR/42",
					kind: versionKindPullRequest,
					ref:  "pr/42", id: pullRequestCommit[:7], contents: "pull request\n",
				},
				{
					name: "default", requested: "", kind: versionKindTag,
					ref: latestTag, id: latestTag, contents: latestContents,
				},
			}

			for _, local := range []bool{true, false} {
				mode := "remote"
				localPath := ""

				if local {
					mode = "local"
					localPath = localRepoDir
				}

				for _, testCase := range cases {
					t.Run(mode+"/"+testCase.name, func(t *testing.T) {
						installCtx := InstallCtx{
							Program:          program,
							LocalRepo:        localPath,
							requestedVersion: testCase.requested,
						}

						resolved, err := resolveInstallVersion(installCtx)
						require.NoError(t, err)
						assert.Equal(t, testCase.kind, resolved.kind)
						assert.Equal(t, testCase.ref, resolved.ref)
						assert.Equal(t, testCase.id, resolved.name)

						buildPath := t.TempDir()
						logFile, err := os.Create(filepath.Join(t.TempDir(), "git.log"))
						require.NoError(t, err)
						t.Cleanup(func() { require.NoError(t, logFile.Close()) })

						if program == binary.ProgramTt {
							err = downloadTtSource(installCtx, buildPath,
								resolved, logFile)
						} else {
							err = downloadTarantoolSource(installCtx, buildPath,
								resolved, logFile)
						}

						require.NoError(t, err)

						contents, err := os.ReadFile(filepath.Join(buildPath, "README"))
						require.NoError(t, err)
						assert.Equal(t, testCase.contents, string(contents))
					})
				}

				failures := []struct {
					name      string
					requested string
					wantErr   error
					wantMsg   string
				}{
					{
						name: "unsupported branch", requested: "feature",
						wantErr: errHashHasAWrongFormat,
					},
					{
						name: "numeric commit fallback", requested: "0000000",
						wantErr: errUnableToGetHashInfo,
					},
					{
						name: "invalid tag mask", requested: "18446744073709551616",
						wantMsg: "can't parse Major",
					},
				}

				for _, testCase := range failures {
					t.Run(mode+"/"+testCase.name, func(t *testing.T) {
						installCtx := InstallCtx{
							Program:          program,
							LocalRepo:        localPath,
							requestedVersion: testCase.requested,
						}

						_, err := resolveInstallVersion(installCtx)
						if testCase.wantErr != nil {
							require.ErrorIs(t, err, testCase.wantErr)
						} else {
							require.ErrorContains(t, err, testCase.wantMsg)
						}
					})
				}
			}
		})
	}
}

func TestResolveInstallVersionMasterWithoutRepository(t *testing.T) {
	t.Setenv("PATH", "")

	for _, program := range []binary.Program{binary.ProgramTt, binary.ProgramCe} {
		installCtx := InstallCtx{
			Program:          program,
			LocalRepo:        "missing",
			requestedVersion: masterVersion,
		}

		resolved, err := resolveInstallVersion(installCtx)
		require.NoError(t, err)
		assert.Equal(t, versionKindMaster, resolved.kind)
		assert.Equal(t, masterVersion, resolved.ref)
		assert.Equal(t, masterVersion, resolved.name)
	}
}
