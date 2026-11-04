package wasm

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// templates are the starting points of tt wasm new --template, by language
// and name: the world of the component (world.wit, a text/template of the
// package and the world), its source, laid over what tarawasm wrote, and the
// Lua module that uses it (module.lua, of the output), which tt prints.
//
//go:embed templates
var templates embed.FS

const (
	// templateWorld, templateModule are the files of a template tt renders.
	templateWorld  = "world.wit"
	templateModule = "module.lua"
)

var (
	// errUnknownTemplate reports a template tt has not for the language.
	errUnknownTemplate = errors.New("unknown template")
	// errNoWorld reports a component whose WIT file is not where tarawasm puts it.
	errNoWorld = errors.New("no WIT file of the world")
)

// templateData is what the templates are rendered with.
type templateData struct {
	// Package and World name the WIT package and world of the component.
	Package string
	World   string
	// Output is the component the build makes, relative to the component.
	Output string

	// worldFile is the WIT file of the component that declares the world.
	worldFile string
}

// Templates returns the templates of language.
func Templates(language string) []string {
	entries, err := templates.ReadDir(path.Join("templates", language))
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// applyTemplate lays the template name over the component tarawasm created
// in dir and returns the Lua module that uses it.
func applyTemplate(dir, language, name string, component *Component) (string, error) {
	root := path.Join("templates", language, name)

	entries, err := templates.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("%w %q for %s; there are: %s", errUnknownTemplate, name, language,
			strings.Join(Templates(language), ", "))
	}

	data, err := componentTemplateData(dir, component)
	if err != nil {
		return "", err
	}

	module := ""

	for _, entry := range entries {
		source := path.Join(root, entry.Name())

		switch entry.Name() {
		case templateWorld:
			err = renderInto(source, data, data.worldFile)
		case templateModule:
			module, err = render(source, data)
		default:
			var content []byte

			content, err = templates.ReadFile(source)
			if err == nil {
				err = writeFileAtomic(filepath.Join(dir, entry.Name()), content)
			}
		}

		if err != nil {
			return "", fmt.Errorf("template %s: %w", name, err)
		}
	}

	return module, nil
}

// componentTemplateData reads the package and the world of the component
// from its tarawasm.json and checks that its world is where tarawasm put it.
func componentTemplateData(dir string, component *Component) (templateData, error) {
	var config struct {
		World string `json:"world"`
		WIT   struct {
			Package string `json:"package"`
		} `json:"wit"`
	}

	_, err := readJSON(filepath.Join(dir, tarawasmConfig), &config)
	if err != nil {
		return templateData{}, err
	}

	// tarawasm writes the world into wit/app.wit, whatever its name: the
	// file that declares it is the one the template replaces.
	declares := regexp.MustCompile(`(?m)^\s*world\s+` + regexp.QuoteMeta(config.World) + `\s*\{`)

	files, err := filepath.Glob(filepath.Join(dir, "wit", "*.wit"))
	if err != nil {
		return templateData{}, fmt.Errorf("listing the WIT of %s: %w", dir, err)
	}

	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			return templateData{}, fmt.Errorf("reading %s: %w", file, err)
		}

		if declares.Match(text) && config.WIT.Package != "" {
			return templateData{
				Package: config.WIT.Package, World: config.World, Output: component.Output,
				worldFile: file,
			}, nil
		}
	}

	return templateData{}, fmt.Errorf("%w %s in %s", errNoWorld, config.World,
		filepath.Join(dir, "wit"))
}

// render renders the template file source with data.
func render(source string, data templateData) (string, error) {
	text, err := templates.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", source, err)
	}

	parsed, err := template.New(path.Base(source)).Option("missingkey=error").Parse(string(text))
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", source, err)
	}

	var out bytes.Buffer

	err = parsed.Execute(&out, data)
	if err != nil {
		return "", fmt.Errorf("rendering %s: %w", source, err)
	}

	return out.String(), nil
}

// renderInto renders the template file source with data into target.
func renderInto(source string, data templateData, target string) error {
	text, err := render(source, data)
	if err != nil {
		return err
	}

	return writeFileAtomic(target, []byte(text))
}
