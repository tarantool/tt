package steps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestTemplateRender(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, copy.Copy("testdata/cartridge", workDir))

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.Vars = map[string]string{
		"cluster_cookie": "cookie",
		"user_name":      "admin",
		"app_name":       "app1",
	}

	renderTemplate := RenderTemplate{}
	require.NoError(t, renderTemplate.Run(&createCtx, &templateCtx))

	assert.FileExists(t, filepath.Join(workDir, "app1.yml"))

	configFileName := filepath.Join(workDir, "config.lua")
	require.FileExists(t, configFileName)
	buf, err := os.ReadFile(configFileName)
	require.NoError(t, err)
	const expectedText = `cluster_cookie = cookie
login = admin
`
	require.Equal(t, expectedText, string(buf))

	userFileName := filepath.Join(workDir, "admin.cfg")
	require.FileExists(t, userFileName)
	buf, err = os.ReadFile(userFileName)
	require.NoError(t, err)
	const userCfgExpectedText = `user=admin
`
	require.Equal(t, userCfgExpectedText, string(buf))
}

func TestTemplateRenderMissingVar(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, copy.Copy("testdata/cartridge", workDir))

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir

	renderTemplate := RenderTemplate{}
	require.EqualError(t, renderTemplate.Run(&createCtx, &templateCtx), "template instantiation "+
		"error: template execution failed: template: "+
		"config.lua.tt.template:1:19: executing \"config.lua.tt.template\" "+
		"at <.cluster_cookie>: map has no entry for key \"cluster_cookie\"")
}

func TestTemplateRenderMissingVarInFileName(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, copy.Copy("testdata/cartridge", workDir))

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.Vars = map[string]string{
		"cluster_cookie": "cookie",
		"user_name":      "admin",
	}

	renderTemplate := RenderTemplate{}
	require.Error(t, renderTemplate.Run(&createCtx, &templateCtx))
}
