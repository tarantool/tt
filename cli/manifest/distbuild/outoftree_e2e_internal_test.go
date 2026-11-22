//go:build integration

package distbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smokeGoMod is the go.mod of testdata/outoftree as its author writes it to
// build it on a local copy of tt: the SDK modules it imports, and the
// sdk/connect the go.mod of sdk requires at a placeholder (see Set up the Go
// module of doc/how-to/write-a-module.md), each at a commit no proxy has and
// replaced by its directory in the copy. An SDK module that a package of sdk
// starts to import, required at a placeholder, which every module outside
// tt then has to require too, fails go mod tidy here.
const smokeGoMod = `module example.com/tt-smoke

go 1.27.0

require (
	github.com/tarantool/tt/sdk/v3 ` + unpushedSDK + `
	github.com/tarantool/tt/sdk/build/v3 ` + unpushedSDK + `
	github.com/tarantool/tt/sdk/connect/v3 ` + unpushedSDK + `
)

replace (
	github.com/tarantool/tt/sdk/v3 => CORE/sdk
	github.com/tarantool/tt/sdk/build/v3 => CORE/sdk/build
	github.com/tarantool/tt/sdk/connect/v3 => CORE/sdk/connect
)
`

// smokePackage is a package with a component the backend of the module
// builds.
const smokePackage = `manifest_version = '0.1'

[package]
name = 'demo'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.greeting]
path = 'greeting'

[components.greeting.build]
backend = 'smoke'
text = 'hello from a backend'
`

// TestOutOfTreeModule builds testdata/outoftree, a tt module with a go.mod of
// its own that uses nothing of tt but the public API of the SDK - a command,
// a build backend and a check of the files of a package - on the SDK of this
// checkout, as its author does: go mod tidy, go vet and go test of the
// module, then a distribution of tt with it, whose command runs and whose
// backend builds a package. A change of the SDK that breaks such a module
// fails it, which the modules of tt's repository, changed together with the
// SDK, need not show.
func TestOutOfTreeModule(t *testing.T) {
	t.Parallel()
	needGo(t)

	coreDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	root := t.TempDir()
	dir := filepath.Join(root, "tt-smoke")
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "outoftree"))))
	writeFile(t, filepath.Join(dir, "go.mod"), strings.ReplaceAll(smokeGoMod, "CORE", coreDir))

	// The sums of what the SDK requires, which go mod tidy would otherwise
	// look up; it drops those the module does not need.
	var sum strings.Builder

	for _, module := range []string{"sdk", "sdk/build", "sdk/connect"} {
		data, err := os.ReadFile(filepath.Join(coreDir, module, "go.sum"))
		require.NoError(t, err)
		sum.Write(data)
	}

	writeFile(t, filepath.Join(dir, "go.sum"), sum.String())

	env := append(slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOFLAGS=")
	}), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")

	for _, args := range [][]string{
		{"mod", "tidy"},
		{"vet", "./..."},
		{"test", "-count=1", "./..."},
	} {
		out, err := goIn(t, dir, env, args...)
		require.NoError(t, err, "go %s:\n%s", strings.Join(args, " "), out)
	}

	out, err := goIn(t, dir, env, "list", "-m", "-f", "{{.Replace.Dir}}",
		"github.com/tarantool/tt/sdk/v3")
	require.NoError(t, err, out)
	assert.Equal(t, filepath.Join(coreDir, "sdk"), strings.TrimSpace(out))

	// No standard module: the backend of the module is the only one of the
	// distribution.
	manifest := writeDistribution(t, filepath.Join(root, "dist"),
		"[core]\npath = \""+coreDir+"\"\n\n[distribution]\nstandard = false",
		"smoke = \"../tt-smoke\"\n")

	result, err := Run(t.Context(), Options{Manifest: manifest, GeneratedBy: "tt test"})
	require.NoError(t, err)

	output, err := exec.CommandContext(t.Context(), result.Output,
		"smoke", "greet", "world", "-o", "json").CombinedOutput()
	require.NoError(t, err, string(output))
	assert.JSONEq(t, `{"greeting": "hello, world"}`, string(output))

	output, err = exec.CommandContext(t.Context(), result.Output, "version", "--modules").
		CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Regexp(t, `(?m)^  smoke +\(devel\) +example\.com/tt-smoke$`, string(output))

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "app.manifest.toml"), smokePackage)
	writeFile(t, filepath.Join(project, "VERSION"), "1.0.0\n")
	writeFile(t, filepath.Join(project, "greeting", "README"), "Built by the backend smoke.\n")

	output, err = packageBuild(t, result.Output, project)
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "greeting.lua checked by smoke")

	data, err := os.ReadFile(filepath.Join(project, ".rocks", "share", "tarantool", "demo",
		"greeting.lua"))
	require.NoError(t, err)
	assert.Equal(t, "return \"hello from a backend\"\n", string(data))
}
