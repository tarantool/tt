// Package steps provides a set of handlers for create command chain of responsibility.
package steps

import (
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
)

// Step is an interface for single step in create chain.
type Step interface {
	Run(ctx *create_ctx.CreateCtx, appTemplateCtx *app_template.TemplateCtx) error
}
