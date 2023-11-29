package rocks

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	lua "github.com/yuin/gopher-lua"
)

//go:embed extra/*
//go:embed third_party/luarocks/src/*
var luarocks embed.FS

//go:embed completions/*
var EmbedCompletions embed.FS

const (
	rocksRepoManifestName     = "manifest"
	repoRocksPathEnvVarName   = "TT_CLI_REPO_ROCKS"
	tarantoolPrefixEnvVarName = "TT_CLI_TARANTOOL_PREFIX"
	tarantoolDefaultPrefixDir = "/usr"
)

// addLuarocksRepoOpts adds --server option to luarocks command line if rocks repository
// info is specified in tt config. Return updated args slice.
func addLuarocksRepoOpts(cliOpts *config.CliOpts, args []string) ([]string, error) {
	// Make sure there is no --only-server option is specified.
	for _, opt := range args {
		if opt == "--only-server" || strings.HasPrefix(opt, "--only-server=") {
			return args, nil // If --only-server is specified, no need to add --server option.
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

	return args, nil
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

	output, err := exec.Command(cli.TarantoolCli.Executable, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tarantool version: %s", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 3 {
		return "", fmt.Errorf("failed to get prefix path: expected more data")
	}

	re := regexp.MustCompile(`^.*\s-DCMAKE_INSTALL_PREFIX=(?P<prefix>\/.*)\s.*$`)
	matches := util.FindNamedMatches(re, lines[2])
	if len(matches) == 0 {
		return "", fmt.Errorf("failed to get prefix path: regexp does not match")
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

// getwdWrapperForLua is getwd call wrapper.
func getwdWrapperForLua(L *lua.LState) int {
	dir, _ := os.Getwd()
	L.Push(lua.LString(dir))
	return 1
}

// Execute LuaRocks command. All args will be processed by LuaRocks.
func Exec(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts, args []string) error {
	var cmd string
	var rocks_cmd string

	cliOpts.Repo.Rocks = getRocksRepoPath(cliOpts.Repo.Rocks)

	var err error
	if args, err = addLuarocksRepoOpts(cliOpts, args); err != nil {
		return err
	}

	for idx, arg := range args {
		if (len(args))-1 == idx {
			cmd += fmt.Sprintf("'%s'", arg)
		} else {
			cmd += fmt.Sprintf("'%s', ", arg)
		}
	}

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

	os.Setenv("TT_CLI_TARANTOOL_VERSION", version.Str)
	os.Setenv("TT_CLI_TARANTOOL_PREFIX", tarantoolPrefixDir)
	os.Setenv("TT_CLI_TARANTOOL_INCLUDE", tarantoolIncludeDir)
	os.Setenv("TARANTOOL_DIR", filepath.Dir(tarantoolIncludeDir))
	os.Setenv("TT_CLI_TARANTOOL_PATH", filepath.Dir(cmdCtx.Cli.TarantoolCli.Executable))

	if len(args) == 0 {
		rocks_cmd = fmt.Sprintf("t=require('extra.wrapper').exec('%s')", os.Args[0])
	} else {
		rocks_cmd = fmt.Sprintf("t=require('extra.wrapper').exec('%s', %s)",
			os.Args[0], cmd)
	}
	extra_path := "extra/"
	rocks_path := "third_party/luarocks/src/"

	rocks_preload := map[string]string{
		"extra.wrapper":                    extra_path + "wrapper.lua",
		"luarocks.core.hardcoded":          extra_path + "hardcoded.lua",
		"luarocks.core.util":               rocks_path + "luarocks/core/util.lua",
		"luarocks.core.persist":            rocks_path + "luarocks/core/persist.lua",
		"luarocks.core.sysdetect":          rocks_path + "luarocks/core/sysdetect.lua",
		"luarocks.core.cfg":                rocks_path + "luarocks/core/cfg.lua",
		"luarocks.core.dir":                rocks_path + "luarocks/core/dir.lua",
		"luarocks.core.path":               rocks_path + "luarocks/core/path.lua",
		"luarocks.core.manif":              rocks_path + "luarocks/core/manif.lua",
		"luarocks.core.vers":               rocks_path + "luarocks/core/vers.lua",
		"luarocks.util":                    rocks_path + "luarocks/util.lua",
		"luarocks.loader":                  rocks_path + "luarocks/loader.lua",
		"luarocks.dir":                     rocks_path + "luarocks/dir.lua",
		"luarocks.path":                    rocks_path + "luarocks/path.lua",
		"luarocks.fs":                      rocks_path + "luarocks/fs.lua",
		"luarocks.persist":                 rocks_path + "luarocks/persist.lua",
		"luarocks.fun":                     rocks_path + "luarocks/fun.lua",
		"luarocks.tools.patch":             rocks_path + "luarocks/tools/patch.lua",
		"luarocks.tools.zip":               rocks_path + "luarocks/tools/zip.lua",
		"luarocks.tools.tar":               rocks_path + "luarocks/tools/tar.lua",
		"luarocks.fs.unix":                 rocks_path + "luarocks/fs/unix.lua",
		"luarocks.fs.unix.tools":           rocks_path + "luarocks/fs/unix/tools.lua",
		"luarocks.fs.lua":                  rocks_path + "luarocks/fs/lua.lua",
		"luarocks.fs.tools":                rocks_path + "luarocks/fs/tools.lua",
		"luarocks.queries":                 rocks_path + "luarocks/queries.lua",
		"luarocks.type_check":              rocks_path + "luarocks/type_check.lua",
		"luarocks.type.rockspec":           rocks_path + "luarocks/type/rockspec.lua",
		"luarocks.rockspecs":               rocks_path + "luarocks/rockspecs.lua",
		"luarocks.signing":                 rocks_path + "luarocks/signing.lua",
		"luarocks.fetch":                   rocks_path + "luarocks/fetch.lua",
		"luarocks.type.manifest":           rocks_path + "luarocks/type/manifest.lua",
		"luarocks.manif":                   rocks_path + "luarocks/manif.lua",
		"luarocks.build.builtin":           rocks_path + "luarocks/build/builtin.lua",
		"luarocks.deps":                    rocks_path + "luarocks/deps.lua",
		"luarocks.deplocks":                rocks_path + "luarocks/deplocks.lua",
		"luarocks.cmd":                     rocks_path + "luarocks/cmd.lua",
		"luarocks.argparse":                rocks_path + "luarocks/argparse.lua",
		"luarocks.test.busted":             rocks_path + "luarocks/test/busted.lua",
		"luarocks.test.command":            rocks_path + "luarocks/test/command.lua",
		"luarocks.results":                 rocks_path + "luarocks/results.lua",
		"luarocks.search":                  rocks_path + "luarocks/search.lua",
		"luarocks.repos":                   rocks_path + "luarocks/repos.lua",
		"luarocks.cmd.show":                rocks_path + "luarocks/cmd/show.lua",
		"luarocks.cmd.path":                rocks_path + "luarocks/cmd/path.lua",
		"luarocks.cmd.write_rockspec":      rocks_path + "luarocks/cmd/write_rockspec.lua",
		"luarocks.manif.writer":            rocks_path + "luarocks/manif/writer.lua",
		"luarocks.remove":                  rocks_path + "luarocks/remove.lua",
		"luarocks.pack":                    rocks_path + "luarocks/pack.lua",
		"luarocks.build":                   rocks_path + "luarocks/build.lua",
		"luarocks.cmd.make":                rocks_path + "luarocks/cmd/make.lua",
		"luarocks.cmd.build":               rocks_path + "luarocks/cmd/build.lua",
		"luarocks.cmd.install":             rocks_path + "luarocks/cmd/install.lua",
		"luarocks.cmd.list":                rocks_path + "luarocks/cmd/list.lua",
		"luarocks.download":                rocks_path + "luarocks/download.lua",
		"luarocks.cmd.download":            rocks_path + "luarocks/cmd/download.lua",
		"luarocks.cmd.search":              rocks_path + "luarocks/cmd/search.lua",
		"luarocks.cmd.pack":                rocks_path + "luarocks/cmd/pack.lua",
		"luarocks.cmd.new_version":         rocks_path + "luarocks/cmd/new_version.lua",
		"luarocks.cmd.purge":               rocks_path + "luarocks/cmd/purge.lua",
		"luarocks.cmd.init":                rocks_path + "luarocks/cmd/init.lua",
		"luarocks.cmd.lint":                rocks_path + "luarocks/cmd/lint.lua",
		"luarocks.test":                    rocks_path + "luarocks/test.lua",
		"luarocks.cmd.test":                rocks_path + "luarocks/cmd/test.lua",
		"luarocks.cmd.which":               rocks_path + "luarocks/cmd/which.lua",
		"luarocks.cmd.remove":              rocks_path + "luarocks/cmd/remove.lua",
		"luarocks.upload.multipart":        rocks_path + "luarocks/upload/multipart.lua",
		"luarocks.upload.api":              rocks_path + "luarocks/upload/api.lua",
		"luarocks.cmd.upload":              rocks_path + "luarocks/cmd/upload.lua",
		"luarocks.cmd.doc":                 rocks_path + "luarocks/cmd/doc.lua",
		"luarocks.cmd.unpack":              rocks_path + "luarocks/cmd/unpack.lua",
		"luarocks.cmd.config":              rocks_path + "luarocks/cmd/config.lua",
		"luarocks.require":                 rocks_path + "luarocks/require.lua",
		"luarocks.build.cmake":             rocks_path + "luarocks/build/cmake.lua",
		"luarocks.build.make":              rocks_path + "luarocks/build/make.lua",
		"luarocks.build.command":           rocks_path + "luarocks/build/command.lua",
		"luarocks.fetch.cvs":               rocks_path + "luarocks/fetch/cvs.lua",
		"luarocks.fetch.svn":               rocks_path + "luarocks/fetch/svn.lua",
		"luarocks.fetch.sscm":              rocks_path + "luarocks/fetch/sscm.lua",
		"luarocks.fetch.git":               rocks_path + "luarocks/fetch/git.lua",
		"luarocks.fetch.git_file":          rocks_path + "luarocks/fetch/git_file.lua",
		"luarocks.fetch.git_http":          rocks_path + "luarocks/fetch/git_http.lua",
		"luarocks.fetch.git_https":         rocks_path + "luarocks/fetch/git_https.lua",
		"luarocks.fetch.git_ssh":           rocks_path + "luarocks/fetch/git_ssh.lua",
		"luarocks.fetch.hg":                rocks_path + "luarocks/fetch/hg.lua",
		"luarocks.fetch.hg_http":           rocks_path + "luarocks/fetch/hg_http.lua",
		"luarocks.fetch.hg_https":          rocks_path + "luarocks/fetch/hg_https.lua",
		"luarocks.fetch.hg_ssh":            rocks_path + "luarocks/fetch/hg_ssh.lua",
		"luarocks.admin.cache":             rocks_path + "luarocks/admin/cache.lua",
		"luarocks.admin.cmd.refresh_cache": rocks_path + "luarocks/admin/cmd/refresh_cache.lua",
		"luarocks.admin.index":             rocks_path + "luarocks/admin/index.lua",
		"luarocks.admin.cmd.add":           rocks_path + "luarocks/admin/cmd/add.lua",
		"luarocks.admin.cmd.remove":        rocks_path + "luarocks/admin/cmd/remove.lua",
		"luarocks.admin.cmd.make_manifest": rocks_path + "luarocks/admin/cmd/make_manifest.lua",
	}

	L := lua.NewState()
	defer L.Close()
	L.SetGlobal("tt_getwd", L.NewFunction(getwdWrapperForLua))
	preload := L.GetField(L.GetField(L.Get(lua.EnvironIndex), "package"), "preload")

	for modname, path := range rocks_preload {
		ctx, err := util.ReadEmbedFile(luarocks, path)
		if err != nil {
			return err
		}
		mod, err := L.LoadString(ctx)
		if err != nil {
			return err
		}
		L.SetField(preload, modname, mod)
	}

	if err := L.DoString(rocks_cmd); err != nil {
		return err
	}

	return nil
}
