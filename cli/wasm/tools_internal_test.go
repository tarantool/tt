package wasm

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, compareVersions("1.2", "1.2.0"))
	assert.Equal(t, -1, compareVersions("1.9.9", "1.10.0"))
	assert.Equal(t, 1, compareVersions("0.25.1", "0.25.0"))

	assert.True(t, versionFits("0.43.0", "0.43.0", "0.44.0"))
	assert.False(t, versionFits("0.44.0", "0.43.0", "0.44.0"))
	assert.False(t, versionFits("3.9.6", "3.10.0", ""))
	assert.True(t, versionFits("3.14.4", "3.10.0", ""))
}

func TestToolRequirement(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.261.0", toolWasmTools.Requirement())
	assert.Equal(t, "0.43.0", toolTinyGo.Requirement())
	assert.Equal(t, ">=1.25.0, <1.28.0", toolGo.Requirement())
	assert.Equal(t, ">=1.96.0", toolRustc.Requirement())
}

// fakeTool writes an executable script printing out into dir and returns dir.
func fakeTool(t *testing.T, name, out string) string {
	t.Helper()

	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + out + "'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))

	return dir
}

// The tests that run a fake tool are serial: a script written while a
// parallel test forks can be busy (ETXTBSY) when it is run.
func TestCheckTool(t *testing.T) {
	ctx := context.Background()

	ok := CheckTool(ctx, toolGo, fakeTool(t, "go", "go version go1.27.2 linux/amd64"))
	assert.True(t, ok.OK)
	assert.Equal(t, "1.27.2", ok.Version)

	old := CheckTool(ctx, toolGo, fakeTool(t, "go", "go version go1.24.1 linux/amd64"))
	assert.False(t, old.OK)
	assert.Equal(t, "version 1.24.1 does not fit", old.Problem())

	missing := CheckTool(ctx, toolGo, t.TempDir())
	assert.Equal(t, "missing", missing.Problem())
}

func TestEnsureToolchainReportsUserTools(t *testing.T) {
	statuses, err := checkToolchain(context.Background(), []Tool{toolDotnet},
		fakeTool(t, "dotnet", "8.0.100"), "csharp")
	require.ErrorIs(t, err, ErrToolchain)
	require.Len(t, statuses, 1)
	assert.Contains(t, err.Error(), "dotnet >=10.0.402, <10.0.403 (version 8.0.100 does not fit)")
	assert.Contains(t, FormatToolchain("csharp", statuses), "dotnet")
}

func TestFetchToolFromArchive(t *testing.T) {
	t.Parallel()

	archive := makeTarGz(t, []tarEntry{
		{name: "wasm-tools-1.261.0-x86_64-linux/README.md", body: "readme",
			kind: tar.TypeReg, mode: 0o644},
		{name: "wasm-tools-1.261.0-x86_64-linux/wasm-tools", body: "program",
			kind: tar.TypeReg, mode: 0o755},
	})
	server := newFileServer(t, map[string][]byte{"/wasm-tools.tar.gz": archive})
	fetcher := Fetcher{CacheDir: "", Client: nil}

	tool := func(name string) Tool {
		return Tool{Name: name, Release: &ToolRelease{Version: "1.261.0",
			Assets: map[string]ToolAsset{hostKey(): {
				URL: server.URL + "/wasm-tools.tar.gz", SHA256: fetch.SHA256(archive),
			}},
		}}
	}

	program := filepath.Join(t.TempDir(), "bin", "wasm-tools")
	require.NoError(t, fetchTool(t.Context(), tool("wasm-tools"), fetcher, program))

	got, err := os.ReadFile(program)
	require.NoError(t, err)
	assert.Equal(t, "program", string(got))

	err = fetchTool(t.Context(), tool("wkg"), fetcher, filepath.Join(t.TempDir(), "wkg"))
	require.ErrorIs(t, err, fetch.ErrNotInArchive)
	require.ErrorContains(t, err, "wasm-tools.tar.gz: ")
}

func TestToolchainsHaveHostBuilds(t *testing.T) {
	t.Parallel()

	for language, tools := range toolchains {
		for _, tool := range tools {
			if tool.Release == nil {
				continue
			}

			for _, host := range []string{hostLinuxAMD64, hostDarwinARM64} {
				asset, ok := tool.Release.Assets[host]
				assert.True(t, ok, "%s: %s has no %s build", language, tool.Name, host)
				assert.Len(t, asset.SHA256, 64, "%s %s", tool.Name, host)
			}
		}
	}
}
