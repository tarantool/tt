package emmylua

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
)

// runner runs the programs of EmmyLua in the project of the command.
type runner struct {
	services sdk.Services
}

// newCheckCmd builds tt emmylua check.
func newCheckCmd(services sdk.Services) *cobra.Command {
	run := runner{services: services}

	return &cobra.Command{
		Use:   "check [PATH...] [-- EMMYLUA_CHECK_ARGS...]",
		Short: "Check the Lua code of the project",
		Long: "Run emmylua_check on PATH (default the project) with the configuration " +
			"tt writes, then the project's .luarc.json and .emmyrc.json. The command " +
			"fails as emmylua_check does: when it reports an error, or with " +
			"--warnings-as-errors a warning. " + passHelp,
		Example: `  $ tt emmylua check
  $ tt emmylua check app -- --warnings-as-errors --output-format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run.withConfig(cmd, args, analyzer.Check)
		},
	}
}

// newDocCmd builds tt emmylua doc.
func newDocCmd(services sdk.Services) *cobra.Command {
	run := runner{services: services}

	return &cobra.Command{
		Use:   "doc [PATH...] [-- EMMYLUA_DOC_CLI_ARGS...]",
		Short: "Generate the documentation of the Lua code of the project",
		Long: "Run emmylua_doc_cli on PATH (default the project) with the configuration " +
			"tt writes, then the project's .luarc.json and .emmyrc.json: it writes the " +
			"documentation of the annotated code, by default as an HTML site in ./output " +
			"(--output-format markdown or json for the others). " +
			passHelp,
		Example: `  $ tt emmylua doc -- --output doc/api --site-name "My application"
  $ tt emmylua doc -- --output-format json --output api.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run.withConfig(cmd, args, analyzer.Doc)
		},
	}
}

// newFmtCmd builds tt emmylua fmt.
func newFmtCmd(services sdk.Services) *cobra.Command {
	run := runner{services: services}

	return &cobra.Command{
		Use:   "fmt [PATH...] [-- LUAFMT_ARGS...]",
		Short: "Format the Lua code of the project",
		Long: "Run luafmt, the formatter of EmmyLua, on PATH (default the project). It " +
			"reads .luafmt.toml or luafmt.toml and .luafmtignore, not the " +
			"configuration of the analyzer. " + passHelp,
		Example: `  $ tt emmylua fmt -- --check
  $ tt emmylua fmt app -- --write`,
		RunE: run.format,
	}
}

// newLsCmd builds tt emmylua ls.
func newLsCmd(services sdk.Services) *cobra.Command {
	run := runner{services: services}

	return &cobra.Command{
		Use:   "ls [-- EMMYLUA_LS_ARGS...]",
		Short: "Run the language server, for an editor",
		Long: "Run emmylua_ls, the language server of EmmyLua, on the standard streams, " +
			"in place of tt: make it the command of the Lua server of the editor. It " +
			"reads the configuration tt writes (through " + analyzer.ConfigEnv + "), then " +
			"the project's .luarc.json, .emmyrc.json and .emmyrc.lua. " + passHelp,
		Example: `  # Neovim (nvim-lspconfig):
  #   vim.lsp.config('emmylua_ls', { cmd = { 'tt', 'emmylua', 'ls' } })
  $ tt emmylua ls -- --log-level debug --log-path /tmp/emmylua_ls.log`,
		Args: cobra.ArbitraryArgs,
		RunE: run.languageServer,
	}
}

// newConfigCmd builds tt emmylua config.
func newConfigCmd(services sdk.Services) *cobra.Command {
	run := runner{services: services}

	return &cobra.Command{
		Use:   "config",
		Short: "Write the configuration EmmyLua runs with, and print its path",
		Long: "Write the configuration tt gives EmmyLua - LuaJIT, the globals of " +
			"Tarantool, the stubs of the Tarantool the project runs on and the modules " +
			"its rocks install - to " + analyzer.ConfigDir + "/" + analyzer.ConfigFileName +
			" and print its path: for an editor that runs emmylua_ls itself, through " +
			analyzer.ConfigEnv + ". The other commands write it anew on every run.",
		Example: `  $ EMMYLUALS_CONFIG=$(tt emmylua config) code .`,
		Args:    cobra.NoArgs,
		RunE:    run.config,
	}
}

// paths are the paths a program of EmmyLua runs on.
type paths struct {
	// project is the project directory.
	project string
	// list are the paths given, or the project.
	list []string
}

