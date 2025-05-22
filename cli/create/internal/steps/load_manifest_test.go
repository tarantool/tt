package steps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestManifestLoad(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, copy.Copy("testdata/cartridge", workDir))

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir
	loadManifest := LoadManifest{}
	require.NoError(t, loadManifest.Run(&createCtx, &templateCtx))

	expectedManifest := app_template.TemplateManifest{
		Description: "Cartridge template",
		Vars: []app_template.UserPrompt{
			{
				Prompt:  "Cluster cookie",
				Name:    "cluster_cookie",
				Default: "cookie",
				Re:      `^\w+$`,
			},
			{
				Prompt:  "User name",
				Name:    "user_name",
				Default: "admin",
				Re:      "",
			},
		},
		PreHook:  "./hooks/pre-gen.sh",
		PostHook: "./hooks/post-gen.sh",
	}

	require.True(t, templateCtx.IsManifestPresent)
	require.Equal(t, expectedManifest, templateCtx.Manifest)
}

func TestMissingManifest(t *testing.T) {
	workDir := t.TempDir()

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir

	loadManifest := LoadManifest{}
	require.NoError(t, loadManifest.Run(&createCtx, &templateCtx))
	require.False(t, templateCtx.IsManifestPresent)
}

func TestManifestInvalidYaml(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(workDir, app_template.DefaultManifestName),
		[]byte(`Description: [`), 0o644))

	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.AppPath = workDir

	loadManifest := LoadManifest{}
	require.EqualError(t, loadManifest.Run(&createCtx, &templateCtx), "failed to load manifest "+
		"file: failed to parse YAML: yaml: line 1: did not find expected node content")
}
