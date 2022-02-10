package util

import (
	"bytes"
	htmlTemplate "html/template"
	"strings"
	textTemplate "text/template"
)

// GetHTMLTemplatedStr returns the processed string html template.
func GetHTMLTemplatedStr(text *string, obj interface{}) (string, error) {
	tmpl, err := htmlTemplate.New("s").Parse(*text)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	if err = tmpl.Execute(buf, obj); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// GetTextTemplatedStr returns the processed string text template.
func GetTemplatedStr(text *string, obj interface{}) (string, error) {
	funcMap := textTemplate.FuncMap{
		"ToLower": strings.ToLower,
	}

	tmpl, err := textTemplate.New("s").Funcs(funcMap).Parse(*text)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	if err = tmpl.Execute(buf, obj); err != nil {
		return "", err
	}

	return buf.String(), nil
}
