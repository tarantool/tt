package modules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

const (
	manifestFileName = "manifest"
	mainEntryPoint   = "main"
)

// ModuleInfo stores information about Tarantool CLI module.
type ModuleInfo struct {
	// Is this module internal (or external).
	IsInternal bool
	// Path to module (used only is module external).
	ExternalPath string
}

// ModulesInfo stores information about all CLI modules.
type ModulesInfo map[string]*ModuleInfo

// modulesEntries keeps detected entry points while scan modules.
type modulesEntries struct {
	// Modules location path.
	Directory string
	// Path to manifest.yaml file.
	Manifest string
	// Path to `main` executable file.
	Main string
}

// possibleModules map module name with found its entry points.
type possibleModules map[string]modulesEntries

// fillSubCommandsInfo collects information about subcommands.
func fillSubCommandsInfo(cmd *cobra.Command, modulesInfo *ModulesInfo) {
	for _, subCmd := range cmd.Commands() {
		commandPath := subCmd.CommandPath()
		if _, found := (*modulesInfo)[commandPath]; !found {
			(*modulesInfo)[commandPath] = &ModuleInfo{
				IsInternal: true,
			}

			if subCmd.HasSubCommands() {
				fillSubCommandsInfo(subCmd, modulesInfo)
			}
		}
	}
}

// GetModulesInfo collects information about available modules (both external and internal).
func GetModulesInfo(cmdCtx *cmdcontext.CmdCtx, rootCmd *cobra.Command,
	cliOpts *config.CliOpts) (ModulesInfo, error) {
	modulesDirs, err := getConfigModulesDirs(cmdCtx, cliOpts)
	if err != nil {
		return nil, err
	}

	modulesEnvDirs, err := getEnvironmentModulesDirs()
	if err != nil {
		return nil, err
	}
	modulesDirs = append(modulesDirs, modulesEnvDirs...)

	// FIXME: working with modules list at https://github.com/tarantool/tt/issues/1016
	/* externalModules */
	_, err = getExternalModules(modulesDirs)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get available external modules information: %s", err)
	}

	// External modules have a higher priority than internal.
	modulesInfo := ModulesInfo{}
	// FIXME: adjust filling `modulesInfo` according results from `possibleModules` list.
	// See: https://github.com/tarantool/tt/issues/1016
	// for name, path := range externalModules {
	// 	commandPath := rootCmd.Name() + " " + name
	// 	modulesInfo[commandPath] = &ModuleInfo{
	// 		IsInternal:   false,
	// 		ExternalPath: path,
	// 	}
	// }

	fillSubCommandsInfo(rootCmd, &modulesInfo)

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
			// TODO: Add warning in next patches, discussion
			// what if the file exists, but access is denied, etc.
			// FIXME: resolve this question while prepare list:
			// https://github.com/tarantool/tt/issues/1014
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
	// Configuration file not detected - ignore and work on.
	// TODO: Add warning in next patches, discussion
	// what if the file exists, but access is denied, etc.
	if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err != nil {
		if !os.IsNotExist(err) {
			return []string{}, fmt.Errorf("failed to get access to configuration file: %s", err)
		}

		return []string{}, nil
	}

	// Unspecified `modules` field is not considered an error.
	if cliOpts.Modules == nil {
		return []string{}, nil
	}

	return collectDirectoriesList(cliOpts.Modules.Directories)
}

func getEnvironmentModulesDirs() ([]string, error) {
	env_var := os.Getenv("TT_CLI_MODULES_PATH")
	if env_var == "" {
		return []string{}, nil
	}
	paths := strings.Split(env_var, ":")
	return collectDirectoriesList(paths)
}

// isPossibleModule checks is exists any manifest or executable `main` file inside dir.
func isPossibleModule(dir string) (modulesEntries, bool) {
	is_module := false
	entries := modulesEntries{Directory: dir}
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

// getExternalModules returns map[name] = directory of available modules by
// parsing the contents of the list folders.
func getExternalModules(paths []string) (possibleModules, error) {
	modules := possibleModules{}
	for _, path := range paths {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf(`failed to read "%s" directory: %s`, path, err)
		}
		cnt_modules := 0
		for _, e := range entries {
			mod_path := filepath.Join(path, e.Name())
			if !e.IsDir() {
				continue
			}
			if mod_entry, is_module := isPossibleModule(mod_path); is_module {
				modules[e.Name()] = mod_entry
				cnt_modules += 1
			}
		}
		if cnt_modules == 0 {
			log.Warnf("Directory %q does not have any module", path)
		}
	}
	return modules, nil
}
