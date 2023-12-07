package cmdcontext

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/cli/integrity"
	"github.com/tarantool/tt/cli/version"
)

// CmdCtx is the main structure of the program context.
// Contains within itself other structures of CLI modules.
type CmdCtx struct {
	// Cli - CLI context. Contains flags passed when starting
	// Tarantool CLI and some other parameters.
	Cli CliCtx
	// CommandName contains name of the command.
	CommandName string
	// FileRepository is used for reading files that require
	// integrity control.
	FileRepository integrity.Repository
}

// TarantoolCli describes tarantool executable.
type TarantoolCli struct {
	// Executable is a path to Tarantool executable.
	Executable string
	// Tarantool version.
	version version.Version
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
	// IntegrityCheck is a public key used for integrity check.
	IntegrityCheck string
}
