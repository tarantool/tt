package pack

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const (
	defaultInstanceFdLimit = 65535
)

//go:embed templates/app-unit-template.txt
var appUnitContentTemplate string

//go:embed templates/app-inst-unit-template.txt
var appInstUnitContentTemplate string

// initSystemdDir generates systemd unit files for every application in the current bundle.
// pathToEnv is a path to environment in the target system.
// baseDirPath is a root of the directory which will get packed.
func initSystemdDir(packCtx *PackCtx, opts *config.CliOpts,
	baseDirPath, pathToEnv string) error {
	log.Infof("Initializing systemd directory.")

	packageName, err := getPackageName(packCtx, opts, "", false)
	if err != nil {
		return err
	}

	systemdBaseDir := filepath.Join(baseDirPath, "etc", "systemd", "system")
	err = os.MkdirAll(systemdBaseDir, dirPermissions)
	if err != nil {
		return err
	}

	appUnitTemplate := appUnitContentTemplate
	appInstUnitTemplate := appInstUnitContentTemplate

	contentParams, err := getUnitParams(packCtx, pathToEnv, packageName)
	if err != nil {
		return err
	}

	appUnitPath := fmt.Sprintf("%s.service", packageName)
	appUnitPath = filepath.Join(systemdBaseDir, appUnitPath)
	err = util.InstantiateFileFromTemplate(appUnitPath, appUnitTemplate, contentParams)
	if err != nil {
		return err
	}

	appInstUnitPath := fmt.Sprintf("%s%%.service", packageName)
	appInstUnitPath = filepath.Join(systemdBaseDir, appInstUnitPath)
	err = util.InstantiateFileFromTemplate(appInstUnitPath, appInstUnitTemplate, contentParams)
	if err != nil {
		return err
	}

	return nil
}

// getUnitParams checks if there is a passed unit params file in context and
// returns its content. Otherwise, it returns the default params.
func getUnitParams(packCtx *PackCtx, pathToEnv,
	envName string) (map[string]interface{}, error) {
	pathToEnvFile := filepath.Join(pathToEnv, envFileName)
	ttBinary := getTTBinary(packCtx, pathToEnv)

	referenceParams := map[string]interface{}{
		"TT":         ttBinary,
		"ConfigPath": pathToEnvFile,
		"FdLimit":    defaultInstanceFdLimit,
		"EnvName":    envName,
	}

	contentParams := make(map[string]interface{})

	if packCtx.RpmDeb.SystemdUnitParamsFile != "" {
		unitTemplFile, err := os.Open(packCtx.RpmDeb.SystemdUnitParamsFile)
		if err != nil {
			return nil, err
		}

		err = yaml.NewDecoder(unitTemplFile).Decode(&contentParams)
		if err != nil {
			return nil, err
		}
	}
	for key := range referenceParams {
		if _, ok := contentParams[key]; !ok {
			contentParams[key] = referenceParams[key]
		}
	}
	return contentParams, nil
}

// getTTBinary returns a path to tt binary for the systemd ExecStart command.
// packagePath is path to the root of the package in the target system,
// where the package will be installed.
func getTTBinary(packCtx *PackCtx, packagePath string) string {
	if (!packCtx.TarantoolIsSystem && !packCtx.WithoutBinaries) ||
		packCtx.WithBinaries {
		return filepath.Join(packagePath, envBinPath, "tt")
	}
	return "tt"
}
