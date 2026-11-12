package emmylua

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/fetch"
)

// runner runs the programs of EmmyLua in the project of the command, or
// outside every project from the user cache: the values of the flags the
// commands share.
type runner struct {
	services sdk.Services
	// version and rockServer choose the programs outside a project.
	version, rockServer string
	// libraries are the directories --library adds to the libraries.
	libraries []string
}

// outsideHelp says how the commands run outside a project.
const outsideHelp = "Outside every project the programs come from the user cache, " +
	"fetched there once from the rock server (--rock-server, --version), and the " +
	"configuration tt writes is kept there too, not in the directory."

// Flags of the runner.
const (
	versionFlag    = "version"
	rockServerFlag = "rock-server"
	libraryFlag    = "library"
)

// addToolFlags declares --version and --rock-server on cmd: where the
// programs come from outside a project.
func (r *runner) addToolFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.version, versionFlag, analyzer.DefaultVersion,
		"outside a project, the EmmyLua release to run; a project runs the one its lock pins")
	cmd.Flags().StringVar(&r.rockServer, rockServerFlag, "",
		"outside a project, the rock server to fetch EmmyLua from: a URL, or a directory "+
			"of rocks such as tt emmylua rock packs (default the rock index of "+
			"tarantool-contrib/rocks-repo)")
}

// addLibraryFlag declares --library on cmd.
func (r *runner) addLibraryFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&r.libraries, libraryFlag, nil,
		"read the Lua modules of this directory as a library too; repeat for more")
}

// programFlag is a flag of a command that is a flag of the program it runs:
// given on the command line, it goes to the program as the program spells
// it, so the frequent ones need no --.
type programFlag struct {
	// name and shorthand are the flag's, as the program has them.
	name, shorthand string
	// usage is the help of the flag.
	usage string
	// boolean is a flag without a value; the others take a string.
	boolean bool
	// values are what the program takes for a string flag, offered by the
	// completion; the program checks them.
	values []string
}

// checkFlags are the flags of tt emmylua check that go to emmylua_check.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var checkFlags = []programFlag{
	{
		name: "warnings-as-errors", shorthand: "", boolean: true, values: nil,
		usage: "fail on a warning as on an error",
	},
	{
		name: "output-format", shorthand: "f", boolean: false,
		values: []string{"text", "json", "sarif", "github", "gitlab"},
		usage: "the format of the report: text, json, sarif, github or gitlab " +
			"(gitlab from EmmyLua 0.30)",
	},
	{
		name: "severity", shorthand: "", boolean: false,
		values: []string{"error", "warn", "info", "hint"},
		usage:  "report the diagnostics of this severity and above: error, warn, info or hint",
	},
}

// docFlags are the flags of tt emmylua doc that go to emmylua_doc_cli.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var docFlags = []programFlag{
	{
		name: "output", shorthand: "o", boolean: false, values: nil,
		usage: "the directory to write the documentation to, or a file for json " +
			"(default ./output)",
	},
	{
		name: "output-format", shorthand: "f", boolean: false,
		values: []string{"html", "markdown", "json"},
		usage:  "the format of the documentation: html, markdown or json (default html)",
	},
}

// fmtFlags are the flags of tt emmylua fmt that go to luafmt: its modes.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var fmtFlags = []programFlag{
	{
		name: fmtCheck, shorthand: "", boolean: true, values: nil,
		usage: "print how the files would change and fail if any would (the default)",
	},
	{
		name: "write", shorthand: "", boolean: true, values: nil,
		usage: "write the formatted files back",
	},
}

// fmtCheck is the mode of luafmt tt emmylua fmt runs it in unless told
// another.
const fmtCheck = "check"

// fmtActions are the flags of luafmt that choose what it does, its modes
// among them: given one after --, tt emmylua fmt adds no --check.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var fmtActions = []string{
	"--check", "--write", "--list-different", "--stdin", "--dump-default-config", "--init",
	"--explain-config", "--help", "-h", "--version", "-V",
}

