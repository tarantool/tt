package install

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/internal/binary"
)

type ttPrebuiltTestRepository struct {
	requests      []string
	archive       []byte
	fileStatus    int
	downloadError error
}

func mockTtPrebuiltRepository(t *testing.T) *ttPrebuiltTestRepository {
	t.Helper()

	if checkTtPackagePlatform(runtime.GOOS, runtime.GOARCH) != nil {
		t.Skip("requires a platform with tt release archives")
	}

	commands := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(commands, "git"), []byte(`#!/bin/sh
if [ "$1 $2 $3" != "ls-remote --tags --refs" ]; then exit 1; fi
printf 'abcdef0\trefs/tags/v2.14.0\nabcdef0\trefs/tags/v2.15.0\n'
`), 0o755))
	t.Setenv("PATH", commands)

	repository := &ttPrebuiltTestRepository{
		archive: ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"}),
	}
	previousTransport := http.DefaultTransport

	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	http.DefaultTransport = prebuiltTestTransport(func(request *http.Request) (
		*http.Response, error,
	) {
		repository.requests = append(repository.requests, request.URL.Path)

		if request.URL.Host != "github.com" {
			return nil, fmt.Errorf("unexpected repository host: %s", request.URL.Host)
		}

		if repository.downloadError != nil {
			return nil, repository.downloadError
		}

		status := http.StatusOK
		if repository.fileStatus != 0 {
			status = repository.fileStatus
		}

		content := repository.archive

		return &http.Response{
			StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(content)),
			ContentLength: int64(len(content)), Request: request,
		}, nil
	})

	return repository
}

func TestInstallTtDefaultDelivery(t *testing.T) {
	repository := mockTtPrebuiltRepository(t)

	for _, keepTemp := range []bool{false, true} {
		for _, requested := range []string{"", "2", "2.15", "2.15.0"} {
			t.Run(fmt.Sprintf("version=%s/keep=%t", requested, keepTemp), func(t *testing.T) {
				t.Chdir(t.TempDir())

				tempRoot := t.TempDir()
				t.Setenv("TMPDIR", tempRoot)

				repository.requests = nil

				ctx := InstallCtx{
					Program: binary.ProgramTt, Scope: binary.ScopeProject,
					requestedVersion: requested, KeepTemp: keepTemp,
				}
				require.NoError(t, Install(ctx))
				require.Equal(t, []string{
					fmt.Sprintf("/tarantool/tt/releases/download/v2.15.0/tt-2.15.0-%s-%s.tar.gz",
						runtime.GOOS, runtime.GOARCH),
				}, repository.requests)

				root, err := ctx.Scope.ArtifactPath()
				require.NoError(t, err)

				versionDir := filepath.Join(root, "versions", "tt", "tt_v2.15.0")
				active := filepath.Join(root, "bin", "tt")
				executable, err := filepath.EvalSymlinks(active)
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(versionDir, "tt"), executable)

				contents, err := os.ReadFile(active)
				require.NoError(t, err)
				assert.Equal(t, "executable", string(contents))
				assert.NoDirExists(t, filepath.Join(root, "include"))
				assert.FileExists(t, ".env/activate.sh")
				assert.FileExists(t, ".env/deactivate.sh")

				// Reactivate an existing version without downloads or build dependencies.
				require.NoError(t, os.Remove(active))
				require.NoError(t, Install(ctx))
				require.Len(t, repository.requests, 1)
				assert.FileExists(t, active)

				stale := filepath.Join(versionDir, "stale")
				require.NoError(t, os.WriteFile(stale, nil, 0o644))

				otherVersion := filepath.Join(root, "versions", "tt", "tt_v2.14.0")
				require.NoError(t, os.MkdirAll(otherVersion, 0o755))

				ctx.Reinstall = true
				require.NoError(t, Install(ctx))
				require.Len(t, repository.requests, 2)
				assert.NoFileExists(t, stale)
				assert.DirExists(t, otherVersion)

				tempDirectories, err := os.ReadDir(tempRoot)
				require.NoError(t, err)

				if keepTemp {
					require.Len(t, tempDirectories, 2)

					for _, directory := range tempDirectories {
						archives, err := filepath.Glob(filepath.Join(tempRoot, directory.Name(),
							"tt-package-*", "package-*.tar.gz"))
						require.NoError(t, err)
						assert.Len(t, archives, 1, "--no-clean retains the downloaded archive")
					}
				} else {
					assert.Empty(t, tempDirectories)
				}
			})
		}
	}
}

