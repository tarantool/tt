# Environment variables

## Scopes and binaries

| Variable | |
| --- | --- |
| `TT_USER_ROOT` | the root of the user scope instead of `$XDG_DATA_HOME/tarantool` or `~/.local/share/tarantool`; an absolute path |
| `TT_SYSTEM_ROOT` | the root of the system scope instead of `/opt/tarantool`; an absolute path |
| `XDG_DATA_HOME` | the user scope is `$XDG_DATA_HOME/tarantool` when it is an absolute path |
| `TT_USE_SYSTEM_TARANTOOL` | any value but empty, `0` or `false`: a project runs on the `tarantool` on `PATH` even when it has its own; installed applications are not affected |
| `TT_CLI_TARANTOOL_PREFIX` | the prefix of the Tarantool headers, `<prefix>/include/tarantool`, that rocks with a C part and `c` and `lua-c` components are built against, instead of the ones tt finds beside the `tarantool`, or of its version in its `CMAKE_INSTALL_PREFIX` or `/usr`; taken whatever version they are of, a warning when it is not the version of the `tarantool` ([Headers for rocks with a C part](../how-to/binaries.md#headers-for-rocks-with-a-c-part)); `.env/activate.sh` sets it |
| `TT_PROJECT_DIR` | set by `.env/activate.sh` to the project directory |
| `TT_CLI_EE_USERNAME`, `TT_CLI_EE_PASSWORD` | the customer-zone credentials for Enterprise, TCM and the SDK, when `--credentials-file` is not given |
| `GITHUB_TOKEN` | the token `tt binary install tt` lists the releases of tt with in the GitHub API, which it does for a version without `--source-build` and, without a version, when the latest release on GitHub is not one of tt 3; it lifts the rate limit of anonymous requests. A listing the API refuses leaves the version to the Git tags, which needs `git` |
| `CMAKE_POLICY_VERSION_MINIMUM` | the policy version CMake 4 configures a project of an older CMake with; tt sets it to `3.5` for the `cmake` and `make` that build Tarantool from source and for the rocks it builds, unless it is set, the empty value included |

CMake 4 refuses to configure a project that asks for a CMake older than 3.5
with `cmake_minimum_required`, as a Tarantool commit of 2024 or older and
many rocks do, unless `CMAKE_POLICY_VERSION_MINIMUM` says the policy version
to assume. CMake 3 does not read the variable. tt does not set it for the
components of a project and their hooks, which are the user's code, nor for
the Tarantool that `tt run` and `tt test` start.

See [Projects and scopes](../concepts/projects-and-scopes.md) and
[Manage binaries](../how-to/binaries.md).

## Packages

| Variable | |
| --- | --- |
| `TT_REGISTRIES` | the rock servers, comma-separated, instead of `[platform].registries` and the defaults; `--registry` wins over it |
| `SOURCE_DATE_EPOCH` | the time stamped into `version.lua` and the archive; the time of the HEAD commit by default |
| `TT_PROJECT_LOCK` | set by tt while it holds the lock of a project, `_build/tt.lock`, for the commands it runs: a tt among them works on that project under the lock rather than wait for it; a value naming a lock nobody holds is ignored. Not to be set by hand |

A component build and a hook get `TT_PROJECT_ROOT`, `TT_PACKAGE`,
`TT_VERSION`, `TT_PLATFORM_OS` and `TT_PLATFORM_ARCH`, the platform of the
build as `GOOS` and `GOARCH` spell it - the machine, or that of `--target`
(see [Other platforms](app-manifest.md#other-platforms)) - and
`TT_BUILD_JOBS`,
the number of jobs a build may run at once - that of `-j`, `--jobs`, or the
number of CPUs - for `make -j"$TT_BUILD_JOBS"` of a script, and, when tt knows
the Tarantool it builds for, `TT_TARANTOOL_PREFIX` and
`TT_TARANTOOL_INCLUDE_DIR`, the directory of its `module.h` and `lua.h`; see
[app.manifest.toml](app-manifest.md#componentsnamebuild). `.tarantool/bin`
of the project comes first on the `PATH` they get. A component build also
gets `TT_OUTPUT_DIR`, where it puts the modules it makes,
`TT_COMPONENT_NAME` and `TT_BUILD_DIR`, `_build/component/<component>/`,
the directory of its own for what it makes on the way, out of the sources,
and a `cargo` build `CARGO_TARGET_DIR`, `_build/cargo/`, unless
`CARGO_TARGET_DIR` or `CARGO_BUILD_TARGET_DIR` is set; both are in
`_build/target/<os>-<arch>/` instead for a build for another platform.

## WebAssembly components

| Variable | |
| --- | --- |
| `TT_WASM_RELEASES` | a JSON file of tarantool-wasm releases `tt wasm` uses instead of the ones it knows: the assets of a mirror, say |
| `XDG_CACHE_HOME` | `tt wasm` caches what it downloads in `tt/wasm/` of it, `~/.cache/tt/wasm` by default; `~/Library/Caches/tt/wasm` on macOS. `tt emmylua` keeps the programs it runs outside a project, and their configuration, in `tt/emmylua/` of it (see [Layout](layout.md#the-user-cache)) |

The file of `TT_WASM_RELEASES` maps a version to its release: the native
module of each platform, checked against its sha256, and the SDK archive the
rockspec of the `wasm` rock points at. An asset is downloaded from
`<base_url>/v<version>/<asset>`; without `base_url`, from the releases of
tarantool-wasm-rs on GitHub.

```json
{
  "0.1.0": {
    "version": "0.1.0",
    "rock_revision": 1,
    "base_url": "https://mirror.example.com/tarantool-wasm",
    "sdk_asset": "tarantool-wasm-0.1.0-sdk.tar.gz",
    "sdk_md5": "f986e42dd8d430110c06e9fb2963209b",
    "platforms": [
      {
        "goos": "linux",
        "goarch": "amd64",
        "asset": "tarantool-wasm-0.1.0-linux-x86_64.so",
        "sha256": "65866215ce6424389381644ef65adc3906f896d46a03843f213baf16df758585",
        "rock_arch": "linux-x86_64",
        "module": "wasm.so"
      },
      {
        "goos": "darwin",
        "goarch": "arm64",
        "asset": "tarantool-wasm-0.1.0-macos-arm64.dylib",
        "sha256": "bf2960db7dfe3535f7b6facecd2b33c4dbd9479f90b691e1b964a2499ef5a38d",
        "rock_arch": "macosx-aarch64",
        "module": "wasm.dylib"
      }
    ]
  }
}
```

`rock_arch` is the LuaRocks arch of the rock, which is what go-luarocks
takes a binary rock for, and `module` the file the rock carries in `lib/`:
`wasm.dylib` on macOS, where Tarantool loads C modules as `?.dylib` only.
See [Run WebAssembly components](../how-to/wasm-components.md).

## Connections

| Variable | |
| --- | --- |
| `TT_CLI_USERNAME`, `TT_CLI_PASSWORD` | the user and the password tt connects to Tarantool instances by URI and config storages with |
| `TT_CLI_ETCD_USERNAME`, `TT_CLI_ETCD_PASSWORD` | the user and the password tt connects to etcd with |

Credentials in a URI win over the flags of a command, which win over these
variables. An instance of an application, `<app>:<instance>`, takes the
credentials of the flags only: `tt connect --binary`, `tt data play` and the
`tt backup` commands log in to it as guest without them, whatever
`TT_CLI_USERNAME` says (see
[Over iproto](../concepts/instances.md#over-iproto)).

## Modules and distributions

| Variable | |
| --- | --- |
| `TT_CLI_MODULES_PATH` | the directories of external modules, separated by colons |
| `TT_DIST_CORE` | `tt binary build tt`: the core to build on instead of `[core]`; `--core` wins over it |
| `TT_CLI_BUILD_SSL` | `mage build` and `tt binary build tt`: the TLS backend, `default`, `openssl` or `openssl_static` |

## What tt sets for tt run

`tt run` started below the project root puts the root at the head of
`LUA_PATH` (`<root>/?.lua;<root>/?/init.lua`) and `LUA_CPATH`
(`<root>/?.so`, `<root>/?.dylib` on macOS), so `require` finds the sources of
the root as it does there. A value already set follows them; an unset one
becomes `;;`, the default paths. In the root, the environment is passed
unchanged. `.rocks/` needs nothing: Tarantool searches it upward from the
directory it runs in. `tt test` runs luatest in the root and sets neither.

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
[instances.yaml](instances-yaml.md). tt reads them, those of the record and
of its own environment, into the config it dials an instance by:
`TT_IPROTO_ADVERTISE_CLIENT` in the environment of `tt connect --binary`
makes it dial that address (see
[Over iproto](../concepts/instances.md#over-iproto)).

<!-- TODO(writers): the full list of TT_*_DEFAULT variables tt sets; see
cli/running/instance_spec.go. -->
