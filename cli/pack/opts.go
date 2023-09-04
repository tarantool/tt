package pack

import (
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

	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolCli.Executable
	packCtx.Type = args[0]

	return nil
}
