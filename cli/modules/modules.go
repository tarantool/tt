package modules

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

const (
	helpModuleName = "help"
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

// GetModulesInfo collects information about available modules (both external and internal).
func GetModulesInfo(cmdCtx *cmdcontext.CmdCtx, subCommands []*cobra.Command,
	cliOpts *config.CliOpts) (ModulesInfo, error) {
	modulesDir, err := getExternalModulesDir(cmdCtx, cliOpts)
	if err != nil {
		return nil, err
	}

	externalModules, err := getExternalModules(modulesDir)
	if err != nil {
		return nil, fmt.Errorf(
			"Failed to get available external modules information: %s", err)
	}

	// External modules have a higher priority than internal.
	modulesInfo := ModulesInfo{}
	for name, path := range externalModules {
		modulesInfo[name] = &ModuleInfo{
			IsInternal:   false,
			ExternalPath: path,
		}
	}

	for _, cmd := range subCommands {
		if _, found := modulesInfo[cmd.Name()]; !found {
			modulesInfo[cmd.Name()] = &ModuleInfo{
				IsInternal: true,
			}
		}
	}

	return modulesInfo, nil
}

// getExternalModulesDir returns the directory where external modules are located.
func getExternalModulesDir(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) (string, error) {
	// Configuraion file not detected - ignore and work on.
	// TODO: Add warning in next patches, discussion
	// what if the file exists, but access is denied, etc.
	if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("Failed to get access to configuration file: %s", err)
		}

		return "", nil
	}

	// Unspecified `modules` field is not considered an error.
	if cliOpts.Modules == nil {
		return "", nil
	}

	// We return an error only if the following conditions are met:
	// 1. If a directory field is specified;
	// 2. Specified path exists;
	// 3. Path points to not a directory.
	modulesDir := cliOpts.Modules.Directory
	if info, err := os.Stat(modulesDir); err == nil {
		// TODO: Add warning in next patches, discussion
		// what if the file exists, but access is denied, etc.
		if !info.IsDir() {
			return "", fmt.Errorf("Specified path in configuration file is not a directory")
		}
	}

	return modulesDir, nil
}

// getExternalModules returns map of available modules by
// parsing the contents of the path folder.
func getExternalModules(path string) (map[string]string, error) {
	modules := make(map[string]string)

	// If the directory doesn't exist, it is not an error.
	// TODO: Add warning in next patches, discussion
	// what if the file exists, but access is denied, etc.
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf(`Failed to read "%s" directory: %s`, path, err)
		}

		return nil, nil
	}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf(`Failed to read "%s" directory: %s`, path, err)
	}

	for _, f := range files {
		// Ignore non executable files.
		if path, err := exec.LookPath(filepath.Join(path, f.Name())); err == nil {
			modules[strings.Split(f.Name(), ".")[0]] = path
		}
	}

	return modules, nil
}
