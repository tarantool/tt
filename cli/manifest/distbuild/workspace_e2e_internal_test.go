//go:build integration

package distbuild

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// produced is a tt module that uses sdk/build of this checkout, which no
// pushed commit of tt has.
const produced = `// Package produced is a tt module of a backend that is not written yet.
package produced

import "github.com/tarantool/tt/sdk/build"

// Err is what the core says of a build that left files of a type its
// backend does not produce.
var Err = build.ErrNotProduced
`

// TestWorkspaceOnThisCheckout writes the workspace of a distribution on the
// core of this checkout for a module that requires sdk/build at a commit no
// proxy has, and checks that the go command builds the module on the SDK
// of the checkout.
func TestWorkspaceOnThisCheckout(t *testing.T) {
	needGo(t)

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	sum, err := os.ReadFile(filepath.Join(coreDir, "sdk", "build", "go.sum"))
	require.NoError(t, err)

	root := t.TempDir()
	dir := filepath.Join(root, "tt-produced")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tt-produced\n\ngo 1.27.0\n\n"+
		"require github.com/tarantool/tt/sdk/build "+unpushedSDK+"\n")
	writeFile(t, filepath.Join(dir, "go.sum"), string(sum))
	writeFile(t, filepath.Join(dir, "produced.go"), produced)

	manifest := writeDistribution(t, filepath.Join(root, "dist"), "[core]\npath = \""+coreDir+"\"",
		"produced = \"../tt-produced\"\n")

	// What sdk/build requires comes from the proxy, or the module cache of
	// the machine, which a CI runner has not filled for sdk/build. The
	// modules of tt are private: the go command would take them from
	// GitHub, which has no such commit, so only the workspace can give it.
	env := append(slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOFLAGS=") ||
			strings.HasPrefix(entry, "GOPRIVATE=") || strings.HasPrefix(entry, "GONOPROXY=") ||
			strings.HasPrefix(entry, "GONOSUMDB=")
	}), "GOPRIVATE=github.com/tarantool/tt", "GOFLAGS=")

	result, err := Workspace(t.Context(), WorkspaceOptions{Manifest: manifest, Environ: env})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "go.work")}, filePaths(result))

	out, err := goIn(t, dir, env, "list", "-m", "all")
	require.NoError(t, err, out)
	assert.Contains(t, out, "github.com/tarantool/tt/sdk/build "+unpushedSDK+" => ")

	out, err = goIn(t, dir, env, "build", "./...")
	require.NoError(t, err, out)
}
