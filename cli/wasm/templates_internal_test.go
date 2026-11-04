package wasm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTarawasmComponent writes what tarawasm init leaves for a component of
// world: its config and wit/app.wit declaring the world.
func writeTarawasmComponent(t *testing.T, world string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "wit"), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, tarawasmConfig), []byte(`{
  "language": "python", "world": "`+world+`",
  "wit": {"path": "wit", "package": "example:app@0.1.0"},
  "source": "main.py", "output": "dist/`+world+`.wasm"
}`), filePerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "wit", "app.wit"), []byte(
		"package example:app@0.1.0;\n\nworld "+world+" {\n"+
			"  include tarantool:tarantool/guest@0.2.0;\n}\n"), filePerm))

	return dir
}

func TestApplyTemplate(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"call", "http"}, Templates("python"))
	assert.Empty(t, Templates("cobol"))

	dir := writeTarawasmComponent(t, "router")
	component := &Component{
		Name: "router", Dir: dir, Language: "python", Output: "dist/router.wasm",
		SDKVersion: "0.1.0",
	}

	module, err := applyTemplate(dir, "python", "http", component)
	require.NoError(t, err)
	assert.Contains(t, module, "fio.pathjoin(debug.sourcedir(), 'dist/router.wasm')")
	assert.Contains(t, module, "wasm.run(router)")

	world, err := os.ReadFile(filepath.Join(dir, "wit", "app.wit"))
	require.NoError(t, err)
	assert.Contains(t, string(world), "package example:app@0.1.0;")
	assert.Contains(t, string(world), "world router {")
	assert.Contains(t, string(world), "export wasi:cli/run@0.2.12;")
	assert.NoFileExists(t, filepath.Join(dir, "wit", "router.wit"))

	source, err := os.ReadFile(filepath.Join(dir, "main.py"))
	require.NoError(t, err)
	assert.Contains(t, string(source), "class Run(exports.Run):")
}

func TestApplyTemplateRefusesUnknown(t *testing.T) {
	t.Parallel()

	dir := writeTarawasmComponent(t, "calc")

	_, err := applyTemplate(dir, "python", "grpc", &Component{
		Name: "calc", Dir: dir, Language: "python", Output: "dist/calc.wasm", SDKVersion: "",
	})
	require.ErrorIs(t, err, errUnknownTemplate)
}

func TestWitName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"router", "my-api", "calc2"} {
		assert.True(t, witNameRe.MatchString(name), name)
	}

	for _, name := range []string{"Router", "my_api", "2calc", "api-", "-api"} {
		assert.False(t, witNameRe.MatchString(name), name)
	}
}

func TestStatusOfAnUnpreparedProject(t *testing.T) {
	project := writeProject(t, "0.1.0")

	report, err := Status(context.Background(), project)
	require.NoError(t, err)

	assert.Equal(t, "0.1.0", report.Declared)
	assert.Equal(t, ".tarantool/registry", report.Registry)
	assert.Empty(t, report.Rock)
	assert.Empty(t, report.Tarawasm)
	require.Len(t, report.Components, 1)
	assert.False(t, report.Components[0].SDKRestored)
	assert.Equal(t, []string{"python"}, report.Languages)

	joined := strings.Join(report.Problems, "\n")

	assert.Contains(t, joined, "tarawasm is not in")
	assert.Contains(t, joined, "the SDK of component router is not restored")
	assert.Contains(t, joined, "the python toolchain is not ready")
}

func TestStatusOfARockServer(t *testing.T) {
	project := writeProject(t, "0.1.0")
	path := filepath.Join(project, manifestFileName)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data),
		`registry = ".tarantool/registry"`, `registry = "https://rocks.example.test/v0.1.0/"`, 1)),
		filePerm))

	report, err := Status(context.Background(), project)
	require.NoError(t, err)

	_, hostErr := testRelease(t).HostPlatform()
	if hostErr == nil {
		assert.Contains(t, report.Rock, "https://rocks.example.test/v0.1.0/wasm-0.1.0-1.")
	}

	for _, problem := range report.Problems {
		assert.NotContains(t, problem, "no wasm-0.1.0-1", "a rock server is not looked into")
	}
}
