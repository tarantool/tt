# Publish a module as a rock

A Tarantool module other projects depend on is published as a rock. This
page follows one module through its life: it describes `hashkit`, a Lua
module with a small C part, as a package, builds and tests it, versions it
by git tags, packs its rocks, publishes them, and then depends on them from
an application, `kv-api`. What a package, a lock and a rock server are is in
[Packages](../concepts/packages.md) and
[Rock servers](../concepts/rock-servers.md); every field of the manifest is
in [`app.manifest.toml`](../reference/app-manifest.md).

The page is a walkthrough: run its commands in order, each from the
directory it names, starting in an empty directory. The steps that need
another platform, CI or a web server are marked as not run in it. The output
shown is that of Linux x86_64; paths, commit hashes, the versions and digests
made of them and timings differ on your machine.

You need:

- tt 3, git, a C compiler (`cc`), `unzip`, and a `tar` that reads zstd
  (`tar --zstd`, GNU tar 1.31 or newer with `zstd` installed);
- a Tarantool 3 on `PATH`, with its headers where tt finds them (see
  [Headers for rocks with a C part](binaries.md#headers-for-rocks-with-a-c-part));
- access to `https://rocks.tarantool.org/`, for luatest.

The compiler and the headers are for the builds: the binary rock, the tests,
and a project that builds the module from its source rock. Packing the
source rock alone builds nothing and needs neither.

## Describe the module

Start in the empty directory, and make the project of the module there:

```console
$ mkdir hashkit && cd hashkit
$ git init -q
$ mkdir hashkit test
```

`app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "hashkit"
description = "String hashing for Tarantool"
license = "BSD-2-Clause"
license_files = ["LICENSE"]
repository = "https://github.com/example/hashkit"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
platforms = ["linux-amd64", "linux-arm64", "darwin-arm64"]

[dependencies]

[dev_dependencies]
luatest = "*"

# The Lua modules hashkit and hashkit.*, and the C module hashkit.fnv.
[components.hashkit]
path = "hashkit"

[components.hashkit.build]
backend = "lua-c"
module = "fnv"
sources = ["fnv.c"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]

[components.hashkit.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]

[components.hashkit.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
```

- `[package]` names the rock and fills its rockspec: `description` and
  `license` describe it, and `repository` is its homepage and its source.
  `license_files` go with the module wherever it goes: into `doc/` of its
  binary rock and of the rock a project installs, and so into every project
  and archive that has it; the source rock keeps them where the project has
  them.
- `[platform]`: `tarantool` becomes the dependency of the rock on
  `tarantool`; `platforms` lists the platforms the module is for, and
  becomes `supported_platforms` of the rockspec.
- `[dev_dependencies]` are for the tests: the rock does not depend on them.
- The component `hashkit` lays out the `*.lua` files of its `path`,
  `hashkit/hashkit/`, under `.rocks/share/tarantool/hashkit/`, and its
  `lua-c` build compiles
  `fnv.c` into the C module `fnv`, `.rocks/lib/tarantool/hashkit/fnv.so`
  (`fnv.dylib` on macOS), which `require('hashkit.fnv')` loads. `cflags`
  and `ldflags` are the flags of the project, one per entry, and
  `platforms.linux` and `platforms.darwin` add to them on that OS only. The
  `c` and `lua-c` backends are described in the
  [reference](../reference/app-manifest.md#componentsnamebuild).

`hashkit/fnv.c`, the C part:

```c
#include <stddef.h>
#include <stdint.h>

#include <lua.h>
#include <lauxlib.h>

/* hash(s): the 32-bit FNV-1a hash of the string s. */
static int
fnv_hash(lua_State *L)
{
	size_t len;
	const unsigned char *s =
		(const unsigned char *)luaL_checklstring(L, 1, &len);
	uint32_t h = 2166136261u;
	for (size_t i = 0; i < len; i++) {
		h ^= s[i];
		h *= 16777619u;
	}
	lua_pushnumber(L, h);
	return 1;
}

/* require('hashkit.fnv') */
int
luaopen_hashkit_fnv(lua_State *L)
{
	lua_newtable(L);
	lua_pushcfunction(L, fnv_hash);
	lua_setfield(L, -2, "hash");
	return 1;
}
```

`hashkit/init.lua`, the module itself, `require('hashkit')`:

```lua
local fnv = require('hashkit.fnv')

local hashkit = {
    VERSION = require('hashkit.version').version,
}

-- The 32-bit FNV-1a hash of a string.
hashkit.hash = fnv.hash

-- The bucket of a key among n buckets, from 1 to n.
function hashkit.bucket(key, n)
    return fnv.hash(key) % n + 1
end

return hashkit
```

`hashkit.version` is the `version.lua` tt generates for the package at
every build. `test/hash_test.lua`:

```lua
local t = require('luatest')
local hashkit = require('hashkit')

local g = t.group('hash')

g.test_known_values = function()
    t.assert_equals(hashkit.hash(''), 2166136261)
    t.assert_equals(hashkit.hash('a'), 0xe40c292c)
end
```

`test/bucket_test.lua`:

```lua
local t = require('luatest')
local hashkit = require('hashkit')

local g = t.group('bucket')

g.test_range = function()
    for i = 1, 1000 do
        local b = hashkit.bucket('key-' .. i, 16)
        t.assert(b >= 1 and b <= 16)
    end
end
```

The license file, which `license_files` names, and the `.gitignore` of the
project; the walkthrough makes do with a line in place of the text of the
license:

```console
$ echo 'BSD 2-Clause License. Copyright (c) 2026, Example.' > LICENSE
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
```

## Build and test

The commands of the module run in the root of its project, the directory
with `app.manifest.toml`, not in `hashkit/hashkit/`. There:

```console
$ tt package build
$ tt run -e "local h = require('hashkit') print(h.VERSION, h.hash('a'), h.bucket('user:1', 4))" - </dev/null
0.0.0-dev.0+dirty	3826002220	4
```

`tt package build` resolves the lock, installs luatest into `.rocks/`,
compiles the C module and lays the package out in `.rocks/` as the rock
`hashkit`. The version is `0.0.0-dev.0+dirty` while the repository has no
commit; see [Version it](#version-it).

`tt -V package build` logs the command line of the compiler before it runs
it:

```console
$ tt -V package build 2>&1 | grep running
   · running cc -O2 -fPIC -std=gnu99 -Wall -Wextra -Werror -I/usr/include/tarantool -shared -Wl,--as-needed -o .../hashkit/_build/rock/default/lib/hashkit/fnv.so fnv.c dir=.../hashkit/hashkit
```

tt's flags come first, then `cflags`, then `CFLAGS` of the environment,
which so wins a conflict; setting `CFLAGS`, even to nothing, drops tt's
`-O2`. A build for a debugger:

```console
$ CFLAGS='-O0 -g' tt -V package build 2>&1 | grep running
   · running cc -fPIC -std=gnu99 -Wall -Wextra -Werror -O0 -g -I/usr/include/tarantool -shared -Wl,--as-needed -o .../hashkit/_build/rock/default/lib/hashkit/fnv.so fnv.c dir=.../hashkit/hashkit
```

`tt test` builds the package and runs luatest over `test/`; paths narrow the
run, as many as you name, and the arguments after `--` go to luatest:

```console
$ tt test
Tarantool version is 3.9.0-0-g8b9a0f59a95b
started logging into a pipe, SIGHUP log rotation disabled
..
Ran 2 tests in 0.021 seconds, 2 succeeded, 0 failed
$ tt test test/hash_test.lua test/bucket_test.lua -- --verbose
Tarantool version is 3.9.0-0-g8b9a0f59a95b
started logging into a pipe, SIGHUP log rotation disabled
Started on Thu Nov 12 22:38:47 2026
    bucket.test_range ... (0.017s) Ok
    hash.test_known_values ... (0.000s) Ok
=========================================================
Ran 2 tests in 0.019 seconds, 2 succeeded, 0 failed
```

Now and then luatest prints a line of its own log as well, `luatest | ...
I> Bootstrap finished: ...`, which says nothing is wrong. A path that does
not exist fails the run before the build.

## Version it

Unless a `VERSION` file gives it, tt takes the version of the package from
git: the nearest version tag, and the commits since it. Commit, and the
version names the commit; tag it, and it is the version of the tag:

```console
$ git add -A
$ git commit -qm 'hashkit: string hashing'
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
0.0.0-dev.1+g1746c78
$ git tag v1.0.0
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.0.0
```

A tag is `v1.0.0`, or `1.0.0` in full SemVer, without the `v`. A rock
version has no `+` and no `-` but the one of its revision, so tt makes one
of the package's version:

| The tree | The version | The rock version | A project depending on the rock |
| --- | --- | --- | --- |
| on the tag `v1.0.0` | `1.0.0` | `1.0.0-1` | takes it as a release |
| a commit after the tag | `1.0.1-dev.1+gd0b7479` | `1.0.1.dev.1.gd0b7479-1` | takes it as a release: the version names one commit |
| a commit, no tag yet | `0.0.0-dev.1+g1746c78` | `0.0.0.dev.1.g1746c78-1` | takes it as a release as well |
| uncommitted changes | `1.1.0+dirty`, `1.0.1-dev.1+gd0b7479.dirty` | `1.1.0.dirty-1`, `1.0.1.dev.1.gd0b7479.dirty-1` | passes it over for a release, and takes it only when nothing else fits, warning that it was built from uncommitted changes |

A change to the lock alone does not make the tree dirty. A module that is
one project of a monorepo, in a subdirectory of the repository, is versioned
by its directory alone: by the last commit that changed it, and `dirty` only
for a change under it. Tag a commit that changes the directory to release
it. See [Building](../concepts/packages.md#building) for the rules.

## Pack the rocks

In the root of the project of the module, at the tag `v1.0.0`, pack the
binary rock into `../dist`, a directory beside the project:

```console
$ tt package pack --format rock --out ../dist
.../dist/hashkit-1.0.0-1.linux-x86_64.rock
.../dist/hashkit-1.0.0-1.rockspec
$ unzip -l ../dist/hashkit-1.0.0-1.linux-x86_64.rock
Archive:  ../dist/hashkit-1.0.0-1.linux-x86_64.rock
  Length      Date    Time    Name
---------  ---------- -----   ----
       51  1980-01-01 00:00   doc/LICENSE
      561  1980-01-01 00:00   hashkit-1.0.0-1.rockspec
    15832  1980-01-01 00:00   lib/hashkit/fnv.so
      309  1980-01-01 00:00   lua/hashkit/init.lua
      143  1980-01-01 00:00   lua/hashkit/version.lua
      422  1980-01-01 00:00   rock_manifest
---------                     -------
    17318                     6 files
```

The rock holds what the build laid out, the C module built for this
platform, and the license files in `doc/`. The project needs no rockspec:
tt writes it from the manifest, the same for the rock of every platform.
Its build is done, so its build type is `none`; its source is the
repository at the tag of the version, here `v1.0.0`:

```console
$ cat ../dist/hashkit-1.0.0-1.rockspec
-- The rock of the tt package hashkit 1.0.0, which tt packs from the
-- files it lays out: its build is done.
rockspec_format = "3.0"
package = "hashkit"
version = "1.0.0-1"
source = {
   url = "git+https://github.com/example/hashkit",
   tag = "v1.0.0",
}
description = {
   summary = "String hashing for Tarantool",
   homepage = "https://github.com/example/hashkit",
   license = "BSD-2-Clause",
}
supported_platforms = { "linux", "macosx" }
dependencies = {
   "tarantool >= 3.0.0",
}
build = {
   type = "none",
}
deploy = {
   wrap_bin_scripts = false,
}
```

A version no tag names, of a commit after a tag, has the full sha of the
commit in `source.tag` instead (see [Take a new release](#take-a-new-release));
one of uncommitted changes names the repository and no commit.

A binary rock serves its own platform. The source rock serves the others:
it holds the files of the project git tracks, with the manifest, the license
files at their path in the project and a `VERSION` file, and a project that
depends on the module builds it from there when no binary rock fits its
platform (see [Build from the source rock](#build-from-the-source-rock)).
Packed alone, it takes no build: tt checks the manifest and the lock and
compiles nothing, so the pack needs no compiler and no headers of Tarantool:

```console
$ tt package pack --format rock-src --out ../dist
.../dist/hashkit-1.0.0-1.src.rock
.../dist/hashkit-1.0.0-1.rockspec
$ unzip -l ../dist/hashkit-1.0.0-1.src.rock
Archive:  ../dist/hashkit-1.0.0-1.src.rock
  Length      Date    Time    Name
---------  ---------- -----   ----
      561  1980-01-01 00:00   hashkit-1.0.0-1.rockspec
        6  1980-01-01 00:00   hashkit/VERSION
       34  1980-01-01 00:00   hashkit/.gitignore
       51  1980-01-01 00:00   hashkit/LICENSE
      409  1980-01-01 00:00   hashkit/app.manifest.lock
      766  1980-01-01 00:00   hashkit/app.manifest.toml
      563  1980-01-01 00:00   hashkit/hashkit/fnv.c
      309  1980-01-01 00:00   hashkit/hashkit/init.lua
      242  1980-01-01 00:00   hashkit/test/bucket_test.lua
      230  1980-01-01 00:00   hashkit/test/hash_test.lua
---------                     -------
     3171                     10 files
```

Both rocks are reproducible: packing the same commit again gives the same
bytes. A rock once published is not changed; a new build of a version that
is out, made with another compiler say, gets a new revision. Without `--out`
the rocks go into `_build/pack/` of the project, which is indexed as a rock
server as `--out` is:

```console
$ tt package pack --format rock --revision 2
.../hashkit/_build/pack/hashkit-1.0.0-2.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.0-2.rockspec
$ ls _build/pack
hashkit-1.0.0-2.linux-x86_64.rock
hashkit-1.0.0-2.rockspec
manifest
manifest-5.1
manifest-5.2
manifest-5.3
manifest-5.4
tt-rocks-index.json
```

`--format` takes a list: `tt,rock` packs the `.tt` archive of the package
and its rock from one build, and `rock,rock-src` both rocks. The archive
bundles a Tarantool and a tt: those of the runtime cache, else those
`tt binary install` put into the project, else the active ones, with a
warning. The warnings depend on your machine (see
[Troubleshooting](#troubleshooting)), and a rock bundles neither:

```console
$ tt package pack --format tt,rock
   ⚠ no tarantool >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tarantool 3.9.0-0-g8b9a0f59a95b instead (tt binary install tarantool puts one into the project)
   ⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
   ⚠ no tt >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tt 3.0.0-alpha.2 instead (tt binary install tt puts one into the project)
   ⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
.../hashkit/_build/pack/hashkit-1.0.0-linux-amd64.tt
.../hashkit/_build/pack/hashkit-1.0.0-1.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.0-1.rockspec
```

A package of Lua alone, with no C part, packs one rock for every platform,
`<name>-<version>.all.rock`, and needs no source rock. See
[Packing a rock](../concepts/packages.md#packing-a-rock) for what goes into
a rock and what fails a pack.

## Publish

### A directory of rocks

`--out` indexes the directory as a rock server once the rock is written,
keeping the rocks already there, so `../dist` is a rock server now: the
LuaRocks manifests list its rocks, and so does `tt-rocks-index.json`, the
[rock index](../concepts/rock-servers.md#a-rock-index), with the sha256 of
every file:

```console
$ ls ../dist
hashkit-1.0.0-1.linux-x86_64.rock
hashkit-1.0.0-1.rockspec
hashkit-1.0.0-1.src.rock
manifest
manifest-5.1
manifest-5.2
manifest-5.3
manifest-5.4
tt-rocks-index.json
```

Projects on this machine depend on the rock from the directory, and pin the
digests of its files in their locks; an HTTP server that serves it serves
the rocks to everyone, LuaRocks included.

A binary rock is built on its platform, so the rocks of a release come from
several machines, a CI matrix say. This sketch of a GitHub Actions workflow
is not run in the walkthrough: each job of `pack` packs the rock of its
platform, the first one the source rock too, and `index` puts them into one
directory and indexes it:

```yaml
name: rocks
on:
  push:
    tags: ["v*", "[0-9]*"]
jobs:
  pack:
    strategy:
      matrix:
        include:
          - {runner: ubuntu-22.04, format: "rock,rock-src"}
          - {runner: ubuntu-22.04-arm, format: rock}
          - {runner: macos-14, format: rock}
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # the tags and the commits the version is made of
      - name: Install tt and Tarantool
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
          "$HOME/.local/bin/tt" binary install tarantool
      - run: tt package pack --format ${{ matrix.format }} --out dist
      - uses: actions/upload-artifact@v4
        with:
          name: rocks-${{ matrix.runner }}
          path: dist/hashkit-*
  index:
    needs: pack
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/download-artifact@v4
        with:
          pattern: rocks-*
          path: dist
          merge-multiple: true
      - name: Install tt
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: tt package index dist
      - uses: actions/upload-artifact@v4
        with:
          name: rocks
          path: dist
```

`tt binary install tarantool` gives the project a Tarantool with its
headers; on macOS it builds Tarantool from source. Build a binary rock on the
oldest system it is to run on: its C module links against the C library of
the machine it is built on. The rocks of the jobs, put into one directory,
are not indexed yet, since a job uploads its rocks alone: `tt package
index dist` in the `index` job writes the LuaRocks manifests and the rock
index of every rock and rockspec in it, which makes it a rock server. The
rocks may instead go to release pages, with an index apart.

### A rock index for release pages

A rock index, `tt-rocks-index.json`, lists every file of every version by
its URL and its sha256, so that the files may be elsewhere - on the release
pages of the repository, say - and a project that depends on the rock pins
the digests in its lock and checks every file it installs against them. Its
format, and how tt writes it, are in
[A rock index](../concepts/rock-servers.md#a-rock-index).

A URL may be relative, to a file below the directory of the index, so a
directory stands in for the release pages here: `../site`, with the files of
each tag under `../site/<tag>/`. `tt package index` with `--index` writes
the index alone, with the files of the directory it indexes named relative
to the index. In the root of the project of the module:

```console
$ mkdir -p ../site/v1.0.0
$ cp ../dist/hashkit-1.0.0-1.* ../site/v1.0.0/
$ tt package index ../site/v1.0.0 --index ../site/tt-rocks-index.json
.../site/tt-rocks-index.json
indexed 1 version(s) of .../site/v1.0.0
$ cat ../site/tt-rocks-index.json
{
  "format": 1,
  "repository": {
    "hashkit": {
      "1.0.0-1": {
        "artifacts": [
          {
            "arch": "linux-x86_64",
            "sha256": "d6385ac73ecd90037ebb668607328aad2251a050665ffe5f83267384fa3bd0a2",
            "size": 4132,
            "url": "v1.0.0/hashkit-1.0.0-1.linux-x86_64.rock"
          },
          {
            "arch": "src",
            "sha256": "864b7ee6a85739c298abc0b62748387a8b6d8adfa35b96976e0e3077d652ccd8",
            "size": 3293,
            "url": "v1.0.0/hashkit-1.0.0-1.src.rock"
          }
        ],
        "rockspec": {
          "sha256": "dd1aae98df5c2cb94f0f3e6f272fecbe9f6cb574505a632a912267251595b959",
          "size": 561,
          "url": "v1.0.0/hashkit-1.0.0-1.rockspec"
        }
      }
    }
  }
}
```

An index that is there is updated: the next release adds its version and
keeps this one, whose files are elsewhere.

On GitHub, which the walkthrough does not run, the files of `dist` are
uploaded to the release of the tag, `--base-url` is the URL the release
serves them from, and the index is a file in a branch of its own, `index`
say, which CI commits:

```sh
gh release upload v1.0.0 dist/*.rock dist/*.rockspec
tt package index dist --index tt-rocks-index.json \
    --base-url https://github.com/example/hashkit/releases/download/v1.0.0
```

A project then depends on the rock from the raw URL of that branch:
`registry = "https://raw.githubusercontent.com/example/hashkit/index/"`.
LuaRocks does not read an index: see [For LuaRocks users](#for-luarocks-users).

## Depend on the rock

The application `kv-api` is a project beside `hashkit`. From the root of
the project of the module:

```console
$ cd ..
$ mkdir kv-api && cd kv-api
$ git init -q
$ mkdir kv
```

`app.manifest.toml`, which takes `hashkit` from the index:

```toml
manifest_version = "0.1"

[package]
name = "kv-api"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[dependencies]
hashkit = { version = ">=1.0.0", registry = "../site" }

[components.app]
path = "kv"
```

`kv/init.lua`:

```lua
local hashkit = require('hashkit')

-- The shard of a key among 4.
local function shard(key)
    return hashkit.bucket(key, 4)
end

return {shard = shard}
```

```console
$ tt package build
$ tt run -e "print(require('kv-api').shard('user:1'))" - </dev/null
4
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE    ORIGIN
default  hashkit  >=1.0.0     1.0.0-1  registry  direct
$ cat app.manifest.lock
lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0-alpha.2'
manifest_hash = 'toml1-sha256:3bb122da9541b581f433f50754437bfd8a3a9914e171bd89e2ad5def90e2b781'

[lock]
[lock.products]
[lock.products.default]
[[lock.products.default.dependencies]]
name = 'hashkit'
version = '1.0.0-1'
source = 'registry'
artifacts = {linux-x86_64 = 'sha256:d6385ac73ecd90037ebb668607328aad2251a050665ffe5f83267384fa3bd0a2', rockspec = 'sha256:dd1aae98df5c2cb94f0f3e6f272fecbe9f6cb574505a632a912267251595b959', src = 'sha256:864b7ee6a85739c298abc0b62748387a8b6d8adfa35b96976e0e3077d652ccd8'}
$ ls .rocks/share/tarantool/rocks/hashkit/1.0.0-1/doc
LICENSE
```

The lock pins the version of the rock and, since the server publishes an
index, the sha256 of each of its files: every install of the rock checks
the file of its platform against them. A dependency from a server with no
index, a server of LuaRocks manifests alone such as rocks.tarantool.org, is
pinned by its version alone. The license of the module is installed with it. Commit the lock with the project:

```console
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
$ git add -A
$ git commit -qm 'kv-api: shards by hashkit'
```

### Take a new release

Back in the project of the module, make a release, 1.1.0:

```console
$ cd ../hashkit
$ echo '# hashkit' > README.md
$ git add README.md
$ git commit -qm 'Add a README'
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.0.1-dev.1+gd0b7479
$ tt package pack --format rock
.../hashkit/_build/pack/hashkit-1.0.1.dev.1.gd0b7479-1.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.1.dev.1.gd0b7479-1.rockspec
$ sed -n '/^source/,/^}/p' _build/pack/hashkit-1.0.1.*.rockspec
source = {
   url = "git+https://github.com/example/hashkit",
   -- The commit of the version, which no tag names. LuaRocks fetches
   -- a tag with git clone --branch, which takes no commit.
   tag = "d0b7479c5e1f3a9b8d2c6e4f0a7b9c1d3e5f7a92",
}
```

A commit after a tag is versioned past it, by the number of commits and the
commit; its rock is a release a project may depend on, a nightly build say.
Its rockspec names the commit by its full sha in `source.tag`, since
LuaRocks has no field for a commit: tt builds the rock from its files, and a
fetch of the source by LuaRocks fails rather than take another commit. Tag
the commit, this time without a `v`, pack both rocks of 1.1.0 into
`../dist`, and add the version to the index:

```console
$ git tag 1.1.0
$ tt package pack --format rock,rock-src --out ../dist
.../dist/hashkit-1.1.0-1.linux-x86_64.rock
.../dist/hashkit-1.1.0-1.src.rock
.../dist/hashkit-1.1.0-1.rockspec
$ mkdir ../site/1.1.0
$ cp ../dist/hashkit-1.1.0-1.* ../site/1.1.0/
$ tt package index ../site/1.1.0 --index ../site/tt-rocks-index.json
.../site/tt-rocks-index.json
indexed 1 version(s) of .../site/1.1.0, and kept 1 listed with files elsewhere
```

In `kv-api/`, a build keeps the version the lock pins; `tt package update`
takes the newest one the constraint allows, for every dependency or for
those it names:

```console
$ cd ../kv-api
$ tt package build
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE    ORIGIN
default  hashkit  >=1.0.0     1.0.0-1  registry  direct
$ tt package update hashkit
  hashkit 1.0.0-1 -> 1.1.0-1
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0
```

### Keep CI on the lock

`tt package build --locked`, and `tt package pack --locked`, fail on a lock
that does not match the manifest instead of resolving it again: what CI
wants, so that it builds what was committed. Raise the constraint of
`kv-api` to the release it now needs; in `app.manifest.toml`, change the
line of `hashkit` to:

```toml
hashkit = { version = ">=1.1.0", registry = "../site" }
```

```console
$ tt package build --locked
   ⨯ lock is out of date: manifest changed since the lock was written (--locked); run tt package resolve to bring the lock back in step
$ tt package resolve
no version moved, but the lock changed:
  manifest hash refreshed
$ tt package build --locked
$ git commit -qam 'kv-api: hashkit 1.1.0'
```

The lock matches the manifest by a hash of what the manifest says, not of
its bytes: a comment, or another layout, leaves it current.

```console
$ echo '# Keys are sharded by hashkit.' >> app.manifest.toml
$ tt package build --locked
$ git commit -qam 'kv-api: say what shards the keys'
```

### Ship it in an archive

`tt package pack` of `kv-api` packs an archive with its dependencies: the
module, its C part built for this platform, and its license.

```console
$ tt package pack
   ⚠ no tarantool >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tarantool 3.9.0-0-g8b9a0f59a95b instead (tt binary install tarantool puts one into the project)
   ⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
   ⚠ no tt >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tt 3.0.0-alpha.2 instead (tt binary install tt puts one into the project)
   ⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
.../kv-api/_build/pack/kv-api-0.0.0-dev.3+gdca3f06-linux-amd64.tt
$ tar --zstd -tf _build/pack/kv-api-*.tt | grep hashkit
.rocks/lib/tarantool/hashkit/
.rocks/lib/tarantool/hashkit/fnv.so
.rocks/share/tarantool/hashkit/
.rocks/share/tarantool/hashkit/init.lua
.rocks/share/tarantool/hashkit/version.lua
.rocks/share/tarantool/rocks/hashkit/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc/LICENSE
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/hashkit-1.1.0-1.rockspec
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/rock_manifest
```

### Build from the source rock

A project on a platform no binary rock is published for builds the module
from its source rock. A server of the source rock alone shows it on this
machine. In the project of the module, at the tag `1.1.0`, pack the source
rock alone, which builds nothing, into a directory of its own:

```console
$ cd ../hashkit
$ tt package pack --format rock-src --out ../dist-src
.../dist-src/hashkit-1.1.0-1.src.rock
.../dist-src/hashkit-1.1.0-1.rockspec
$ cd ../kv-api
```

In `app.manifest.toml` of `kv-api`, change the line of `hashkit` to:

```toml
hashkit = { version = ">=1.1.0", registry = "../dist-src" }
```

```console
$ tt package resolve
no version moved, but the lock changed:
  manifest hash refreshed
  file digests of "hashkit" pinned anew
$ tt -V package build 2>&1 | grep -E 'Download|running'
   · rocks.Download: downloaded library=go-luarocks name=hashkit version=1.1.0-1 arch=src path=.../kv-api/_build/src/hashkit-1.1.0-1.src.rock
   · running cc -O2 -fPIC -std=gnu99 -Wall -Wextra -Werror -I/usr/include/tarantool -shared -Wl,--as-needed -o .../kv-api/_build/src/hashkit/rock/default/lib/hashkit/fnv.so fnv.c dir=.../kv-api/_build/src/hashkit-1.1.0-1/hashkit/hashkit
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0
$ ls .rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc
LICENSE
```

`../dist-src`, indexed by the pack, has no binary rock, so the lock now pins
the digests of the source rock and the rockspec alone. tt builds the package from the source rock in `_build/src/`, with its
manifest and the compiler of this machine, and installs it at the version
the lock pins, its license included; the build needs the headers of
Tarantool, as the build of the module itself does.

### Work on both side by side

A path dependency builds the module from its working tree, so that a change
to it is tried in the application before it is released. In
`app.manifest.toml` of `kv-api`, change the line of `hashkit` to:

```toml
hashkit = { source = "path", path = "../hashkit" }
```

```console
$ tt package build
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE  ORIGIN
default  hashkit  -           1.1.0-1  path    direct
$ grep -A5 "name = 'hashkit'" app.manifest.lock
name = 'hashkit'
version = '1.1.0-1'
source = 'path'
path = '../hashkit'
content_hash = 'sha256:0b20793689f3ace21b08133d84fc1e04dd5da2e57d46e7e1dcf7cb5485cebbd9'
```

The lock pins the directory and a hash of its files. A change to the module
makes the lock out of date, which a build resolves and `--locked` refuses,
and the module is versioned as its tree is:

```console
$ echo '/* A change in progress. */' >> ../hashkit/hashkit/fnv.c
$ tt package build --locked
   ⨯ lock is out of date: path dependency "hashkit" changed on disk (--locked); run tt package resolve to bring the lock back in step
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0+dirty
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION        SOURCE  ORIGIN
default  hashkit  -           1.1.0.dirty-1  path    direct
$ git -C ../hashkit checkout -q hashkit/fnv.c
```

tt builds the dependency in `_build/path/hashkit/` of `kv-api` and leaves
the directory of the module as it is. The path may be absolute too; the lock
then keeps it absolute, and holds only on a machine with that directory. A
project with a path dependency packs into a `.tt` archive, the license of the
module included, but not into a rock: depend on a released version for that.

## For LuaRocks users

LuaRocks installs the binary rock from a directory of rocks or an HTTP
server of one. `tt rocks` runs LuaRocks itself; from `kv-api/`:

```console
$ cd ..
$ mkdir lr && cd lr
$ tt rocks install hashkit --only-server ../dist
Installing ../dist/hashkit-1.1.0-1.linux-x86_64.rock

hashkit 1.1.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
No existing manifest. Attempting to rebuild...
hashkit 1.1.0-1 is now installed in .../lr/.rocks (license: BSD-2-Clause)

$ tarantool -e "local h = require('hashkit') print(h.VERSION, h.bucket('user:1', 4))" - </dev/null
1.1.0	4
```

The source rock needs tt. Its rockspec is the one of the binary rocks, of
build type `none`, so LuaRocks installs the rock with nothing to build and
no module in it. A platform with no binary rock gets the source rock from
LuaRocks; `tt rocks` sees that LuaRocks built nothing of it, removes it from
the tree again and fails, after LuaRocks has said the rock is installed. The
tree, `.rocks/`, was made for the rock, so it goes as well:

```console
$ cd ..
$ mkdir lr-src && cd lr-src
$ tt rocks install hashkit --only-server ../dist-src
Installing ../dist-src/hashkit-1.1.0-1.src.rock

hashkit 1.1.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
No existing manifest. Attempting to rebuild...
hashkit 1.1.0-1 is now installed in .../lr-src/.rocks (license: BSD-2-Clause)

   ⨯ LuaRocks built nothing of the source rock of a tt package: hashkit 1.1.0-1 is not installed, whatever LuaRocks printed above; tt removed it from the tree again. Install the binary rock of linux-x86_64 from a server that has one, or depend on the rock in app.manifest.toml and run tt package build, which builds the source rock with its manifest
$ ls -A
$ tarantool -e "require('hashkit')" - </dev/null 2>&1 | head -2
(command line):1: module 'hashkit' not found:
	no field package.preload['hashkit']
```

Plain `luarocks` does the same install with no such check: it says the rock
is installed, and `require` of its module fails as above.

Nor does LuaRocks read a rock index:

```console
$ tt rocks install hashkit --only-server ../site
   ⨯ the rock server serves only a rock index: ../site has nothing but tt-rocks-index.json; LuaRocks reads only manifests; install from it with tt package
```

So publish the binary rocks of every platform the users of LuaRocks are on
in a directory of rocks or its HTTP server, beside the index if there is
one.

## Troubleshooting

These messages are not part of the walkthrough.

A warning when the lock is resolved:

```text
⚠ checks resolved to scm-1, which tracks a branch; the lock will not pin fixed code
```

The rock has no other version, or the constraint names it. `scm-1` and
`dev-1` are the code of a branch, at whatever commit it is when installed.
Depend on a release, or ask the author for one: a version tt makes of a
commit is one.

```text
⚠ hashkit resolved to 1.1.0.dirty-1, which was built from uncommitted changes; the lock will not pin fixed code
```

The rock was packed from a tree with changes not committed, and every such
pack of the commit has that version; it is taken only when no other version
fits the constraint. Pack the rocks from a clean checkout, as CI does; on
the consumer's side, a constraint a release fits takes the release instead.

A failure of `--locked` after an upgrade of tt:

```text
⨯ lock is out of date: manifest_hash of the lock is of an older tt; tt hashes the manifest differently now (--locked); run tt package resolve to bring the lock back in step
```

tt hashes the manifest otherwise than the tt that wrote the lock. Run
`tt package resolve`, or a build without `--locked`, and commit the lock: no
version moves, and `tt package resolve` says what it rewrote.

```text
no version moved, but the lock changed:
  manifest hash refreshed
  generated_by is "tt 3.0.0-alpha.2", was "tt 3.0.0-alpha.1"
```

A lock of an older tt may hold `checksum` keys, the md5 of the source of a
rock, which tt no longer locks; the resolve drops them and names the rocks:

```text
  checksum of "hashkit" dropped: tt no longer locks the md5 of the source of a rock
```

Warnings of `tt package pack` of an archive:

```text
⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
```

An archive bundles a tt: one of the runtime cache that `[platform].tt`
allows, else one `tt binary install tt` put into the project, else the tt
that packs it. This one is a prerelease, as every tt 3 is before its
release, which satisfies the constraint by its version without the
prerelease part. Pack an archive meant for production with a released tt. A
rock bundles no tt, and gets no such warning.

```text
⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
```

The archive bundles a Tarantool with no license file beside it: no
`LICENSE`, `LICENSE.md`, `LICENSE.txt` or `COPYING` in the directories the
message lists, its install prefix and `share/tarantool/` of it. A Tarantool
`tt binary install` installed is one, since it comes without its `LICENSE`;
so may be one built from source and run from its build directory. The pack
goes on, and the archive carries no license of Tarantool. Before packing an
archive you ship, put the `LICENSE` of that Tarantool release into one of
the directories listed. A rock bundles no Tarantool, and gets no such
warning.

A file of the rock index that does not match its digest is not installed:

```text
⨯ installing hashkit 1.1.0-1: ... sha256 of hashkit-1.1.0-1.linux-x86_64.rock is ded9d516..., want 0d72753c...: rocks: checksum mismatch; the file does not have the sha256 the lock or the rock server gives for it, so it was not installed; if the rock server published the rock anew on purpose, run tt package resolve to pin what it serves now
```

A rock once published is not to change: publish a new build under a new
`--revision` instead.

The manifest is checked before anything is built. The flags of the compiler
go into `cflags` and `ldflags`, not into `env` of the build, which the
compiler does not read:

```text
⨯ invalid manifest: components.hashkit.build.env.CFLAGS: does nothing on backend "lua-c": put the flags of the compiler into cflags; CFLAGS of the environment tt runs in comes after them
⨯ invalid manifest: components.hashkit.build.env.CPPFLAGS: does nothing on backend "lua-c": put the macros into defines, the directories of headers into include_dirs and other flags of the preprocessor into cflags
```

A field of another backend does nothing on this one, and the OS of
`platforms.<os>` is `linux` or `darwin`:

```text
⨯ invalid manifest: components.hashkit.build.make_target: is a field of backend make, not of "lua-c"
⨯ invalid manifest: components.hashkit.build.output: is a field of backend make, shell or cmake, not of "lua-c"
⨯ invalid manifest: components.hashkit.build.platforms.macos: "macos" is not an OS tt builds for (want linux or darwin)
```

A license file `license_files` names and the project does not have fails
the pack of either rock:

```text
⨯ license_files entry "LICENSE": no such file
```
