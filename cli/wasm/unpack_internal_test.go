package wasm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

// tarEntry is one entry of a test archive.
type tarEntry struct {
	name, body, link string
	kind             byte
	mode             int64
}

// makeTarGz packs entries into a .tar.gz.
func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()

	var buffer bytes.Buffer

	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)

	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o644
		}

		require.NoError(t, writer.WriteHeader(&tar.Header{
			Name: entry.name, Linkname: entry.link, Typeflag: entry.kind,
			Mode: mode, Size: int64(len(entry.body)),
		}))

		_, err := writer.Write([]byte(entry.body))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())

	return buffer.Bytes()
}

func TestUnpackTreeStripsTopAndOverlays(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "tree")

	require.NoError(t, unpackTree(makeTarGz(t, []tarEntry{
		{name: "tinygo/", kind: tar.TypeDir},
		{name: "tinygo/bin/tinygo", body: "tinygo", kind: tar.TypeReg, mode: 0o755},
		{name: "tinygo/bin/wasm-opt", body: "old", kind: tar.TypeReg, mode: 0o755},
		{name: "tinygo/lib/libc.a", body: "lib", kind: tar.TypeReg},
		{name: "tinygo/lib/link.a", link: "libc.a", kind: tar.TypeSymlink},
	}), dest))
	require.NoError(t, unpackTree(makeTarGz(t, []tarEntry{
		{name: "binaryen-version_133/bin/wasm-opt", body: "new", kind: tar.TypeReg, mode: 0o755},
	}), dest))

	data, err := os.ReadFile(filepath.Join(dest, "bin", "wasm-opt"))
	require.NoError(t, err)
	assert.Equal(t, "new", string(data), "the overlay replaces what the tree had")
	assert.True(t, isExecutable(filepath.Join(dest, "bin", "tinygo")))

	target, err := os.Readlink(filepath.Join(dest, "lib", "link.a"))
	require.NoError(t, err)
	assert.Equal(t, "libc.a", target)
}

func TestUnpackTreeRefusesEscapes(t *testing.T) {
	t.Parallel()

	for name, entries := range map[string][]tarEntry{
		"dotdot":   {{name: "top/../../evil", body: "x", kind: tar.TypeReg}},
		"absolute": {{name: "/etc/evil", body: "x", kind: tar.TypeReg}},
		"symlink":  {{name: "top/bin/evil", link: "../../../etc/passwd", kind: tar.TypeSymlink}},
		"absolute symlink": {
			{name: "top/bin/evil", link: "/etc/passwd", kind: tar.TypeSymlink},
		},
		"hardlink": {{name: "top/evil", link: "top/../../outside", kind: tar.TypeLink}},
	} {
		err := unpackTree(makeTarGz(t, entries), filepath.Join(t.TempDir(), "tree"))
		require.ErrorIs(t, err, errUnsafeEntry, name)
	}
}

// TestInstallTreeTool installs a tree tool from a fake server: the tree and
// its overlay are unpacked, the program checked, the links made, and a tool
// kept out of bin is left out.
func TestInstallTreeTool(t *testing.T) {
	tree := makeTarGz(t, []tarEntry{
		{name: "sdk/bin/clang", kind: tar.TypeReg, mode: 0o755,
			body: "#!/bin/sh\necho 'clang version 21.1.0-wasi-sdk'\n"},
		{name: "sdk/share/wasi-sysroot/include/stdio.h", body: "", kind: tar.TypeReg},
	})
	overlay := makeTarGz(t, []tarEntry{
		{name: "extra/bin/wasm-ld", body: "#!/bin/sh\n", kind: tar.TypeReg, mode: 0o755},
	})
	server, _ := assetServer(t, nil)
	server.Close()

	files := map[string][]byte{"/tree.tar.gz": tree, "/overlay.tar.gz": overlay}
	fake := newFileServer(t, files)

	tool := Tool{
		Name: "clang", VersionArgs: []string{versionFlag}, Minimum: "19.1.5", Below: "",
		Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "34",
			Assets: map[string]ToolAsset{
				hostKey(): {URL: fake.URL + "/tree.tar.gz", SHA256: fetch.SHA256(tree)},
			},
			Wheel: false, Tree: "bin/clang",
			Overlays: map[string][]ToolAsset{
				hostKey(): {{URL: fake.URL + "/overlay.tar.gz", SHA256: fetch.SHA256(overlay)}},
			},
			Links: []string{"bin/wasm-ld"}, Unlinked: true, Dir: "wasi-sdk",
		},
	}

	root := t.TempDir()
	opts := ToolchainOptions{
		Root: root, Language: "c", Fetcher: Fetcher{CacheDir: "", Client: nil},
		TarawasmEnv: "", Output: nil,
	}

	program, err := installTool(context.Background(), tool, opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "versions", "wasi-sdk", "wasi-sdk_34", "bin", "clang"),
		program)
	assert.DirExists(t, filepath.Join(filepath.Dir(filepath.Dir(program)), "share", "wasi-sysroot"))
	assert.NoFileExists(t, filepath.Join(root, "bin", "clang"), "clang is kept out of bin")

	link, err := os.Readlink(filepath.Join(root, "bin", "wasm-ld"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(filepath.Dir(program), "wasm-ld"), link)

	// Installed already: nothing is downloaded again.
	fake.Close()

	again, err := installTool(context.Background(), tool, opts)
	require.NoError(t, err)
	assert.Equal(t, program, again)
}
