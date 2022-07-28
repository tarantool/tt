package steps

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

type mockReader struct {
	lines []string
}

func (reader *mockReader) readLine() (string, error) {
	linesLeft := len(reader.lines)
	if linesLeft <= 0 {
		return "", fmt.Errorf("User input is empty.")
	}

	line := reader.lines[0]
	reader.lines = reader.lines[1:]
	return line, nil
}

func TestNonInteractiveMode(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		templates.UserPrompt{
			Prompt:  "User name",
			Var:     "user_name",
			Default: "admin",
			Re:      "^[a-z]+$",
		})

	templateCtx.IsManifestPresent = true
	createCtx.SilentMode = true
	collectVars := CollectTemplateVarsFromUser{Reader: &mockReader{}}
	err := collectVars.Run(createCtx, &templateCtx)
	if err != nil {
		t.Errorf("Collecting vars failed: %s", err)
	}

	expected := map[string]string{
		"user_name": "admin",
	}
	if !reflect.DeepEqual(templateCtx.Vars, expected) {
		t.Errorf("Actual vars map does not equal expected: \n%s\n%s", templateCtx.Vars, expected)
	}
}

func TestNonInteractiveModeReMismatch(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		templates.UserPrompt{
			Prompt:  "User name",
			Var:     "user_name",
			Default: "admin2",
			Re:      "^[a-z]+$",
		})

	templateCtx.IsManifestPresent = true
	createCtx.SilentMode = true
	collectVars := CollectTemplateVarsFromUser{Reader: &mockReader{}}
	err := collectVars.Run(createCtx, &templateCtx)
	if err == nil {
		t.Error("RE mismatch must cause failure")
	}
}

func TestInteractiveMode(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.Manifest.Vars = append(templateCtx.Manifest.Vars,
		templates.UserPrompt{
			Prompt:  "User name",
			Var:     "user_name",
			Default: "admin",
			Re:      "^[a-z]+$",
		},
		templates.UserPrompt{
			Prompt: "Password",
			Var:    "pwd",
			Re:     "^[a-zA-Z0-9_]+$",
		},
		templates.UserPrompt{
			Prompt: "Hash",
			Var:    "hash",
		},
		templates.UserPrompt{
			Prompt: "Cluster cookie",
			Var:    "cookie",
			Re:     `^[a-zA-Z\-]+$`,
		},
		templates.UserPrompt{
			Prompt: "Cluster cookie",
			Var:    "cookie",
			Re:     `^[a-zA-Z\-]+$`,
		},
		templates.UserPrompt{
			Prompt: "First Name",
			Var:    "first_name",
			Re:     `^[A-Z][a-z]+$`,
		},
		templates.UserPrompt{
			Prompt:  "Retry count",
			Var:     "retry_count",
			Default: "3",
			Re:      `^\d+$`,
		})

	templateCtx.Vars["cookie"] = "cluster_cookie"
	templateCtx.Vars["first_name"] = "John"

	templateCtx.IsManifestPresent = true
	mockReader := mockReader{lines: []string{
		"user2", // Invalid input.
		"",      // Empty input. Will take the Default value.

		"@)(#*(sd[f[",            // Invalid pwd input.
		"",                       // Empty input. Invalid if Default is not set.
		"pwd with space",         // This line does not match the regex.
		"weak",                   // Valid input.
		`^(*&\/..zxzc.>))!@(*)(`, // Valid input: no Re check, no Default value.
		"cluster-cookie",         // Valid cookie value.
		"5",                      // Valid retry count value.
	}}
	collectVars := CollectTemplateVarsFromUser{Reader: &mockReader}
	err := collectVars.Run(createCtx, &templateCtx)
	if err != nil {
		t.Errorf("Collecting vars failed: %s", err)
	}

	expected := map[string]string{
		"user_name":   "admin",
		"pwd":         "weak",
		"hash":        `^(*&\/..zxzc.>))!@(*)(`,
		"cookie":      "cluster-cookie",
		"first_name":  "John",
		"retry_count": "5",
	}
	if !reflect.DeepEqual(templateCtx.Vars, expected) {
		t.Errorf("Actual vars map does not equal expected: \n%s\n%s", templateCtx.Vars, expected)
	}
}
