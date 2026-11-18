package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// makeMirror makes dir a mirror with the tarantool of version for this
// machine, repacked from a fake installation.
func makeMirror(t *testing.T, dir, ver string) {
	t.Helper()

	installation := t.TempDir()
	for name, content := range map[string]string{
		"tarantool": "#!/bin/sh\n", "include/tarantool/module.h": "/* module */",
	} {
		path := filepath.Join(installation, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	}

	_, err := mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramCe, Version: ver, Installation: installation,
		Source: nil, OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	require.NoError(t, err)
}

func TestBinaryMirrors(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	t.Setenv("TT_REGISTRIES", "")

	withBinaries := filepath.Join(work, "mirror")
	makeMirror(t, withBinaries, "3.9.0")

	rocksOnly := filepath.Join(work, "rocks")
	require.NoError(t, os.MkdirAll(rocksOnly, 0o755))

	// Nothing configured: no mirror, and no default server is looked at.
	mirrors, err := binaryMirrors(nil, "")
	require.NoError(t, err)
	assert.Empty(t, mirrors)

	mirrors, err = binaryMirrors([]string{"rocks", "https://rocks.example.org", "mirror"}, "")
	require.NoError(t, err)
	require.Len(t, mirrors, 1)
	assert.Equal(t, withBinaries, mirrors[0].Dir())

	// TT_REGISTRIES is taken without the flag; the flag replaces it.
	t.Setenv("TT_REGISTRIES", "mirror")

	mirrors, err = binaryMirrors(nil, "")
	require.NoError(t, err)
	require.Len(t, mirrors, 1)

	mirrors, err = binaryMirrors([]string{"rocks"}, "")
	require.NoError(t, err)
	assert.Empty(t, mirrors)
}

func TestInstallFromMirrors(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	t.Setenv("TT_REGISTRIES", "")

	makeMirror(t, filepath.Join(work, "mirror"), "3.9.0")

	saved := binary.BundleSources()

	t.Cleanup(func() { binary.SetBundleSources(saved) })

	saveRegistries := binaryRegistries

	t.Cleanup(func() { binaryRegistries = saveRegistries })

	binaryRegistries = []string{"mirror"}

	for requested, fromMirror := range map[string]bool{
		"": true, "3.9.0": true, "3.9": true, "3": true, "3.8.1": false, "3.90.0": false,
	} {
		binary.SetBundleSources(nil)

		require.NoError(t, installFromMirrors(binary.ProgramCe, requested, ""))

		source, err := binary.BundleSource(binary.ProgramCe)
		if !fromMirror {
			require.ErrorIs(t, err, binary.ErrNoBundleSource, requested)

			continue
		}

		require.NoError(t, err, requested)
		assert.Equal(t, "mirror "+filepath.Join(work, "mirror"), source.Name(), requested)
	}

	// A list of rock servers tt cannot use fails the install when --registry
	// gives it, and is only a warning when TT_REGISTRIES does.
	binaryRegistries = []string{"mirror", "mirror"}

	require.ErrorContains(t, installFromMirrors(binary.ProgramCe, "", ""), "duplicate")

	binaryRegistries = nil

	t.Setenv("TT_REGISTRIES", "mirror,,mirror")
	require.NoError(t, installFromMirrors(binary.ProgramCe, "", ""))
}

func TestPinnedInstallArgs(t *testing.T) {
	dir := t.TempDir()

	write := func(platform, lock string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName),
			[]byte("manifest_version = '0.1'\n\n[package]\nname = 'app'\n\n[platform]\n"+
				platform), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, lockFileName),
			[]byte("lock_version = '0.1'\nmanifest_version = '0.1'\ngenerated_by = 'tt'\n"+
				"manifest_hash = 'x'\n"+lock), 0o644))
	}

	write("tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n",
		"tarantool_version = '3.9.0'\ntt_version = '3.0.0'\ntcm_version = '1.3.1'\n")
	assert.Equal(t, []string{"3.9.0"}, pinnedInstallArgs("tarantool", dir))
	assert.Equal(t, []string{"3.0.0"}, pinnedInstallArgs("tt", dir))
	// [platform].tcm is not set: a pin of it is no pin.
	assert.Empty(t, pinnedInstallArgs("tcm", dir))
	assert.Empty(t, pinnedInstallArgs("tarantool-sdk", dir))
	assert.Empty(t, pinnedInstallArgs("tarantool", ""))

	write("tarantool = '>=3.0.0[ee]'\ntt = '>=2.0.0[ee]'\ntcm = '>=1.0.0'\n",
		"tarantool_version = '3.4.1'\ntt_version = '2.15.0'\ntcm_version = '1.3.1'\n")
	assert.Equal(t, []string{"3.4.1ee"}, pinnedInstallArgs("tarantool", dir))
	assert.Equal(t, []string{"2.15.0ee"}, pinnedInstallArgs("tt", dir))
	assert.Equal(t, []string{"1.3.1"}, pinnedInstallArgs("tcm", dir))

	write("tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n", "")
	assert.Empty(t, pinnedInstallArgs("tarantool", dir))
}
