package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNarrowToTheOnlyFile pins the narrowing of a root that holds nothing but
// the path: no other entry is left out, and the configuration says so with an
// empty ignoreDir, not a null one, which EmmyLua refuses, dropping the whole
// configuration with the LuaJIT runtime in it.
func TestNarrowToTheOnlyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "hello.lua")
	require.NoError(t, os.WriteFile(file, []byte("return 0ULL\n"), 0o600))

	narrowing, err := Narrow(filepath.Join(t.TempDir(), "emmylua"), root, []string{file})
	require.NoError(t, err)

	t.Cleanup(func() { _ = narrowing.Remove() })

	require.NotEmpty(t, narrowing.Config)

	data, err := os.ReadFile(narrowing.Config)
	require.NoError(t, err)

	var config struct {
		Workspace map[string]json.RawMessage `json:"workspace"`
	}

	require.NoError(t, json.Unmarshal(data, &config))
	assert.JSONEq(t, `[]`, string(config.Workspace["ignoreDir"]))
}
