// Package steps provides a set of handlers for create command chain of responsibility.
package steps

import (
	"github.com/tarantool/tt/cli/cmdcontext"
)

// Step is an interface for single step in create chain.
type Step interface {
	Run(ctx *cmdcontext.CreateCtx, appTemplateCtx *TemplateCtx) error
}
