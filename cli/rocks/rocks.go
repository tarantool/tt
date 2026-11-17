package rocks

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/cmakepolicy"
)

//go:embed completions/*
var EmbedCompletions embed.FS

const (
	repoRocksPathEnvVarName = "TT_CLI_REPO_ROCKS"
	serverFlag              = "--server"
	onlyServerFlag          = "--only-server"
)

// addLuarocksRepoOpts adds the rocks repository repo to the luarocks command
// line as a --server option, unless repo is empty or the command line names
// --only-server. Return updated args slice.
func addLuarocksRepoOpts(repo string, args []string) []string {
	// Make sure there is no --only-server option is specified.
	for _, opt := range args {
		if opt == onlyServerFlag || strings.HasPrefix(opt, onlyServerFlag+"=") {
			return args // If --only-server is specified, no need to add --server option.
		}
	}

	if repo != "" {
		isServerSet := false

		for argIdx, opt := range args {
			if opt == serverFlag {
				isServerSet = true
				args[argIdx+1] = args[argIdx+1] + " " + repo
			} else if strings.HasPrefix(opt, "--server=") {
				isServerSet = true

				args[argIdx] += " " + repo
			}
		}

		if !isServerSet {
			args = append(args, "--server", repo)
		}
	}

	return args
}

// GetTarantoolPrefix returns the install prefix of the tarantool of cli, the
// directory whose include/tarantool holds its headers (ResolveTarantoolPrefix).
func GetTarantoolPrefix(cli *cmdcontext.CliCtx) (string, error) {
	prefix, err := ResolveTarantoolPrefix(cli)
	if err != nil {
		return "", err
	}

	return prefix.Dir, nil
}

// warnedPrefixes are the lines on a prefix already shown: the warnings
// WarnHeaders showed and the debug line of ResolveTarantoolPrefix on the
// prefix it found. A command that compiles or resolves the prefix more than
// once, tt emmylua prepare installing the rocks say, shows each once.
var warnedPrefixes sync.Map

// WarnHeaders shows the warnings of prefix - its headers are of another
// version than the tarantool - once per command. A command shows them when it
// is about to compile against the headers, and only then: one that builds
// nothing has nothing a mismatch could break.
func WarnHeaders(prefix tarantool.Prefix) {
	for _, warning := range prefix.Warnings {
		if _, shown := warnedPrefixes.LoadOrStore(warning, true); !shown {
			log.Warn(warning)
		}
	}
}

// ResolveTarantoolPrefix resolves the install prefix of the tarantool of cli
// by the one rule of tarantool.ResolvePrefix: the prefix of the project
// scope, TT_CLI_TARANTOOL_PREFIX, the directories of the executable, then its
// CMAKE_INSTALL_PREFIX. Its warnings - headers of another version - are not
// shown: WarnHeaders shows them for a command that compiles. The prefix it
// found is said with -V, once per command.
func ResolveTarantoolPrefix(cli *cmdcontext.CliCtx) (tarantool.Prefix, error) {
	prefix, err := tarantool.ResolvePrefix(tarantool.PrefixQuery{
		Project:    cli.TarantoolPrefix,
		Env:        os.Getenv(tarantool.PrefixEnv),
		Executable: cli.TarantoolCli.Executable,
		System:     "",
	})
	if err != nil {
		return tarantool.Prefix{}, err
	}

	found := fmt.Sprintf("Tarantool prefix path: %q (headers: %q, looked in %q)",
		prefix.Dir, prefix.IncludeDir, prefix.Tried)
	if _, shown := warnedPrefixes.LoadOrStore(found, true); !shown {
		log.Debug(found)
	}

	return prefix, nil
}

