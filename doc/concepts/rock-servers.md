# Rock servers

The dependencies of a package are rocks, which come from rock servers: an
HTTP(S) LuaRocks server, or a local directory with a LuaRocks manifest. Either
may publish a [rock index](#a-rock-index) instead of the manifest.

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

## Searching

`tt package search TERM` asks every server for rocks whose name contains
TERM and lists every version each one offers. Unlike resolution, it does not
stop at the first server.

## A mirror for offline builds

`tt package download --dir DIR` downloads rocks into DIR and indexes them,
so that DIR is a rock server itself. Without arguments it mirrors the
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
