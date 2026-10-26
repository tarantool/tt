package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRocksServers checks that tt rocks queries the rock servers tt registry
// takes from TT_REGISTRIES or the manifest, and leaves LuaRocks its own
// servers when neither names any.
func TestRocksServers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TT_REGISTRIES", "")

	servers, err := rocksServers()
	require.NoError(t, err)
	assert.Nil(t, servers, "the defaults of tt registry are not handed to LuaRocks")

	manifest := "manifest_version = '0.1'\n\n[package]\nname = 'app'\n\n" +
		"[platform]\nregistries = ['repo']\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"), []byte(manifest),
		0o644))

	servers, err = rocksServers()
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "repo")}, servers)

	t.Setenv("TT_REGISTRIES", "https://a.example/, local")

	servers, err = rocksServers()
	require.NoError(t, err)
	assert.Equal(t, []string{"https://a.example/", filepath.Join(dir, "local")}, servers)
}
