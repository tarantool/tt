package engines

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

type GoTextEngine struct {
}

func (GoTextEngine) RenderFile(srcPath string, dstPath string, data interface{}) error {
	// Get file mode of the source file.
	stat, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("Error getting file info %s: %s", srcPath, err)
	}
	originFileMode := stat.Mode()

	// Parse the source file.
	parsedTemplate, err := template.ParseFiles(srcPath)
	if err != nil {
		return fmt.Errorf("Error parsing %s: %s", srcPath, err)
	}
	parsedTemplate.Option("missingkey=error") // Treat missing variable as error.

	outFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("Error creating %s: %s", dstPath, err)
	}
	defer func() {
		outFile.Close()
		os.Chmod(outFile.Name(), originFileMode)
	}()

	if err := parsedTemplate.Execute(outFile, data); err != nil {
		return fmt.Errorf("Template execution failed: %s", err)
	}
	return nil
}

func (GoTextEngine) RenderText(in string, data interface{}) (string, error) {
	tmpl, err := template.New("file").Parse(in)
	if err != nil {
		return "", fmt.Errorf("Failed to parse %s: %s", in, err)
	}

	var buffer bytes.Buffer
	if err = tmpl.Execute(&buffer, &data); err != nil {
		return "", fmt.Errorf("Template execution failed: %s", err)
	}

	return buffer.String(), nil
}
