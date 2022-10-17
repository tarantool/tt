package create

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/create/internal/steps"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

// FillCtx fills create context.
func FillCtx(cliOpts *config.CliOpts, createCtx *cmdcontext.CreateCtx, args []string) error {
	for _, p := range cliOpts.Templates {
		createCtx.TemplateSearchPaths = append(createCtx.TemplateSearchPaths, p.Path)
	}

	if len(args) >= 1 {
		createCtx.TemplateName = args[0]
	} else {
		return fmt.Errorf("Missing template name argument. " +
			"Try `tt create --help` for more information.")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	createCtx.WorkDir = workingDir

	return nil
}

// RollbackOnErr removes temporary application directory.
func rollbackOnErr(templateCtx *steps.TemplateCtx) {
	if templateCtx.AppPath != "" {
		os.RemoveAll(templateCtx.AppPath)
	}
	templateCtx.AppPath = ""
}

// Run creates an application from a template.
func Run(createCtx *cmdcontext.CreateCtx) error {
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
		steps.CollectTemplateVarsFromUser{Reader: steps.NewConsoleReader()},
		steps.RunHook{HookType: "pre"},
		steps.RenderTemplate{},
		steps.RunHook{HookType: "post"},
		steps.Cleanup{},
		steps.CreateDockerfile{},
		steps.MoveAppDirectory{},
	}

	templateCtx := steps.NewTemplateContext()
	for _, step := range stepsChain {
		if err := step.Run(createCtx, &templateCtx); err != nil {
			rollbackOnErr(&templateCtx)
			return err
		}
	}

	return nil
}

// checkCtx checks create context for validity.
func checkCtx(ctx *cmdcontext.CreateCtx) error {
	if ctx.TemplateName == "" {
		return fmt.Errorf("Template name is missing")
	}

	return nil
}
