package cmdcontext

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/cli/version"
)

var (
	errFailedToGetTarantoolVersionCorruptedData = errors.New(
		"failed to get tarantool version: corrupted data",
	)
	errFileNotFound                                           = errors.New("file ")
	errTarantoolExecutableIsNotSetUnableToGetTarantoolVersion = errors.New(
		"tarantool executable is not set, unable to get tarantool version",
	)
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
}

const minVersionFields = 2

// GetVersion returns and caches the tarantool version.
func (tntCli *TarantoolCli) GetVersion() (version.Version, error) {
	if tntCli.version.Str != "" {
		return tntCli.version, nil
	}

	if tntCli.Executable == "" {
		return tntCli.version, errTarantoolExecutableIsNotSetUnableToGetTarantoolVersion
	}

	output, err := exec.CommandContext(
		context.Background(), tntCli.Executable, "--version").Output()
	if err != nil {
		return tntCli.version, fmt.Errorf("failed to get tarantool version: %w", err)
	}

	versionOut := strings.Split(string(output), "\n")
	versionLine := strings.Split(versionOut[0], " ")

	if len(versionLine) < minVersionFields {
		return tntCli.version, errFailedToGetTarantoolVersionCorruptedData
	}

	tntVersion, err := version.Parse(versionLine[len(versionLine)-1])
	if err != nil {
		return tntCli.version, fmt.Errorf("failed to get tarantool version: %w", err)
	}

	tntCli.version = tntVersion

	return tntCli.version, nil
}

// selfFlagUnknown is what a tt that has no --self flag says when it is given
// one.
const selfFlagUnknown = "unknown flag: --self"

// TtVersionOutput returns what the tt at pathToBin prints for version
// --commit.
//
// The tt asked may be an older one, which runs the tt of the bin_dir of the
// tt.yaml it finds - from its working directory upward, or the system one -
// in its own place unless it is given --self; asked plainly, it could report
// the version of that other tt. So --self is passed first, and the question is
// asked again without it only when the tt says it knows no such flag: a tt
// that has none runs no other tt either.
func TtVersionOutput(ctx context.Context, pathToBin string) ([]byte, error) {
	var stderr strings.Builder

	command := exec.CommandContext(ctx, pathToBin, "--self", "version", "--commit")

	command.Stderr = &stderr

	output, err := command.Output()
	if err == nil {
		return output, nil
	}

	if !strings.Contains(stderr.String(), selfFlagUnknown) {
		return nil, fmt.Errorf("%s --self version --commit: %w", pathToBin, err)
	}

	output, err = exec.CommandContext(ctx, pathToBin, "version", "--commit").Output()
	if err != nil {
		return nil, fmt.Errorf("%s version --commit: %w", pathToBin, err)
	}

	return output, nil
}

// GetTtVersion returns version of Tt provided by its executable path.
func GetTtVersion(pathToBin string) (version.Version, error) {
	if !util.IsRegularFile(pathToBin) {
		return version.Version{}, fmt.Errorf("%w%q not found", errFileNotFound, pathToBin)
	}

	output, err := TtVersionOutput(context.Background(), pathToBin)
	if err != nil {
		return version.Version{}, fmt.Errorf("failed to get tt version: %w", err)
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
	// Use internal module even if an external one is found.
	ForceInternal bool
	// ProjectDir is the project directory the working directory is in, as
	// internal/scope finds it, or the working directory outside a project.
	ProjectDir string
	// Path to tt daemon (tt_daemon.yaml) config.
	DaemonCfgPath string
	// TarantoolPrefix is the directory TarantoolCli is installed under, whose
	// include/tarantool holds its headers, when tt binary installed it: the
	// root of the project scope for the tarantool of the project. It is
	// empty for a tarantool from PATH.
	TarantoolPrefix string
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
	// NoPrompt flag needs to skip cli interaction using default behavior.
	NoPrompt bool
}
