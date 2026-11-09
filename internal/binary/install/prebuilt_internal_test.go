package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/internal/binary"
)

var errTestDelivery = errors.New("connection reset")

type ttArchiveEntry struct {
	name    string
	mode    int64
	kind    byte
	content string
}

func ttPackageTestArchive(t *testing.T, entries ...ttArchiveEntry) []byte {
	t.Helper()

	var contents bytes.Buffer

	compressed := gzip.NewWriter(&contents)
	archive := tar.NewWriter(compressed)

	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Mode: entry.mode, Typeflag: entry.kind,
			Size: int64(len(entry.content)),
		}
		if entry.kind == tar.TypeSymlink || entry.kind == tar.TypeLink {
			header.Linkname = "../outside"
		}

		require.NoError(t, archive.WriteHeader(header))

		_, err := archive.Write([]byte(entry.content))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return contents.Bytes()
}

// prebuiltTestSource is a source of one product: it lists artifacts and
// serves one archive for all of them.
type prebuiltTestSource struct {
	product   string
	artifacts []bundle.Artifact
	listErr   error
	archive   []byte
	fetchErr  error
	// queries and fetched are what the source was asked.
	queries []bundle.Query
	fetched []bundle.Artifact
}

func (*prebuiltTestSource) Name() string { return "test-source" }

func (s *prebuiltTestSource) Products() []string { return []string{s.product} }

func (s *prebuiltTestSource) List(_ context.Context, query bundle.Query) (
	[]bundle.Artifact, error,
) {
	s.queries = append(s.queries, query)

	return s.artifacts, s.listErr
}

func (s *prebuiltTestSource) Fetch(_ context.Context, artifact bundle.Artifact,
	dst io.Writer,
) error {
	s.fetched = append(s.fetched, artifact)

	if s.fetchErr != nil {
		return s.fetchErr
	}

	_, err := dst.Write(s.archive)

	return err
}

// setTestBundleSources makes sources the sources of the process for the test.
func setTestBundleSources(t *testing.T, sources ...bundle.Source) {
	t.Helper()

	previous := binary.BundleSources()

	binary.SetBundleSources(sources)
	t.Cleanup(func() { binary.SetBundleSources(previous) })
}

// tarantoolTestArchive is the archive of tarantool sdk/bundle defines, with
// a file at the root and a header outside include/tarantool tt skips.
func tarantoolTestArchive(t *testing.T) []byte {
	t.Helper()

	return ttPackageTestArchive(t,
		ttArchiveEntry{name: "README", kind: tar.TypeReg, mode: 0o644, content: "readme"},
		ttArchiveEntry{name: "./tarantool", kind: tar.TypeReg, mode: 0o755, content: "executable"},
		ttArchiveEntry{name: "include/", kind: tar.TypeDir, mode: 0o755},
		ttArchiveEntry{name: "include/other.h", kind: tar.TypeReg, mode: 0o644, content: "other"},
		ttArchiveEntry{name: "include/tarantool/", kind: tar.TypeDir, mode: 0o755},
		ttArchiveEntry{
			name: "include/tarantool/module.h", kind: tar.TypeReg, mode: 0o644, content: "module",
		},
		ttArchiveEntry{name: "include/tarantool/curl/", kind: tar.TypeDir, mode: 0o755},
		// A header without its directory entry.
		ttArchiveEntry{
			name: "include/tarantool/luajit/luajit.h", kind: tar.TypeReg, mode: 0o644,
			content: "luajit",
		})
}

func tarantoolTestSource(t *testing.T) *prebuiltTestSource {
	t.Helper()

	archive := tarantoolTestArchive(t)
	digest := sha256.Sum256(archive)

	return &prebuiltTestSource{
		product: binary.ProgramCe.String(),
		artifacts: []bundle.Artifact{
			{Version: "3.7.0", Name: "tarantool-3.7.0.tar.gz", Locator: "3.7.0"},
			{Version: "3.7.1", Flavor: []string{"debug"}, Name: "debug", Locator: "debug"},
			{
				Version: "3.7.1", Name: "tarantool-3.7.1.tar.gz", Locator: "3.7.1",
				SHA256: hex.EncodeToString(digest[:]),
			},
		},
		archive: archive,
	}
}

