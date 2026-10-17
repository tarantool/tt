package install

import (
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

type prebuiltTestTransport func(*http.Request) (*http.Response, error)

func (t prebuiltTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t(request)
}

type prebuiltTestRepository struct {
	requests      []string
	downloadError error
	headersRPM    []byte
}

func mockPrebuiltRepository(t *testing.T) *prebuiltTestRepository {
	t.Helper()

	if runtime.GOOS != osLinux || (runtime.GOARCH != archAMD64 && runtime.GOARCH != archARM64) {
		t.Skip("Linux prebuilt delivery on a supported architecture")
	}

	err := checkTarantoolPackagePlatform(runtime.GOARCH, "/")
	if err != nil {
		t.Skipf("Linux prebuilt delivery requires glibc: %v", err)
	}

	// Only Git is on PATH: package installation must not require source build tools.
	commands := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(commands, "git"), []byte(`#!/bin/sh
if [ "$1 $2 $3" != "ls-remote --tags --refs" ]; then exit 1; fi
printf 'abcdef0\trefs/tags/3.6.0\nabcdef0\trefs/tags/3.7.1\nabcdef0\trefs/tags/4.0.0-rc1\n'
`), 0o755))
	t.Setenv("PATH", commands)

	arch := map[string]string{archAMD64: rpmArchAMD64, archARM64: rpmArchARM64}[runtime.GOARCH]
	runtimeName := "tarantool-3.7.1-1." + arch + ".rpm"
	headersName := "tarantool-devel-3.7.1-1." + arch + ".rpm"
	runtimeRPM := packageTestRPM(t, "./usr/bin/tarantool", "executable", 0100755)
	headersRPM := packageTestRPM(t, "./usr/include/tarantool/module.h", "headers", 0100644)
	repository := &prebuiltTestRepository{
		requests: nil, downloadError: nil, headersRPM: headersRPM,
	}
	previousTransport := http.DefaultTransport

	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	transport := func(request *http.Request) (*http.Response, error) {
		repository.requests = append(repository.requests, request.URL.Path)

		var content []byte

		if filepath.Base(request.URL.Path) != "Packages" && repository.downloadError != nil {
			return nil, repository.downloadError
		}

		switch filepath.Base(request.URL.Path) {
		case "Packages":
			content = fmt.Appendf(nil, `<a href="%s">runtime</a><a href="%s">headers</a>`,
				runtimeName, headersName)
		case runtimeName:
			content = runtimeRPM
		case headersName:
			content = repository.headersRPM
		default:
			return nil, fmt.Errorf("unexpected package request: %s", request.URL)
		}

		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(bytes.NewReader(content)), ContentLength: int64(len(content)),
			Request: request,
		}, nil
	}

	http.DefaultTransport = prebuiltTestTransport(transport)

	return repository
}

