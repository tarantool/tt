package rocktree_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// versionLua is a _version.lua as a build generates it.
const versionLua = `return {
    version  = "1.2.0",
    commit   = "59173d4a1b2c3d4e5f60718293a4b5c6d7e8f901",
    dirty    = false,
    flavor   = "ce",
    built_at = "2026-11-22T10:00:00Z",
}
`

// TestReleaseRockspecInstall pins the build of a package of Lua and data:
// build.install says every file by a module name, those of a namespace and
// the flat ones, a native module the layout takes among them, and the
// _version.lua the rockspec carries in build.extra_files. A file that is not
// Lua is keyed by its directory and any free last segment, as LuaRocks names
// it by its source.
func TestReleaseRockspecInstall(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name: "mylib", Version: "1.2.0", Revision: 1, Summary: "A library",
		SourceURL: "git+https://github.com/example/mylib", SourceTag: "v1.2.0",
		Build: rocktree.ReleaseBuild{
			Install: []rocktree.ReleaseFile{
				{Source: "mylib/init.lua", Dest: "lua/mylib/init.lua"},
				{Source: "mylib/util/str.lua", Dest: "lua/mylib/util/str.lua"},
				{Source: "mylib/data/schema.lua", Dest: "lua/mylib/data/schema.lua"},
				{Source: "mylib/data/schema.json", Dest: "lua/mylib/data/schema.json"},
				{Source: "mylib/data/schema.yaml", Dest: "lua/mylib/data/schema.yaml"},
				{Source: "mylib/data/schema_yaml.lua", Dest: "lua/mylib/data/schema_yaml.lua"},
				{Source: "mylib/data/.keep", Dest: "lua/mylib/data/.keep"},
				{Source: "mylib/rates.v2.json", Dest: "lua/mylib/rates.v2.json"},
				{Source: "roles/mylib.lua", Dest: "lua/roles/mylib.lua"},
				{Source: "top/flat.lua", Dest: "lua/flat.lua"},
				{Source: "top/flat.txt", Dest: "lua/flat.txt"},
				{Source: "prebuilt/fast.so", Dest: "lib/mylib/fast.so"},
				{Source: "prebuilt/fast.dylib", Dest: "lib/mylib/fast.dylib"},
				{Source: "tt-rock-version.lua", Dest: "lua/mylib/_version.lua"},
			},
			ExtraFiles: map[string]string{"tt-rock-version.lua": versionLua},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, `-- The rock of the tt package mylib 1.2.0.
-- tt package pack generated it from app.manifest.toml. tt builds the
-- package from that manifest; LuaRocks builds it as the build table says.
rockspec_format = "3.0"
package = "mylib"
version = "1.2.0-1"
source = {
   url = "git+https://github.com/example/mylib",
   tag = "v1.2.0",
}
description = {
   summary = "A library",
}
build = {
   type = "none",
   install = {
      lua = {
         ["flat"] = "top/flat.lua",
         ["flat_txt"] = "top/flat.txt",
         ["mylib._version"] = "tt-rock-version.lua",
         ["mylib.data._keep"] = "mylib/data/.keep",
         ["mylib.data.schema"] = "mylib/data/schema.lua",
         ["mylib.data.schema_json"] = "mylib/data/schema.json",
         ["mylib.data.schema_yaml"] = "mylib/data/schema_yaml.lua",
         ["mylib.data.schema_yaml_2"] = "mylib/data/schema.yaml",
         ["mylib.init"] = "mylib/init.lua",
         ["mylib.rates"] = "mylib/rates.v2.json",
         ["mylib.util.str"] = "mylib/util/str.lua",
         ["roles.mylib"] = "roles/mylib.lua",
      },
      lib = {
         ["mylib.fast"] = "prebuilt/fast.dylib",
         ["mylib.fast_so"] = "prebuilt/fast.so",
      },
   },
   extra_files = {
      ["tt-rock-version.lua"] = [=[
`+versionLua+`]=],
   },
}
deploy = {
   wrap_bin_scripts = false,
}
`, string(spec.RawSource))

	assert.Equal(t, "none", spec.Build.Type)
	assert.Equal(t, map[string]string{"tt-rock-version.lua": versionLua}, spec.Build.ExtraFiles)
	assert.Len(t, spec.Build.Install.Lua, 12)
	assert.Equal(t, "prebuilt/fast.so", spec.Build.Install.Lib["mylib.fast_so"])
	assert.Empty(t, spec.Build.Install.Bin)
}