func TestPreparePrebuiltTarantool(t *testing.T) {
	for _, keepTemp := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep=%t", keepTemp), func(t *testing.T) {
			source := tarantoolTestSource(t)
			setTestBundleSources(t, source)

			destination := filepath.Join(t.TempDir(), "new-directory")
			prepared, err := preparePrebuilt(t.Context(), binary.ProgramCe, "3.7.1",
				destination, keepTemp)
			require.NoError(t, err)

			assert.Equal(t, []bundle.Query{{
				Product: "tarantool", OS: runtime.GOOS, Arch: runtime.GOARCH,
				Channel: bundle.ChannelRelease, Version: "3.7.1",
			}}, source.queries)
			require.Len(t, source.fetched, 1)
			assert.Equal(t, "3.7.1", source.fetched[0].Locator,
				"the build of the tag without flavours is fetched")

			assert.Equal(t, binary.ProgramCe, prepared.program)
			assert.True(t, filepath.IsAbs(prepared.executable))
			assert.True(t, filepath.IsAbs(prepared.headers))
			assert.Equal(t, filepath.Dir(prepared.executable),
				filepath.Dir(filepath.Dir(prepared.headers)))

			contents, err := os.ReadFile(prepared.executable)
			require.NoError(t, err)
			assert.Equal(t, "executable", string(contents))

			info, err := os.Stat(prepared.executable)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

			for name, content := range map[string]string{
				"module.h": "module", "luajit/luajit.h": "luajit",
			} {
				contents, err := os.ReadFile(filepath.Join(prepared.headers, name))
				require.NoError(t, err)
				assert.Equal(t, content, string(contents))
			}

			assert.DirExists(t, filepath.Join(prepared.headers, "curl"))

			extractDir := filepath.Dir(prepared.executable)
			assert.NoFileExists(t, filepath.Join(extractDir, "README"))
			assert.NoFileExists(t, filepath.Join(extractDir, "include", "other.h"))

			archives, err := filepath.Glob(filepath.Join(destination,
				"tarantool-package-*", "package-*.tar.gz"))
			require.NoError(t, err)

			if keepTemp {
				assert.Len(t, archives, 1, "--no-clean retains the downloaded archive")
			} else {
				assert.Empty(t, archives)
			}
		})
	}
}

func TestPreparePrebuiltUnavailable(t *testing.T) {
	for _, test := range []struct {
		name     string
		sources  func(*prebuiltTestSource) []bundle.Source
		keepTemp bool
	}{
		{
			name:    "no source of the product",
			sources: func(*prebuiltTestSource) []bundle.Source { return nil },
		},
		{
			name: "a source of other products",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.product = "tt"

				return []bundle.Source{source}
			},
		},
		{
			name: "no build for the host",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.artifacts = nil

				return []bundle.Source{source}
			},
		},
		{
			name: "no build of the tag",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.artifacts = source.artifacts[:2]

				return []bundle.Source{source}
			},
		},
		{
			name: "the archive is not found",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.fetchErr = fmt.Errorf("download: %w", bundle.ErrNotFound)

				return []bundle.Source{source}
			},
		},
		{
			name: "the archive is not found with no-clean", keepTemp: true,
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.fetchErr = fmt.Errorf("download: %w", bundle.ErrNotFound)

				return []bundle.Source{source}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setTestBundleSources(t, test.sources(tarantoolTestSource(t))...)

			destination := t.TempDir()
			_, err := preparePrebuilt(t.Context(), binary.ProgramCe, "3.7.1", destination,
				test.keepTemp)
			require.ErrorIs(t, err, errPrebuiltPackageUnavailable)
			require.ErrorContains(t, err, "--source-build")

			entries, err := os.ReadDir(destination)
			require.NoError(t, err)
			assert.Empty(t, entries, "the source checkout directory must stay empty")
		})
	}
}

func TestPreparePrebuiltFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *prebuiltTestSource)
		want   error
	}{
		{
			name: "list failure",
			change: func(_ *testing.T, source *prebuiltTestSource) {
				source.listErr = errTestDelivery
			},
			want: errTestDelivery,
		},
		{
			name: "fetch failure",
			change: func(_ *testing.T, source *prebuiltTestSource) {
				source.fetchErr = errTestDelivery
			},
			want: errTestDelivery,
		},
		{
			name: "checksum mismatch",
			change: func(_ *testing.T, source *prebuiltTestSource) {
				source.artifacts[2].SHA256 = strings.Repeat("0", sha256.Size*2)
			},
			want: errArtifactChecksum,
		},
		{
			name: "no headers",
			change: func(t *testing.T, source *prebuiltTestSource) {
				t.Helper()

				source.archive = ttPackageTestArchive(t, ttArchiveEntry{
					name: "tarantool", kind: tar.TypeReg, mode: 0o755, content: "executable",
				})
				source.artifacts[2].SHA256 = ""
			},
			want: errPackageContents,
		},
		{
			name: "invalid archive",
			change: func(_ *testing.T, source *prebuiltTestSource) {
				source.archive = []byte("invalid gzip archive")
				source.artifacts[2].SHA256 = ""
			},
			want: errPackageContents,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := tarantoolTestSource(t)
			test.change(t, source)
			setTestBundleSources(t, source)

			destination := t.TempDir()
			_, err := preparePrebuilt(t.Context(), binary.ProgramCe, "3.7.1", destination, false)
			require.ErrorIs(t, err, test.want)
			require.NotErrorIs(t, err, errPrebuiltPackageUnavailable,
				"a failed delivery must not fall back to a source build")

			entries, err := os.ReadDir(destination)
			require.NoError(t, err)
			assert.Empty(t, entries, "failed delivery should clean its temporary artifacts")
		})
	}
}

