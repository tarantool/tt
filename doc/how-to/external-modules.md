# External modules

An external module is an executable that tt runs as a command: `tt <name>`
runs the module `<name>`, with every argument after the name. It adds a
command to tt, or replaces one of tt's own, with no rebuild of tt. To build
commands into tt instead, see
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
`main` the name of its executable in the directory.

A module with no manifest is an executable named `main` that prints the same
`version` and `help`, as YAML, when called with `--description --version`.

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

## Replace a command of tt

A module named like a command of tt replaces it: with an external `cat`
module, `tt cat` runs the module, and `-V` logs that it replaces the
command. A module named like a group of commands, such as `cluster`, takes
over the whole group, and a module answers to the aliases of the command it
replaces. `--internal` (`-I`) runs the command of tt instead:
`tt -I cat`.

The `modules`, `help`, `completion` and `version` commands cannot be
replaced: a module named so is ignored with a warning.

A distribution of tt may be built without external modules (see
[Distributions and modules](../concepts/distributions.md)).
