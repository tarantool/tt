package luabody

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

//go:embed eval_func_body.lua
var evalFuncBody string

//go:embed get_suggestions_func_body.lua
var getSuggestionsFuncBody string

// GetTemplatedStr returns a templated string.
func GetTemplatedStr(text string, obj any) (string, error) {
	tmpl, err := template.New("s").Parse(text)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)

	err = tmpl.Execute(buf, obj)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// GetEvalFuncBody returns lua code of eval func.
func GetEvalFuncBody(evaler string) (string, error) {
	mapping := map[string]string{}

	if len(evaler) != 0 {
		if after, ok := strings.CutPrefix(evaler, "@"); ok {
			evalerFileBytes, err := os.ReadFile(after)
			if err != nil {
				return "", fmt.Errorf("failed to read the evaler file: %w", err)
			}

			mapping["evaler"] = string(evalerFileBytes)
		} else {
			mapping["evaler"] = evaler
		}
	}

	return GetTemplatedStr(evalFuncBody, mapping)
}

// getSuggestionsFuncBody returns lua code for completions.
func GetSuggestionsFuncBody() string {
	return getSuggestionsFuncBody
}
