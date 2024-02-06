package pack

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const (
	defaultInstanceFdLimit = 65535
)

//go:embed templates/app-inst-unit-template.txt
var appInstUnitContentTemplate string

// systemdUnitFileName generates systemd unit file name for application.
func systemdUnitFileName(inst running.InstanceCtx) string {
	if inst.SingleApp {
		return fmt.Sprintf("%s.service", inst.AppName)
	} else {
		return fmt.Sprintf("%s@.service", inst.AppName)
	}
}

// systemdDescriptionAppName generates an app name to use in description line in systemd unit.
func systemdDescriptionAppName(inst running.InstanceCtx) string {
	if inst.SingleApp {
		return inst.AppName
	} else {
		return fmt.Sprintf("%s@%%i", inst.AppName)
	}
}

// initSystemdDir generates systemd unit files for every application in the current bundle.
// pathToEnv is a path to environment in the target system.
// baseDirPath is a root of the directory which will get packed.
func initSystemdDir(packCtx *PackCtx, opts *config.CliOpts,
	baseDirPath, pathToEnv string) error {
	log.Infof("Initializing systemd directory.")

	systemdBaseDir := filepath.Join(baseDirPath, "usr", "lib", "systemd", "system")
	err := os.MkdirAll(systemdBaseDir, dirPermissions)
	if err != nil {
		return err
	}

	for appName, instances := range packCtx.AppsInfo {
		if len(instances) == 0 {
			return fmt.Errorf("missing instances list for %q application", appName)
		}
		inst := instances[0]
		// Create service systemd.unit for each application.
		appInstUnitPath := systemdUnitFileName(inst)
		appInstUnitPath = filepath.Join(systemdBaseDir, appInstUnitPath)

		log.Debugf("Generating systemd unit for %q application.", appName)
		contentParams, err := getUnitParams(packCtx, pathToEnv, inst)
		if err != nil {
			return err
		}

		if err = util.InstantiateFileFromTemplate(appInstUnitPath, appInstUnitContentTemplate,
			contentParams); err != nil {
			return fmt.Errorf("failed to create systemd unit file: %s", err)
		}
	}

	return nil
}

// systemdExecArgsForApp generates CLI arguments for start/stop commands in unit file.
func systemdExecArgsForApp(inst running.InstanceCtx) (string, error) {
	if inst.SingleApp {
		// There are no instances for single instance application. So only app name is returned.
		return inst.AppName, nil
	} else {
		// Return <app><delimiter>%i format ars. %i will be replaced with instance name.
		return fmt.Sprintf("%s%c%%i", inst.AppName, running.InstanceDelimiter), nil
	}
}

// getUnitParams checks if there is a passed unit params file in context and
// returns its content. Otherwise, it returns the default params.
func getUnitParams(packCtx *PackCtx, pathToEnv string,
	inst running.InstanceCtx) (map[string]interface{}, error) {
	ttBinary := getTTBinary(packCtx, pathToEnv)

	referenceParams := map[string]interface{}{
		"TT":         ttBinary,
		"ConfigPath": pathToEnv,
		"FdLimit":    defaultInstanceFdLimit,
	}

	contentParams := make(map[string]interface{})

	if packCtx.RpmDeb.SystemdUnitParamsFile != "" {
		unitTemplFile, err := os.Open(packCtx.RpmDeb.SystemdUnitParamsFile)
		if err != nil {
			return nil, err
		}

		if err = yaml.NewDecoder(unitTemplFile).Decode(&contentParams); err != nil {
			return nil, err
		}
	}
	for key := range referenceParams {
		if _, ok := contentParams[key]; !ok {
			contentParams[key] = referenceParams[key]
		}
	}
	// Application name is specific for each generated per-application systemd unit, so it
	// should not be set by unit params file, because it will become the same for all units.
	contentParams["AppName"] = systemdDescriptionAppName(inst)
	var err error
	if contentParams["ExecArgs"], err = systemdExecArgsForApp(inst); err != nil {
		return contentParams,
			fmt.Errorf("cannot generate tt start/stop argument for systemd unit: %s", err)
	}
	return contentParams, nil
}

// getTTBinary returns a path to tt binary for the systemd ExecStart command.
// packagePath is path to the root of the package in the target system,
// where the package will be installed.
func getTTBinary(packCtx *PackCtx, packagePath string) string {
	if (!packCtx.TarantoolIsSystem && !packCtx.WithoutBinaries) ||
		packCtx.WithBinaries {
		return filepath.Join(packagePath, configure.BinPath, "tt")
	}
	return "tt"
}
