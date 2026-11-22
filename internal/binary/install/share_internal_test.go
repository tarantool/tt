package install

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/v3/bundle"
	"github.com/tarantool/tt/sdk/v3/execfile"
	"github.com/tarantool/tt/v3/internal/binary"
)

// assertInstallationFiles checks the files of the installation directory
// with the contents they must have; "" is a file that must not be there.
func assertInstallationFiles(t *testing.T, directory string, files map[string]string) {
	t.Helper()

	for name, want := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if want == "" {
			assert.NoFileExists(t, path)

			continue
		}

		contents, err := os.ReadFile(path)
		if assert.NoError(t, err, name) {
			assert.Equal(t, want, string(contents), name)
		}
	}
}

// TestInstallPrebuiltKeepsShareAndLicenses keeps the share/tarantool of a
// prebuilt Tarantool, the EmmyLua stubs of its built-in modules among them,
// and the license files of the archive in the installation.
func TestInstallPrebuiltKeepsShareAndLicenses(t *testing.T) {
	archive := ttPackageTestArchive(t,
		ttArchiveEntry{name: "tarantool", kind: tar.TypeReg, mode: 0o755, content: "executable"},
		ttArchiveEntry{name: "LICENSE", kind: tar.TypeReg, mode: 0o644, content: "license"},
		ttArchiveEntry{name: "AUTHORS", kind: tar.TypeReg, mode: 0o644, content: "authors"},
		ttArchiveEntry{name: "README.md", kind: tar.TypeReg, mode: 0o644, content: "readme"},
		ttArchiveEntry{
			name: "include/tarantool/module.h", kind: tar.TypeReg, mode: 0o644, content: "module",
		},
		ttArchiveEntry{name: "share/", kind: tar.TypeDir, mode: 0o755},
		ttArchiveEntry{name: "share/tarantool/", kind: tar.TypeDir, mode: 0o755},
		// A file without its directory entry.
		ttArchiveEntry{
			name: "share/tarantool/emmylua/box.lua", kind: tar.TypeReg, mode: 0o644, content: "box",
		},
		ttArchiveEntry{
			name: "share/man/tarantool.1", kind: tar.TypeReg, mode: 0o644, content: "man",
		},
		ttArchiveEntry{name: "doc/LICENSE", kind: tar.TypeReg, mode: 0o644, content: "nested"},
	)
	digest := sha256.Sum256(archive)
	setTestBundleSources(t, &prebuiltTestSource{
		product: binary.ProgramCe.String(),
		artifacts: []bundle.Artifact{{
			Version: "3.9.0", Name: "tarantool-3.9.0.tar.gz", Locator: "3.9.0",
			SHA256: hex.EncodeToString(digest[:]),
		}},
		archive: archive,
	})

	prepared, err := preparePrebuilt(t.Context(), binary.ProgramCe, "3.9.0", t.TempDir(), false)
	require.NoError(t, err)

	installation := filepath.Join(t.TempDir(), "tarantool_3.9.0")
	require.NoError(t, copyProgramArtifact(installation, prepared))

	assertInstallationFiles(t, installation, map[string]string{
		"tarantool":                       "executable",
		"include/tarantool/module.h":      "module",
		"share/tarantool/emmylua/box.lua": "box",
		"LICENSE":                         "license",
		"AUTHORS":                         "authors",
		"README.md":                       "",
		"share/man/tarantool.1":           "",
		"doc/LICENSE":                     "",
	})

	info, err := os.Stat(filepath.Join(installation, "LICENSE"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestInstallPrebuiltTtKeepsLicense keeps the LICENSE goreleaser puts into
// the archives of tt, and no share/tarantool, which is Tarantool's.
func TestInstallPrebuiltTtKeepsLicense(t *testing.T) {
	archive := ttPackageTestArchive(t,
		ttArchiveEntry{name: "tt", kind: tar.TypeReg, mode: 0o755, content: "executable"},
		ttArchiveEntry{name: "LICENSE", kind: tar.TypeReg, mode: 0o644, content: "license"},
		ttArchiveEntry{name: "CHANGELOG.md", kind: tar.TypeReg, mode: 0o644, content: "changes"},
		ttArchiveEntry{name: "share/tarantool/x.lua", kind: tar.TypeReg, mode: 0o644, content: "x"},
	)
	setTestBundleSources(t, &prebuiltTestSource{
		product:   binary.ProgramTt.String(),
		artifacts: []bundle.Artifact{{Version: "3.0.0", Name: "tt-3.0.0.tar.gz", Locator: "3.0.0"}},
		archive:   archive,
	})

	prepared, err := preparePrebuilt(t.Context(), binary.ProgramTt, "v3.0.0", t.TempDir(), false)
	require.NoError(t, err)

	installation := filepath.Join(t.TempDir(), "tt_3.0.0")
	require.NoError(t, copyProgramArtifact(installation, prepared))

	assertInstallationFiles(t, installation, map[string]string{
		"tt": "executable", "LICENSE": "license", "CHANGELOG.md": "",
		"share/tarantool/x.lua": "",
	})
}

// TestInstallSourceBuildKeepsShareAndLicenses keeps the share/tarantool the
// build installs into its prefix and the license files of the source tree.
func TestInstallSourceBuildKeepsShareAndLicenses(t *testing.T) {
	t.Chdir(t.TempDir())

	commands := t.TempDir()
	for command, script := range map[string]string{
		// git runs in the directory it clones into.
		"git": `#!/bin/sh
if [ "$1" = clone ]; then
	printf license > LICENSE
	printf authors > AUTHORS
fi
`,
		"cmake": "#!/bin/sh\nexit 0\n",
		"make": `#!/bin/sh
set -e
mkdir -p tarantool-prefix/bin tarantool-prefix/include/tarantool
mkdir -p tarantool-prefix/share/tarantool/emmylua
printf 'built' > tarantool-prefix/bin/tarantool
chmod 755 tarantool-prefix/bin/tarantool
printf 'module' > tarantool-prefix/include/tarantool/module.h
printf 'box' > tarantool-prefix/share/tarantool/emmylua/box.lua
`,
	} {
		require.NoError(t,
			execfile.WriteFile(filepath.Join(commands, command), []byte(script), 0o755))
	}

	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := InstallCtx{
		Program: binary.ProgramCe, Scope: binary.ScopeProject, Force: true,
		SourceBuild: true, requestedVersion: masterVersion,
	}
	require.NoError(t, Install(ctx))

	assertInstallationFiles(t, filepath.Join(".tarantool", "versions", "tarantool",
		"tarantool_master"), map[string]string{
		"tarantool":                       "built",
		"include/tarantool/module.h":      "module",
		"share/tarantool/emmylua/box.lua": "box",
		"LICENSE":                         "license",
		"AUTHORS":                         "authors",
	})
}

// TestInstallSDKKeepsShareAndLicenses keeps the share/tarantool of an SDK
// with Tarantool Enterprise and its license with every product.
func TestInstallSDKKeepsShareAndLicenses(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())

	directory := t.TempDir()
	writeTestSDK(t, directory, "gc64-3.9.0-0-r100", "")

	require.NoError(t, Install(sdkTestContext(binary.ScopeProject, directory, "")))

	versions := filepath.Join(".tarantool", "versions")
	assertInstallationFiles(t, filepath.Join(versions, "tarantool-ee",
		"tarantool_sdk_gc64-3.9.0-0-r100"), map[string]string{
		"share/tarantool/emmylua/box.lua": "-- box",
		"LICENSE":                         "license",
		"VERSION":                         "",
	})
	assertInstallationFiles(t, filepath.Join(versions, "tt-ee", "tt_sdk_gc64-3.9.0-0-r100"),
		map[string]string{"LICENSE": "license", "share/tarantool/emmylua/box.lua": ""})

	// A standalone Enterprise installation of the same archive keeps them too.
	ctx := sdkTestContext(binary.ScopeProject, directory, "3.9.0")

	ctx.Program = binary.ProgramEe

	require.NoError(t, Install(ctx))
	assertInstallationFiles(t, filepath.Join(versions, "tarantool-ee", "tarantool-ee_3.9.0"),
		map[string]string{
			"share/tarantool/emmylua/box.lua": "-- box",
			"LICENSE":                         "license",
		})
}
