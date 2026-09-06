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

	"github.com/apex/log"
	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

var (
	errPrefixPathExpectedMoreData = errors.New("failed to get prefix path: expected more data")
	errPrefixPathRegexpMismatch   = errors.New("failed to get prefix path: regexp does not match")
)

//go:embed completions/*
var EmbedCompletions embed.FS

const (
	rocksRepoManifestName     = "manifest"
	repoRocksPathEnvVarName   = "TT_CLI_REPO_ROCKS"
	tarantoolPrefixEnvVarName = "TT_CLI_TARANTOOL_PREFIX"
	tarantoolDefaultPrefixDir = "/usr"
	minVersionOutputLines     = 3
)

// addLuarocksRepoOpts adds --server option to luarocks command line if rocks repository
// info is specified in tt config. Return updated args slice.
func addLuarocksRepoOpts(cliOpts *config.CliOpts, args []string) []string {
	// Make sure there is no --only-server option is specified.
	for _, opt := range args {
		if opt == "--only-server" || strings.HasPrefix(opt, "--only-server=") {
			return args // If --only-server is specified, no need to add --server option.
		}
	}

	// Check whether rocks repository is specified in tt config.
	if cliOpts.Repo != nil && cliOpts.Repo.Rocks != "" {
		isServerSet := false
		for i, opt := range args {
			if opt == "--server" {
				isServerSet = true
				args[i+1] = args[i+1] + " " + cliOpts.Repo.Rocks
			} else if strings.HasPrefix(opt, "--server=") {
				isServerSet = true
				args[i] += " " + cliOpts.Repo.Rocks
			}
		}
		if !isServerSet {
			args = append(args, "--server", cliOpts.Repo.Rocks)
		}
	}

	return args
}

// getRocksRepoPath returns actual rocks repo path: either from passed path argument or
// from current environment.
func getRocksRepoPath(rocksRepoPath string) string {
	rockRepoPathFromEnv := os.Getenv(repoRocksPathEnvVarName)
	if rocksRepoPath == "" || (rocksRepoPath != "" &&
		!util.IsRegularFile(filepath.Join(rocksRepoPath, rocksRepoManifestName))) {
		if rockRepoPathFromEnv != "" {
			rocksRepoPath = rockRepoPathFromEnv
		}
	}
	return rocksRepoPath
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
func Exec(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, args []string) error {
	cliOpts.Repo.Rocks = getRocksRepoPath(cliOpts.Repo.Rocks)

	args = addLuarocksRepoOpts(cliOpts, args)

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
		return err
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
		Tarantool: luarocks.TarantoolConfig{
			Executable: cmdCtx.Cli.TarantoolCli.Executable,
			Prefix:     tarantoolPrefixDir,
			IncludeDir: tarantoolIncludeDir,
			Version:    version.Str,
		},
	}

	rocksClient, err := client.New(cfg, client.WithBackend(client.BackendLua))
	if err != nil {
		return err
	}

	// Match the historical progname: LuaRocks prints "<argv0> rocks" (and
	// "<argv0> rocks admin") in its usage, the wrapper appending " admin" for
	// the admin sub-CLI.
	return rocksClient.Exec(context.Background(), os.Args[0]+" rocks", args)
}