func TestExtractPrebuiltTarantoolRejectsInvalidArchives(t *testing.T) {
	executable := ttArchiveEntry{
		name: "tarantool", kind: tar.TypeReg, mode: 0o755, content: "executable",
	}
	module := ttArchiveEntry{
		name: "include/tarantool/module.h", kind: tar.TypeReg, mode: 0o644, content: "module",
	}

	for _, test := range []struct {
		name    string
		entries []ttArchiveEntry
	}{
		{"missing executable", []ttArchiveEntry{module}},
		{"missing module.h", []ttArchiveEntry{executable, {
			name: "include/tarantool/lua.h", kind: tar.TypeReg, mode: 0o644, content: "lua",
		}}},
		{"duplicate header", []ttArchiveEntry{executable, module, module}},
		{"header symlink", []ttArchiveEntry{executable, module, {
			name: "include/tarantool/lua.h", kind: tar.TypeSymlink, mode: 0o644,
		}}},
		{"header traversal", []ttArchiveEntry{executable, module, {
			name: "include/tarantool/../../../outside", kind: tar.TypeReg, mode: 0o644,
		}}},
		{"headers file", []ttArchiveEntry{executable, {
			name: "include/tarantool", kind: tar.TypeReg, mode: 0o644, content: "file",
		}, module}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "package.tar.gz")
			require.NoError(t, os.WriteFile(archivePath,
				ttPackageTestArchive(t, test.entries...), 0o600))

			destination := filepath.Join(root, "extract")
			require.NoError(t, os.Mkdir(destination, 0o755))

			_, err := extractPrebuilt(binary.ProgramCe, archivePath, destination)
			require.ErrorIs(t, err, errPackageContents)
			assert.NoFileExists(t, filepath.Join(root, "outside"))
		})
	}
}

func TestExtractPrebuiltTtRejectsInvalidArchives(t *testing.T) {
	valid := ttPackageTestArchive(t,
		ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"})
	badChecksum := bytes.Clone(valid)

	badChecksum[len(badChecksum)-8] ^= 1

	for _, test := range []struct {
		name    string
		archive []byte
	}{
		{"invalid gzip", []byte("broken")},
		{"truncated gzip", valid[:len(valid)-4]},
		{"bad gzip checksum", badChecksum},
		{"missing executable", ttPackageTestArchive(t,
			ttArchiveEntry{name: "README", kind: tar.TypeReg, mode: 0o644, content: "readme"})},
		{"not executable", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o644, content: "executable"})},
		{"directory", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeDir, mode: 0o755})},
		{"duplicate binary", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "one"},
			ttArchiveEntry{name: "./tt", kind: tar.TypeReg, mode: 0o755, content: "two"})},
		{"symlink", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeSymlink, mode: 0o755})},
		{"hardlink", ttPackageTestArchive(t,
			ttArchiveEntry{name: "tt", kind: tar.TypeLink, mode: 0o755})},
		{"parent traversal", ttPackageTestArchive(t,
			ttArchiveEntry{name: "../outside", kind: tar.TypeReg, mode: 0o755, content: "bad"})},
		{"absolute path", ttPackageTestArchive(t,
			ttArchiveEntry{name: "/outside", kind: tar.TypeReg, mode: 0o755, content: "bad"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "package.tar.gz")
			require.NoError(t, os.WriteFile(archivePath, test.archive, 0o600))

			destination := filepath.Join(root, "extract")
			require.NoError(t, os.Mkdir(destination, 0o755))

			_, err := extractPrebuilt(binary.ProgramTt, archivePath, destination)
			require.ErrorIs(t, err, errPackageContents)
			require.NotErrorIs(t, err, errPrebuiltPackageUnavailable)
			assert.NoFileExists(t, filepath.Join(root, "outside"))
		})
	}
}

