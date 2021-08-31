package util

import (
	"bytes"
	"html/template"
)

// GetTemplatedStr returns the processed string template.
func GetTemplatedStr(text *string, obj interface{}) (string, error) {
	tmpl, err := template.New("s").Parse(*text)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	if err = tmpl.Execute(buf, obj); err != nil {
		return "", err
	}

	return buf.String(), nil
}
