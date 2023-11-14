package pack

import (
	"errors"

	"github.com/tarantool/tt/cli/cmdcontext"
)

type PackageType string

const (
	Tgz    = "tgz"
	Rpm    = "rpm"
	Deb    = "deb"
	Docker = "docker"
)

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	args []string) error {

	if (packCtx.IntegrityPrivateKey != "") && packCtx.CartridgeCompat {
		return errors.New("cannot pack with integrity checks in cartridge-compat mode")
	}

	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	packCtx.Type = args[0]

	return nil
}
