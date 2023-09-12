package steps

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestRunHooks(t *testing.T) {
	workDir := t.TempDir()

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.PreHook = "pre-gen.sh"
	templateCtx.Manifest.PostHook = "post-gen.sh"

	require.NoError(t, copy.Copy("testdata/hooks", workDir))

	runPreHook := RunHook{HookType: "pre"}
	runPostHook := RunHook{HookType: "post"}
	assert.NoError(t, runPreHook.Run(&createCtx, &templateCtx))
	assert.NoError(t, runPostHook.Run(&createCtx, &templateCtx))
	assert.FileExists(t, filepath.Join(templateCtx.AppPath, "pre-script-invoked"))
	assert.FileExists(t, filepath.Join(templateCtx.AppPath, "post-script-invoked"))

	// Check if scripts are removed.
	assert.NoFileExists(t, filepath.Join(workDir, templateCtx.Manifest.PreHook))
	assert.NoFileExists(t, filepath.Join(workDir, templateCtx.Manifest.PostHook))
}

func TestRunHooksMissingScript(t *testing.T) {
	workDir := t.TempDir()

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.IsManifestPresent = true
	templateCtx.Manifest.PreHook = "pre-gen.sh"
	templateCtx.Manifest.PostHook = "post-gen.sh"

	runPreHook := RunHook{HookType: "pre"}
	runPostHook := RunHook{HookType: "post"}
	require.EqualError(t, runPreHook.Run(&createCtx, &templateCtx),
		fmt.Sprintf("error access to %[1]s: stat %[1]s: no such file or directory",
			filepath.Join(workDir, "pre-gen.sh")))

	require.EqualError(t, runPostHook.Run(&createCtx, &templateCtx),
		fmt.Sprintf("error access to %[1]s: stat %[1]s: no such file or directory",
			filepath.Join(workDir, "post-gen.sh")))

	// Emulate missing scripts in manifest file.
	templateCtx.Manifest.PreHook = ""
	templateCtx.Manifest.PostHook = ""

	require.NoError(t, runPreHook.Run(&createCtx, &templateCtx))
	require.NoError(t, runPostHook.Run(&createCtx, &templateCtx))
}
