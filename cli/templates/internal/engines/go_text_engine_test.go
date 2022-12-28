package engines

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

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
	buf, err = ioutil.ReadFile(dstFileName)
	require.NoError(t, err)

	const expected = `cluster_cookie=test_cookie
login=admin
password=pwd`
	require.Equal(t, expected, string(buf))
}

func TestTemplateFileRenderMissingValues(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	srcFileName := filepath.Join(workDir, templateFileName)
	require.NoError(t, os.WriteFile(srcFileName, []byte(templateText), 0666))

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
	data := map[string]string{"hello": "Hello",
		"world": "world"}
	engine := GoTextEngine{}
	actualText, err := engine.RenderText(templateText, data)
	require.NoError(t, err)
	assert.Equal(t, expectedText, actualText)

	// Test missing key.
	delete(data, "hello")
	_, err = engine.RenderText(templateText, data)
	require.EqualError(t, err, "template execution failed: template: file:1:2: "+
		"executing \"file\" at <.hello>: map has no entry for key \"hello\"")
}
