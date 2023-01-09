package engines

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type GoTextEngine struct {
}

var commonTemplateFuncs map[string]interface{}

// relativeToCurrentWorkingDir returns a path relative to current working dir.
// In case of error, fullpath is returned.
func relativeToCurrentWorkingDir(fullpath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fullpath
	}
	relPath, err := filepath.Rel(cwd, fullpath)
	if err != nil {
		return fullpath
	}
	return relPath
}

func init() {
	commonTemplateFuncs = make(map[string]interface{}, 0)
	commonTemplateFuncs["cwdRelative"] = relativeToCurrentWorkingDir
}

// RenderFile renders srcPath template to dstPath using go text/template engine.
func (GoTextEngine) RenderFile(srcPath string, dstPath string, data interface{}) error {
	stat, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("error getting file info %s: %s", srcPath, err)
	}
	originFileMode := stat.Mode()

	parsedTemplate, err := template.ParseFiles(srcPath)
	if err != nil {
		return fmt.Errorf("error parsing %s: %s", srcPath, err)
	}
	parsedTemplate.Option("missingkey=error") // Treat missing variable as error.
	parsedTemplate.Funcs(commonTemplateFuncs)

	outFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("error creating %s: %s", dstPath, err)
	}
	defer func() {
		outFile.Close()
		os.Chmod(outFile.Name(), originFileMode)
	}()

	if err := parsedTemplate.Execute(outFile, data); err != nil {
		return fmt.Errorf("template execution failed: %s", err)
	}
	return nil
}

// RenderText renders in text using go tex/template engine.
func (GoTextEngine) RenderText(in string, data interface{}) (string, error) {
	parsedTemplate, err := template.New("file").Funcs(commonTemplateFuncs).Parse(in)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s: %s", in, err)
	}
	parsedTemplate.Option("missingkey=error")

	var buffer bytes.Buffer
	if err = parsedTemplate.Execute(&buffer, &data); err != nil {
		return "", fmt.Errorf("template execution failed: %s", err)
	}

	return buffer.String(), nil
}
