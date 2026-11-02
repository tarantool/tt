# Application templates

`tt create` creates an application from a template.

A template is a built-in one, named by its name (`tt create cluster`), or a
template of your own, given by its path: a directory, or a `.tgz` or
`.tar.gz` archive, which may be named without its extension
(`tt create ./templates/basic`). A path is told from a name by a slash in
it, so a template in the current directory is given as `./<name>`.

The application is created in the `--name` directory of the current
directory, or of the `--dst` directory.

## Built-in templates

The built-in templates are `single_instance`, `cluster`, `vshard_cluster`
and `config_storage`. Each of them creates a project that starts on
localhost as it is: the code of the application in the project directory,
and the pair it runs from, `instances.yaml` and `config.yaml`, in
`.templates/default/`:

```text
$ tt create cluster --name app1
app1/
├── .templates
│   └── default
│       ├── config.yaml
│       └── instances.yaml
└── app.lua
```

`app.file` and the other paths of `config.yaml` are relative to the project
directory, the directory the instances run in; the data, logs and sockets go
to `.tarantool/var/` there. The project is named after its directory;
`vshard_cluster` also writes `app.manifest.toml`, which names it `--name` with
`_` replaced by `-`, and needs `tt package build` for its `vshard`
dependency before it starts. `config_storage` needs Tarantool Enterprise
Edition, which provides the `config.storage` role.

When the template writes `app.manifest.toml`, `tt create` refuses a `--name`
that does not make a package name there - lowercase letters, digits and
dashes, starting with a letter - and creates nothing: `--name my_app` gives
`vshard_cluster` the name `my-app`, `--name My_App` is refused.

## Templates of your own

A template of your own lays a project out the same way when it carries
`.templates/<name>/instances.yaml` and `config.yaml`; the files under
`.templates/` are instantiated like any other.

Application template may contain:

- `*.tt.template` - template files, that will be instantiated during
    application creation.
- `MANIFEST.yaml` - template manifest (see details below).

## MANIFEST.yaml

The template manifest `MANIFEST.yaml` has the following format:

```yaml
description: Template description
vars:
    - prompt: User name
      name: user_name
      default: admin
      re: ^\w+$

    - prompt: Retry count
      default: "3"
      name: retry_count
      re: ^\d+$
pre-hook: ./hooks/pre-gen.sh
post-hook: ./hooks/post-gen.sh
include:
- init.lua
- .templates/default/instances.yaml
- .templates/default/config.yaml
follow-up-message: Run "tt instance start" in {{.name}}.
```

Where:

- `description` (string) - template description.
- `vars` - template variables used for instantiation.
  + `prompt` - user prompt for variable value input.
  + `name` - variable name.
  + `default` - default value of the variable.
  + `re` - regular expression used for value validation.
- `pre-hook` (string) - executable to run before template
    instantiation.
- `post-hook` (string) - executable to run after template
    instantiation.
- `include` (list) - list of files to keep in application directory
    after create.
- `follow-up-message` (string) - message printed after the application
    is created, with the variables instantiated in it.

There are pre-defined variables that can be used in template text:
`name` - application name. It is set to `--name` CLI argument value.

Don't include the .rocks directory in your application template. To
specify application dependencies, declare them in `[dependencies]` of
`app.manifest.toml` (see [Packages](../concepts/packages.md)).

<!-- TODO(writers): an example of a template of your own; doc/examples.md
has one written for tt 2.x. -->

## Variables

`tt create` asks for the value of every variable of `vars`, offering its
default. `--var name=value`, which can be repeated, and `--vars-file FILE`
give values in advance, and `-s` (`--non-interactive`) takes the defaults of
the rest without asking. `-f` overwrites an application directory that
exists.
