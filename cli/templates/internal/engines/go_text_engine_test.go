package engines

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const templateText = `cluster_cookie={{.cluster_cookie}}
login={{ .login }}
password={{ .password }}`

const (
	templateFileName = "origin.lua.tt.template"
	resultFileName   = "origin.lua"
	fileMode         = os.FileMode(0o640)
)

const testWorkDirName = "work-dir"

func TestTemplateFileRender(t *testing.T) {
	workDir := t.TempDir()

	srcFileName := filepath.Join(workDir, templateFileName)
	require.NoError(t, os.WriteFile(srcFileName, []byte(templateText), fileMode))

	dstFileName := filepath.Join(workDir, resultFileName)
	data := map[string]string{
		"cluster_cookie": "test_cookie",
		"login":          "admin",
		"password":       "pwd",
	}

	engine := GoTextEngine{}
	require.NoError(t, engine.RenderFile(srcFileName, dstFileName, data))

	// Check generated file permissions equal to origin.
	stat, err := os.Stat(dstFileName)
	if err != nil {
		t.Errorf("error getting info for %s: %s", dstFileName, err)
	}
	if stat.Mode() != fileMode {
		t.Errorf("%s file permissions are changed. Expected %o, actual %o",
			dstFileName, fileMode, stat.Mode())
	}

	// Check file content.
	var buf []byte
	buf, err = os.ReadFile(dstFileName)
	require.NoError(t, err)

	const expected = `cluster_cookie=test_cookie
login=admin
password=pwd`
	require.Equal(t, expected, string(buf))
}

func TestTemplateFileRenderMissingValues(t *testing.T) {
	workDir := t.TempDir()

	srcFileName := filepath.Join(workDir, templateFileName)
	require.NoError(t, os.WriteFile(srcFileName, []byte(templateText), 0o666))

	dstFileName := filepath.Join(workDir, resultFileName)
	data := map[string]string{"cluster_cookie": "test_cookie"} // login & password are missing
	engine := GoTextEngine{}
	require.EqualError(t, engine.RenderFile(srcFileName, dstFileName, data), "template execution "+
		"failed: template: origin.lua.tt.template:2:9: executing \"origin.lua.tt.template\" at "+
		"<.login>: map has no entry for key \"login\"")
}

func TestTextRendering(t *testing.T) {
	templateText := `{{.hello}} {{.world}}!`
	expectedText := `Hello world!`
	data := map[string]string{
		"hello": "Hello",
		"world": "world",
	}
	engine := GoTextEngine{}
	actualText, err := engine.RenderText(templateText, data)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)

	// Test missing key.
	delete(data, "hello")
	_, err = engine.RenderText(templateText, data)
	require.EqualError(t, err, "template execution failed: template: file:1:2: "+
		"executing \"file\" at <.hello>: map has no entry for key \"hello\"")

	// Test builtin functions.
	templateText = "{{port}}"
	expectedText = "3301"
	actualText, err = engine.RenderText(templateText, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)

	templateText = "{{metricsPort}}"
	expectedText = "8081"
	actualText, err = engine.RenderText(templateText, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)

	templateText = `{{range replicasets "name" 1 1}}` +
		"Hi, {{.Name}}! Your instances: {{ range .InstNames }}{{.}}{{end}}{{end}}"
	expectedText = "Hi, name-001! Your instances: name-001-a"
	actualText, err = engine.RenderText(templateText, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)

	templateText = `{{atoi "5"}} apples`
	expectedText = "5 apples"
	actualText, err = engine.RenderText(templateText, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)
}
