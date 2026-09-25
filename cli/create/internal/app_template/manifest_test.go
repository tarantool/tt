package app_template_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

type manifestLoadOutput struct {
	manifest app_template.TemplateManifest
	errMsg   string
}

func TestLoadManifest(t *testing.T) {
	input := []string{
		"good_manifest.yaml",
		"missing_var_name.yaml",
		"missing_var_prompt.yaml",
		"non_existing.yaml",
	}
	output := map[string]manifestLoadOutput{
		"good_manifest.yaml": {
			app_template.TemplateManifest{
				Description: "Good template",
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
					},
				},
				PreHook:  `./hooks/pre-gen.sh`,
				PostHook: "./hooks/post-gen.sh",
				Include:  []string(nil),
			},
			"",
		},
		"missing_var_name.yaml": {
			app_template.TemplateManifest{},
			"invalid manifest format: missing variable name",
		},
		"missing_var_prompt.yaml": {
			app_template.TemplateManifest{},
			"invalid manifest format: missing user prompt",
		},
		"non_existing.yaml": {
			app_template.TemplateManifest{},
			"failed to get access to manifest file: " +
				"stat testdata/non_existing.yaml: no such file or directory",
		},
	}

	for _, inFile := range input {
		t.Run(inFile, func(t *testing.T) {
			manifest, err := app_template.LoadManifest(filepath.Join("testdata", inFile))
			if output[inFile].errMsg != "" {
				require.EqualError(t, err, output[inFile].errMsg)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, output[inFile].manifest, manifest)
		})
	}
}
