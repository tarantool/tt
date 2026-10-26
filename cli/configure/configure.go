// Package configure fills the process context of tt for the command line:
// the project the working directory is in, the tarantool and tcm executables
// the commands run, and the config of tt daemon.
//
// No configuration file of tt is read here. tt.yaml, which described a tt
// environment, is gone: the project is found by its markers, and the
// executables come from the project scope tt binary installs into, and from
// PATH.
package configure

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

var (
	errFailedToParseDaemonConfigurationMissingDaemonSection = errors.New(
		"failed to parse daemon configuration: missing daemon section",
	)
	errIntegrityCheckPeriodMustTakeNonNegativeValue = errors.New(
		"--integrity-check-period must take non-negative value",
	)
	errIntegrityCheckPublicKeyRequired = errors.New(
		"need to specify public key in --integrity-check to use --integrity-check-period",
	)
)

const (
	defaultDaemonPort    = 1024
	defaultDaemonPidFile = "tt_daemon.pid"
	defaultDaemonLogFile = "tt_daemon.log"

	daemonCfgPath     = "tt_daemon.yaml"
	configHomeEnvName = "XDG_CONFIG_HOME"
)

var (
	// varRunPath is where tt daemon keeps its pid file when its config does
	// not say, relative to the config.
	varRunPath = filepath.Join("var", "run")
	// varLogPath is where tt daemon keeps its log when its config does not
	// say, relative to the config.
	varLogPath = filepath.Join("var", "log")
)

// getDefaultDaemonOpts returns `DaemonOpts` filled with default values.
func getDefaultDaemonOpts() *config.DaemonOpts {
	return &config.DaemonOpts{
		Port:            defaultDaemonPort,
		PIDFile:         defaultDaemonPidFile,
		RunDir:          varRunPath,
		LogDir:          varLogPath,
		LogFile:         defaultDaemonLogFile,
		ListenInterface: "",
	}
}

// GetDaemonOpts returns tt daemon options from the config file
// located at path configurePath.
func GetDaemonOpts(configurePath string) (*config.DaemonOpts, error) {
	var cfg config.DaemonCfg

	if configurePath == "" {
		return getDefaultDaemonOpts(), nil
	}

	// Config could not be processed.
	_, err := os.Stat(configurePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get access to daemon configuration file: %w", err)
	}

	rawConfigOpts, err := util.ParseYAML(configurePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse daemon configuration: %w", err)
	}

	err = mapstructure.Decode(rawConfigOpts, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse daemon configuration: %w", err)
	}

	if cfg.DaemonConfig == nil {
		return nil, errFailedToParseDaemonConfigurationMissingDaemonSection
	}

	if cfg.DaemonConfig.PIDFile == "" {
		cfg.DaemonConfig.PIDFile = defaultDaemonPidFile
	}

	if cfg.DaemonConfig.LogFile == "" {
		cfg.DaemonConfig.LogFile = defaultDaemonLogFile
	}

	if cfg.DaemonConfig.Port == 0 {
		cfg.DaemonConfig.Port = defaultDaemonPort
	}

	if cfg.DaemonConfig.RunDir == "" {
		cfg.DaemonConfig.RunDir = filepath.Join(filepath.Dir(configurePath),
			varRunPath)
	}

	if cfg.DaemonConfig.LogDir == "" {
		cfg.DaemonConfig.LogDir = filepath.Join(filepath.Dir(configurePath),
			varLogPath)
	}

	return cfg.DaemonConfig, nil
}

// ValidateCliOpts checks the root flags for values that contradict each
// other.
func ValidateCliOpts(cliCtx *cmdcontext.CliCtx) error {
	if len(cliCtx.IntegrityCheck) == 0 && cliCtx.IntegrityCheckPeriod != 0 {
		return errIntegrityCheckPublicKeyRequired
	}

	if cliCtx.IntegrityCheckPeriod < 0 {
		return errIntegrityCheckPeriodMustTakeNonNegativeValue
	}

	return nil
}

// Cli fills the process context in cmdCtx: the project directory, the
// tarantool and tcm executables and the path of the tt daemon config.
func Cli(cmdCtx *cmdcontext.CmdCtx) error {
	var err error

	cmdCtx.Cli.ProjectDir, err = projectDir(cmdCtx.Cli.IntegrityCheck != "")
	if err != nil {
		return err
	}

	cmdCtx.Cli.DaemonCfgPath, err = getDaemonCfgPath(daemonCfgPath)
	if err != nil {
		return fmt.Errorf("failed to get tt daemon config: %w", err)
	}

	detectTarantool(&cmdCtx.Cli)

	cmdCtx.Cli.TcmCli.Executable = detectTcm(cmdCtx.Cli.ProjectDir)
	log.Debugf("tcm executable found: %q", cmdCtx.Cli.TcmCli.Executable)

	return nil
}

