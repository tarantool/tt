# Projects and scopes

tt has no configuration file. What it acts on follows from where it runs and
from the `--scope` a command is given: a project, the scope of the current
user, or the scope of the system.

## Projects

A project is the directory of an application under development. A directory
is a project when it holds:

- `app.manifest.toml`, the manifest of a [package](packages.md), or
- an instance template: `instances.yaml`, `config.yaml` or both in
  `.templates/<name>/` (see [Instances](instances.md)).

tt looks for the project from the current directory upward, so the commands
work from any directory inside it: the instance commands, `tt package`,
`tt run` and `tt test`, which run the package in its root, `tt wasm` and the
modules, such as [`tt emmylua`](../how-to/emmylua.md). A command that finds the project above the
current directory says so on stderr (`working on the project in DIR`).
`-C DIR` of the `tt package` commands and `tt test` names the project
itself: DIR is taken as it is, and nothing above it is looked into.
`tt new` creates a project in the current directory, or in DIR with `-C`,
and `tt binary build` works on the distribution in the directory it runs
in. The nearest project wins, so a project nested in another is found from
its own subdirectories. Other files - `.rocks/`, `.git/`, Lua files, a
`config.yaml` elsewhere - do not make a project. Neither does `.tarantool/`:
`tt binary install` creates it in a directory that is not a project yet.

The application of a project is named by `[package].name` of its manifest,
or after the project directory without one.

## Scopes

Binaries, installed applications and the data of their instances live in
one of three scopes, each with its root directory, `<root>`:

| Scope | `<root>` |
| --- | --- |
| `project` | `.tarantool/` in the project directory; the current directory outside a project |
| `user` | `$TT_USER_ROOT`; else `$XDG_DATA_HOME/tarantool` when it is an absolute path; else `~/.local/share/tarantool`. On macOS: `~/Library/Application Support/tarantool` |
| `system` | `$TT_SYSTEM_ROOT`; else `/opt/tarantool`. On macOS: `/Library/Application Support/tarantool` |

`TT_USER_ROOT` and `TT_SYSTEM_ROOT` must be absolute paths. Every root has
the same [layout](../reference/layout.md):

```text
<root>/
├── bin/          # the active tarantool, tt and tcm (links)
├── include/      # the headers of the active tarantool
├── versions/     # installed binaries and, in user and system, applications
├── apps/<app>    # user and system: a link to the active version of <app>
├── etc/<app>/    # user and system: the pair of the host, the systemd record
└── var/          # lib/, log/, run/ of every instance, by application
```

A command acts on one scope and never falls back to another: what it does
must not depend on what happens to be installed elsewhere.

- `tt binary` installs and switches binaries in `--scope project` (the
  default), `user` or `system`.
- `tt package install` installs an archive into a project (the default) or
  makes it a version of an application in the user or the system scope. See
  [Packages](packages.md#installing).
- The instance commands act on `<app>:<instance>`: the current project when
  it is named `<app>`, the system scope otherwise. The user scope is reached
  with `--scope user` only, and `--scope` picks any scope outright.

## The project scope

The project scope is the project's own: `tt binary install` in a project
puts the binaries into its `.tarantool/`, apart from other applications,
and the instances of the project keep their data in `.tarantool/var/`.

The commands that run Tarantool in a project - `tt instance`, `tt run`,
`tt test`, `tt package build` - run `.tarantool/bin/tarantool` when the
project has one, and the first `tarantool` on `PATH` otherwise.
`TT_USE_SYSTEM_TARANTOOL=1` makes a project run on the one on `PATH` even
when it has its own; it does not apply to installed applications. A
Tarantool of another version than the one the lock bundles is a warning,
not an error.

The first `tt binary install` of a project writes `.env/activate.sh` and
`.env/deactivate.sh` into it:

```console
$ source .env/activate.sh     # .tarantool/bin first on PATH, TT_PROJECT_DIR set
$ source .env/deactivate.sh   # back as it was
```

`activate.sh` also points `TT_CLI_TARANTOOL_PREFIX` to `.tarantool`, so that
rocks with a C part are built against the headers of the project's
Tarantool.

## The user and system scopes

The user and system scopes hold installed applications. An application is
a set of versions, `<root>/versions/<app>/<app>_<version>/`, each a package
laid out as a project, and `<root>/apps/<app>` points to the active one. An
installed application runs on the Tarantool its package bundles, whatever
`<root>/bin/tarantool` is. A version is never written to: the instances keep
their data in `<root>/var/`, and the pair of the host, `<root>/etc/<app>/`,
describes what runs on this host apart from the version, so both survive an
upgrade.

For the binaries of these scopes, put `<root>/bin` on `PATH`.