// Exec runs a LuaRocks command through the embedded LuaRocks engine
// (go-luarocks). All args are passed verbatim to the upstream LuaRocks CLI
// dispatcher, which parses them and prints its own output. This is the
// transitional `tt rocks` escape-hatch; the new manifest commands go through
// cli/manifest/rocks instead.
//
// servers replace the rock servers LuaRocks queries by default, as the server
// list of tt registry replaces its defaults; an empty one leaves the defaults
// of LuaRocks in place. The repository TT_CLI_REPO_ROCKS names is added on top
// of them with --server, which LuaRocks queries first.
//
// LuaRocks reads manifests only, so a server that publishes nothing but a
// tt-rocks-index.json is of no use to it (see indexProbe): one of servers or
// TT_CLI_REPO_ROCKS is left out with a warning, and one args name with
// --server or --only-server fails the command with ErrIndexOnlyServer.
//
// LuaRocks builds nothing of the source rock of a tt package: a rock the
// command installed so is removed again, and the command fails with
// ErrEmptySrcRock (checkSrcRocks).
func Exec(cmdCtx *cmdcontext.CmdCtx, servers, args []string) error {
	ctx := context.Background()
	probe := newIndexProbe()

	err := checkNamedServers(ctx, probe, args)
	if err != nil {
		return err
	}

	kept := keepManifestServers(ctx, probe, servers)
	if len(servers) > 0 && len(kept) == 0 {
		// An empty list is no list to LuaRocks: say what it falls back to.
		log.Warn("no configured rock server is left for tt rocks: LuaRocks queries its own")
	}

	servers = kept

	repo := os.Getenv(repoRocksPathEnvVarName)
	if repo != "" && len(keepManifestServers(ctx, probe, []string{repo})) == 0 {
		repo = ""
	}

	args = addLuarocksRepoOpts(repo, args)

	version, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return err
	}

	prefix, err := ResolveTarantoolPrefix(&cmdCtx.Cli)
	if err != nil {
		return err
	}

	if buildingCommands[luarocksCommand(args)] {
		WarnHeaders(prefix)
	}

	tarantoolPrefixDir := prefix.Dir

	tarantoolIncludeDir, err := util.JoinAbspath(tarantoolPrefixDir, "include", "tarantool")
	if err != nil {
		return err
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get the working directory: %w", err)
	}

	// LuaRocks builtin/cmake rocks (and tt's own cmake hand-off in
	// build/local.go) read these at compile time via extra/hardcoded.lua's
	// contract. The library never sets process env; only this shim does,
	// mirroring the previous embedded-luarocks behavior.
	_ = os.Setenv("TT_CLI_TARANTOOL_VERSION", version.Str)
	_ = os.Setenv("TT_CLI_TARANTOOL_PREFIX", tarantoolPrefixDir)
	_ = os.Setenv("TT_CLI_TARANTOOL_INCLUDE", tarantoolIncludeDir)
	_ = os.Setenv("TARANTOOL_DIR", filepath.Dir(tarantoolIncludeDir))
	_ = os.Setenv("TT_CLI_TARANTOOL_PATH", filepath.Dir(cmdCtx.Cli.TarantoolCli.Executable))

	// A cmake rock may ask for a CMake older than CMake 4 configures.
	err = cmakepolicy.Setenv()
	if err != nil {
		return err
	}

	// A rock build that runs tarantool runs this one, the tarantool of the
	// project with no .env/activate.sh sourced included.
	err = tarantool.PrependPath(cmdCtx.Cli.TarantoolCli.Executable, tarantoolPrefixDir)
	if err != nil {
		return err
	}

	// LuaRocks is given headers that are none when there are none, so that
	// a rock of Lua alone installs without them.
	luarocksIncludeDir, removeStub, err := includeDirOf(prefix, args)
	if err != nil {
		return err
	}
	defer removeStub()

	cfg := luarocks.Config{
		Tree:       filepath.Join(workingDir, ".rocks"),
		WorkingDir: workingDir,
		Servers:    servers,
		Tarantool: luarocks.TarantoolConfig{
			Executable: cmdCtx.Cli.TarantoolCli.Executable,
			Prefix:     tarantoolPrefixDir,
			IncludeDir: luarocksIncludeDir,
			Version:    version.Str,
		},
	}

	args = pinRelease(ctx, searchRocks(cfg), servers, args)

	rocksClient, err := client.New(cfg, client.WithBackend(client.BackendLua))
	if err != nil {
		return fmt.Errorf("failed to create the luarocks client: %w", err)
	}

	root := rockTree(workingDir, args)
	before := inspectBefore(ctx, root)

	// Match the historical progname: LuaRocks prints "<argv0> rocks" (and
	// "<argv0> rocks admin") in its usage, the wrapper appending " admin" for
	// the admin sub-CLI.
	zip, runArgs := newZipProbe(args)
	defer zip.remove()

	err = rocksClient.Exec(ctx, os.Args[0]+" rocks", runArgs)
	if err != nil {
		err = fmt.Errorf("failed to run luarocks: %w", zip.explain(err))

		return explainMissingHeaders(err, prefix, args)
	}

	return checkSrcRocks(ctx, root, before)
}

// ErrNoHeaders says that a LuaRocks run failed with no headers of Tarantool
// to compile a rock of C against.
var ErrNoHeaders = errors.New("tarantool development headers not found")

// buildingCommands are the LuaRocks commands that may compile a rock.
var buildingCommands = map[string]bool{"install": true, "build": true, "make": true}

// explainMissingHeaders adds ErrNoHeaders, with where they were looked for,
// to err, the failure of a LuaRocks run of args, when args may compile a rock
// and prefix has no headers: a rock of C then fails to find lua.h or
// module.h, and says no more.
func explainMissingHeaders(err error, prefix tarantool.Prefix, args []string) error {
	hint := prefix.MissingHeaders()
	if hint == "" || !buildingCommands[luarocksCommand(args)] {
		return err
	}

	return fmt.Errorf("%w (%w: %s)", err, ErrNoHeaders, hint)
}

// ErrNoZip says why LuaRocks failed to pack a rock on a system with no zip.
var ErrNoZip = errors.New("LuaRocks packs a rock with the zip program, " +
	"which is not found in PATH: install zip")