func TestInstallTarantoolDefaultDelivery(t *testing.T) {
	repository := mockPrebuiltRepository(t)

	for _, keepTemp := range []bool{false, true} {
		for _, requested := range []string{"", "3", "3.7", "3.7.1"} {
			t.Run(fmt.Sprintf("version=%s/keep=%t", requested, keepTemp), func(t *testing.T) {
				t.Chdir(t.TempDir())

				tempRoot := t.TempDir()
				t.Setenv("TMPDIR", tempRoot)

				repository.requests = nil

				base := binary.BinaryContext{Program: binary.ProgramCe, Scope: binary.ScopeProject}
				ctx := InstallCtx{
					BinaryContext:    base,
					requestedVersion: requested,
					KeepTemp:         keepTemp,
				}
				require.NoError(t, Install(ctx))
				require.Len(t, repository.requests, 3)

				root, err := ctx.Scope.ArtifactPath()
				require.NoError(t, err)

				versionDir := filepath.Join(root, "versions", "tarantool", "tarantool_3.7.1")
				executable, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "tarantool"))
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(versionDir, "tarantool"), executable)

				data, err := os.ReadFile(executable)
				require.NoError(t, err)
				assert.Equal(t, "executable", string(data))

				info, err := os.Stat(executable)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

				headers, err := filepath.EvalSymlinks(filepath.Join(root, "include", "tarantool"))
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(versionDir, "include", "tarantool"), headers)

				data, err = os.ReadFile(filepath.Join(headers, "module.h"))
				require.NoError(t, err)
				assert.Equal(t, "headers", string(data))

				// A complete existing version is reactivated without downloading again.
				require.NoError(t, Install(ctx))
				require.Len(t, repository.requests, 3)

				staleHeader := filepath.Join(headers, "stale.h")
				require.NoError(t, os.WriteFile(staleHeader, nil, 0o644))

				otherVersion := filepath.Join(root, "versions", "tarantool", "tarantool_3.1.0")
				require.NoError(t, os.MkdirAll(otherVersion, 0o755))

				ctx.Reinstall = true
				require.NoError(t, Install(ctx))
				require.Len(t, repository.requests, 6)
				assert.NoFileExists(t, staleHeader)
				assert.DirExists(t, otherVersion)
				assert.FileExists(t, filepath.Join(headers, "module.h"))
				assert.FileExists(t, filepath.Join(root, "bin", "tarantool"))

				tempDirectories, err := os.ReadDir(tempRoot)
				require.NoError(t, err)

				if keepTemp {
					assert.Len(t, tempDirectories, 2)

					for _, directory := range tempDirectories {
						archives, err := filepath.Glob(filepath.Join(tempRoot, directory.Name(),
							"tarantool-package-*", "*", "package-*.rpm"))
						require.NoError(t, err)
						assert.Len(t, archives, 2, "--no-clean must retain both downloaded RPMs")
					}
				} else {
					assert.Empty(t, tempDirectories)
				}
			})
		}
	}
}

func TestInstallTarantoolFallsBackToSource(t *testing.T) {
	for _, test := range []struct {
		name        string
		version     string
		sourceBuild bool
		requests    int
	}{
		{name: "unsupported release", version: "4.0.0-rc1", requests: 0},
		{name: "missing release package", version: "3.6.0", requests: 1},
		{name: "explicit source build", version: "3.7.1", sourceBuild: true, requests: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			originalPath := os.Getenv("PATH")
			repository := mockPrebuiltRepository(t)
			t.Chdir(t.TempDir())

			commands := t.TempDir()
			for command, script := range map[string]string{
				"git": `#!/bin/sh
case "$1" in
ls-remote)
    printf 'abcdef0\trefs/tags/3.6.0\nabcdef0\trefs/tags/3.7.1\nabcdef0\trefs/tags/4.0.0-rc1\n'
    ;;
--version) printf 'git version 2.7.0\n' ;;
clone)
    for destination do :; done
    mkdir -p "$destination"
    ;;
*) exit 1 ;;
esac
`,
				"cmake": "#!/bin/sh\nexit 0\n",
				"make": `#!/bin/sh
set -e
mkdir -p tarantool-prefix/bin tarantool-prefix/include/tarantool
printf 'source executable' > tarantool-prefix/bin/tarantool
printf 'source headers' > tarantool-prefix/include/tarantool/module.h
printf 'build\n' >> "$TT_CE_SOURCE_BUILD_LOG"
`,
			} {
				require.NoError(t,
					os.WriteFile(filepath.Join(commands, command), []byte(script), 0o755))
			}

			t.Setenv("PATH", commands+string(os.PathListSeparator)+originalPath)

			buildLog := filepath.Join(t.TempDir(), "build.log")
			t.Setenv("TT_CE_SOURCE_BUILD_LOG", buildLog)

			base := binary.BinaryContext{Program: binary.ProgramCe, Scope: binary.ScopeProject}
			ctx := InstallCtx{
				BinaryContext: base, requestedVersion: test.version,
				SourceBuild: test.sourceBuild, Force: true,
			}
			require.NoError(t, Install(ctx))
			require.Len(t, repository.requests, test.requests)

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)

			for name, expected := range map[string]string{
				"bin/tarantool":              "source executable",
				"include/tarantool/module.h": "source headers",
			} {
				data, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				assert.Equal(t, expected, string(data))
			}

			data, err := os.ReadFile(buildLog)
			require.NoError(t, err)
			assert.Equal(t, "build\n", string(data))
		})
	}
}

