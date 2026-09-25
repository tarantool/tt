package modules

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/util"
	"gopkg.in/yaml.v3"
)

var (
	errHelpFieldIsMandatoryForModuleManifest = errors.New(
		"help field is mandatory for module Manifest",
	)
	errModuleIsDisabledToOverride                      = errors.New("module ")
	errSpecifiedPathInConfigurationFileIsNotADirectory = errors.New(
		"specified path in configuration file is not a directory",
	)
	errVersionFieldIsMandatoryForModuleManifest = errors.New(
		"version field is mandatory for module Manifest",
	)
)

const (
	manifestFileName = "manifest"
	mainEntryPoint   = "main"
)

// disabledOverride list of internal commands that can't be overridden by external modules.
var disabledOverride = []string{"modules"}

// Manifest stores information about Tarantool CLI module.
type Manifest struct {
	// Name of module.
	Name string `yaml:"-"`
	// Version of module.
	Version string `yaml:"version"`
	// Main is name of executable file.
	Main string `yaml:"main"`
	// Help is a short description of the module.
	Help string `yaml:"help"`
	// TtVersion is required a version of TT CLI (optional).
	TtVersion string `yaml:"tt-version"`
	// Description is a full description of the module (optional).
	// It can be used in the future for the help command.
	Description string `yaml:"description"`
	// Homepage is a link to the module homepage (optional).
	Homepage string `yaml:"homepage_url"`
}

// ModulesInfo stores information about all CLI modules.
type ModulesInfo map[string]Manifest

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

// readManifest parses the manifest file to module requirements.
func readManifest(dir, manifest string) (Manifest, error) {
	var parsed Manifest

	data, err := os.ReadFile(manifest)
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

	if parsed.Help == "" {
		return parsed, errHelpFieldIsMandatoryForModuleManifest
	}

	return parsed, nil
}

func makeManifest(entry modulesEntry) (Manifest, error) {
	if entry.Manifest != "" {
		return readManifest(entry.Directory, entry.Manifest)
	}

	return fillManifest(Manifest{
		Name:        "",
		Version:     "",
		Main:        entry.Main,
		Help:        "",
		TtVersion:   "",
		Description: "",
		Homepage:    "",
	})
}

// GetModulesInfo collects information about available modules (both external and internal).
func GetModulesInfo(
	cmdCtx *cmdcontext.CmdCtx,
	rootCmd string,
	cliOpts *config.CliOpts,
) (ModulesInfo, error) {
	modulesDirs, err := getConfigModulesDirs(cmdCtx, cliOpts)
	if err != nil {
		return nil, err
	}

	modulesEnvDirs, err := getEnvironmentModulesDirs()
	if err != nil {
		return nil, err
	}

	modulesDirs = append(modulesDirs, modulesEnvDirs...)

	externalModules, err := getExternalModules(modulesDirs)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get available external modules information: %w", err)
	}

	modulesInfo := ModulesInfo{}

	for name, info := range externalModules {
		manifest, err := makeManifest(info)
		if err != nil {
			log.Warnf("Failed to get information about module %q: %s", name, err)

			continue
		}

		manifest.Name = name

		commandPath := rootCmd + " " + name

		modulesInfo[commandPath] = manifest
	}

	return modulesInfo, nil
}

// collectDirectoriesList checks list to ensure that all items is directories.
func collectDirectoriesList(paths []string) ([]string, error) {
	dirs := make([]string, 0, len(paths))
	// We return an error only if the following conditions are met:
	// 1. If a directory field is specified;
	// 2. Specified path exists;
	// 3. Path points to not a directory.
	for _, dir := range paths {
		// The directories come from the tt configuration and the environment
		// on purpose: any path the user names is a valid modules location.
		info, err := os.Stat(dir) //nolint:gosec // user-supplied modules directory.
		if err == nil {
			if !info.IsDir() {
				return dirs, errSpecifiedPathInConfigurationFileIsNotADirectory
			}

			dirs = append(dirs, dir)
		}
	}

	return dirs, nil
}

// getConfigModulesDirs returns from configuration the list of directories,
// where external modules are located.
func getConfigModulesDirs(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) ([]string, error) {
	if cmdCtx.Cli.ConfigPath == "" {
		// Ignore cliOpts.Modules without actual configuration file.
		return []string{}, nil
	}

	// Unspecified `modules` field is not considered an error.
	if cliOpts.Modules == nil || cliOpts.Modules.Directories == nil {
		return []string{}, nil
	}

	return collectDirectoriesList(cliOpts.Modules.Directories)
}

// getEnvironmentModulesDirs returns the list of modules directory based on environment info.
func getEnvironmentModulesDirs() ([]string, error) {
	envVar := os.Getenv("TT_CLI_MODULES_PATH")
	if envVar == "" {
		return []string{}, nil
	}

	paths := strings.Split(envVar, ":")

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
// parsing the contents of the list folders.
func getExternalModules(paths []string) (possibleModules, error) {
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
				log.Warnf("Ignore duplicate module %q overlap with %q", modPath, e.Directory)

				continue
			}

			if slices.Contains(disabledOverride, dirName) {
				return modules, fmt.Errorf("%w%q is disabled to override",
					errModuleIsDisabledToOverride, dirName)
			}

			if modEntry, isModule := isPossibleModule(modPath); isModule {
				modules[dirName] = modEntry
			}
		}
	}

	return modules, nil
}
