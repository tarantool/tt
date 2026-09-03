package util

import (
	"bytes"
	"strings"
	textTemplate "text/template"
)

// GetTextTemplatedStr returns the processed string text template.
func GetTextTemplatedStr(text *string, obj any) (string, error) {
	funcMap := textTemplate.FuncMap{
		"ToLower": strings.ToLower,
	}

	tmpl, err := textTemplate.New("s").Funcs(funcMap).Parse(*text)
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
