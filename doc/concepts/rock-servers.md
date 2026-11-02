# Rock servers

The dependencies of a package are rocks, which come from rock servers: an
HTTP(S) LuaRocks server, or a local directory with a LuaRocks manifest.

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
registry = "https://my.mirror/" }`.

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

<!-- TODO(writers): verify the offline build end to end; a rockspec-only rock
was reported to need its git source (see the v3 features notes). -->

## tt rocks

`tt rocks` runs LuaRocks itself, for the commands `tt package` has no
equivalent of. It queries the servers of `TT_REGISTRIES` or of the manifest
instead of its own defaults when either is set. A rock with a C part is
built against the headers of the Tarantool that runs it: those of the
project's `.tarantool/include/tarantool`, of `TT_CLI_TARANTOOL_PREFIX`, or
the install prefix of that Tarantool.
