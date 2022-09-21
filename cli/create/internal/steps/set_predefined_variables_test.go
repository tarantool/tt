package steps

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
)

func TestSetPredefinedVariables(t *testing.T) {
	createCtx := cmdcontext.CreateCtx{}
	createCtx.AppName = "app1"
	templateCtx := NewTemplateContext()
	setPredefinedVars := SetPredefinedVariables{}
	require.NoError(t, setPredefinedVars.Run(&createCtx, &templateCtx))
	require.Equal(t, map[string]string{"name": "app1"}, templateCtx.Vars)
}
