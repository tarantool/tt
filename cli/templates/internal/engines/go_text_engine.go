package engines

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"text/template"
)

type GoTextEngine struct{}

// spell-checker:ignore missingkey

// makeTemplate creates template for rendering.
func makeTemplate(name string) *template.Template {
	state := newGenState()
	funcMap := template.FuncMap{
		"port":        state.genPort,
		"metricsPort": state.genMetricsPort,
		"replicasets": genReplicasets,
		"atoi":        strconv.Atoi,
		"replace":     strings.ReplaceAll,
	}

	return template.New(name).Funcs(funcMap)
}

// RenderFile renders srcPath template to dstPath using go text/template engine.
func (GoTextEngine) RenderFile(srcPath, dstPath string, data any) error {
	stat, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("error getting file info %s: %w", srcPath, err)
	}

	originFileMode := stat.Mode()

	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("error reading file %s: %w", srcPath, err)
	}

	parsedTemplate, err := makeTemplate(path.Base(srcPath)).Parse(string(content))
	if err != nil {
		return fmt.Errorf("error parsing %s: %w", srcPath, err)
	}

	parsedTemplate.Option("missingkey=error") // Treat missing variable as error.

	outFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("error creating %s: %w", dstPath, err)
	}

	defer func() {
		outFile.Close()
		os.Chmod(outFile.Name(), originFileMode)
	}()

	if err := parsedTemplate.Execute(outFile, data); err != nil {
		return fmt.Errorf("template execution failed: %w", err)
	}

	return nil
}

// RenderText renders in text using go tex/template engine.
func (GoTextEngine) RenderText(in string, data any) (string, error) {
	parsedTemplate, err := makeTemplate("file").Parse(in)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", in, err)
	}

	parsedTemplate.Option("missingkey=error")

	var buffer bytes.Buffer

	err = parsedTemplate.Execute(&buffer, &data)
	if err != nil {
		return "", fmt.Errorf("template execution failed: %w", err)
	}

	return buffer.String(), nil
}
