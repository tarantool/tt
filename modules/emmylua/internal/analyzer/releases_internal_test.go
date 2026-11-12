package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupRelease(t *testing.T) {
	t.Parallel()

	release, err := LookupRelease("0.25.1")
	require.NoError(t, err)
	assert.Equal(t, "0.25.1-1", release.RockVersion())
	assert.Equal(t,
		"https://github.com/EmmyLuaLs/emmylua-analyzer-rust/releases/download/0.25.1/"+
			"emmylua_ls-darwin-arm64.tar.gz",
		release.AssetURL(Platform{Suffix: "darwin-arm64"}.Asset(LanguageServer)))
	assert.Equal(t,
		"https://github.com/EmmyLuaLs/emmylua-analyzer-rust/archive/0.25.1.tar.gz",
		release.SourceURL())

	for _, platform := range release.Platforms {
		for _, program := range Programs {
			assert.Len(t, platform.SHA256[program], 64, "%s %s", platform.Suffix, program)
		}
	}

	_, err = LookupRelease("9.9.9")
	require.ErrorIs(t, err, ErrUnknownRelease)
	assert.Contains(t, err.Error(), "0.25.1, 0.30.0pre1")
}

func TestReleasePlatform(t *testing.T) {
	t.Parallel()

	release, err := LookupRelease("0.25.1")
	require.NoError(t, err)

	platform, err := release.platform("darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, "macosx-aarch64", platform.RockArch())
	assert.Equal(t, "emmylua_check-darwin-arm64.tar.gz", platform.Asset(Check))

	_, err = release.platform("freebsd", "amd64")
	require.ErrorIs(t, err, ErrUnsupportedPlatform)

	local, err := LookupRelease("0.30.0pre1")
	require.NoError(t, err)

	_, err = local.platform("linux", "amd64")
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
	assert.Contains(t, err.Error(), "--from")
}

func TestDownloadableVersion(t *testing.T) {
	t.Parallel()

	// DefaultVersion is a commit, with no builds: 0.25.1 has them.
	version, err := DownloadableVersion()
	require.NoError(t, err)
	assert.Equal(t, "0.25.1", version)
}

//nolint:paralleltest // Sets the environment.
func TestDownloadableVersionReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "releases.json")

	t.Setenv(ReleasesEnv, path)

	// Versions compare as versions, not as strings.
	require.NoError(t, os.WriteFile(path, []byte(`{
		"0.9.0": {"version": "0.9.0", "platforms": [{"goos": "linux"}]},
		"0.10.0": {"version": "0.10.0", "platforms": [{"goos": "linux"}]},
		"0.11.0pre1": {"version": "0.11.0pre1"}
	}`), 0o644))

	version, err := DownloadableVersion()
	require.NoError(t, err)
	assert.Equal(t, "0.10.0", version)

	require.NoError(t, os.WriteFile(path,
		[]byte(`{"0.11.0pre1": {"version": "0.11.0pre1"}}`), 0o644))

	_, err = DownloadableVersion()
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
}

func TestRockArch(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "linux-x86_64", RockArch("linux", "amd64"))
	assert.Equal(t, "linux-aarch64", RockArch("linux", "arm64"))
	assert.Equal(t, "macosx-x86_64", RockArch("darwin", "amd64"))
	assert.Equal(t, "macosx-aarch64", RockArch("darwin", "arm64"))
}

//nolint:paralleltest // Sets the environment.
func TestReleasesEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "releases.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"1.0.0": {
		"version": "1.0.0", "rock_revision": 2, "ref": "abc", "source_md5": "00",
		"base_url": "https://mirror.example/emmylua/"
	}}`), 0o644))
	t.Setenv(ReleasesEnv, path)

	release, err := LookupRelease("1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0-2", release.RockVersion())
	assert.Equal(t, "https://mirror.example/emmylua/luafmt-x.tar.gz",
		release.AssetURL("luafmt-x.tar.gz"))
	assert.Equal(t, []string{"1.0.0"}, Versions())

	_, err = LookupRelease(DefaultVersion)
	require.ErrorIs(t, err, ErrUnknownRelease)
}
