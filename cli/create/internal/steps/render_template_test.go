package steps

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

func TestTemplateRender(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	if err = os.WriteFile(filepath.Join(workDir, "config.lua.tt.template"),
		[]byte(`
cluster_cookie = {{.cluster_cookie}}
login = {{.user_name}}
	`), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	if err = os.WriteFile(filepath.Join(workDir, "{{.app_name}}.yml"),
		[]byte(`Sample text`), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir
	templateCtx.Vars = map[string]string{
		"cluster_cookie": "cookie",
		"user_name":      "admin",
		"app_name":       "app1",
	}

	renderTemplate := RenderTemplate{}
	if err := renderTemplate.Run(createCtx, &templateCtx); err != nil {
		t.Errorf("Template rendering error: %s", err)
	}

	configFileName := filepath.Join(workDir, "config.lua")
	checkForExistence(t, configFileName, 0644)
	checkForExistence(t, filepath.Join(workDir, "app1.yml"), 0644)

	buf, err := os.ReadFile(configFileName)
	if err != nil {
		t.Errorf("Error reading file %s: %s", configFileName, err)
	}
	const expectedText = `
cluster_cookie = cookie
login = admin
	`
	actualText := string(buf)
	if actualText != expectedText {
		t.Errorf("Rendered text does not equal expected: \n%s\n%s", actualText, expectedText)
	}
}

func TestTemplateRenderMissingVar(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	if err = os.WriteFile(filepath.Join(workDir, "config.lua.tt.template"),
		[]byte(`
login = {{.user_name}}
	`), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir

	renderTemplate := RenderTemplate{}
	if err := renderTemplate.Run(createCtx, &templateCtx); err == nil {
		t.Errorf("Template rendering must fail due to missing template var: %s", err)
	}
}

func TestTemplateRenderMissingVarInFileName(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temporary dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	if err = os.WriteFile(filepath.Join(workDir, "{{.app_name}}.yml"),
		[]byte(`Sample text`), 0644); err != nil {
		t.Fatalf("Failed to create file: %s", err)
	}

	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()
	templateCtx.AppPath = workDir

	renderTemplate := RenderTemplate{}
	if err := renderTemplate.Run(createCtx, &templateCtx); err == nil {
		t.Errorf("Template rendering must fail due to missing template var: %s", err)
	}
}