func TestInstallTarantoolPrebuiltExtractionFailure(t *testing.T) {
	for _, keepTemp := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep=%t", keepTemp), func(t *testing.T) {
			repository := mockPrebuiltRepository(t)
			t.Chdir(t.TempDir())

			tempRoot := t.TempDir()
			t.Setenv("TMPDIR", tempRoot)

			base := binary.BinaryContext{Program: binary.ProgramCe, Scope: binary.ScopeProject}
			ctx := InstallCtx{
				BinaryContext:    base,
				requestedVersion: "3.7.1",
			}
			require.NoError(t, Install(ctx))

			repository.headersRPM = []byte("invalid RPM")
			ctx.Reinstall = true
			ctx.KeepTemp = keepTemp

			logger, recorder := logtest.New(t)
			previousLogger := slog.Default()

			slog.SetDefault(logger)
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			require.ErrorContains(t, Install(ctx), "extract Tarantool package")

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)

			for name, expected := range map[string]string{
				"bin/tarantool": "executable", "include/tarantool/module.h": "headers",
			} {
				data, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				assert.Equal(t, expected, string(data),
					"failed reinstall must preserve active files")
			}

			tempDirectories, err := os.ReadDir(tempRoot)
			require.NoError(t, err)

			if !keepTemp {
				assert.Empty(t, tempDirectories)

				return
			}

			require.Len(t, tempDirectories, 1)

			keptDirectory := filepath.Join(tempRoot, tempDirectories[0].Name())
			packageDirectories, err := filepath.Glob(
				filepath.Join(keptDirectory, "tarantool-package-*"))
			require.NoError(t, err)
			require.Len(t, packageDirectories, 1)

			packageDirectory := packageDirectories[0]
			assert.FileExists(t,
				filepath.Join(packageDirectory, "runtime", "usr", "bin", "tarantool"))

			for _, component := range []string{"runtime", "headers"} {
				archives, err := filepath.Glob(
					filepath.Join(packageDirectory, component, "package-*.rpm"))
				require.NoError(t, err)
				require.Len(t, archives, 1, "--no-clean must retain downloaded RPMs on failure")

				data, err := os.ReadFile(archives[0])
				require.NoError(t, err)

				if component == "headers" {
					assert.Equal(t, repository.headersRPM, data)
				} else {
					assert.Equal(t,
						packageTestRPM(t, "./usr/bin/tarantool", "executable", 0o100755), data)
				}
			}

			var messages []string

			for _, record := range recorder.Records() {
				messages = append(messages, record.Message)
			}

			assert.Contains(t, messages, "Artifacts can be found at: "+keptDirectory)
		})
	}
}

func TestInstallTarantoolPrebuiltDownloadFailure(t *testing.T) {
	repository := mockPrebuiltRepository(t)
	t.Chdir(t.TempDir())

	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)

	base := binary.BinaryContext{Program: binary.ProgramCe, Scope: binary.ScopeProject}
	ctx := InstallCtx{
		BinaryContext:    base,
		requestedVersion: "3.7.1",
	}
	require.NoError(t, Install(ctx))

	root, err := ctx.Scope.ArtifactPath()
	require.NoError(t, err)

	executable := filepath.Join(root, "bin", "tarantool")
	headers := filepath.Join(root, "include", "tarantool")
	previousExecutable, err := filepath.EvalSymlinks(executable)
	require.NoError(t, err)

	previousHeaders, err := filepath.EvalSymlinks(headers)
	require.NoError(t, err)

	failure := errors.New("download failed")

	repository.downloadError = failure
	ctx.Reinstall = true
	require.ErrorIs(t, Install(ctx), failure)

	data, err := os.ReadFile(executable)
	require.NoError(t, err)
	assert.Equal(t, "executable", string(data))

	actualExecutable, err := filepath.EvalSymlinks(executable)
	require.NoError(t, err)
	assert.Equal(t, previousExecutable, actualExecutable)

	actualHeaders, err := filepath.EvalSymlinks(headers)
	require.NoError(t, err)
	assert.Equal(t, previousHeaders, actualHeaders)
	assert.FileExists(t, filepath.Join(headers, "module.h"))

	tempDirectories, err := os.ReadDir(tempRoot)
	require.NoError(t, err)
	assert.Empty(t, tempDirectories)
}
