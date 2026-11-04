package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
)

const (
	// tarawasmConfig marks the directory of a tarawasm project.
	tarawasmConfig = "tarawasm.json"
	// tarawasmSDKLock pins the tarantool-wasm SDK of a tarawasm project.
	tarawasmSDKLock = "tarawasm.tarantool.lock.json"
)

// Component is a component of the package that tarawasm builds: the
// directory of its build holds a tarawasm project.
type Component struct {
	// Name is the component's name in the manifest.
	Name string
	// Dir is the absolute directory of the tarawasm project.
	Dir string
	// Language is the language of the component, from tarawasm.json.
	Language string
	// Output is the component tarawasm builds, relative to Dir.
	Output string
	// SDKVersion is the tarantool-wasm release the component's SDK lock pins;
	// empty for a project without one.
	SDKVersion string
}

// FindComponents returns the components of the package in projectDir that
// tarawasm builds, in the order of their names.
func FindComponents(projectDir string) ([]Component, error) {
	man, err := readManifest(projectDir)
	if err != nil {
		return nil, err
	}

	var components []Component

	for _, name := range sortedKeys(man.Components) {
		component := man.Components[name]

		dir := component.Path
		if component.Build != nil && component.Build.Cwd != "" {
			dir = component.Build.Cwd
		}

		if !filepath.IsAbs(dir) {
			dir = filepath.Join(projectDir, dir)
		}

		found, err := readComponent(name, dir)
		if err != nil {
			return nil, err
		}

		if found != nil {
			components = append(components, *found)
		}
	}

	return components, nil
}

// readComponent reads the tarawasm project in dir; nil when there is none.
func readComponent(name, dir string) (*Component, error) {
	var config struct {
		Language string `json:"language"`
		Output   string `json:"output"`
	}

	ok, err := readJSON(filepath.Join(dir, tarawasmConfig), &config)
	if err != nil || !ok {
		return nil, err
	}

	var lock struct {
		Version string `json:"version"`
	}

	_, err = readJSON(filepath.Join(dir, tarawasmSDKLock), &lock)
	if err != nil {
		return nil, err
	}

	return &Component{
		Name: name, Dir: dir, Language: config.Language, Output: config.Output,
		SDKVersion: lock.Version,
	}, nil
}

// readJSON decodes the JSON file at path into value; false when there is no
// such file.
func readJSON(path string, value any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	err = json.Unmarshal(data, value)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	return true, nil
}

// readManifest parses the manifest of the package in projectDir.
func readManifest(projectDir string) (*manifest.Manifest, error) {
	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s has no %s (create one with tt new)",
			ErrNoManifest, projectDir, manifestFileName)
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return man, nil
}

// exactVersionRe matches a constraint that names one version: 0.1.0 or
// ==0.1.0.
var exactVersionRe = regexp.MustCompile(`^\s*(?:==\s*)?([0-9][0-9A-Za-z.\-]*)\s*$`)

// DeclaredVersion returns the version of the wasm rock the manifest declares,
// when its constraint names one; empty otherwise.
func DeclaredVersion(projectDir string) (string, error) {
	man, err := readManifest(projectDir)
	if err != nil {
		return "", err
	}

	dependency, ok := man.Dependencies[RockName]
	if !ok {
		return "", nil
	}

	match := exactVersionRe.FindStringSubmatch(dependency.Version)
	if match == nil {
		return "", nil
	}

	return match[1], nil
}

// SDKMismatches returns a line for every component whose SDK is of another
// release than version, the runtime of the package: a component builds
// against the WIT of its SDK, and the runtime serves the WIT of its own.
func SDKMismatches(components []Component, version string) []string {
	var mismatches []string

	for _, component := range components {
		if component.SDKVersion == "" || component.SDKVersion == version {
			continue
		}

		mismatches = append(mismatches, fmt.Sprintf(
			"component %q is built with the tarantool-wasm %s SDK, the package declares "+
				"wasm %s", component.Name, component.SDKVersion, version))
	}

	return mismatches
}

// SDKWarnings returns the SDK mismatches of the package in projectDir, for a
// build to warn of; nothing when it declares no exact wasm version.
func SDKWarnings(projectDir string) []string {
	version, err := DeclaredVersion(projectDir)
	if err != nil || version == "" {
		return nil
	}

	components, err := FindComponents(projectDir)
	if err != nil {
		return nil
	}

	return SDKMismatches(components, version)
}

// restoreSDK has tarawasm install the SDK of component as its lock pins it,
// into the component's .tarawasm, which a checkout does not carry.
func (t Tarawasm) restoreSDK(ctx context.Context, program string, component Component) error {
	t.binDir = filepath.Dir(program)

	err := t.runIn(ctx, component.Dir, program, "tarantool", "setup")
	if err != nil {
		return fmt.Errorf("restoring the SDK of component %q: %w", component.Name, err)
	}

	return nil
}

// gitignores are what a tarawasm project of each language generates and
// rebuilds: its state, its output and its bindings.
//
//nolint:gochecknoglobals // A table of constants.
var gitignores = map[string][]string{
	"python": {"__pycache__/", "wit_world/", "componentize_py_*", "poll_loop.py"},
	"rust":   {"target/", "src/bindings.rs"},
	"go":     {"internal/"},
	"js":     {"internal/", "node_modules/"},
	"c":      {"bindings/"},
	"csharp": {"bin/", "obj/"},
}

// gitignore renders the .gitignore of a component of language.
func gitignore(language string) string {
	lines := append([]string{
		"# Generated by tarawasm, and rebuilt by tarawasm all.",
		".tarawasm/",
		"dist/",
		"# Where the tools tt installed are, on this machine.",
		"tarawasm.local.json",
	}, gitignores[language]...)

	return strings.Join(lines, "\n") + "\n"
}
