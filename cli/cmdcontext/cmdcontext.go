package cmdcontext

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
	"github.com/tarantool/tt/lib/integrity"
)

// CmdCtx is the main structure of the program context.
// Contains within itself other structures of CLI modules.
type CmdCtx struct {
	// Cli - CLI context. Contains flags passed when starting
	// Tarantool CLI and some other parameters.
	Cli CliCtx
	// CommandName contains name of the command.
	CommandName string
	// Integrity contains tools used for integrity checking.
	Integrity integrity.IntegrityCtx
}

// TarantoolCli describes tarantool executable.
type TarantoolCli struct {
	// Executable is a path to Tarantool executable.
	Executable string
	// Tarantool version.
	version version.Version
}

// TcmCli describes tcm executable.
type TcmCli struct {
	// Executable is a path to tcm executable.
	Executable string
	// ConfigPath is a path to tcm config (tcm.yaml)
	ConfigPath string
}

// GetVersion returns and caches the tarantool version.
func (tntCli *TarantoolCli) GetVersion() (version.Version, error) {
	if tntCli.version.Str != "" {
		return tntCli.version, nil
	}

	if tntCli.Executable == "" {
		return tntCli.version, fmt.Errorf(
			"tarantool executable is not set, unable to get tarantool version")
	}
	output, err := exec.Command(tntCli.Executable, "--version").Output()
	if err != nil {
		return tntCli.version, fmt.Errorf("failed to get tarantool version: %s", err)
	}

	versionOut := strings.Split(string(output), "\n")
	versionLine := strings.Split(versionOut[0], " ")

	if len(versionLine) < 2 {
		return tntCli.version, fmt.Errorf("failed to get tarantool version: corrupted data")
	}

	tntVersion, err := version.Parse(versionLine[len(versionLine)-1])
	if err != nil {
		return tntCli.version, fmt.Errorf("failed to get tarantool version: %w", err)
	}

	tntCli.version = tntVersion

	return tntCli.version, nil
}

// GetTtVersion returns version of Tt provided by its executable path.
func GetTtVersion(pathToBin string) (version.Version, error) {
	if !util.IsRegularFile(pathToBin) {
		return version.Version{}, fmt.Errorf("file %q not found", pathToBin)
	}

	output, err := exec.Command(pathToBin, "--self", "version",
		"--commit").Output()
	if err != nil {
		return version.Version{}, fmt.Errorf("failed to get tt version: %s", err)
	}

	ttVersion, err := version.ParseTt(string(output))
	if err != nil {
		return version.Version{}, err
	}

	return ttVersion, nil
}

// CliCtx - CLI context. Contains flags passed when starting
// Tarantool CLI and some other parameters.
type CliCtx struct {
	// Is CLI launch system (or local).
	IsSystem bool
	// Use internal module even if an external one is found.
	ForceInternal bool
	// Path to local directory (or empty string if CLI launch is system).
	LocalLaunchDir string
	// Path to Tarantool CLI (tt.yaml) config.
	ConfigPath string
	// ConfigDir is tt configuration file directory.
	// And current working directory, if there is no config.
	ConfigDir string
	// Path to tt daemon (tt_daemon.yaml) config.
	DaemonCfgPath string
	// The flag determines if the tarantool binary is from the internal tt repository.
	IsTarantoolBinFromRepo bool
	// Verbose logging flag. Enables debug log output.
	Verbose bool
	// TarantoolCli is current tarantool cli.
	TarantoolCli TarantoolCli
	// TcmCli is current tcm cli.
	TcmCli TcmCli
	// IntegrityCheck is a public key used for integrity check.
	IntegrityCheck string
	// IntegrityCheckPeriod is an period during which the integrity check is reproduced.
	IntegrityCheckPeriod int
	// This flag disables searching of other tt versions to run
	// instead of the current one.
	IsSelfExec bool
	// NoPrompt flag needs to skip cli interaction using default behavior.
	NoPrompt bool
}
