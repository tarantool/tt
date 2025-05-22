package modules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v3"
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
	mf := Manifest{}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return mf, fmt.Errorf("failed to read manifest: %s", err)
	}

	if err := yaml.Unmarshal(data, &mf); err != nil {
		return mf, fmt.Errorf("failed to parse manifest: %s", err)
	}

	mf.Main, err = exec.LookPath(filepath.Join(dir, mf.Main))
	if err != nil {
		return mf, fmt.Errorf("failed to find module executable: %s", err)
	}

	if mf.Version == "" {
		return mf, fmt.Errorf("version field is mandatory for module Manifest")
	}
	if mf.Help == "" {
		return mf, fmt.Errorf("help field is mandatory for module Manifest")
	}

	return mf, nil
}

func makeManifest(entry modulesEntry) (Manifest, error) {
	if entry.Manifest != "" {
		return readManifest(entry.Directory, entry.Manifest)
	}

	return fillManifest(Manifest{Main: entry.Main})
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
			"failed to get available external modules information: %s", err)
	}

	modulesInfo := ModulesInfo{}
	for name, info := range externalModules {
		mf, err := makeManifest(info)
		if err != nil {
			log.Warnf("Failed to get information about module %q: %s", name, err)
			continue
		}
		mf.Name = name
		commandPath := rootCmd + " " + name
		modulesInfo[commandPath] = mf
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
		if info, err := os.Stat(dir); err == nil {
			if !info.IsDir() {
				return dirs, fmt.Errorf("specified path in configuration file is not a directory")
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
	env_var := os.Getenv("TT_CLI_MODULES_PATH")
	if env_var == "" {
		return []string{}, nil
	}

	paths := strings.Split(env_var, ":")
	return collectDirectoriesList(paths)
}

// isPossibleModule checks is exists any manifest or executable `main` file inside dir.
func isPossibleModule(dir string) (modulesEntry, bool) {
	is_module := false
	entries := modulesEntry{Directory: dir}
	manifest, _ := util.GetYamlFileName(filepath.Join(dir, manifestFileName), false)
	if manifest != "" {
		entries.Manifest = manifest
		is_module = true
	}

	if main, err := exec.LookPath(filepath.Join(dir, mainEntryPoint)); err == nil {
		entries.Main = main
		is_module = true
	}

	return entries, is_module
}

// readSubDirectories returns sorted list of subdirectories in the specified path.
func readSubDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf(`failed to read "%s" directory: %s`, path, err)
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

		for _, d := range dirs {
			mod_path := filepath.Join(path, d)

			e, exists := modules[d]
			if exists {
				log.Warnf("Ignore duplicate module %q overlap with %q", mod_path, e.Directory)
				continue
			}

			if slices.Contains(disabledOverride, d) {
				return modules, fmt.Errorf("module %q is disabled to override", d)
			}

			if mod_entry, is_module := isPossibleModule(mod_path); is_module {
				modules[d] = mod_entry
			}
		}
	}
	return modules, nil
}
