package steps

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// spell-checker:ignore zxzc

func TestNonInteractiveMode(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		app_template.UserPrompt{
			Prompt:  "User name",
			Name:    "user_name",
			Default: "admin",
			Re:      "^[a-z]+$",
		})

	templateCtx.IsManifestPresent = true
	createCtx.SilentMode = true
	collectVars := CollectTemplateVarsFromUser{&bytes.Buffer{}}
	require.NoError(t, collectVars.Run(&createCtx, &templateCtx), "Collecting vars failed")

	expected := map[string]string{
		"user_name": "admin",
	}
	assert.Equal(t, expected, templateCtx.Vars)
}

func TestNonInteractiveModeReMismatch(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		app_template.UserPrompt{
			Prompt:  "User name",
			Name:    "user_name",
			Default: "admin2",
			Re:      "^[a-z]+$",
		})

	templateCtx.IsManifestPresent = true
	createCtx.SilentMode = true
	collectVars := CollectTemplateVarsFromUser{&bytes.Buffer{}}
	err := collectVars.Run(&createCtx, &templateCtx)
	assert.EqualError(t, err, "invalid format of user_name variable")
}

func TestInteractiveMode(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		app_template.UserPrompt{
			Prompt:  "User name",
			Name:    "user_name",
			Default: "admin",
			Re:      "^[a-z]+$",
		},
		app_template.UserPrompt{
			Prompt: "Password",
			Name:   "pwd",
			Re:     "^[a-zA-Z0-9_]+$",
		},
		app_template.UserPrompt{
			Prompt: "Hash",
			Name:   "hash",
		},
		app_template.UserPrompt{
			Prompt: "Cluster cookie",
			Name:   "cookie",
			Re:     `^[a-zA-Z\-]+$`,
		},
		app_template.UserPrompt{
			Prompt: "Cluster cookie",
			Name:   "cookie",
			Re:     `^[a-zA-Z\-]+$`,
		},
		app_template.UserPrompt{
			Prompt: "First Name",
			Name:   "first_name",
			Re:     `^[A-Z][a-z]+$`,
		},
		app_template.UserPrompt{
			Prompt:  "Retry count",
			Name:    "retry_count",
			Default: "3",
			Re:      `^\d+$`,
		})

	templateCtx.Vars["cookie"] = "cluster_cookie"
	templateCtx.Vars["first_name"] = "John"

	templateCtx.IsManifestPresent = true
	var mockReader bytes.Buffer
	mockReader.Write([]byte("user2\n" + // Invalid input.
		"\n" + // Empty input. Will take the Default value.
		"@)(#*(sd[f[\n" + // Invalid pwd input.
		"\n" + // Empty input. Invalid if Default is not set.
		"pwd with space\n" + // This line does not match the regex.
		"weak\n" + // Valid input.
		"^(*&\\/..zxzc.>))!@(*)(\n" + // Valid input: no Re check, no Default value.
		"cluster-cookie\n" + // Valid cookie value.
		"5\n", // Valid retry count value.
	))
	collectVars := CollectTemplateVarsFromUser{Reader: &mockReader}
	require.NoError(t, collectVars.Run(&createCtx, &templateCtx))

	expected := map[string]string{
		"user_name":   "admin",
		"pwd":         "weak",
		"hash":        `^(*&\/..zxzc.>))!@(*)(`,
		"cookie":      "cluster-cookie",
		"first_name":  "John",
		"retry_count": "5",
	}
	require.Equal(t, expected, templateCtx.Vars)
}
