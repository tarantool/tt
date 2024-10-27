package luabody

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/tarantool/cartridge-cli/cli/templates"
)

//go:embed eval_func_body.lua
var evalFuncBody string

//go:embed get_suggestions_func_body.lua
var getSuggestionsFuncBody string

// GetEvalFuncBody returns lua code of eval func.
func GetEvalFuncBody(evaler string) (string, error) {
	mapping := map[string]string{}
	if len(evaler) != 0 {
		if strings.HasPrefix(evaler, "@") {
			evalerFileBytes, err := os.ReadFile(strings.TrimPrefix(evaler, "@"))
			if err != nil {
				return "", fmt.Errorf("failed to read the evaler file: %s", err)
			}
			mapping["evaler"] = string(evalerFileBytes)
		} else {
			mapping["evaler"] = evaler
		}
	}

	return templates.GetTemplatedStr(&evalFuncBody, mapping)
}

// GetEvalFuncBody returns lua code for completions.
func GetSuggestionsFuncBody() string {
	return getSuggestionsFuncBody
}
