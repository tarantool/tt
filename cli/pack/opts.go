package pack

import (
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

type PackageType string

const (
	Tgz    PackageType = "tgz"
	Rpm                = "rpm"
	Deb                = "deb"
	Docker             = "docker"
)

// FillCtx fills pack context.
func FillCtx(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, packCtx *cmdcontext.PackCtx,
	args []string) error {

	if cliOpts.Modules != nil {
		packCtx.ModulesDirectory = cliOpts.Modules.Directory
	}
	packCtx.App = cliOpts.App
	packCtx.ConfigPath = cmdCtx.Cli.ConfigPath
	packCtx.TarantoolIsSystem = cmdCtx.Cli.IsSystem
	packCtx.TarantoolExecutable = cmdCtx.Cli.TarantoolExecutable
	packCtx.Type = args[0]

	cmdCtx.Pack = *packCtx

	return nil
}
