package pack

import (
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

// Packer is an interface that packs an app.
type Packer interface {
	Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx, opts *config.CliOpts) error
}