func TestExtractPrebuiltTt(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "package.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, ttPackageTestArchive(t,
		ttArchiveEntry{name: "README.md", kind: tar.TypeReg, mode: 0o644, content: "readme"},
		ttArchiveEntry{name: "include/", kind: tar.TypeDir, mode: 0o755},
		ttArchiveEntry{name: "include/tt.h", kind: tar.TypeReg, mode: 0o644, content: "header"},
		ttArchiveEntry{name: "./tt", kind: tar.TypeReg, mode: 0o755, content: "executable"},
	), 0o600))

	destination := filepath.Join(root, "extract")
	require.NoError(t, os.Mkdir(destination, 0o755))

	prepared, err := extractPrebuilt(binary.ProgramTt, archivePath, destination)
	require.NoError(t, err)
	assert.Equal(t, programArtifact{
		program: binary.ProgramTt, executable: filepath.Join(destination, "tt"), headers: "",
	}, prepared)

	contents, err := os.ReadFile(prepared.executable)
	require.NoError(t, err)
	assert.Equal(t, "executable", string(contents))

	info, err := os.Stat(prepared.executable)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	files, err := os.ReadDir(destination)
	require.NoError(t, err)
	require.Len(t, files, 1, "tt has no headers: only the executable is extracted")
	assert.Equal(t, "tt", files[0].Name())
}

// fakeTarantoolBuild puts git, cmake and make of a source build of
// Tarantool on PATH: git lists the tags 3.7.0 and 3.7.1.
func fakeTarantoolBuild(t *testing.T) {
	t.Helper()

	commands := t.TempDir()
	for command, script := range map[string]string{
		"git": `#!/bin/sh
case "$1" in
ls-remote) printf 'abcdef0\trefs/tags/3.7.0\nabcdef0\trefs/tags/3.7.1\n' ;;
*) exit 0 ;;
esac
`,
		"cmake": "#!/bin/sh\nexit 0\n",
		"make": `#!/bin/sh
set -e
mkdir -p tarantool-prefix/bin tarantool-prefix/include/tarantool
printf 'source executable' > tarantool-prefix/bin/tarantool
printf 'headers' > tarantool-prefix/include/tarantool/module.h
`,
	} {
		require.NoError(t,
			os.WriteFile(filepath.Join(commands, command), []byte(script), 0o755))
	}

	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInstallTarantoolPrebuilt(t *testing.T) {
	for _, test := range []struct {
		name string
		// sources are the sources of the process, given the source of tarantool.
		sources func(*prebuiltTestSource) []bundle.Source
		want    string
		fetched int
	}{
		{
			name: "prebuilt",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				return []bundle.Source{source}
			},
			want: "executable", fetched: 1,
		},
		{
			name:    "distribution without a source of tarantool",
			sources: func(*prebuiltTestSource) []bundle.Source { return nil },
			want:    "source executable",
		},
		{
			name: "no build for the host",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.artifacts = nil

				return []bundle.Source{source}
			},
			want: "source executable",
		},
		{
			name: "the archive is not found",
			sources: func(source *prebuiltTestSource) []bundle.Source {
				source.fetchErr = bundle.ErrNotFound

				return []bundle.Source{source}
			},
			want: "source executable", fetched: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeTarantoolBuild(t)
			t.Chdir(t.TempDir())
			t.Setenv("TMPDIR", t.TempDir())

			source := tarantoolTestSource(t)
			setTestBundleSources(t, test.sources(source)...)

			ctx := InstallCtx{
				Program: binary.ProgramCe, Scope: binary.ScopeProject,
				requestedVersion: "3.7.1", Force: true,
			}
			require.NoError(t, Install(ctx))
			assert.Len(t, source.fetched, test.fetched)

			root, err := ctx.Scope.ArtifactPath()
			require.NoError(t, err)

			// The links are compared resolved, and the temporary directory of
			// macOS is under /var, a link to /private/var.
			root, err = filepath.EvalSymlinks(root)
			require.NoError(t, err)

			versionDir := filepath.Join(root, "versions", "tarantool", "tarantool_3.7.1")
			executable, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "tarantool"))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(versionDir, "tarantool"), executable)

			contents, err := os.ReadFile(executable)
			require.NoError(t, err)
			assert.Equal(t, test.want, string(contents))

			headers, err := filepath.EvalSymlinks(filepath.Join(root, "include", "tarantool"))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(versionDir, "include", "tarantool"), headers)
			assert.FileExists(t, filepath.Join(headers, "module.h"))
		})
	}
}
