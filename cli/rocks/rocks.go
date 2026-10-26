package rocks

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errPrefixPathExpectedMoreData = errors.New("failed to get prefix path: expected more data")
	errPrefixPathRegexpMismatch   = errors.New("failed to get prefix path: regexp does not match")
)

//go:embed completions/*
var EmbedCompletions embed.FS

const (
	repoRocksPathEnvVarName   = "TT_CLI_REPO_ROCKS"
	tarantoolPrefixEnvVarName = "TT_CLI_TARANTOOL_PREFIX"
	tarantoolDefaultPrefixDir = "/usr"
	minVersionOutputLines     = 3
)

// addLuarocksRepoOpts adds the rocks repository repo to the luarocks command
// line as a --server option, unless repo is empty or the command line names
// --only-server. Return updated args slice.
func addLuarocksRepoOpts(repo string, args []string) []string {
	// Make sure there is no --only-server option is specified.
	for _, opt := range args {
		if opt == "--only-server" || strings.HasPrefix(opt, "--only-server=") {
			return args // If --only-server is specified, no need to add --server option.
		}
	}

	if repo != "" {
		isServerSet := false

		for argIdx, opt := range args {
			if opt == "--server" {
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

// GetTarantoolPrefix returns tarantool installation prefix.
func GetTarantoolPrefix(cli *cmdcontext.CliCtx, cliOpts *config.CliOpts) (string, error) {
	if cli.IsTarantoolBinFromRepo {
		prefixDir, err := util.JoinAbspath(cliOpts.Env.IncludeDir)
		if err != nil {
			return "", err
		}

		log.Debugf("Tarantool prefix path: %q", prefixDir)

		return prefixDir, nil
	}

	if prefixPathFromEnv := os.Getenv(tarantoolPrefixEnvVarName); prefixPathFromEnv != "" {
		log.Debugf("Tarantool prefix path: %q", prefixPathFromEnv)

		return prefixPathFromEnv, nil
	}

	output, err := exec.CommandContext(
		context.Background(), cli.TarantoolCli.Executable, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tarantool version: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < minVersionOutputLines {
		return "", errPrefixPathExpectedMoreData
	}

	re := regexp.MustCompile(`^.*\s-DCMAKE_INSTALL_PREFIX=(?P<prefix>\/.*)\s.*$`)
	matches := util.FindNamedMatches(re, lines[2])

	if len(matches) == 0 {
		return "", errPrefixPathRegexpMismatch
	}

	prefixDir := matches["prefix"]

	if !util.IsDir(prefixDir) {
		log.Debugf("%q does not exist or is not a directory. Using default: %q",
			prefixDir, tarantoolDefaultPrefixDir)

		prefixDir = tarantoolDefaultPrefixDir
	}

	log.Debugf("Tarantool prefix path: %q", prefixDir)

	return prefixDir, nil
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
func Exec(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, servers, args []string) error {
	args = addLuarocksRepoOpts(os.Getenv(repoRocksPathEnvVarName), args)

	version, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return err
	}

	tarantoolPrefixDir, err := GetTarantoolPrefix(&cmdCtx.Cli, cliOpts)
	if err != nil {
		return err
	}

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

	cfg := luarocks.Config{
		Tree:       filepath.Join(workingDir, ".rocks"),
		WorkingDir: workingDir,
		Servers:    servers,
		Tarantool: luarocks.TarantoolConfig{
			Executable: cmdCtx.Cli.TarantoolCli.Executable,
			Prefix:     tarantoolPrefixDir,
			IncludeDir: tarantoolIncludeDir,
			Version:    version.Str,
		},
	}

	rocksClient, err := client.New(cfg, client.WithBackend(client.BackendLua))
	if err != nil {
		return fmt.Errorf("failed to create the luarocks client: %w", err)
	}

	// Match the historical progname: LuaRocks prints "<argv0> rocks" (and
	// "<argv0> rocks admin") in its usage, the wrapper appending " admin" for
	// the admin sub-CLI.
	err = rocksClient.Exec(context.Background(), os.Args[0]+" rocks", args)
	if err != nil {
		return fmt.Errorf("failed to run luarocks: %w", err)
	}

	return nil
}