// zipTool is the program LuaRocks packs a rock with. luarocks.tools.zip, which
// fs.zip would take first, needs lua-zlib, which the engine of go-luarocks
// does not have, so fs.zip runs zip (fs/unix/tools.lua).
const zipTool = "zip"

// luarocksValueOptions are the global options of LuaRocks that take the
// argument after them as their value (cmd.lua).
var luarocksValueOptions = map[string]bool{
	serverFlag: true, onlyServerFlag: true, "--only-sources": true,
	"--namespace": true, "--lua-dir": true, "--lua-version": true,
	treeFlag: true, "--timeout": true, "--project-tree": true,
}

// luarocksCommand returns the LuaRocks command args run: the first argument
// that is neither an option, nor the value of one, nor a VAR=VALUE
// assignment. It is empty when args name none.
func luarocksCommand(args []string) string {
	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]

		switch {
		case luarocksValueOptions[arg]:
			idx++
		case strings.HasPrefix(arg, "-"), strings.Contains(arg, "="):
		default:
			return arg
		}
	}

	return ""
}

// zipProbe stands in for the zip LuaRocks would pack a rock with on a system
// that has none, to tell a run that failed for want of zip from one that
// failed before it needed one: pack of a rock that is not installed fails
// with "cannot find package", and the hint that zip is missing would be
// wrong there. LuaRocks 3.9.2 does not say why it failed in the first case:
// pack_installed_rock, which pack <rock name> and --pack-binary-rock run,
// says "Failed packing <rock>" and drops the reason fs.zip gives, and tt does
// not see what LuaRocks prints.
//
// The probe is a program that records that it ran and fails, which the run
// is given as LuaRocks' ZIP variable. LuaRocks runs it where it would run
// zip, and fails as it does with no zip, "Failed packing"; the record says
// that it got there.
type zipProbe struct {
	// dir holds the program and its record; the probe removes it.
	dir string
	// assignment is the ZIP=<program> argument the run is given.
	assignment string
	// record is the file the program creates when it runs.
	record string
}

// zipProbeScript is the program of the probe: it creates the file it is
// given and fails.
const zipProbeScript = "#!/bin/sh\n: > '%s'\nexit 1\n"

// zipProbeMode is the mode of the program of the probe.
const zipProbeMode = 0o700

// newZipProbe returns the probe for a LuaRocks run of args, and the args to
// run with it, when args pack a rock, zip is not in PATH and args assign no
// ZIP variable; otherwise nil and args as they are. A probe that cannot be
// made is none: the run goes as it would, without the hint.
func newZipProbe(args []string) (*zipProbe, []string) {
	packs := luarocksCommand(args) == "pack"

	for _, arg := range args {
		if arg == "--pack-binary-rock" {
			packs = true
		}

		if strings.HasPrefix(arg, "ZIP=") {
			return nil, args
		}
	}

	if !packs {
		return nil, args
	}

	_, lookErr := exec.LookPath(zipTool)
	if lookErr == nil {
		return nil, args
	}

	dir, err := os.MkdirTemp("", "tt-zip-")
	if err != nil {
		return nil, args
	}

	probe := &zipProbe{
		dir:        dir,
		assignment: "ZIP=" + filepath.Join(dir, zipTool),
		record:     filepath.Join(dir, "ran"),
	}

	// LuaRocks runs ZIP through the shell unquoted, so a directory the shell
	// would split, or the quotes of the script would not hold, makes none.
	if strings.ContainsAny(dir, " \t\n'\"\\$`;&|<>()*?[]#~") {
		probe.remove()

		return nil, args
	}

	err = os.WriteFile(filepath.Join(dir, zipTool),
		fmt.Appendf(nil, zipProbeScript, probe.record), zipProbeMode)
	if err != nil {
		probe.remove()

		return nil, args
	}

	return probe, append([]string{probe.assignment}, args...)
}

// remove removes the program of the probe and its record. A nil probe has
// none.
func (p *zipProbe) remove() {
	if p != nil {
		_ = os.RemoveAll(p.dir)
	}
}

// explain makes err, the failure of the run the probe was given to, read as
// the failure of the args the user gave, with no ZIP assignment of the
// probe, and adds ErrNoZip when LuaRocks ran the probe: it failed for want
// of zip. A nil probe leaves err as it is.
func (p *zipProbe) explain(err error) error {
	if p == nil {
		return err
	}

	err = &probedError{err: err, msg: strings.Replace(err.Error(), p.assignment+" ", "", 1)}

	_, statErr := os.Stat(p.record)
	if statErr != nil {
		return err
	}

	return fmt.Errorf("%w: %w", err, ErrNoZip)
}

// probedError is the failure of a run given a probe, its message without the
// ZIP assignment of the probe.
type probedError struct {
	err error
	msg string
}

func (e *probedError) Error() string { return e.msg }

func (e *probedError) Unwrap() error { return e.err }
