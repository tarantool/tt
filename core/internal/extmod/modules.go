package extmod

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errHelpFieldIsMandatoryForModuleManifest = errors.New(
		"help field is mandatory for module Manifest",
	)
	errModulesPathIsNotADirectory = errors.New(
		PathEnv + " names a path that is not a directory",
	)
	errVersionFieldIsMandatoryForModuleManifest = errors.New(
		"version field is mandatory for module Manifest",
	)
)

const (
	manifestFileName = "manifest"
	mainEntryPoint   = "main"
)

// Manifest stores information about Tarantool CLI module.
type Manifest struct {
	// Name of module.
	Name string `yaml:"-"`
	// Version of module.
	Version string `yaml:"version"`
	// Main is name of executable file.
	Main string `yaml:"main"`
	// Help is the module's one-line description, which tt lists the module
	// with: the first line of the help its manifest, or its reply to
	// --description, gives.
	Help string `yaml:"help"`
	// TtVersion is required a version of TT CLI (optional).
	TtVersion string `yaml:"tt-version"`
	// Description is a full description of the module (optional).
	// It can be used in the future for the help command.
	Description string `yaml:"description"`
	// Homepage is a link to the module homepage (optional).
	Homepage string `yaml:"homepage_url"`
}

// modulesEntry keeps detected entry points while scan modules.
type modulesEntry struct {
	// Modules location path.
	Directory string
	// Path to manifest.yaml file.
	Manifest string
	// Path to `main` executable file.
	Main string
}

// possibleModules map module name with found its entry points.
type possibleModules map[string]modulesEntry

// readManifest parses the manifest file, read through open, to module
// requirements.
func readManifest(dir, manifest string, open Opener) (Manifest, error) {
	var parsed Manifest

	data, err := readAll(manifest, open)
	if err != nil {
		return parsed, fmt.Errorf("failed to read manifest: %w", err)
	}

	err = yaml.Unmarshal(data, &parsed)
	if err != nil {
		return parsed, fmt.Errorf("failed to parse manifest: %w", err)
	}

	parsed.Main, err = exec.LookPath(filepath.Join(dir, parsed.Main))
	if err != nil {
		return parsed, fmt.Errorf("failed to find module executable: %w", err)
	}

	if parsed.Version == "" {
		return parsed, errVersionFieldIsMandatoryForModuleManifest
	}

	parsed.Help = firstLine(parsed.Help)
	if parsed.Help == "" {
		return parsed, errHelpFieldIsMandatoryForModuleManifest
	}

	return parsed, nil
}

// firstLine returns the first line of help, without the spaces around it:
// the one-line description tt lists a module with, wherever it lists it. A
// help of several lines would break those lists; the module's own --help is
// where a longer text goes.
func firstLine(help string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(help), "\n")

	return strings.TrimSpace(line)
}

// makeManifest describes the module of entry: from its manifest when it has
// one, from the answer of its executable otherwise. Both are read through
// open first.
func makeManifest(entry modulesEntry, open Opener) (Manifest, error) {
	if entry.Manifest != "" {
		return readManifest(entry.Directory, entry.Manifest, open)
	}

	return fillManifest(Manifest{
		Name:        "",
		Version:     "",
		Main:        entry.Main,
		Help:        "",
		TtVersion:   "",
		Description: "",
		Homepage:    "",
	}, open)
}

// discover returns the modules in the directories list names, separated by
// colons, by name. The first directory holding a module of a name wins; a
// later one is ignored with a warning to logger.
func discover(list string, logger *slog.Logger) (possibleModules, error) {
	modulesDirs, err := getModulesDirs(list)
	if err != nil {
		return nil, err
	}

	externalModules, err := getExternalModules(modulesDirs, logger)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get available external modules information: %w", err)
	}

	return externalModules, nil
}

// collectDirectoriesList returns the paths that exist, checking that each of
// them is a directory. A path tt cannot stat - one that does not exist, or
// one it may not look into - is skipped; one that exists and is not a
// directory is an error.
func collectDirectoriesList(paths []string) ([]string, error) {
	dirs := make([]string, 0, len(paths))

	for _, dir := range paths {
		// The directories come from TT_CLI_MODULES_PATH on purpose: any path
		// the user names is a valid modules location.
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return dirs, fmt.Errorf("%w: %s", errModulesPathIsNotADirectory, dir)
			}

			dirs = append(dirs, dir)
		}
	}

	return dirs, nil
}

// getModulesDirs returns the list of modules directories list names,
// separated by colons.
func getModulesDirs(list string) ([]string, error) {
	if list == "" {
		return []string{}, nil
	}

	paths := strings.Split(list, ":")

	return collectDirectoriesList(paths)
}

// isPossibleModule checks is exists any manifest or executable `main` file inside dir.
func isPossibleModule(dir string) (modulesEntry, bool) {
	isModule := false
	entries := modulesEntry{Directory: dir, Manifest: "", Main: ""}
	manifest, _ := util.GetYamlFileName(filepath.Join(dir, manifestFileName), false)

	if manifest != "" {
		entries.Manifest = manifest
		isModule = true
	}

	main, err := exec.LookPath(filepath.Join(dir, mainEntryPoint))
	if err == nil {
		entries.Main = main
		isModule = true
	}

	return entries, isModule
}

// readSubDirectories returns sorted list of subdirectories in the specified path.
func readSubDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf(`failed to read "%s" directory: %w`, path, err)
	}

	entries = slices.DeleteFunc(entries, func(e os.DirEntry) bool {
		return !e.IsDir()
	})

	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		dirs = append(dirs, e.Name())
	}

	return dirs, nil
}

// getExternalModules returns map[name] = directory of available modules by
// parsing the contents of the list folders. A module of a name found again
// in a later folder is ignored with a warning to logger.
func getExternalModules(paths []string, logger *slog.Logger) (possibleModules, error) {
	modules := possibleModules{}

	for _, path := range paths {
		dirs, err := readSubDirectories(path)
		if err != nil {
			return nil, err
		}

		for _, dirName := range dirs {
			modPath := filepath.Join(path, dirName)

			e, exists := modules[dirName]
			if exists {
				warnf(logger, "Ignore duplicate module %q overlap with %q", modPath, e.Directory)

				continue
			}

			if modEntry, isModule := isPossibleModule(modPath); isModule {
				modules[dirName] = modEntry
			}
		}
	}

	return modules, nil
}

// readAll returns the contents of the file at path, read through open.
func readAll(path string, open Opener) ([]byte, error) {
	file, err := open(path)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(file)

	return data, errors.Join(err, file.Close())
}
