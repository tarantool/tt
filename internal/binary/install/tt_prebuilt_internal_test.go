package install

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/ghrelease"
)

type ttPrebuiltTestRepository struct {
	requests      []string
	archive       []byte
	fileStatus    int
	downloadError error
	// latest is the tag of the latest release, or empty if the lookup fails.
	latest string
	// releases is the API listing of the releases, newest first: tags, with
	// /pre for a pre-release. Nil for a listing that fails.
	releases []string
	checksum string
}

// downloads returns the archive requests, without the release lookups and checksums.
func (repository *ttPrebuiltTestRepository) downloads() []string {
	var downloads []string

	for _, request := range repository.requests {
		if strings.HasSuffix(request, ".tar.gz") {
			downloads = append(downloads, request)
		}
	}

	return downloads
}

func mockTtPrebuiltRepository(t *testing.T) *ttPrebuiltTestRepository {
	t.Helper()

	// The repository below publishes tt 3 releases: macOS archives are arm64.
	if runtime.GOOS != osLinux && runtime.GOOS != osDarwin ||
		runtime.GOARCH != archAMD64 && runtime.GOARCH != archARM64 ||
		runtime.GOOS == osDarwin && runtime.GOARCH != archARM64 {
		t.Skip("requires a platform with tt release archives")
	}

	setTestBundleSources(t, ghrelease.New())
	t.Setenv("GITHUB_TOKEN", "")

	commands := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(commands, "git"), []byte(`#!/bin/sh
if [ "$1 $2 $3" != "ls-remote --tags --refs" ]; then exit 1; fi
printf 'abcdef0\trefs/tags/v3.0.0-rc1\nabcdef0\trefs/tags/v3.0.0\n'
`), 0o755))
	t.Setenv("PATH", commands)

	repository := &ttPrebuiltTestRepository{
		archive: ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"}),
		latest: "v3.0.0",
	}
	previousTransport := http.DefaultTransport

	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	http.DefaultTransport = prebuiltTestTransport(func(request *http.Request) (
		*http.Response, error,
	) {
		repository.requests = append(repository.requests, request.URL.Path)

		if request.URL.Host == "api.github.com" &&
			request.URL.Path == "/repos/tarantool/tt/releases" {
			return repository.listing(request), nil
		}

		if request.URL.Host != "github.com" {
			return nil, fmt.Errorf("unexpected repository host: %s", request.URL.Host)
		}

		if repository.downloadError != nil {
			return nil, repository.downloadError
		}

		status := http.StatusOK
		header := make(http.Header)
		content := repository.archive

		switch {
		case request.URL.Path == "/tarantool/tt/releases/latest":
			status, content = http.StatusNotFound, nil
			if repository.latest != "" {
				status = http.StatusFound

				header.Set("Location",
					"https://github.com/tarantool/tt/releases/tag/"+repository.latest)
			}
		case strings.HasSuffix(request.URL.Path, "/checksums.txt"):
			checksum := repository.checksum
			if checksum == "" {
				checksum = fmt.Sprintf("%x", sha256.Sum256(repository.archive))
			}

			release := strings.TrimPrefix(path.Base(path.Dir(request.URL.Path)), "v")

			content = fmt.Appendf(nil, "%s  tt-%s-%s-%s.tar.gz\n", checksum, release,
				runtime.GOOS, runtime.GOARCH)
		case repository.fileStatus != 0:
			status = repository.fileStatus
		}

		return &http.Response{
			StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: header, Body: io.NopCloser(bytes.NewReader(content)),
			ContentLength: int64(len(content)), Request: request,
		}, nil
	})

	return repository
}

// listing answers the request of a page of the API listing of the releases.
func (repository *ttPrebuiltTestRepository) listing(request *http.Request) *http.Response {
	status, content := http.StatusForbidden, []byte(`{"message": "API rate limit exceeded"}`)

	if repository.releases != nil {
		releases := []string{}

		if request.URL.Query().Get("page") == "1" {
			for _, release := range repository.releases {
				tag, kind, _ := strings.Cut(release, "/")

				releases = append(releases, fmt.Sprintf(`{"tag_name": %q, "prerelease": %t}`,
					tag, kind == "pre"))
			}
		}

		status, content = http.StatusOK, []byte("["+strings.Join(releases, ",")+"]")
	}

	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(content)),
		ContentLength: int64(len(content)), Request: request,
	}
}