// addProgramFlags declares flags on cmd, with the completion of their
// values.
func addProgramFlags(cmd *cobra.Command, flags []programFlag) {
	for _, flag := range flags {
		if flag.boolean {
			cmd.Flags().BoolP(flag.name, flag.shorthand, false, flag.usage)

			continue
		}

		cmd.Flags().StringP(flag.name, flag.shorthand, "", flag.usage)

		if flag.values != nil {
			_ = cmd.RegisterFlagCompletionFunc(flag.name,
				cobra.FixedCompletions(flag.values, cobra.ShellCompDirectiveNoFileComp))
		}
	}
}

// programArgs returns the flags of flags given to cmd as the program takes
// them: --name for a boolean set, --name and the value for a string.
func programArgs(cmd *cobra.Command, flags []programFlag) []string {
	var argv []string

	for _, flag := range flags {
		if !cmd.Flags().Changed(flag.name) {
			continue
		}

		value := cmd.Flags().Lookup(flag.name).Value.String()

		switch {
		case !flag.boolean:
			argv = append(argv, "--"+flag.name, value)
		case value == "true":
			argv = append(argv, "--"+flag.name)
		}
	}

	return argv
}

// newCheckCmd builds tt emmylua check.
func newCheckCmd(services sdk.Services) *cobra.Command {
	run := &runner{services: services, version: "", rockServer: "", libraries: nil}

	cmd := &cobra.Command{
		Use:   "check [PATH...] [flags] [-- EMMYLUA_CHECK_ARGS...]",
		Short: "Check the Lua code of the project",
		Long: "Run emmylua_check on PATH (default the project) with the configuration " +
			"tt writes, then the project's .luarc.json and .emmyrc.json. The command " +
			"fails as emmylua_check does: when it reports an error, or with " +
			"--warnings-as-errors a warning. --warnings-as-errors, -f/--output-format and " +
			"--severity go to emmylua_check as they are, its other flags, such as --ignore " +
			"and --output, after --. " + passHelp + "\n\n" +
			outsideHelp,
		Example: `$ tt emmylua check
  $ tt emmylua check app --warnings-as-errors -f json -- --output report.json

  # Outside a project, with the modules of a checkout of a rock.
  $ tt emmylua check --library ~/src/checks`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run.withConfig(cmd, args, analyzer.Check, checkFlags)
		},
	}

	addProgramFlags(cmd, checkFlags)
	run.addLibraryFlag(cmd)
	run.addToolFlags(cmd)

	return cmd
}

