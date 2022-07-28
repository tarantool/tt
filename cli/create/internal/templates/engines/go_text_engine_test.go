package engines

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

const templateText = `cluster_cookie={{.cluster_cookie}}
login={{ .login }}
password={{ .password }}`

const templateFileName = "origin.lua.tt.template"
const resultFileName = "origin.lua"
const fileMode = os.FileMode(0640)

const testWorkDirName = "work-dir"

func TestTemplateFileRender(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temp dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	srcFileName := filepath.Join(workDir, templateFileName)
	err = os.WriteFile(srcFileName, []byte(templateText), fileMode)
	if err != nil {
		t.Fatalf("Error writing to %s: %s", srcFileName, err)
	}

	dstFileName := filepath.Join(workDir, resultFileName)
	data := map[string]string{
		"cluster_cookie": "test_cookie",
		"login":          "admin",
		"password":       "pwd",
	}

	engine := GoTextEngine{}
	if err = engine.RenderFile(srcFileName, dstFileName, data); err != nil {
		t.Errorf("Template render error: %s", err)
	}

	// Check generated file permissions equal to origin.
	stat, err := os.Stat(dstFileName)
	if err != nil {
		t.Errorf("Error getting info for %s: %s", dstFileName, err)
	}
	if stat.Mode() != fileMode {
		t.Errorf("%s file permissions are changed. Expected %o, actual %o",
			dstFileName, fileMode, stat.Mode())
	}

	// Check file content.
	var buf []byte
	buf, err = ioutil.ReadFile(dstFileName)
	if err != nil {
		t.Errorf("Error reading %s: %s", dstFileName, err)
	}

	actual := string(buf)
	const expected = `cluster_cookie=test_cookie
login=admin
password=pwd`
	if actual != expected {
		t.Errorf("Rendered templated does not match the expected text.")
	}
}

func TestTemplateFileRenderMissingValues(t *testing.T) {
	// create tmp working directory
	workDir, err := ioutil.TempDir("", testWorkDirName)
	if err != nil {
		t.Fatalf("Temp dir is not created: %s", err)
	}
	defer os.RemoveAll(workDir)

	srcFileName := filepath.Join(workDir, templateFileName)
	err = os.WriteFile(srcFileName, []byte(templateText), 0666)
	if err != nil {
		t.Fatalf("Error writing to %s: %s", srcFileName, err)
	}

	dstFileName := filepath.Join(workDir, resultFileName)
	data := map[string]string{"cluster_cookie": "test_cookie"} // login & password are missing
	engine := GoTextEngine{}
	if err = engine.RenderFile(srcFileName, dstFileName, data); err == nil {
		t.Errorf("Missing template variable must cause render failure.")
	}
}

func TestTextRendering(t *testing.T) {
	templateText := `{{.hello}} {{.world}}!`
	expectedText := `Hello world!`
	data := map[string]string{"hello": "Hello",
		"world": "world"}
	engine := GoTextEngine{}
	actualText, err := engine.RenderText(templateText, data)
	if err != nil {
		t.Errorf("Text rendering failed: %s", err)
	}

	if actualText != expectedText {
		t.Error("Actual text does not equal expected")
	}

	// Test missing key.
	delete(data, "hello")
	actualText, err = engine.RenderText(templateText, data)
	if err == nil {
		t.Error("Rendering must fail on missing keys.")
	}
}