func TestInstallTtDefaultDelivery(t *testing.T) {
	repository := mockTtPrebuiltRepository(t)

	for _, keepTemp := range []bool{false, true} {
		for _, requested := range []string{"", "3", "3.0", "3.0.0"} {
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

				var requests []string

				if requested == "" {
					requests = append(requests, "/tarantool/tt/releases/latest")
				}

				// The source lists the archive with its checksum, then the install fetches it.
				require.Equal(t, append(requests,
					"/tarantool/tt/releases/download/v3.0.0/checksums.txt",
					fmt.Sprintf("/tarantool/tt/releases/download/v3.0.0/tt-3.0.0-%s-%s.tar.gz",
						runtime.GOOS, runtime.GOARCH),
				), repository.requests)

				root, err := ctx.Scope.ArtifactPath()
				require.NoError(t, err)

				versionDir := filepath.Join(root, "versions", "tt", "tt_3.0.0")
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
				require.Len(t, repository.downloads(), 1)
				assert.FileExists(t, active)

				stale := filepath.Join(versionDir, "stale")
				require.NoError(t, os.WriteFile(stale, nil, 0o644))

				otherVersion := filepath.Join(root, "versions", "tt", "tt_v2.15.0")
				require.NoError(t, os.MkdirAll(otherVersion, 0o755))

				ctx.Reinstall = true
				require.NoError(t, Install(ctx))
				require.Len(t, repository.downloads(), 2)
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

func TestInstallTtLatestRelease(t *testing.T) {
	for _, test := range []struct {
		name        string
		latest      string
		releases    []string
		sourceBuild bool
		// noSource is a distribution without a source of tt.
		noSource bool
		want     string
	}{
		{name: "release behind the newest tag", latest: "v3.0.0", want: "3.0.0"},
		{
			name: "tt 3 has pre-releases only", latest: "v2.15.0",
			releases: []string{"v2.15.0", "v3.0.0-alpha.2/pre", "v3.0.0-alpha.1/pre"},
			want:     "3.0.0-alpha.2",
		},
		{
			name: "a stable tt 3 behind a newer tt 2", latest: "v2.16.0",
			releases: []string{"v2.16.0", "v3.1.0-rc1/pre", "v3.0.0"}, want: "3.0.0",
		},
		{name: "lookup failure", want: "3.1.0"},
		{name: "listing failure", latest: "v2.16.0", want: "3.1.0"},
		{name: "source build", latest: "v3.0.0", sourceBuild: true, want: "3.1.0"},
		{name: "no source of tt", latest: "v3.0.0", noSource: true, want: "3.1.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := mockTtPrebuiltRepository(t)

			repository.latest, repository.releases = test.latest, test.releases

			if test.noSource {
				setTestBundleSources(t)
			}

			t.Chdir(t.TempDir())

			commands := t.TempDir()
			for command, script := range map[string]string{
				"git": `#!/bin/sh
case "$1" in
ls-remote) printf 'abcdef0\trefs/tags/%s\n' v2.16.0 v3.0.0 v3.1.0 v3.2.0-rc1 v4.0.0 ;;
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
				SourceBuild: test.sourceBuild,
			}
			require.NoError(t, Install(ctx))

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)
			assert.DirExists(t, filepath.Join(root, "versions", "tt", "tt_"+test.want))

			if test.sourceBuild {
				assert.NotContains(t, repository.requests, "/tarantool/tt/releases/latest")
			}

			if test.noSource {
				assert.Empty(t, repository.requests, "without a source tt asks no releases")
			}
		})
	}
}

// TestInstallTtLegacyInstallation keeps using a release installed as
// tt_v<version>, as tt named it before, rather than installing it again.
func TestInstallTtLegacyInstallation(t *testing.T) {
	repository := mockTtPrebuiltRepository(t)
	t.Chdir(t.TempDir())

	ctx := InstallCtx{
		Program: binary.ProgramTt, Scope: binary.ScopeProject, requestedVersion: "3.0.0",
	}

	root, err := ctx.Scope.ArtifactPath()
	require.NoError(t, err)

	legacy := filepath.Join(root, "versions", "tt", "tt_v3.0.0")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "tt"), []byte("legacy"), 0o755))

	require.NoError(t, Install(ctx))
	assert.Empty(t, repository.downloads())
	assert.NoDirExists(t, filepath.Join(root, "versions", "tt", "tt_3.0.0"))

	executable, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "tt"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(legacy, "tt"), executable)
}

func TestInstallTtFallsBackToSource(t *testing.T) {
	for _, test := range []struct {
		name        string
		fileStatus  int
		keepTemp    bool
		sourceBuild bool
		// noSource is a distribution without a source of tt.
		noSource bool
		requests int
	}{
		{name: "missing archive", fileStatus: 404, requests: 1},
		{name: "missing archive with no-clean", fileStatus: 404, keepTemp: true, requests: 1},
		{name: "explicit source build", sourceBuild: true, requests: 0},
		{name: "no source of tt", noSource: true, requests: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := mockTtPrebuiltRepository(t)

			repository.fileStatus = test.fileStatus

			if test.noSource {
				setTestBundleSources(t)
			}

			t.Chdir(t.TempDir())

			commands := t.TempDir()
			for command, script := range map[string]string{
				"git": `#!/bin/sh
case "$1" in
ls-remote) printf 'abcdef0\trefs/tags/v3.0.0\n' ;;
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
				requestedVersion: "3.0.0", SourceBuild: test.sourceBuild, KeepTemp: test.keepTemp,
			}
			require.NoError(t, Install(ctx))
			assert.Len(t, repository.downloads(), test.requests)

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)

			contents, err := os.ReadFile(filepath.Join(root, "bin", "tt"))
			require.NoError(t, err)
			assert.Equal(t, "source executable", string(contents))
		})
	}
}

func TestInstallTtPrebuiltFailurePreservesInstallation(t *testing.T) {
	for _, failure := range []string{
		"download", "forbidden", "server error", "archive", "checksum",
	} {
		for _, keepTemp := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/keep=%t", failure, keepTemp), func(t *testing.T) {
				repository := mockTtPrebuiltRepository(t)
				t.Chdir(t.TempDir())

				tempRoot := t.TempDir()
				t.Setenv("TMPDIR", tempRoot)

				ctx := InstallCtx{
					Program: binary.ProgramTt, Scope: binary.ScopeProject,
					requestedVersion: "3.0.0",
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
				case "checksum":
					repository.checksum = fmt.Sprintf("%x", sha256.Sum256(nil))
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
		requestedVersion: "3.0.0",
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
