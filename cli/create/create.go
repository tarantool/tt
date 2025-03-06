package create

import (
	"bufio"
	"fmt"
	"os"

	"github.com/tarantool/tt/cli/config"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/create/internal/steps"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// FillCtx fills create context.
func FillCtx(cliOpts *config.CliOpts, createCtx *create_ctx.CreateCtx) error {
	for _, p := range cliOpts.Templates {
		createCtx.TemplateSearchPaths = append(createCtx.TemplateSearchPaths, p.Path)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	createCtx.WorkDir = workingDir

	return nil
}

// RollbackOnErr removes temporary application directory.
func rollbackOnErr(templateCtx *app_template.TemplateCtx) {
	if templateCtx.AppPath != "" {
		os.RemoveAll(templateCtx.AppPath)
	}
	templateCtx.AppPath = ""
}

// Run creates an application from a template.
func Run(cliOpts *config.CliOpts, createCtx *create_ctx.CreateCtx) error {
	util.CheckRecommendedBinaries("git")

	if err := checkCtx(createCtx); err != nil {
		return util.InternalError("Create context check failed: %s", version.GetVersion, err)
	}

	stepsChain := []steps.Step{
		steps.SetPredefinedVariables{},
		steps.LoadVarsFile{},
		steps.FillTemplateVarsFromCli{},
		steps.CreateTemporaryAppDirectory{},
		steps.CopyAppTemplate{},
		steps.LoadManifest{},
		steps.CollectTemplateVarsFromUser{Reader: bufio.NewReader(os.Stdin)},
		steps.RunHook{HookType: "pre"},
		steps.RenderTemplate{},
		steps.RunHook{HookType: "post"},
		steps.Cleanup{},
		steps.MoveAppDirectory{},
		steps.CreateAppSymlink{SymlinkDir: cliOpts.Env.InstancesEnabled},
		steps.PrintFollowUpMessage{Writer: os.Stdout},
	}

	templateCtx := app_template.NewTemplateContext()
	for _, step := range stepsChain {
		if err := step.Run(createCtx, &templateCtx); err != nil {
			rollbackOnErr(&templateCtx)
			return err
		}
	}

	return nil
}

// checkCtx checks create context for validity.
func checkCtx(ctx *create_ctx.CreateCtx) error {
	if ctx.TemplateName == "" {
		return fmt.Errorf("template name is missing")
	}

	return nil
}
