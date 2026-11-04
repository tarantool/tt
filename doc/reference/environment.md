# Environment variables

## Scopes and binaries

| Variable | |
| --- | --- |
| `TT_USER_ROOT` | the root of the user scope instead of `$XDG_DATA_HOME/tarantool` or `~/.local/share/tarantool`; an absolute path |
| `TT_SYSTEM_ROOT` | the root of the system scope instead of `/opt/tarantool`; an absolute path |
| `XDG_DATA_HOME` | the user scope is `$XDG_DATA_HOME/tarantool` when it is an absolute path |
| `TT_USE_SYSTEM_TARANTOOL` | any value but empty, `0` or `false`: a project runs on the `tarantool` on `PATH` even when it has its own; installed applications are not affected |
| `TT_CLI_TARANTOOL_PREFIX` | the prefix of the Tarantool headers rocks with a C part are built against; `.env/activate.sh` sets it |
| `TT_PROJECT_DIR` | set by `.env/activate.sh` to the project directory |
| `TT_CLI_EE_USERNAME`, `TT_CLI_EE_PASSWORD` | the customer-zone credentials for Enterprise, TCM and the SDK, when `--credentials-file` is not given |

See [Projects and scopes](../concepts/projects-and-scopes.md) and
[Manage binaries](../how-to/binaries.md).

## Packages

| Variable | |
| --- | --- |
| `TT_REGISTRIES` | the rock servers, comma-separated, instead of `[platform].registries` and the defaults; `--registry` wins over it |
| `SOURCE_DATE_EPOCH` | the time stamped into `version.lua` and the archive; the time of the HEAD commit by default |

A component build and a hook get `TT_OUTPUT_DIR`, `TT_PROJECT_ROOT`,
`TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION`, `TT_PLATFORM_OS` and
`TT_PLATFORM_ARCH`; see [app.manifest.toml](app-manifest.md#componentsnamebuild).
`.tarantool/bin` of the project comes first on the `PATH` they get.

## Connections

| Variable | |
| --- | --- |
| `TT_CLI_USERNAME`, `TT_CLI_PASSWORD` | the user and the password tt connects to Tarantool instances and config storages with |
| `TT_CLI_ETCD_USERNAME`, `TT_CLI_ETCD_PASSWORD` | the user and the password tt connects to etcd with |

Credentials in a URI win over the flags of a command, which win over these
variables.

## Modules and distributions

| Variable | |
| --- | --- |
| `TT_CLI_MODULES_PATH` | the directories of external modules, separated by colons |
| `TT_DIST_CORE` | `tt binary build tt`: the core to build on instead of `[core]`; `--core` wins over it |
| `TT_CLI_BUILD_SSL` | `mage build` and `tt binary build tt`: the TLS backend, `default`, `openssl` or `openssl_static` |

## What tt sets for an instance

A Tarantool instance gets the paths of its [layout](layout.md) as
`TT_*_DEFAULT` variables - `TT_SNAPSHOT_DIR_DEFAULT`, `TT_WAL_DIR_DEFAULT`,
`TT_VINYL_DIR_DEFAULT`, `TT_PROCESS_PID_FILE_DEFAULT`,
`TT_CONSOLE_SOCKET_DEFAULT` and others - which the cluster config overrides.
An instance with `script` gets `TT_WAL_DIR`, `TT_MEMTX_DIR`, `TT_VINYL_DIR`
and `TT_LISTEN`. Under systemd, `TT_SYSTEMD_UNIT` names the unit.

Tarantool itself reads its configuration from `TT_<NAME>` variables: an
option of the cluster config from the path of the option in uppercase with
`_` for `.`, such as `TT_IPROTO_LISTEN`, and a `box.cfg` parameter of a
script instance from its name, such as `TT_MEMTX_MEMORY`. tt adds nothing
to that; set them in `env` of the record in
[instances.yaml](instances-yaml.md).

<!-- TODO(writers): the full list of TT_*_DEFAULT variables tt sets; see
cli/running/instance_spec.go. -->
