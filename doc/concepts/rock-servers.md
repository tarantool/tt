# Rock servers

The dependencies of a package are rocks, which come from rock servers: an
HTTP(S) LuaRocks server, or a local directory with a LuaRocks manifest. Either
may publish a [rock index](#a-rock-index) beside the manifest or instead of
it.

## Which servers

The list of servers is taken from the first of these layers that names any:

1. `--registry`, which can be repeated, of the command;
2. `TT_REGISTRIES`, comma-separated;
3. `[platform].registries` of `app.manifest.toml`;
4. the built-in defaults: `https://rocks.tarantool.org/` and
   `https://luarocks.org/`.

The layers replace one another rather than merge: resolution takes a rock
from the first server that has it, so a server appended to the defaults
would change where a rock comes from. A relative directory is taken from the
current directory in `TT_REGISTRIES` and from the manifest's directory in the
manifest.

`tt registry list` prints the list a command would use, and the layer of
each server:

```console
$ tt registry list
URL                           SOURCE
https://rocks.tarantool.org/  default
https://luarocks.org/         default
```

A dependency may name a server of its own: `crud = { version = "1.5.2",
registry = "https://my.mirror/" }`. The rock is then resolved from that
server and installed from it, whatever the list is; the rocks it depends on
come from the list. A directory there is taken from the manifest's directory,
as in `[platform].registries`, whichever directory tt runs in.

## A rock index

A server may publish a `tt-rocks-index.json` where its LuaRocks manifest
would be. The index names every file of a rock version by its URL, with its
sha256: the rock of each platform, and the rockspec apart from them. The files
may be elsewhere, on release pages for instance, while the index is a file
in git:

```json
{
  "format": 1,
  "repository": {
    "emmylua": {
      "0.30.0pre1-1": {
        "artifacts": [
          {
            "arch": "linux-x86_64",
            "sha256": "…",
            "size": 19777374,
            "url": "https://github.com/tarantool-contrib/rocks-repo/releases/download/emmylua-0.30.0pre1-1/emmylua-0.30.0pre1-1.linux-x86_64.rock"
          }
        ],
        "rockspec": {
          "sha256": "…",
          "url": "emmylua-0.30.0pre1-1.rockspec"
        }
      }
    }
  }
}
```

A `url` is absolute `https`, or relative to the directory of the index, and
its last segment is the name of the file: `<name>-<version>.<arch>.rock` or
`<name>-<version>.rockspec`. A field tt does not know is ignored; an index of
a newer `format` is an error that asks to upgrade tt.

A version may be withdrawn with `"yanked": true`. Resolving afresh passes it
over - `tt package add`, `tt package update`, a first build - while a lock
that pins it keeps resolving to it and installs it: holding the versions a
lock has is what every command but `tt package update` does. A constraint
only a yanked version satisfies fails with a note to pin it with `==`.

tt reads the index before any manifest, from an HTTP server and from a
directory alike. When it is there, it is the whole catalogue of the server;
when it is there and does not read, the server is in error rather than read
through its manifests. From such a server:

- resolving reads the rockspec of a version, checked against its sha256,
  and downloads no rock;
- the lock pins the sha256 of every file of the version, under `artifacts`:

  ```toml
  [[lock.products.default.dependencies]]
  name = 'emmylua'
  version = '0.30.0pre1-1'
  source = 'registry'
  artifacts = {linux-x86_64 = 'sha256:…', macosx-aarch64 = 'sha256:…', rockspec = 'sha256:…'}
  ```

- every install of the rock - by `tt package build`, `tt package fetch` and
  `tt test`, and by `tt package install` of an archive packed
  `--without-deps`, from the lock of the archive - checks the file of its
  platform against those digests before it unpacks it, whichever server
  serves it. A file that does not match is not installed. A platform the
  lock pins no file for is an error too: `tt package resolve` pins the files
  the server serves now.

### Writing an index

tt writes the index of every directory it makes a rock server of, beside the
LuaRocks manifests and listing the same rocks: `tt package pack --out`,
`tt package download --dir` and `tt wasm rock --out`. A project that depends
on a rock from such a directory pins the sha256 of its files in the lock.
`tt package index DIR` writes it for a directory filled otherwise, with the
rocks the jobs of a CI matrix packed, say:

```console
$ tt package index dist
.../dist/tt-rocks-index.json
indexed 2 version(s) of .../dist
```

It lists every `<name>-<version>.<arch>.rock` and `<name>-<version>.rockspec`
of DIR at a URL relative to the index, its name, with its sha256 and size,
and writes the LuaRocks manifests too, so DIR serves LuaRocks as well. Since
tt reads the index alone when there is one, a rock copied into such a
directory by hand is served once the index is written again.

An index kept apart from the rocks, a file in a git branch of its own with
the rocks on release pages, is named with `--index`, and the URL the files
are downloaded from with `--base-url`:

```console
$ gh release upload v1.0.0 dist/*.rock dist/*.rockspec
$ tt package index dist --index index/tt-rocks-index.json \
    --base-url https://github.com/example/mylib/releases/download/v1.0.0
```

Without `--base-url` the URLs are relative to the index, which DIR is then
below: `tt package index site/v1.0.0 --index site/tt-rocks-index.json`
names the files `v1.0.0/<file>`. With either flag, only the index is
written.

An index that is there is updated, not replaced. The versions of DIR are
listed anew from its files, and one marked `"yanked": true` stays so, which
is how a version is yanked: by hand, in the index. A version the index lists
with files of DIR that are gone is dropped; one it lists with files
elsewhere, those of another release say, is kept. A version of DIR the index
lists with files elsewhere is an error: a published version does not
change, so a new build of it is a new revision (`tt package pack
--revision`). The same index is written as the same bytes, its keys sorted,
so a commit of it shows what changed; a field tt does not know is not kept.
[A rock index for release pages](../how-to/rocks.md#a-rock-index-for-release-pages)
walks through both, an index beside the rocks and one apart from them, for
a module and the project that depends on it.

## Searching

`tt package search TERM` asks every server for rocks whose name contains
TERM and lists every version each one offers. Unlike resolution, it does not
stop at the first server.

## A mirror for offline builds

`tt package download --dir DIR` downloads rocks into DIR and indexes them,
with the LuaRocks manifests and a [rock index](#writing-an-index), so that
DIR is a rock server itself. Without arguments it mirrors the
locked dependencies of the project at their exact versions, dev ones
included; with names it downloads those.

```console
$ tt package download --dir ./vendor-rocks
$ TT_REGISTRIES=./vendor-rocks tt package build
```

Running it again extends the mirror in place. A dependency that does not
come from a rock server, a `path` one, has no rock to mirror and is skipped
with a warning.

`tt wasm rock` packs a server of the same kind for the `wasm` rock of
tarantool-wasm: the rock of every platform of a release, with its index; see
[Run WebAssembly components](../how-to/wasm-components.md#share-the-rock).

<!-- TODO(writers): verify the offline build end to end; a rockspec-only rock
was reported to need its git source (see the v3 features notes). -->

## tt rocks

`tt rocks` runs LuaRocks itself, for the commands `tt package` has no
equivalent of. It queries the servers of `TT_REGISTRIES` or of the manifest
instead of its own defaults when either is set. A rock with a C part is
built against the headers of the Tarantool that runs it: those of the
project's `.tarantool/include/tarantool`, of `TT_CLI_TARANTOOL_PREFIX`, or
the install prefix of that Tarantool.

LuaRocks reads manifests only, not a [rock index](#a-rock-index). A server
that publishes nothing but the index is left out of those servers, and of
`TT_CLI_REPO_ROCKS`, with a warning; one named with `--server` or
`--only-server` is an error. A directory is such a server when it holds
`tt-rocks-index.json` and no manifest; an HTTP(S) server when a `HEAD` of
`tt-rocks-index.json` succeeds and one of each manifest answers 403, 404 or
410. A server that does not answer is kept, for LuaRocks to report on.

LuaRocks builds nothing of the source rock of a tt package, `--format
rock-src`: its rockspec is that of the binary rocks, of build type `none`,
so LuaRocks installs it with no module in it, only the rockspec and the
license files in `doc/`; of a package with no `[package].repository`, it
fails to unpack the rock instead. `tt rocks` fails a command that installed such a
rock, `install` and `build` say, and removes the rock from the tree again,
so that it satisfies no dependency on it; install the binary rock of the
platform instead, or depend on the rock in `app.manifest.toml` and run
`tt package build`, which builds the source rock with its manifest. LuaRocks
has printed that the rock is installed by then, and the error of tt, which
follows, says it is not. A tree the command made, which holds no rock once
the source rock is gone, is removed whole, its manifest and lock file with
it. The check covers the tree of the working directory or the one `--tree`
names.
Plain `luarocks` does the same install with no such check: it says the rock
is installed, and `require` of its module fails. What LuaRocks installs of
each rock `tt package pack` makes is in
[For LuaRocks users](../how-to/rocks.md#for-luarocks-users).
