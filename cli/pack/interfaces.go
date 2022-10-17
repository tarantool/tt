package pack

import "github.com/tarantool/tt/cli/cmdcontext"

// Packer is an interface that packs an app.
type Packer interface {
	Run(cmdCtx *cmdcontext.CmdCtx, packCtx *cmdcontext.PackCtx) error
}
