package steps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/create/internal/steps"
)

// writeFiles writes files, by their paths relative to dir, as "<path>".
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()

	for _, name := range names {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(name), 0o644))
	}
}

// appTemplateCtx returns a template context of a fresh application holding
// files.
func appTemplateCtx(t *testing.T, names ...string) app_template.TemplateCtx {
	t.Helper()

	templateCtx := app_template.NewTemplateContext()

	templateCtx.AppPath = t.TempDir()
	writeFiles(t, templateCtx.AppPath, names...)

	return templateCtx
}

// layOut runs LayOutPair on an application holding files.
func layOut(t *testing.T, names ...string) (string, error) {
	t.Helper()

	templateCtx := appTemplateCtx(t, names...)

	return templateCtx.AppPath, steps.LayOutPair{}.Run(&create_ctx.CreateCtx{}, &templateCtx)
}

// assertContent checks that path holds the file written as origin.
func assertContent(t *testing.T, path, origin string) {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, origin, string(content))
}

func TestLayOutPairMovesThePairOfTheRoot(t *testing.T) {
	appPath, err := layOut(t, "instances.yaml", "config.yaml", "init.lua")
	require.NoError(t, err)

	assertContent(t, filepath.Join(appPath, ".templates", "default", "instances.yaml"),
		"instances.yaml")
	assertContent(t, filepath.Join(appPath, ".templates", "default", "config.yaml"),
		"config.yaml")
	assert.NoFileExists(t, filepath.Join(appPath, "instances.yaml"))
	assert.NoFileExists(t, filepath.Join(appPath, "config.yaml"))
	assert.FileExists(t, filepath.Join(appPath, "init.lua"))
}

func TestLayOutPairMovesAConfigAlone(t *testing.T) {
	appPath, err := layOut(t, "config.yaml", "init.lua")
	require.NoError(t, err)

	assertContent(t, filepath.Join(appPath, ".templates", "default", "config.yaml"),
		"config.yaml")
	assert.NoFileExists(t, filepath.Join(appPath, ".templates", "default", "instances.yaml"))
}

func TestLayOutPairMovesThePairOfASubdirectory(t *testing.T) {
	appPath, err := layOut(t, "etc/app/instances.yaml", "etc/app/config.yaml", "init.lua")
	require.NoError(t, err)

	assertContent(t, filepath.Join(appPath, ".templates", "default", "instances.yaml"),
		"etc/app/instances.yaml")
	assertContent(t, filepath.Join(appPath, ".templates", "default", "config.yaml"),
		"etc/app/config.yaml")
	assert.NoDirExists(t, filepath.Join(appPath, "etc", "app"))
}

// A subdirectory keeps what else it holds.
func TestLayOutPairLeavesTheRestOfTheSubdirectory(t *testing.T) {
	appPath, err := layOut(t, "etc/instances.yaml", "etc/other.txt")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(appPath, "etc", "other.txt"))
	assert.FileExists(t, filepath.Join(appPath, ".templates", "default", "instances.yaml"))
}

// A config.yaml of a subdirectory with no instances.yaml beside it may be
// anything, and is no pair.
func TestLayOutPairLeavesAConfigOfASubdirectory(t *testing.T) {
	appPath, err := layOut(t, "test/config.yaml", "init.lua")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(appPath, "test", "config.yaml"))
	assert.NoDirExists(t, filepath.Join(appPath, ".templates"))
}

// A template that lays its pair out itself is left alone, a config.yaml of
// its root included.
func TestLayOutPairLeavesAPairInTemplates(t *testing.T) {
	appPath, err := layOut(t,
		".templates/prod/instances.yaml", ".templates/prod/config.yaml", "config.yaml")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(appPath, "config.yaml"))
	assert.FileExists(t, filepath.Join(appPath, ".templates", "prod", "instances.yaml"))
	assert.NoDirExists(t, filepath.Join(appPath, ".templates", "default"))
}

func TestLayOutPairRefusesInstancesInSeveralDirectories(t *testing.T) {
	_, err := layOut(t, "instances.yaml", "a/instances.yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a/instances.yaml, instances.yaml")
}

func TestLayOutPairDoesNothingWithoutAPair(t *testing.T) {
	appPath, err := layOut(t, "init.lua")
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(appPath, ".templates"))
}

func TestCheckProject(t *testing.T) {
	for name, files := range map[string][]string{
		"manifest":       {"app.manifest.toml"},
		"instances":      {".templates/default/instances.yaml"},
		"config":         {".templates/other/config.yaml"},
		"pair and code":  {".templates/default/instances.yaml", "init.lua"},
		"manifest alone": {"app.manifest.toml", "init.lua"},
	} {
		t.Run(name, func(t *testing.T) {
			templateCtx := appTemplateCtx(t, files...)

			require.NoError(t, steps.CheckProject{}.Run(&create_ctx.CreateCtx{}, &templateCtx))
		})
	}

	t.Run("no project", func(t *testing.T) {
		templateCtx := appTemplateCtx(t, "init.lua", "instances.yaml")

		err := steps.CheckProject{}.Run(&create_ctx.CreateCtx{TemplateName: "./tpl"}, &templateCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "template ./tpl makes no project: it writes neither "+
			"app.manifest.toml nor an instances.yaml or a config.yaml")
		assert.NotContains(t, err.Error(), "MANIFEST.yaml")
	})

	t.Run("no project, include list", func(t *testing.T) {
		templateCtx := appTemplateCtx(t, "init.lua")

		templateCtx.IsManifestPresent = true
		templateCtx.Manifest.Include = []string{"init.lua"}

		err := steps.CheckProject{}.Run(&create_ctx.CreateCtx{TemplateName: "./tpl"}, &templateCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MANIFEST.yaml")
	})
}
