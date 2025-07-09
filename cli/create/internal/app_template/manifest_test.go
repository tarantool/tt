package app_template

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

type manifestLoadOutput struct {
	manifest TemplateManifest
	errMsg   string
}

func TestLoadManifest(t *testing.T) {
	assert := assert.New(t)

	input := []string{
		"good_manifest.yaml",
		"missing_var_name.yaml",
		"missing_var_prompt.yaml",
		"non_existing.yaml",
	}
	output := map[string]manifestLoadOutput{
		"good_manifest.yaml": {
			TemplateManifest{
				Description: "Good template",
				Vars: []UserPrompt{
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
			TemplateManifest{},
			"invalid manifest format: missing variable name",
		},
		"missing_var_prompt.yaml": {
			TemplateManifest{},
			"invalid manifest format: missing user prompt",
		},
		"non_existing.yaml": {
			TemplateManifest{},
			"failed to get access to manifest file: " +
				"stat testdata/non_existing.yaml: no such file or directory",
		},
	}

	for _, inFile := range input {
		manifest, err := LoadManifest(filepath.Join("testdata", inFile))
		if output[inFile].errMsg == "" {
			if !assert.Nil(err) {
				continue
			}
		} else {
			assert.EqualError(err, output[inFile].errMsg)
			continue
		}

		assert.Equal(manifest, output[inFile].manifest)
	}
}
