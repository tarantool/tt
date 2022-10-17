package pack

import (
	"github.com/tarantool/tt/cli/cmdcontext"
)

type PackageType string

const (
	Tgz    PackageType = "tgz"
	Rpm                = "rpm"
	Deb                = "deb"
	Docker             = "docker"
)

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	args []string) error {

	packCtx.ConfigPath = cmdCtx.Cli.ConfigPath
	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolExecutable
	packCtx.Type = args[0]

	return nil
}
