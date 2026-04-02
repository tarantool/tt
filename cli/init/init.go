package init

import (
	// Go embed blank import.
	_ "embed"
	"fmt"
	"io"
	"os"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
)

const (
	defaultDirPermissions = os.FileMode(0o750)
)

// InitCtx contains information for tt config creation.
type InitCtx struct {
	// ForceMode, if set, tt config is re-written without a question.
	ForceMode bool
	// reader to use for reading user input.
	reader io.Reader
	// Tarantool executable path.
	TarantoolExecutable string
}

// configData is a configuration data loaded from an existing config.
type configData struct {
	instancesEnabled   string
	logDir             string
	runDir             string
	walDir             string
	vinylDir           string
	memtxDir           string
	tarantoolctlLayout bool
}

// createDirectories creates directories specified in dirList.
func createDirectories(dirList []string) error {
	for _, dirName := range dirList {
		if dirName == "" {
			continue
		}
		if err := util.CreateDirectory(dirName, defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", dirName)
	}
	return nil
}

//go:embed templates/tt.yaml.default
var ttYamlTemplate string

// generateTtEnv generates environment config in configPath using configuration data provided.
func generateTtEnv(configPath string, sourceCfg configData) error {
	cfg := configure.GetDefaultCliOpts()
	if sourceCfg.runDir != "" {
		cfg.App.RunDir = sourceCfg.runDir
	}
	if sourceCfg.walDir != "" {
		cfg.App.WalDir = sourceCfg.walDir
	}
	if sourceCfg.vinylDir != "" {
		cfg.App.VinylDir = sourceCfg.vinylDir
	}
	if sourceCfg.memtxDir != "" {
		cfg.App.MemtxDir = sourceCfg.memtxDir
	}
	if sourceCfg.logDir != "" {
		cfg.App.LogDir = sourceCfg.logDir
	}
	if sourceCfg.instancesEnabled != "" {
		cfg.Env.InstancesEnabled = sourceCfg.instancesEnabled
	}
	cfg.Env.TarantoolctlLayout = sourceCfg.tarantoolctlLayout

	ttYamlContent, err := util.GetTextTemplatedStr(&ttYamlTemplate, cfg)
	if err != nil {
		return err
	}

	err = os.WriteFile(configPath, []byte(ttYamlContent), 0o644)
	if err != nil {
		return err
	}

	directoriesToCreate := []string{
		cfg.Env.InstancesEnabled,
		cfg.Env.IncludeDir,
		cfg.Env.BinDir,
		cfg.Repo.Install,
	}
	directoriesToCreate = append(directoriesToCreate, cfg.Modules.Directories...)
	for _, templatesPathOpts := range cfg.Templates {
		directoriesToCreate = append(directoriesToCreate, templatesPathOpts.Path)
	}

	return createDirectories(directoriesToCreate)
}

// FillCtx initializes init context.
func FillCtx(initCtx *InitCtx) {
	initCtx.reader = os.Stdin
}

// checkExistingConfig checks tt config for existence and asks for confirmation to overwrite.
// Returns file name if init process can continue, and false otherwise. In case of error, non-nil
// error returned as second returned value.
func checkExistingConfig(initCtx *InitCtx) (string, error) {
	configName, err := util.GetYamlFileName(configure.ConfigName, false)
	if err != nil {
		return "", err
	} else if configName == "" {
		return configure.ConfigName, err
	}

	if _, err := os.Stat(configName); err == nil {
		if initCtx.ForceMode {
			if err = os.Remove(configName); err != nil {
				return "", err
			}
		} else {
			confirmed, err := util.AskConfirm(initCtx.reader,
				fmt.Sprintf("%s already exists. Overwrite?", configName))
			if err != nil {
				return "", err
			}
			if confirmed {
				if err = os.Remove(configName); err != nil {
					return "", err
				}
			} else {
				log.Info("Init is cancelled by user.")
				return "", nil
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return configName, nil
}

// Run creates tt environment config for the application in current dir.
func Run(initCtx *InitCtx) error {
	if initCtx.reader == nil {
		initCtx.reader = os.Stdin
	}

	configName, err := checkExistingConfig(initCtx)
	if configName == "" {
		return err
	}

	var sourceCfg configData

	if !util.IsApp(".") {
		// Current directory is not app dir, instances enabled dir will be used.
		sourceCfg.instancesEnabled = configure.InstancesEnabledDirName
	}

	if err := generateTtEnv(configName, sourceCfg); err != nil {
		return err
	}

	log.Infof("Environment config is written to '%s'", configure.ConfigName)

	return nil
}
