package steps_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
	"github.com/tarantool/tt/v3/cli/create/internal/app_template"
	"github.com/tarantool/tt/v3/cli/create/internal/steps"
)

func TestSetPredefinedVariables(t *testing.T) {
	createCtx := create_ctx.CreateCtx{}

	createCtx.AppName = "app1"

	templateCtx := app_template.NewTemplateContext()
	setPredefinedVars := steps.SetPredefinedVariables{}
	require.NoError(t, setPredefinedVars.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"name": "app1"}, templateCtx.Vars)
}