// projectDir returns the project directory the working directory is in, as
// internal/scope finds it, or the working directory itself outside a project.
//
// A failure to look for the project, such as a relative TT_USER_ROOT, leaves
// the working directory in its place rather than failing the command: the
// commands that act on a project look for it themselves and report why it
// is not there, and the others, tt version for one, have no use for it.
// With strict, which the integrity checks ask for, it is an error: the
// project is the root they check, and checking another directory in its
// place would pass files nobody signed.
func projectDir(strict bool) (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to detect current directory: %w", err)
	}

	dir, found, err := scope.FindProject(workingDir)
	if err != nil && strict {
		return "", fmt.Errorf("failed to find the project: %w", err)
	}

	if err != nil {
		log.Debugf("Failed to look for the project, %q stands for it: %s", workingDir, err)

		return workingDir, nil
	}

	if !found {
		return workingDir, nil
	}

	return dir, nil
}

// detectTarantool sets the tarantool the commands run, the one tt run would
// run in the project: its .tarantool/bin/tarantool, which tt binary install
// and switch set, else the first on PATH (cli/tarantool tells the rule). The
// headers of a project tarantool are under the root of the project scope,
// include/tarantool, where tt binary install lays them out, so the root
// becomes the prefix of the tarantool.
//
// A missing tarantool is not an error here: most commands run none, and those
// that do report it. Neither is a broken project tarantool, which is only
// logged, and at the debug level: every command would warn about it
// otherwise, tt binary included, which lists and repairs the link.
func detectTarantool(cli *cmdcontext.CliCtx) {
	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: cli.ProjectDir})
	if err != nil {
		log.Debugf("Tarantool executable is not found: %s", err)

		return
	}

	cli.TarantoolCli.Executable = exe.Path
	if exe.Origin == tarantool.OriginProject {
		cli.TarantoolPrefix = scope.ProjectRoot(cli.ProjectDir)
	}

	log.Debugf("Tarantool executable found: %q", exe.Path)
}

// detectTcm returns the tcm the tcm commands run: the active one of the
// project scope, .tarantool/bin/tcm, which tt binary install sets, else the
// first on PATH, else none.
func detectTcm(projectDir string) string {
	local := filepath.Join(scope.ProjectRoot(projectDir), binary.BinDirName,
		binary.ProgramTcm.Exec())

	path, err := exec.LookPath(local)
	if err == nil {
		return path
	}

	path, _ = exec.LookPath(binary.ProgramTcm.Exec())

	return path
}

// getDaemonCfgPath looks for the path to the tt_daemon.yaml configuration file.
// Tries to locate tt_daemon.cfg in following order:
// 1) $XDG_CONFIG_HOME/tt/tt_daemon.cfg (if $XDG_CONFIG_HOME is not set,
// uses $HOME/.config);
// 2) $HOME/.tt_daemon.cfg;
// 3) looking in current directory for tt_daemon.cfg.
// See: https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html,
// https://unix.stackexchange.com/posts/313001/revisions.
func getDaemonCfgPath(configName string) (string, error) {
	var xdgConfigDir string

	xdgConfigHome := os.Getenv(configHomeEnvName)
	homeDir := os.Getenv("HOME")

	if xdgConfigHome != "" {
		xdgConfigDir = xdgConfigHome + "/tt"
	} else {
		xdgConfigDir = homeDir + "/.config/tt"
	}

	// Config in $XDG_CONFIG_HOME.
	configPath := xdgConfigDir + "/" + configName

	//nolint:gosec // The config location is taken from the environment by design.
	_, err := os.Stat(configPath)
	if err == nil {
		return configPath, nil
	}

	// Config in $HOME.
	configPath = fmt.Sprintf("%s/.%s", homeDir, configName)

	//nolint:gosec // The config location is taken from the environment by design.
	_, err = os.Stat(configPath)
	if err == nil {
		return configPath, nil
	}

	// Config in current dir.
	curDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to detect current directory: %w", err)
	}

	configPath = fmt.Sprintf("%s/%s", curDir, configName)

	_, err = os.Stat(configPath)
	if err == nil {
		return configPath, nil
	}

	return "", nil
}
