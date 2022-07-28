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

func FillCtx(cliOpts config.CliOpts, ctx *cmdcontext.CreateCtx, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("Please specify the name of the application.")
	}

	for _, p := range cliOpts.Templates {
		ctx.Paths = append(ctx.Paths, p.Path)
	}

	ctx.InstancesDir = cliOpts.App.InstancesAvailable
	ctx.AppName = args[0]

	return nil
}

func RollbackOnErr(templateCtx *templates.TemplateCtx) {
	if templateCtx.AppPath != "" {
		os.RemoveAll(templateCtx.AppPath)
	}
	templateCtx.AppPath = ""
}

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
		steps.CollectTemplateVarsFromUser{},
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

func checkCtx(ctx cmdcontext.CreateCtx) error {
	if ctx.TemplateName == "" {
		return fmt.Errorf("Template name is missing")
	}

	return nil
}