// TestReleaseRockspecTtOnly pins the build of a package only tt builds: of
// type command, it prints why to stderr and fails, quoted so that neither sh
// nor the variables of LuaRocks change a word of it, and it installs nothing.
func TestReleaseRockspecTtOnly(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name: "demo", Version: "1.0.0", Revision: 1,
		Build: rocktree.ReleaseBuild{
			Install: []rocktree.ReleaseFile{
				{Source: "demo/init.lua", Dest: "lua/demo/init.lua"},
			},
			ExtraFiles: map[string]string{"tt-rock-version.lua": versionLua},
			TtOnly:     `demo 1.0.0 builds with tt only: it's "fast", $(CC) and $HOME.`,
		},
	})
	require.NoError(t, err)

	source := string(spec.RawSource)

	assert.True(t, strings.HasPrefix(source, `-- The rock of the tt package demo 1.0.0.
-- tt package pack generated it from app.manifest.toml. tt builds the
-- package from that manifest; LuaRocks cannot, as the build table says.
`), source)
	// The command is split after a space, and the pieces are joined again.
	assert.Contains(t, source, `build = {
   type = "command",
   build_command = "printf '%s\\n' 'demo 1.0.0 builds with tt only: " ..
      "it'\\''s \"fast\", $''(CC) and $HOME.' >&2; exit 1",
}
`)
	assert.True(t, rocktree.IsReleaseRockspec(spec.RawSource))

	assert.Equal(t, "command", spec.Build.Type)
	assert.Equal(t, `printf '%s\n' 'demo 1.0.0 builds with tt only: it'\''s "fast", $''(CC) and `+
		`$HOME.' >&2; exit 1`, spec.Build.BuildCommand)
	assert.Empty(t, spec.Build.InstallCommand)
	assert.Empty(t, spec.Build.Install.Lua)
	assert.Empty(t, spec.Build.ExtraFiles)

	// A long message is kept within 80 columns, but for a word longer.
	long := "demo 1.0.0 builds with tt only: " + strings.Repeat("the component \"c\" "+
		"is built by the tt backend \"lua-c\"; ", 5) + strings.Repeat("x", 90) + "."

	spec, err = rocktree.ReleaseRockspec(rocktree.Release{
		Name: "demo", Version: "1.0.0", Revision: 1,
		Build: rocktree.ReleaseBuild{Install: nil, ExtraFiles: nil, TtOnly: long},
	})
	require.NoError(t, err)

	assert.Equal(t, "printf '%s\\n' '"+long+"' >&2; exit 1", spec.Build.BuildCommand)

	for line := range strings.SplitSeq(string(spec.RawSource), "\n") {
		if !strings.Contains(line, strings.Repeat("x", 90)) {
			assert.LessOrEqual(t, len(line), 80, line)
		}
	}
}