func TestInstallTtFallsBackToSource(t *testing.T) {
	for _, test := range []struct {
		name        string
		fileStatus  int
		keepTemp    bool
		sourceBuild bool
		requests    int
	}{
		{name: "missing archive", fileStatus: 404, requests: 1},
		{name: "missing archive with no-clean", fileStatus: 404, keepTemp: true, requests: 1},
		{name: "explicit source build", sourceBuild: true, requests: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := mockTtPrebuiltRepository(t)

			repository.fileStatus = test.fileStatus

			t.Chdir(t.TempDir())

			commands := t.TempDir()
			for command, script := range map[string]string{
				"git": `#!/bin/sh
case "$1" in
ls-remote) printf 'abcdef0\trefs/tags/v2.15.0\n' ;;
--version) printf 'git version 2.7.0\n' ;;
clone) exit 0 ;;
*) exit 1 ;;
esac
`,
				"mage": "#!/bin/sh\nprintf 'source executable' > tt\n",
			} {
				require.NoError(t,
					os.WriteFile(filepath.Join(commands, command), []byte(script), 0o755))
			}

			t.Setenv("PATH", commands)

			ctx := InstallCtx{
				Program: binary.ProgramTt, Scope: binary.ScopeProject,
				requestedVersion: "2.15.0", SourceBuild: test.sourceBuild, KeepTemp: test.keepTemp,
			}
			require.NoError(t, Install(ctx))
			assert.Len(t, repository.requests, test.requests)

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)

			contents, err := os.ReadFile(filepath.Join(root, "bin", "tt"))
			require.NoError(t, err)
			assert.Equal(t, "source executable", string(contents))
		})
	}
}

func TestInstallTtPrebuiltFailurePreservesInstallation(t *testing.T) {
	for _, failure := range []string{"download", "forbidden", "server error", "archive"} {
		for _, keepTemp := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/keep=%t", failure, keepTemp), func(t *testing.T) {
				repository := mockTtPrebuiltRepository(t)
				t.Chdir(t.TempDir())

				tempRoot := t.TempDir()
				t.Setenv("TMPDIR", tempRoot)

				ctx := InstallCtx{
					Program: binary.ProgramTt, Scope: binary.ScopeProject,
					requestedVersion: "2.15.0",
				}
				require.NoError(t, Install(ctx))

				switch failure {
				case "download":
					repository.downloadError = errors.New("connection interrupted")
				case "forbidden":
					repository.fileStatus = http.StatusForbidden
				case "server error":
					repository.fileStatus = http.StatusInternalServerError
				case "archive":
					repository.archive = []byte("invalid gzip archive")
				}

				ctx.Reinstall, ctx.KeepTemp = true, keepTemp

				err := Install(ctx)
				require.Error(t, err)
				require.NotErrorIs(t, err, errPrebuiltPackageUnavailable)

				root, err := ctx.Scope.ArtifactPath()
				require.NoError(t, err)

				contents, err := os.ReadFile(filepath.Join(root, "bin", "tt"))
				require.NoError(t, err)
				assert.Equal(t, "executable", string(contents))

				tempDirectories, err := os.ReadDir(tempRoot)
				require.NoError(t, err)

				if keepTemp {
					assert.Len(t, tempDirectories, 1)
				} else {
					assert.Empty(t, tempDirectories)
				}
			})
		}
	}
}

func TestInstallTtDiagnostics(t *testing.T) {
	mockTtPrebuiltRepository(t)
	t.Chdir(t.TempDir())

	logger, recorder := logtest.New(t)
	previousLogger := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	ctx := InstallCtx{
		Program: binary.ProgramTt, Scope: binary.ScopeProject,
		requestedVersion: "2.15.0",
	}
	require.NoError(t, Install(ctx))

	for _, message := range []string{"Copying executable...", "Changing symlinks..."} {
		found := false

		for _, record := range recorder.Records() {
			if record.Message == message {
				assert.Equal(t, slog.LevelDebug, record.Level)

				found = true
			}
		}

		assert.True(t, found, "missing diagnostic %q", message)
	}
}
