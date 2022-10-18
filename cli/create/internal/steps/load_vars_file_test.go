package steps

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestLoadVarsFile(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createCtx.VarsFile = "testdata/vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.NoError(t, loadVarsFile.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"user-name": "admin", "password": "weak_pwd"},
		templateCtx.Vars)
}

func TestLoadVarsFileVariablesAlreadySet(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	templateCtx.Vars["user-name"] = "root"
	createCtx.VarsFile = "testdata/vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.NoError(t, loadVarsFile.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"user-name": "admin", "password": "weak_pwd"},
		templateCtx.Vars)
}

func TestNonExistingVarsFile(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createCtx.VarsFile = "testdata/non-existing-vars-file.txt"
	loadVarsFile := LoadVarsFile{}
	require.EqualError(t, loadVarsFile.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Vars file loading error: stat %s: no such file or directory",
			createCtx.VarsFile))
}

func TestLoadVarsFileWrongFormat(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createCtx.VarsFile = "testdata/invalid_vars_file.txt"
	loadVarsFile := LoadVarsFile{}
	require.EqualError(t, loadVarsFile.Run(&createCtx, &templateCtx),
		fmt.Sprintf("Failed to load vars from %s: Wrong variable definition "+
			"format: user-name=\nFormat: var-name=value", createCtx.VarsFile))
}
