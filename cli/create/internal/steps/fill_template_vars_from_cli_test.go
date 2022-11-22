package steps

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

func TestCliVarsParsing(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	createCtx.VarsFromCli = append(createCtx.VarsFromCli, "var1=value1",
		"var2=value2", "var3=value=value")
	fillTemplateVarsFromCli := FillTemplateVarsFromCli{}
	require.NoError(t, fillTemplateVarsFromCli.Run(&createCtx, &templateCtx))

	require.Len(t, templateCtx.Vars, 3)
	expected := map[string]string{
		"var1": "value1",
		"var2": "value2",
		"var3": "value=value",
	}
	require.Equal(t, expected, templateCtx.Vars)
}

func TestCliVarsParseErrorHandling(t *testing.T) {
	var createCtx create_ctx.CreateCtx
	templateCtx := app_template.NewTemplateContext()

	invalidVarDefinitions := []string{
		"var=",
		"=value",
		"=",
		"missing_equal_sign",
		"",
	}
	fillTemplateVarsFromCli := FillTemplateVarsFromCli{}
	for _, def := range invalidVarDefinitions {
		createCtx.VarsFromCli = []string{def}
		require.EqualError(t, fillTemplateVarsFromCli.Run(&createCtx, &templateCtx),
			fmt.Sprintf("wrong variable definition format: %s\nFormat: var-name=value", def))
	}
}

func TestCliParseVars(t *testing.T) {
	wrongFormatStrings := []string{"", "=", "var=", "=val"}

	for _, varDef := range wrongFormatStrings {
		_, err := parseVarDefinition(varDef)
		require.EqualError(t, err,
			fmt.Sprintf("wrong variable definition format: %s\nFormat: var-name=value", varDef))
	}

	v, err := parseVarDefinition("var=val")
	require.NoError(t, err)
	require.Equal(t, v.name, "var")
	require.Equal(t, v.value, "val")
}
