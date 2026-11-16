package app_template_test

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/create/builtin_templates"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
)

func TestUserPromptValidate(t *testing.T) {
	replicas := app_template.UserPrompt{
		Name: "num_replicas", Type: app_template.VarTypeInteger, Min: new(2),
	}
	bounded := app_template.UserPrompt{
		Name: "count", Type: app_template.VarTypeInteger, Min: new(1), Max: new(5),
	}
	capped := app_template.UserPrompt{
		Name: "count", Type: app_template.VarTypeInteger, Max: new(5),
	}
	anyInt := app_template.UserPrompt{Name: "count", Type: app_template.VarTypeInteger}
	odd := app_template.UserPrompt{
		Name: "replicas", Type: app_template.VarTypeInteger, Min: new(3), Re: `^\d*[13579]$`,
	}
	word := app_template.UserPrompt{Name: "user", Re: `^\w+$`}
	text := app_template.UserPrompt{Name: "password"}

	tests := []struct {
		name   string
		prompt app_template.UserPrompt
		value  string
		errMsg string
	}{
		{"integer ok", replicas, "3", ""},
		{"integer at min", replicas, "2", ""},
		{"integer below min", replicas, "1", `"1" is not an integer >= 2`},
		{"integer with a suffix", replicas, "3abc", `"3abc" is not an integer >= 2`},
		{"integer with a prefix", replicas, "x10", `"x10" is not an integer >= 2`},
		{"integer empty", replicas, "", `"" is not an integer >= 2`},
		{"bounded ok", bounded, "5", ""},
		{"bounded above", bounded, "6", `"6" is not an integer from 1 to 5`},
		{"capped above", capped, "6", `"6" is not an integer <= 5`},
		{"unbounded not a number", anyInt, "many", `"many" is not an integer`},
		{"unbounded negative", anyInt, "-3", ""},
		{"integer and re ok", odd, "5", ""},
		{"integer and re even", odd, "4", `"4" does not match ^\d*[13579]$`},
		{"integer and re below min", odd, "1", `"1" is not an integer >= 3`},
		{"re ok", word, "admin", ""},
		{"re mismatch", word, "ad min", `"ad min" does not match ^\w+$`},
		{"string takes anything", text, "3abc !", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.prompt.Validate(test.value)
			if test.errMsg == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, test.errMsg)
		})
	}
}

func TestLoadManifestVarTypes(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		errMsg   string
	}{
		{
			name: "integer with bounds",
			manifest: "vars:\n  - prompt: Count\n    name: count\n    type: integer\n" +
				"    min: 1\n    max: 9\n",
		},
		{
			name:     "explicit string",
			manifest: "vars:\n  - prompt: Name\n    name: user\n    type: string\n",
		},
		{
			name:     "unknown type",
			manifest: "vars:\n  - prompt: Count\n    name: count\n    type: number\n",
			errMsg: `invalid manifest format: count: unknown type "number", ` +
				`expected "string" or "integer"`,
		},
		{
			name:     "bounds of a string",
			manifest: "vars:\n  - prompt: Count\n    name: count\n    min: 1\n",
			errMsg:   `invalid manifest format: count: min and max need type "integer"`,
		},
		{
			name: "min above max",
			manifest: "vars:\n  - prompt: Count\n    name: count\n    type: integer\n" +
				"    min: 5\n    max: 1\n",
			errMsg: "invalid manifest format: count: min is greater than max: 5 > 1",
		},
		{
			name:     "bad regular expression",
			manifest: "vars:\n  - prompt: Count\n    name: count\n    re: '[0-9'\n",
			errMsg: "invalid manifest format: count: invalid regular expression: " +
				"error parsing regexp: missing closing ]: `[0-9`",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifestPath := filepath.Join(t.TempDir(), app_template.DefaultManifestName)
			require.NoError(t, os.WriteFile(manifestPath, []byte(test.manifest), 0o644))

			_, err := app_template.LoadManifest(manifestPath)
			if test.errMsg == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, test.errMsg)
		})
	}
}

// TestBuiltinTemplateManifests loads the manifest of every built-in template
// and checks that each default passes the validation of its variable, and
// that the values QA found to slip through the old regular expressions are
// refused.
func TestBuiltinTemplateManifests(t *testing.T) {
	for _, name := range builtin_templates.Names {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(builtin_templates.TemplatesFs,
				path.Join("templates", name, app_template.DefaultManifestName))
			require.NoError(t, err)

			manifestPath := filepath.Join(t.TempDir(), app_template.DefaultManifestName)
			require.NoError(t, os.WriteFile(manifestPath, data, 0o644))

			manifest, err := app_template.LoadManifest(manifestPath)
			require.NoError(t, err)

			for _, varInfo := range manifest.Vars {
				if varInfo.Default != "" {
					require.NoError(t, varInfo.Validate(varInfo.Default), varInfo.Name)
				}

				if varInfo.Type == app_template.VarTypeInteger {
					for _, bad := range []string{"3abc", "x10", "0"} {
						assert.Error(t, varInfo.Validate(bad), "%s=%s", varInfo.Name, bad)
					}
				}
			}
		})
	}
}
