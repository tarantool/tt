package steps

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
)

func TestLoadVarsFile(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createCtx.VarsFile = "testdata/vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.NoError(t, loadVarsFile.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"user-name": "admin", "password": "weak_pwd"},
		templateCtx.Vars)
}

func TestLoadVarsFileVariablesAlreadySet(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	templateCtx.Vars["user-name"] = "root"
	createCtx.VarsFile = "testdata/vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.NoError(t, loadVarsFile.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"user-name": "root", "password": "weak_pwd"},
		templateCtx.Vars)
}

func TestNonExistingVarsFile(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createCtx.VarsFile = "testdata/non-existing-vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.EqualError(t, loadVarsFile.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Vars file loading error: stat %s: no such file or directory",
			createCtx.VarsFile))
}

func TestLoadVarsFileWrongFormat(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()

	createCtx.VarsFile = "testdata/invalid_vars_file.txt"
	loadVarsFile := LoadVarsFile{}
	require.EqualError(t, loadVarsFile.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Failed to load vars from %s: Wrong variable definition "+
			"format: user-name=\nFormat: var-name=value", createCtx.VarsFile))
}
