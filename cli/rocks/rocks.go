package rocks

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
	lua "github.com/yuin/gopher-lua"
)

//go:embed extra/*
//go:embed third_party/luarocks/src/*
var luarocks embed.FS

// Execute LuaRocks command. All args will be processed by LuaRocks.
func Exec(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var cmd string

	// Print rocks help if no arguments given.
	if len(args) == 0 {
		cmd = "help"
	}

	for idx, arg := range args {
		if (len(args))-1 == idx {
			cmd += fmt.Sprintf("'%s'", arg)
		} else {
			cmd += fmt.Sprintf("'%s', ", arg)
		}
	}

	version, err := util.GetTarantoolVersion(&cmdCtx.Cli)
	if err != nil {
		return err
	}

	os.Setenv("TT_CLI_TARANTOOL_VERSION", version)
	os.Setenv("TT_CLI_TARANTOOL_PATH", filepath.Dir(cmdCtx.Cli.TarantoolExecutable))

	rocks_cmd := fmt.Sprintf("t=require('extra.wrapper').exec('%s', %s)",
		os.Args[0], cmd)
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
		"luarocks.cmd":                     rocks_path + "luarocks/cmd.lua",
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
		"luarocks.cmd.help":                rocks_path + "luarocks/cmd/help.lua",
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
