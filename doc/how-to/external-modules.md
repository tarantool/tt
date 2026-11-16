# External modules

An external module is an executable that tt runs as a command: `tt <name>`
runs the module `<name>`, with every argument after the name. It adds a
command to tt, or replaces one of tt's own, with no rebuild of tt. To build
commands into tt instead, see [Write a tt module](write-a-module.md) and
[Build a distribution of tt](build-distribution.md).

## Write a module

A module is a directory named after the command it adds. It holds a
`manifest.yaml`:

```yaml
version: 1.0.0
help: Upload tt backups to an S3 bucket
main: main
```

`version` is the version of the module, `help` its one-line description and
`main` the name of its executable in the directory; all three are required.
The manifest may be `manifest.yml` as well; with both, tt reads neither, as
if the module had no manifest. tt reads
`tt-version`, `description` and `homepage_url` too, and ignores them.

A module with no manifest is an executable named `main` that prints the same
`version` and `help`, as YAML, when called with `--description --version`.
A reply that is not YAML is taken for the description in plain text: its
first line that is not empty is the `help`, and the version is unknown.

```sh
#!/bin/sh
case "$1" in
--description) echo "Upload tt backups to an S3 bucket"; exit 0 ;;
esac
# ... the command itself
```

tt runs it with `--description --version` only for a list that shows the
module: `tt help`, `tt --help`, `tt modules list` and the shell completion
of a command name. Other commands, `tt version` and the completion of
anything else do not run it, and neither does a run of the module itself.
The reply is kept in the user cache, `<user cache>/tt/modules/`
(`~/.cache/tt/modules/` on Linux, `$XDG_CACHE_HOME/tt/modules/` when
`XDG_CACHE_HOME` is set), by the path, the size and the modification time of
the executable, so a module is asked again once it changes. A module that
fails, exits non-zero or cannot be run, is asked again next time.

`help` describes the module wherever tt lists it: among the commands of
`tt --help`, under its EXTERNAL COMMANDS, in shell completion and in
`tt modules list`. tt takes only its first line, so each module stays one
line in these lists. A longer text belongs in the module's own `--help`,
which `tt help <module>` and `tt <module> --help` print to stdout.

## Make tt find it

tt looks for modules in the directories `TT_CLI_MODULES_PATH` lists,
separated by colons:

```sh
export TT_CLI_MODULES_PATH=/path/to/modules:/path/to/other/modules
```

A relative directory is taken from the working directory tt runs in, so list
absolute paths.

```console
$ tt modules list
$ tt upload-backups --help
```

Each subdirectory of a listed directory with a manifest or a `main` is a
module, a command of tt by the name of the directory: tt neither reads nor
runs it to add the command. When two directories hold a module of the same
name, the first one listed wins, and tt warns of the other. A listed path
that does not exist is skipped; one that exists but is not a directory, or a
directory tt cannot read, fails every command of tt. A module tt cannot
describe - a manifest that does not parse or misses a field, a `main` that
fails when called with `--description --version` or cannot be run - is
listed by its name alone, with a warning from `tt help` and
`tt modules list`; the completion and the other commands say nothing of it.
A module whose manifest cannot be read fails to run.

## How tt runs it

`tt [global flags] <name> [args]` runs the executable with `args`, every
one of them as given, flags included; tt does not parse them, so it
completes no arguments or flags of a module. The global flags before the
name, such as `-V` and `--no-prompt`, are tt's: the module does not get
them. tt adds nothing to the environment of the module, which inherits
tt's own, and runs it in the working directory of tt. The module reads
tt's stdin and writes to tt's stdout and stderr itself.

tt exits with the exit code of the module and prints nothing of its own
when the code is not 0: the module has reported the failure. A module
killed by a signal makes tt exit 255, silently too. tt reports an error
only for an executable it cannot start or the integrity checks refuse.

## With integrity checks

In a distribution built with `integrity = "rsa"`, tt reads the manifest of
a module and its executable - before every run and every `--help` - through
the integrity checks. Under `--integrity-check` a file is refused unless a
signed hashes file of the project, or of the runtimes tt runs, lists it, so
a module outside the signed tree stops working: it fails to run, and the
help of tt warns that it cannot describe it. A module whose manifest is
listed and whose executable is not is listed by tt and fails to run (see
[Sign packages](sign-packages.md#limits)). Without `--integrity-check`
nothing is checked.

## Replace a command of tt

A module named like a command of tt replaces it: with an external
`connect` module, `tt connect` runs the module, and `-V` logs that it
replaces the command. A module named like a group of commands, such as `cluster`, takes
over the whole group, and a module answers to the aliases of the command it
replaces. `--internal` (`-I`) runs the command of tt instead:
`tt -I connect`.

The `modules`, `help`, `completion` and `version` commands cannot be
replaced: a module named so is ignored with a warning.

A distribution of tt may be built without external modules (see
[Distributions and modules](../concepts/distributions.md)).