// args splits args at -- into the paths, the project when there are none,
// and the arguments of the program.
func (r runner) args(cmd *cobra.Command, args []string) (paths, []string, error) {
	projectDir, err := r.services.Project().Dir()
	if err != nil {
		return paths{}, nil, err
	}

	list, pass := args, []string(nil)
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		list, pass = args[:dash], args[dash:]
	}

	if len(list) == 0 {
		list = []string{projectDir}
	}

	return paths{project: projectDir, list: list}, pass, nil
}

// withoutTypes ends the warnings of a project whose Tarantool gives EmmyLua
// no stubs.
const withoutTypes = "checking without the types of the Tarantool API"

// layout is what tt knows of the project of the command for EmmyLua. No
// Tarantool, one tt cannot choose and one without stubs are reported, and
// are not errors: the code is checked without the types of the Tarantool
// API.
func (r runner) layout() (analyzer.Layout, error) {
	projectDir, err := r.services.Project().Dir()
	if err != nil {
		return analyzer.Layout{}, err
	}

	logger := r.services.Log()
	stubs := ""

	tarantool, err := r.services.Tarantool()

	switch {
	case errors.Is(err, sdk.ErrNotFound):
		logger.Warn("no Tarantool found: " + withoutTypes)
	case err != nil:
		logger.Warn(err.Error() + ": " + withoutTypes)
	default:
		stubs = analyzer.FindStubs(tarantool.Path())
		if stubs == "" {
			logger.Warn(tarantool.Path() + " has no EmmyLua stubs (share/tarantool/emmylua, " +
				"Tarantool 3.9 and newer): " + withoutTypes)
		}
	}

	return analyzer.Layout{ProjectDir: projectDir, Stubs: stubs}, nil
}

// withConfig runs program, emmylua_check or emmylua_doc_cli, on the paths in
// args with the configuration tt writes and those of the project.
func (r runner) withConfig(cmd *cobra.Command, args []string, program analyzer.Program) error {
	paths, pass, err := r.args(cmd, args)
	if err != nil {
		return err
	}

	path, err := analyzer.ProgramPath(paths.project, program)
	if err != nil {
		return err
	}

	layout, err := r.layout()
	if err != nil {
		return err
	}

	config, err := layout.WriteConfig()
	if err != nil {
		return err
	}

	var argv []string

	for _, file := range layout.ConfigFiles(config) {
		argv = append(argv, "--config", file)
	}

	argv = append(argv, pass...)
	argv = append(argv, paths.list...)

	return r.runProgram(cmd.Context(), path, argv)
}

// format runs luafmt on the paths in args, without the configuration of the
// analyzer.
func (r runner) format(cmd *cobra.Command, args []string) error {
	paths, pass, err := r.args(cmd, args)
	if err != nil {
		return err
	}

	program, err := analyzer.ProgramPath(paths.project, analyzer.Format)
	if err != nil {
		return err
	}

	return r.runProgram(cmd.Context(), program, append(pass, paths.list...))
}

// config writes the configuration tt gives EmmyLua and prints its path.
func (r runner) config(_ *cobra.Command, _ []string) error {
	layout, err := r.layout()
	if err != nil {
		return err
	}

	path, err := layout.WriteConfig()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(r.services.Streams().IO().Out, path)

	return nil
}

// languageServer replaces tt with emmylua_ls, so that the editor talks to
// the server itself and stops it as it stops tt.
func (r runner) languageServer(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	if (dash < 0 && len(args) > 0) || dash > 0 {
		return fmt.Errorf("%w: give the arguments of emmylua_ls after --", errUsage)
	}

	layout, err := r.layout()
	if err != nil {
		return err
	}

	program, err := analyzer.ProgramPath(layout.ProjectDir, analyzer.LanguageServer)
	if err != nil {
		return err
	}

	config, err := layout.WriteConfig()
	if err != nil {
		return err
	}

	env := append(os.Environ(), analyzer.ConfigEnv+"="+config)
	argv := append([]string{program}, args...)

	err = syscall.Exec(program, argv, env)

	return fmt.Errorf("running %s: %w", program, err)
}

// runProgram runs program with args on the standard streams and fails with
// its exit status, which it has reported itself.
func (r runner) runProgram(ctx context.Context, program string, args []string) error {
	streams := r.services.Streams().IO()
	child := exec.CommandContext(ctx, program, args...)

	child.Stdin, child.Stdout, child.Stderr = streams.In, streams.Out, streams.Err

	r.services.Log().Debug("running " + program + " " + strings.Join(args, " "))

	err := child.Run()

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitErr.ExitCode()
		if code <= 0 {
			code = sdk.ExitFailure
		}

		return sdk.Reported(sdk.WithCode(code,
			fmt.Errorf("%s failed: %w", filepath.Base(program), err)))
	}

	if err != nil {
		return fmt.Errorf("running %s: %w", program, err)
	}

	return nil
}
