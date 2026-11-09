package rws

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
)

func TestCheckPlatform(t *testing.T) {
	for _, test := range []struct {
		name   string
		goarch string
		setup  func(*testing.T, string)
		wantOK bool
	}{
		{name: "glibc amd64", goarch: "amd64", wantOK: true},
		{name: "glibc arm64", goarch: "arm64", wantOK: true},
		{name: "unsupported architecture", goarch: "386"},
		{
			name: "missing loader", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(root, "lib64/ld-linux-x86-64.so.2")))
			},
		},
		{
			name: "missing libc", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(root, "lib64/libc.so.6")))
			},
		},
		{
			name: "missing libgcc amd64", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(root, "lib64/libgcc_s.so.1")))
			},
		},
		{
			name: "missing libgcc arm64", goarch: "arm64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(root, "lib/libgcc_s.so.1")))
			},
		},
		{
			name: "libgcc directory", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()

				filename := filepath.Join(root, "lib64/libgcc_s.so.1")
				require.NoError(t, os.Remove(filename))
				require.NoError(t, os.Mkdir(filename, 0o755))
			},
		},
		{
			name: "libgcc symlink", goarch: "amd64", wantOK: true,
			setup: func(t *testing.T, root string) {
				t.Helper()

				directory := filepath.Join(root, "lib64")
				require.NoError(t,
					os.WriteFile(filepath.Join(directory, "libgcc_s-1.so"), nil, 0o644))
				require.NoError(t, os.Remove(filepath.Join(directory, "libgcc_s.so.1")))
				require.NoError(t,
					os.Symlink("libgcc_s-1.so", filepath.Join(directory, "libgcc_s.so.1")))
			},
		},
		{
			name: "not executable", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t,
					os.Chmod(filepath.Join(root, "lib64/ld-linux-x86-64.so.2"), 0o644))
			},
		},
		{
			name: "directory", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()

				filename := filepath.Join(root, "lib64/ld-linux-x86-64.so.2")
				require.NoError(t, os.Remove(filename))
				require.NoError(t, os.Mkdir(filename, 0o755))
			},
		},
		{
			name: "glibc symlink", goarch: "amd64", wantOK: true,
			setup: func(t *testing.T, root string) {
				t.Helper()

				directory := filepath.Join(root, "usr/lib")
				require.NoError(t, os.MkdirAll(directory, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(directory, "ld.so"), nil, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(directory, "libc.so.6"), nil, 0o644))
				require.NoError(t,
					os.WriteFile(filepath.Join(directory, "libgcc_s.so.1"), nil, 0o644))

				filename := filepath.Join(root, "lib64/ld-linux-x86-64.so.2")
				require.NoError(t, os.Remove(filename))
				require.NoError(t, os.Symlink("../usr/lib/ld.so", filename))
			},
		},
		{
			name: "musl compatibility path", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t,
					os.WriteFile(filepath.Join(root, "lib/ld-musl-x86_64.so.1"), nil, 0o755))

				filename := filepath.Join(root, "lib64/ld-linux-x86-64.so.2")
				require.NoError(t, os.Remove(filename))
				require.NoError(t, os.Symlink("../lib/ld-musl-x86_64.so.1", filename))
			},
		},
		{
			name: "gcompat libc", goarch: "amd64",
			setup: func(t *testing.T, root string) {
				t.Helper()
				require.NoError(t,
					os.WriteFile(filepath.Join(root, "lib64/libgcompat.so.0"), nil, 0o755))

				filename := filepath.Join(root, "lib64/libc.so.6")
				require.NoError(t, os.Remove(filename))
				require.NoError(t, os.Symlink("libgcompat.so.0", filename))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			systemRoot := packagePlatformFixture(t)
			if test.setup != nil {
				test.setup(t, systemRoot)
			}

			err := checkPlatform(test.goarch, systemRoot)
			if test.wantOK {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errUnsupportedPlatform)
			}
		})
	}
}

func TestListChecksPlatformBeforeHTTP(t *testing.T) {
	for goarch, libraryPath := range map[string]string{
		"amd64": "lib64/libgcc_s.so.1",
		"arm64": "lib/libgcc_s.so.1",
	} {
		t.Run(goarch, func(t *testing.T) {
			recorder := recordLogs(t)
			requests := 0
			client := packageTestClient(t, func(
				writer http.ResponseWriter, _ *http.Request,
			) {
				requests++

				writer.WriteHeader(http.StatusInternalServerError)
			})

			source := packageTestSource(t, client)
			require.NoError(t, os.Remove(filepath.Join(source.systemRoot, libraryPath)))

			artifacts, err := source.List(t.Context(), packageTestQuery(goarch, "3.7.1"))
			require.NoError(t, err, "an unsupported host has no builds, it is no failure")
			require.Empty(t, artifacts)
			require.Zero(t, requests, "unsupported hosts must not access the package repository")
			requireLogged(t, recorder, "glibc loader")
			requireLogged(t, recorder, "libgcc_s.so.1")
		})
	}
}

func TestListWithoutBuilds(t *testing.T) {
	for _, test := range []struct {
		name  string
		query bundle.Query
	}{
		{"macOS", bundle.Query{
			Product: product, OS: "darwin", Arch: goarchARM64, Channel: bundle.ChannelRelease,
		}},
		{"unsupported architecture", packageTestQuery("386", "3.7.1")},
		{"unsupported release series", packageTestQuery(goarchAMD64, "2.11.8")},
		{"unresolved version", packageTestQuery(goarchAMD64, "master")},
		{"development builds", bundle.Query{
			Product: product, OS: goosLinux, Arch: goarchAMD64, Channel: bundle.ChannelDev,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := packageTestClient(t, func(http.ResponseWriter, *http.Request) {
				t.Error("a query the repository has no builds for must not make HTTP requests")
			})

			artifacts, err := packageTestSource(t, client).List(t.Context(), test.query)
			require.NoError(t, err)
			require.Empty(t, artifacts)
		})
	}

	_, err := New().List(t.Context(), bundle.Query{
		Product: "tt", OS: goosLinux, Arch: goarchAMD64, Channel: bundle.ChannelRelease,
	})
	require.ErrorIs(t, err, errUnknownProduct)
}

// packagePlatformFixture needs only file metadata: empty files must suffice.
func packagePlatformFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, filename := range []string{
		"lib64/ld-linux-x86-64.so.2", "lib/ld-linux-aarch64.so.1",
	} {
		filename = filepath.Join(root, filename)
		require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
		require.NoError(t, os.WriteFile(filename, nil, 0o755))

		for _, library := range []string{"libc.so.6", "libgcc_s.so.1"} {
			require.NoError(t,
				os.WriteFile(filepath.Join(filepath.Dir(filename), library), nil, 0o644))
		}
	}

	return root
}
