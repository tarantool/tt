package create

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/create/internal/steps"
	"github.com/tarantool/tt/cli/create/internal/templates"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// FillCtx fills create context.
func FillCtx(cliOpts config.CliOpts, ctx *cmdcontext.CreateCtx, args []string) error {
	for _, p := range cliOpts.Templates {
		ctx.Paths = append(ctx.Paths, p.Path)
	}

	ctx.InstancesDir = cliOpts.App.InstancesAvailable
	if len(args) >= 1 {
		ctx.TemplateName = args[0]
	} else {
		ctx.TemplateName = "basic"
	}

	if ctx.AppName == "" {
		ctx.AppName = ctx.TemplateName
	}

	return nil
}

// RollbackOnErr removes application directory.
func RollbackOnErr(templateCtx *templates.TemplateCtx) {
	if templateCtx.AppPath != "" {
		os.RemoveAll(templateCtx.AppPath)
	}
	templateCtx.AppPath = ""
}

// Run creates application from a template.
func Run(ctx cmdcontext.CreateCtx) error {
	util.CheckRecommendedBinaries("git")

	if err := checkCtx(ctx); err != nil {
		return util.InternalError("Create context check failed: %s", version.GetVersion, err)
	}

	stepsChain := []steps.Step{
		steps.FillTemplateVarsFromCli{},
		steps.CreateAppDirectory{},
		steps.CopyAppTemplate{},
		steps.LoadManifest{},
		steps.CollectTemplateVarsFromUser{Reader: steps.NewConsoleReader()},
		steps.RunHook{HookType: "pre"},
		steps.RenderTemplate{},
		steps.RunHook{HookType: "post"},
	}

	templateCtx := templates.NewTemplateContext()
	for _, step := range stepsChain {
		if err := step.Run(ctx, &templateCtx); err != nil {
			RollbackOnErr(&templateCtx)
			return err
		}
	}

	return nil
}

// checkCtx checks create context for validity.
func checkCtx(ctx cmdcontext.CreateCtx) error {
	if ctx.TemplateName == "" {
		return fmt.Errorf("Template name is missing")
	}

	return nil
}