// TestReleaseRockspecScript pins the build of a package by a script: of type
// command, whose commands run the script with the variables of LuaRocks in
// single quotes, with build.install of the layout and the script among the
// extra files. A script of a name sh would read otherwise is refused.
func TestReleaseRockspecScript(t *testing.T) {
	t.Parallel()

	release := rocktree.Release{
		Name: "demo", Version: "1.0.0", Revision: 1,
		Build: rocktree.ReleaseBuild{
			Install: []rocktree.ReleaseFile{
				{Source: "demo/init.lua", Dest: "lua/demo/init.lua"},
				{Source: "tt-rock-version.lua", Dest: "lua/demo/_version.lua"},
			},
			ExtraFiles: map[string]string{
				"tt-rock-version.lua": versionLua, "tt-rock.sh": "#!/bin/sh\necho $(x)\n",
			},
			Script: "tt-rock.sh",
			TtOnly: "",
		},
	}

	spec, err := rocktree.ReleaseRockspec(release)
	require.NoError(t, err)

	source := string(spec.RawSource)

	assert.Contains(t, source, `-- package from that manifest; LuaRocks builds it as the build `+
		`table says.`)
	assert.Contains(t, source, `build = {
   type = "command",
   build_command = [=[
sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'
CC=$(CC)
CFLAGS=$(CFLAGS)
LIBFLAG=$(LIBFLAG)
LUA_INCDIR=$(LUA_INCDIR)
TT_ROCK_VARIABLES
]=],
   install_command = [=[
sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'
LUADIR=$(LUADIR)
LIBDIR=$(LIBDIR)
BINDIR=$(BINDIR)
TT_ROCK_VARIABLES
]=],
   install = {
      lua = {
         ["demo._version"] = "tt-rock-version.lua",
         ["demo.init"] = "demo/init.lua",
      },
   },
   extra_files = {
      ["tt-rock-version.lua"] = [=[
`)
	assert.Contains(t, source, `      ["tt-rock.sh"] = [=[
#!/bin/sh
echo $(x)
]=],
   },
}
`)

	assert.Equal(t, "command", spec.Build.Type)
	assert.Equal(t, "sh ./tt-rock.sh build - <<'TT_ROCK_VARIABLES'\nCC=$(CC)\n"+
		"CFLAGS=$(CFLAGS)\nLIBFLAG=$(LIBFLAG)\nLUA_INCDIR=$(LUA_INCDIR)\nTT_ROCK_VARIABLES\n",
		spec.Build.BuildCommand)
	assert.Equal(t, "sh ./tt-rock.sh install - <<'TT_ROCK_VARIABLES'\nLUADIR=$(LUADIR)\n"+
		"LIBDIR=$(LIBDIR)\nBINDIR=$(BINDIR)\nTT_ROCK_VARIABLES\n", spec.Build.InstallCommand)
	assert.Equal(t, "#!/bin/sh\necho $(x)\n", spec.Build.ExtraFiles["tt-rock.sh"])
	assert.Equal(t, "demo/init.lua", spec.Build.Install.Lua["demo.init"])

	for _, name := range []string{"tt rock.sh", "x;y", "$(x)", "a/b"} {
		release.Build.Script = name

		_, err = rocktree.ReleaseRockspec(release)
		require.ErrorContains(t, err, "the script of the build has a name that is no plain "+
			"file name", name)
	}
}

// TestReleaseRockspecExtraFiles: an extra file is a long string whose level
// its content never closes, which holds the content byte for byte - the
// closing brackets of lower levels, a bracket it ends with, a [[ that Lua 5.1
// would refuse in a string of level 0, a newline it begins with - and one
// with a carriage return, which a long string reads as a newline, is quoted.
func TestReleaseRockspecExtraFiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		literal string
	}{
		{"plain", "return 1\n", "[=[\nreturn 1\n]=]"},
		{"level 1 closed", "a]=]b", "[==[\na]=]b]==]"},
		{"levels 1 and 2 closed", "]=] ]==]", "[===[\n]=] ]==]]===]"},
		{"ends with a bracket", "a]", "[=[\na]]=]"},
		{"ends with the start of the closing", "a]=", "[==[\na]=]==]"},
		{"level 0", "x = t[[[a]]]", "[=[\nx = t[[[a]]]]=]"},
		{"leading newline", "\nreturn 1\n", "[=[\n\nreturn 1\n]=]"},
		{"empty", "", "[=[\n]=]"},
		{"carriage return", "a\r\nb\x01" + "2", `"a\013\010b\0012"`},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec, err := rocktree.ReleaseRockspec(rocktree.Release{
				Name: "demo", Version: "1.0.0", Revision: 1,
				Build: rocktree.ReleaseBuild{
					Install:    nil,
					ExtraFiles: map[string]string{"extra.lua": test.content},
					TtOnly:     "",
				},
			})
			require.NoError(t, err)

			assert.Contains(t, string(spec.RawSource), `["extra.lua"] = `+test.literal+",\n")
			assert.Equal(t, test.content, spec.Build.ExtraFiles["extra.lua"])
		})
	}
}

