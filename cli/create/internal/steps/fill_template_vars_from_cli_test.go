package steps

import (
	"testing"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/create/internal/templates"
)

func TestCliVarsParsing(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	createCtx.VarsFromCli = append(createCtx.VarsFromCli, "var1=value1",
		"var2=value2", "var3=value=value")
	fillTemplateVarsFromCli := FillTemplateVarsFromCli{}
	if err := fillTemplateVarsFromCli.Run(createCtx, &templateCtx); err != nil {
		t.Error("Error CLI variables parsing.")
	}

	if len(templateCtx.Vars) != 3 {
		t.Error("Wrong Vars map length.")
	}
	expected := map[string]string{
		"var1": "value1",
		"var2": "value2",
		"var3": "value=value",
	}
	for k, v := range expected {
		value, found := templateCtx.Vars[k]
		if !found {
			t.Errorf("%s is not in Vars map.", k)
		}
		if value != v {
			t.Errorf("Invalid var1 value in Vars map: %s", value)
		}
	}
}

func TestCliVarsParseErrorHandling(t *testing.T) {
	var createCtx cmdcontext.CreateCtx
	templateCtx := templates.NewTemplateContext()

	fillTemplateVarsFromCli := FillTemplateVarsFromCli{}
	createCtx.VarsFromCli = []string{"var1="}
	err := fillTemplateVarsFromCli.Run(createCtx, &templateCtx)
	if err == nil {
		t.Error("Invalid variable definition format is not detected.")
	}

	createCtx.VarsFromCli = []string{"=value"}
	err = fillTemplateVarsFromCli.Run(createCtx, &templateCtx)
	if err == nil {
		t.Error("Invalid variable definition format is not detected.")
	}

	createCtx.VarsFromCli = []string{"="}
	err = fillTemplateVarsFromCli.Run(createCtx, &templateCtx)
	if err == nil {
		t.Error("Invalid variable definition format is not detected.")
	}

	createCtx.VarsFromCli = []string{"missing_equal_sign"}
	err = fillTemplateVarsFromCli.Run(createCtx, &templateCtx)
	if err == nil {
		t.Error("Invalid variable definition format is not detected.")
	}
}