// newDocCmd builds tt emmylua doc.
func newDocCmd(services sdk.Services) *cobra.Command {
	run := &runner{services: services, version: "", rockServer: "", libraries: nil}

	cmd := &cobra.Command{
		Use:   "doc [PATH...] [flags] [-- EMMYLUA_DOC_CLI_ARGS...]",
		Short: "Generate the documentation of the Lua code of the project",
		Long: "Run emmylua_doc_cli on PATH (default the project) with the configuration " +
			"tt writes, then the project's .luarc.json and .emmyrc.json: it writes the " +
			"documentation of the annotated code, by default as an HTML site in ./output. " +
			"-o/--output and -f/--output-format go to emmylua_doc_cli as they are, its " +
			"other flags, such as --site-name, after --. " + passHelp + "\n\n" + outsideHelp,
		Example: `$ tt emmylua doc -o doc/api -- --site-name "My application"
  $ tt emmylua doc -f json -o api.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run.withConfig(cmd, args, analyzer.Doc, docFlags)
		},
	}

	addProgramFlags(cmd, docFlags)
	run.addLibraryFlag(cmd)
	run.addToolFlags(cmd)

	return cmd
}

// newFmtCmd builds tt emmylua fmt.
func newFmtCmd(services sdk.Services) *cobra.Command {
	run := &runner{services: services, version: "", rockServer: "", libraries: nil}

	cmd := &cobra.Command{
		Use:   "fmt [PATH...] [flags] [-- LUAFMT_ARGS...]",
		Short: "Format the Lua code of the project",
		Long: "Run luafmt, the formatter of EmmyLua, on PATH (default the project): " +
			"with --check, the default, it prints how the files would change and fails " +
			"if any would; with --write it rewrites them. It reads .luafmt.toml or " +
			"luafmt.toml and .luafmtignore, not the configuration of the analyzer. It " +
			"parses LuaJIT, as check does, unless [syntax] level of that configuration " +
			"or --level after -- names another: luafmt alone parses Lua 5.5.\n\n" +
			"With no PATH, in a git work tree, it formats the Lua files git sees " +
			"under the project: tracked, and untracked but not ignored by .gitignore, " +
			".git/info/exclude or core.excludesFile; luafmt runs in the root of the " +
			"project, given the list, and still skips the hidden files, those of " +
			".luafmtignore and --exclude. A submodule is left out, and so are _build/, " +
			"where tt builds the project out of its sources, .rocks/ and .tarantool/, " +
			"even when the project tracks them. Out of a git work tree, in a directory git " +
			"ignores, or without git, it walks the whole project but _build/, as check, " +
			"doc and ls do. A PATH is " +
			"formatted as given.\n\nThe " +
			"other flags of luafmt, such as --list-different, go after --, and one that " +
			"chooses what luafmt does takes the place of --check. " + passHelp + "\n\n" +
			"Outside every project luafmt comes from the user cache, fetched there once " +
			"from the rock server (--rock-server, --version).",
		Example: `$ tt emmylua fmt
  $ tt emmylua fmt app --write`,
		RunE: run.format,
	}

	addProgramFlags(cmd, fmtFlags)
	cmd.MarkFlagsMutuallyExclusive(fmtCheck, "write")
	run.addToolFlags(cmd)

	return cmd
}

// newLsCmd builds tt emmylua ls.
func newLsCmd(services sdk.Services) *cobra.Command {
	run := &runner{services: services, version: "", rockServer: "", libraries: nil}

	cmd := &cobra.Command{
		Use:   "ls [flags] [-- EMMYLUA_LS_ARGS...]",
		Short: "Run the language server, for an editor",
		Long: "Run emmylua_ls, the language server of EmmyLua, on the standard streams, " +
			"in place of tt: make it the command of the Lua server of the editor. It " +
			"reads the configuration tt writes (through " + analyzer.ConfigEnv + "), then " +
			"the project's .luarc.json, .emmyrc.json and .emmyrc.lua. " + passHelp + "\n\n" +
			outsideHelp + " So an editor can run tt emmylua ls in any folder.",
		Example: `# Neovim (nvim-lspconfig):
  #   vim.lsp.config('emmylua_ls', { cmd = { 'tt', 'emmylua', 'ls' } })
  $ tt emmylua ls -- --log-level debug --log-path /tmp/emmylua_ls.log`,
		Args: cobra.ArbitraryArgs,
		RunE: run.languageServer,
	}

	run.addLibraryFlag(cmd)
	run.addToolFlags(cmd)

	return cmd
}

// newConfigCmd builds tt emmylua config.
func newConfigCmd(services sdk.Services) *cobra.Command {
	run := &runner{services: services, version: "", rockServer: "", libraries: nil}

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Write the configuration EmmyLua runs with, and print its path",
		Long: "Write the configuration tt gives EmmyLua - LuaJIT, the globals of " +
			"Tarantool, the stubs of the Tarantool the project runs on and the modules " +
			"its rocks install, with .rocks/, .tarantool/ and the build tree _build/ " +
			"left out of the code of the project, and the project's own package, which " +
			"tt package build installs into .rocks/, out of the libraries - to " +
			analyzer.ConfigDir + "/" + analyzer.ConfigFileName +
			" of the project and print its path: for an editor that runs emmylua_ls " +
			"itself, through " + analyzer.ConfigEnv + ". The other commands write it anew " +
			"on every run.\n\nOutside every project it is written into the user cache, " +
			"one file for each directory, with the modules of .rocks/ of the directory, " +
			"and the directory is left as it is.",
		Example: `$ EMMYLUALS_CONFIG=$(tt emmylua config) code .`,
		Args:    cobra.NoArgs,
		RunE:    run.config,
	}

	run.addLibraryFlag(cmd)

	return cmd
}

// workspace returns where cmd runs, as findWorkspace finds it, and refuses
// the flags that choose the programs outside a project in one: a project
// runs the programs its lock pins. Run below the root of a project, it says
// which project it works on, as the project commands of tt do.
func (r *runner) workspace(cmd *cobra.Command) (workspace, error) {
	found, err := findWorkspace(r.services)
	if err != nil {
		return workspace{}, err
	}

	if !found.project {
		return found, nil
	}

	for _, name := range []string{versionFlag, rockServerFlag} {
		if flag := cmd.Flags().Lookup(name); flag != nil && flag.Changed {
			return workspace{}, fmt.Errorf("%w: --%s chooses EmmyLua outside a project; "+
				"the project %s runs the one its lock pins (tt emmylua prepare sets it)",
				errUsage, name, found.dir)
		}
	}

	if found.dir != found.workDir {
		r.services.Log().Info("working on the project in " + found.dir)
	}

	return found, nil
}

// split splits args at -- into the paths, the root of the workspace when
// there are none, and the arguments of the program.
func split(cmd *cobra.Command, args []string, found workspace) ([]string, []string) {
	list, pass := args, []string(nil)
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		list, pass = args[:dash], args[dash:]
	}

	if len(list) == 0 {
		list = []string{found.dir}
	}

	return list, pass
}

// programPath returns the path of program: in the rocks of a project, and
// outside every project in the user cache, which the first run fetches it
// into from the rock server of --rock-server at --version.
func (r *runner) programPath(
	ctx context.Context, found workspace, program analyzer.Program,
) (string, error) {
	if found.project {
		return analyzer.ProgramPath(found.dir, program)
	}

	dir, err := analyzer.Tools(ctx, analyzer.ToolsOptions{
		CacheDir:   analyzer.DefaultCacheDir(),
		RockServer: r.rockServer,
		WorkingDir: found.workDir,
		Version:    r.version,
		Arch:       "",
		Client:     fetch.Client{HTTP: nil, Limit: 0},
		Progress:   r.services.Streams().IO().Err,
		Log:        r.services.Log(),
	})
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, string(program)), nil
}

// libraryDirs returns the directories of --library, absolute: a relative one
// is taken from the directory of the command. Each must be a directory.
func (r *runner) libraryDirs(found workspace) ([]string, error) {
	dirs := make([]string, 0, len(r.libraries))

	for _, library := range r.libraries {
		dir := library
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(found.workDir, dir)
		}

		info, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("--%s %s: %w", libraryFlag, library, err)
		}

		if !info.IsDir() {
			return nil, fmt.Errorf("--%s %s: %w", libraryFlag, library, errNotADirectory)
		}

		dirs = append(dirs, filepath.Clean(dir))
	}

	return dirs, nil
}

// withoutTypes ends the warnings of a project whose Tarantool gives EmmyLua
// no stubs.
const withoutTypes = "checking without the types of the Tarantool API"

// layout is what tt knows of the workspace of the command for EmmyLua, with
// the libraries of --library, and where the configuration goes: into the
// project, or outside every project into the user cache. No Tarantool, one tt
// cannot choose and one without stubs are reported, and are not errors: the
// code is checked without the types of the Tarantool API. Neither is a rock
// tree tt cannot read for the project's own package (analyzer.OwnModules):
// the package is then left in the libraries.
func (r *runner) layout(ctx context.Context, found workspace) (analyzer.Layout, error) {
	extra, err := r.libraryDirs(found)
	if err != nil {
		return analyzer.Layout{}, err
	}

	logger := r.services.Log()
	path := ""

	var own []string

	if found.project {
		own, err = analyzer.OwnModules(ctx, found.dir)
		if err != nil {
			logger.Warn(err.Error() + ": the project's own package is read as a library too")
		}
	} else {
		cache := analyzer.DefaultCacheDir()
		if cache == "" {
			return analyzer.Layout{}, fmt.Errorf("%w for the configuration of %s: run "+
				"tt emmylua in a project", analyzer.ErrNoCache, found.dir)
		}

		path = analyzer.GlobalConfigPath(cache, found.dir)
	}

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
			logger.Warn(tarantoolName(tarantool) + " has no EmmyLua stubs: " + withoutTypes)
			logger.Debug("no " + analyzer.StubsDir + " next to " + tarantool.Path() +
				" or the file it links to: Tarantool 3.9 and newer install the stubs there")
		}
	}

	return analyzer.Layout{
		ProjectDir: found.dir, Stubs: stubs, Extra: extra, Path: path, Own: own,
	}, nil
}

// tarantoolName names tarantool by its version, Tarantool 3.8.1, or by its
// path when the version cannot be told.
func tarantoolName(tarantool sdk.Tarantool) string {
	version, err := tarantool.Version()
	if err != nil {
		return tarantool.Path()
	}

	return fmt.Sprintf("Tarantool %d.%d.%d", version.Major, version.Minor, version.Patch)
}

// withConfig runs program, emmylua_check or emmylua_doc_cli, on the paths in
// args with the configuration tt writes and those of the project, and the
// flags of flags given to cmd.
func (r *runner) withConfig(
	cmd *cobra.Command, args []string, program analyzer.Program, flags []programFlag,
) error {
	found, err := r.workspace(cmd)
	if err != nil {
		return err
	}

	list, pass := split(cmd, args, found)

	layout, err := r.layout(cmd.Context(), found)
	if err != nil {
		return err
	}

	path, err := r.programPath(cmd.Context(), found, program)
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

	argv = append(argv, programArgs(cmd, flags)...)
	argv = append(argv, pass...)
	argv = append(argv, list...)

	return r.runProgram(cmd.Context(), "", path, argv)
}

// format runs luafmt on the paths in args, without the configuration of the
// analyzer, in the mode its flags give: --check unless told another. It
// parses the Lua the analyzer is told of, LuaJIT, unless the arguments of
// luafmt or its configuration choose another (givesLevel). With no path, in
// a work tree of git, it formats the Lua files git sees (formatFiles); out
// of one, it walks the whole workspace and leaves out the build tree, as the
// configuration of the analyzer does: luafmt walks every directory that is
// not hidden, _build/ too.
func (r *runner) format(cmd *cobra.Command, args []string) error {
	found, err := r.workspace(cmd)
	if err != nil {
		return err
	}

	list, pass := split(cmd, args, found)
	// No path precedes --: split took the workspace for them.
	whole := len(args) == len(pass)

	program, err := r.programPath(cmd.Context(), found, analyzer.Format)
	if err != nil {
		return err
	}

	mode := programArgs(cmd, fmtFlags)
	if len(mode) == 0 && !choosesAction(pass) {
		mode = []string{"--" + fmtCheck}
	}

	if whole && formatsFiles(pass) {
		files, ok, err := r.gitLuaFiles(cmd.Context(), found.dir, walkOf(pass))
		if err != nil {
			return err
		}

		if ok {
			return r.formatFiles(cmd.Context(), program, found, mode, pass, files)
		}
	}

	argv := mode

	if givesLevel(found, list, pass) {
		argv = append(argv, "--level", analyzer.FormatLevel)
	}

	// luafmt matches the pattern against the path of a file relative to the
	// path it was given, the root of the workspace here.
	if whole {
		argv = append(argv, "--exclude", analyzer.BuildDirGlob)
	}

	argv = append(argv, pass...)
	argv = append(argv, list...)

	return r.runProgram(cmd.Context(), "", program, argv)
}

// fmtFileActions are the flags of fmtActions that format files, the modes of
// luafmt: the others print, read stdin or write a configuration, and take
// no list of files.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var fmtFileActions = []string{"--check", "--write", "--list-different"}

// formatsFiles reports whether luafmt with pass, the arguments after --,
// formats files: pass chooses no action of fmtActions but its modes.
func formatsFiles(pass []string) bool {
	for _, arg := range pass {
		flag, _, _ := strings.Cut(arg, "=")
		if slices.Contains(fmtActions, flag) && !slices.Contains(fmtFileActions, flag) {
			return false
		}
	}

	return true
}

// fmtArgBudget is how many bytes of paths one run of luafmt is given at
// most: a project with more Lua files is formatted in more runs, so that the
// arguments stay well under the limit of the system on them (ARG_MAX, 1 MiB
// on macOS, with the environment). A variable, so that the tests can make
// it small.
//
//nolint:gochecknoglobals // A constant the tests change.
var fmtArgBudget = 256 << 10

// formatFiles runs luafmt in mode, with pass, on files, the Lua files git
// sees, relative to the root of the workspace. luafmt runs in the root, so
// that it matches the patterns of .luafmtignore and --exclude against the
// path of a file relative to it, as when it walks the root; a relative
// --config among pass is made absolute from the directory of the command,
// which it was taken from. A file whose configuration sets the level runs
// without --level, the others with it (analyzer.SplitFormatLevel), unless
// pass gives the level or the configuration, which decides for all. The
// files go in runs of fmtArgBudget at most, every run is made, and the
// failure that exits with the highest code, luafmt's 2 for an error over 1
// for a file it would change, is the result. git seeing no Lua file is no
// failure: there is nothing to format.
func (r *runner) formatFiles(
	ctx context.Context, program string, found workspace, mode, pass, files []string,
) error {
	if len(files) == 0 {
		r.services.Log().Info("git sees no Lua files in " + found.dir + ": nothing to format")

		return nil
	}

	pass = absoluteConfig(pass, found.workDir)

	var worst error

	for _, group := range levelGroups(found, pass, files) {
		for _, chunk := range chunks(group.files, fmtArgBudget) {
			argv := slices.Concat(mode, group.level, pass, chunk)

			err := r.runProgram(ctx, found.dir, program, argv)
			if err == nil {
				continue
			}

			if !sdk.IsReported(err) {
				return err
			}

			if worst == nil || sdk.ExitCode(err) > sdk.ExitCode(worst) {
				worst = err
			}
		}
	}

	return worst
}

// levelGroup is files luafmt runs on with the arguments level: --level
// analyzer.FormatLevel, or none.
type levelGroup struct {
	level, files []string
}

// levelGroups splits files into the runs of formatFiles: with --level
// analyzer.FormatLevel, and without, those whose configuration sets the
// level. --level among pass leaves it to the user, and --config, the
// configuration of every file, decides for all.
func levelGroups(found workspace, pass, files []string) []levelGroup {
	level := []string{"--level", analyzer.FormatLevel}

	if _, given := flagValue(pass, "--level"); given {
		return []levelGroup{{level: nil, files: files}}
	}

	if config, given := flagValue(pass, "--config"); given {
		if analyzer.FormatLevelConfigured(found.workDir, nil, config) {
			return []levelGroup{{level: nil, files: files}}
		}

		return []levelGroup{{level: level, files: files}}
	}

	set, unset := analyzer.SplitFormatLevel(found.dir, files)

	var groups []levelGroup

	if len(unset) > 0 {
		groups = append(groups, levelGroup{level: level, files: unset})
	}

	if len(set) > 0 {
		groups = append(groups, levelGroup{level: nil, files: set})
	}

	return groups
}

// absoluteConfig returns pass, the arguments of luafmt, with the file of a
// relative --config taken from dir.
func absoluteConfig(pass []string, dir string) []string {
	pass = slices.Clone(pass)

	for i, arg := range pass {
		switch {
		case arg == "--config" && i+1 < len(pass):
			pass[i+1] = absFrom(dir, pass[i+1])
		case strings.HasPrefix(arg, "--config="):
			pass[i] = "--config=" + absFrom(dir, strings.TrimPrefix(arg, "--config="))
		}
	}

	return pass
}

// absFrom returns path, absolute: a relative one is taken from dir.
func absFrom(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(dir, path)
}

// chunks splits files into runs whose paths take budget bytes at most, a
// path and its terminating NUL, each run with one file at least.
func chunks(files []string, budget int) [][]string {
	var (
		runs [][]string
		size int
	)

	start := 0

	for i, file := range files {
		if i > start && size+len(file)+1 > budget {
			runs = append(runs, files[start:i])
			start, size = i, 0
		}

		size += len(file) + 1
	}

	return append(runs, files[start:])
}

// givesLevel reports whether tt emmylua fmt gives luafmt the Lua of
// Tarantool, analyzer.FormatLevel, to format list with pass, the arguments
// after --: unless pass chooses the level itself, or the configuration
// luafmt reads does, its own --config among pass or the one luafmt finds
// for the paths. luafmt takes --level over [syntax] level of the
// configuration, so passing it there would override the user's choice.
func givesLevel(found workspace, list, pass []string) bool {
	if _, given := flagValue(pass, "--level"); given {
		return false
	}

	config, _ := flagValue(pass, "--config")

	return !analyzer.FormatLevelConfigured(found.workDir, list, config)
}

// flagValue returns the value of the flag name in args, the arguments of
// luafmt, given as name value or name=value, and whether args hold it.
func flagValue(args []string, name string) (string, bool) {
	for i, arg := range args {
		if arg == name {
			if i+1 < len(args) {
				return args[i+1], true
			}

			return "", true
		}

		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value, true
		}
	}

	return "", false
}

// choosesAction reports whether args, the arguments of luafmt, hold a flag
// that chooses what it does (fmtActions), alone or with its =value.
func choosesAction(args []string) bool {
	for _, arg := range args {
		flag, _, _ := strings.Cut(arg, "=")
		if slices.Contains(fmtActions, flag) {
			return true
		}
	}

	return false
}

// config writes the configuration tt gives EmmyLua and prints its path. It
// needs no program, so nothing is fetched outside a project.
func (r *runner) config(cmd *cobra.Command, _ []string) error {
	found, err := r.workspace(cmd)
	if err != nil {
		return err
	}

	layout, err := r.layout(cmd.Context(), found)
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
func (r *runner) languageServer(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	if (dash < 0 && len(args) > 0) || dash > 0 {
		return fmt.Errorf("%w: give the arguments of emmylua_ls after --", errUsage)
	}

	found, err := r.workspace(cmd)
	if err != nil {
		return err
	}

	layout, err := r.layout(cmd.Context(), found)
	if err != nil {
		return err
	}

	program, err := r.programPath(cmd.Context(), found, analyzer.LanguageServer)
	if err != nil {
		return err
	}

	config, err := layout.WriteConfig()
	if err != nil {
		return err
	}

	env := append(os.Environ(), analyzer.ConfigEnv+"="+config)
	argv := append([]string{program}, args...)

	// Started in a terminal, the server would wait in silence for an editor
	// that never comes.
	streams := r.services.Streams().IO()
	if isTerminal(streams.In) {
		_, _ = fmt.Fprintln(streams.Err, lsHint)
	}

	err = syscall.Exec(program, argv, env)

	return fmt.Errorf("running %s: %w", program, err)
}

// lsHint is what tt emmylua ls says before the server starts in a terminal.
const lsHint = "emmylua_ls is a language server and waits for an editor on stdin: " +
	"make tt emmylua ls the command of the editor's Lua server (see tt emmylua ls --help). " +
	"Ctrl+C stops it."

// isTerminal reports whether in is a terminal.
func isTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)

	return ok && term.IsTerminal(int(file.Fd()))
}

// runProgram runs program with args in dir, that of tt when it is empty, on
// the standard streams and fails with its exit status, which it has
// reported itself.
func (r *runner) runProgram(ctx context.Context, dir, program string, args []string) error {
	streams := r.services.Streams().IO()
	child := exec.CommandContext(ctx, program, args...)

	child.Dir = dir
	child.Stdin, child.Stdout, child.Stderr = streams.In, streams.Out, streams.Err

	if dir != "" {
		r.services.Log().Debug("running in " + dir)
	}

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