// TestCheckInstallDest: a path whose directory has a dot, or whose Lua file
// has a dot before .lua or nothing, cannot be said by a module name, and
// build.install installs into lua/ and lib/ alone.
func TestCheckInstallDest(t *testing.T) {
	t.Parallel()

	for _, dest := range []string{
		"lua/mylib/init.lua",
		"lua/mylib/data/schema.v2.json",
		"lua/mylib/.keep",
		"lua/flat.lua",
		"lib/mylib/fast.so",
	} {
		require.NoError(t, rocktree.CheckInstallDest(dest), dest)
	}

	for dest, reason := range map[string]string{
		"lua/mylib/v1.2/schema.json": `the name of the directory "v1.2" has a dot`,
		"lua/my.lib/init.lua":        `the name of the directory "my.lib" has a dot`,
		"lua/mylib/.hidden/a.lua":    `the name of the directory ".hidden" has a dot`,
		"lua/mylib/init.min.lua":     `the name of the Lua file "init.min.lua" has a dot`,
		"lua/mylib/.lua":             `the name of the Lua file ".lua" has a dot`,
		"lib/mylib/x.y/fast.so":      `the name of the directory "x.y" has a dot`,
		"bin/tool":                   "build.install installs into lua/ and lib/ alone",
		"lua/mylib/":                 "the path names no file",
	} {
		err := rocktree.CheckInstallDest(dest)
		require.ErrorIs(t, err, rocktree.ErrInstallDest, dest)
		require.ErrorContains(t, err, reason, dest)
	}

	_, err := rocktree.ReleaseRockspec(rocktree.Release{
		Name: "demo", Version: "1.0.0", Revision: 1,
		Build: rocktree.ReleaseBuild{
			Install:    []rocktree.ReleaseFile{{Source: "a.b/c.lua", Dest: "lua/a.b/c.lua"}},
			ExtraFiles: nil, TtOnly: "",
		},
	})
	require.ErrorIs(t, err, rocktree.ErrInstallDest)
}

// TestReleaseRockspecInstallRefuses: build.install has a file at a path once,
// and a source LuaRocks installs under the name the path says.
func TestReleaseRockspecInstallRefuses(t *testing.T) {
	t.Parallel()

	for name, files := range map[string][]rocktree.ReleaseFile{
		"two files at a path": {
			{Source: "a/init.lua", Dest: "lua/demo/init.lua"},
			{Source: "b/init.lua", Dest: "lua/demo/init.lua"},
		},
		"another name":    {{Source: "a/schema.json", Dest: "lua/demo/other.json"}},
		"Lua as another":  {{Source: "a/init.lua", Dest: "lua/demo/init.txt"}},
		"another as Lua":  {{Source: "a/init.txt", Dest: "lua/demo/init.lua"}},
		"a renamed so":    {{Source: "a/fast.so", Dest: "lib/demo/slow.so"}},
		"no file at all":  {{Source: "a/init.lua", Dest: "lua"}},
		"a program, here": {{Source: "a/tool", Dest: "bin/tool"}},
	} {
		_, err := rocktree.ReleaseRockspec(rocktree.Release{
			Name: "demo", Version: "1.0.0", Revision: 1,
			Build: rocktree.ReleaseBuild{Install: files, ExtraFiles: nil, TtOnly: ""},
		})
		require.Error(t, err, name)
	}
}
