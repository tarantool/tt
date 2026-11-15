# Manage binaries

`tt binary` installs, lists, switches and uninstalls the programs
applications run on:

| Product | What |
| --- | --- |
| `tarantool` | Tarantool Community Edition, 3.0 and newer; Enterprise with an `ee` version |
| `tarantool-ee` | Tarantool Enterprise Edition |
| `tt` | tt; tt Enterprise with an `ee` version |
| `tt-ee` | tt Enterprise |
| `tcm` | Tarantool Cluster Manager |
| `tarantool-sdk` (`sdk`) | the Enterprise SDK: Tarantool Enterprise, tt Enterprise, TCM and headers |

Every command acts on one [scope](../concepts/projects-and-scopes.md):
`--scope project` (the default), `user` or `system`, and never falls back to
another.

## Install and switch

```console
$ tt binary install tarantool                 # the latest Community release, into the project
$ tt binary install tarantool 3.8 --scope user
$ tt binary list
$ tt binary switch tarantool 3.8.1
$ tt binary uninstall tarantool 3.9.0
```

The version installed last becomes the active one: `<root>/bin/tarantool`
points to it, and `<root>/include/tarantool` to its headers. Each version is
kept in `<root>/versions/<product>/<product>_<version>/`, with the headers in
its `include/tarantool/`. Tarantool Community and Enterprise share the
active `tarantool` link, and tt and tt Enterprise share `tt`.

To use the active binaries, put `<root>/bin` on `PATH` for the user and the
system scopes, and `source .env/activate.sh` in a project. The first
`tt binary install` of a project writes `.env/activate.sh` and
`.env/deactivate.sh`; existing scripts are kept. The instance commands of a
project do not need them: they run the project's Tarantool by themselves.
Nor do the builds of its components: a build backend and a hook find the
programs of `.tarantool/bin` ahead of `PATH`.

Community releases are installed from prebuilt binaries where there are
some - Tarantool on Linux amd64 and arm64 with glibc, from the static RPMs
of rws.tarantool.org, tt on Linux amd64 and arm64, tt 3 on macOS arm64 and
tt 2 on macOS amd64, from the releases of tt on GitHub - and built from
source elsewhere. A distribution of tt may have other sources, or none
(`bundle_sources` in [tt.manifest.toml](../reference/tt-manifest.md)):
without a source of Tarantool or tt every version of it is built from
source. A commit,
`master`, a pull request (`pr/<number>`) and `--local-repo` are always built
from source, and `--source-build` builds a release from source too.
Enterprise search and installation of tt is available on Linux amd64 only.

Uninstalling the active version selects the latest remaining one in the same
scope; without a version, `uninstall` removes the only installed one.

## Choose a version

- Community Edition: `3`, `3.8` or `3.8.1` select the latest matching
  release; no version selects the latest one.
- Enterprise Edition: append `ee` - `3ee`, `3.8ee`, `3.8.1ee` - to
  `tt binary install tarantool` or `tt binary install tt`. This selects the
  latest matching stable build; Tarantool Enterprise is a `gc64` one by
  default. A complete archive name, such as `gc64-3.8.0-0-r94`, a `nogc64`
  one or a release candidate, selects that build.
- TCM: `1`, `1.3` or `1.3.1` select the latest matching stable build.
- The SDK: like Enterprise - no version, `X`, `X.Y` or `X.Y.Z` select the
  latest stable `gc64` SDK, and a complete archive name an exact one.

A standalone Tarantool Enterprise release is installed under its Tarantool
version, `3.8.1`, without the `gc64` prefix, the revision, or the hash:
`gc64-3.8.1-0-r93` and `gc64-3.8.1-0-r94` both install into
`versions/tarantool-ee/tarantool-ee_3.8.1/`, and installing another
revision keeps the one there unless `--reinstall` is given. Prereleases and
debug builds stay apart: `3.8.1-rc1`, `debug-3.8.1`.

`tt binary switch` and `tt binary uninstall` take `X.Y.Z` for Enterprise,
tt Enterprise and TCM, matching the versions installed in the scope; `X`
and `X.Y` are not expanded. An exact installed name wins; otherwise exactly
one installation must match, and an ambiguous request changes nothing.

### Development builds

`--dev` installs development builds of Tarantool Enterprise, the SDK or
TCM. They keep their full archive version with a `dev_` prefix and live
next to the releases:

```console
$ tt binary install tarantool 3.8.1ee --dev
$ tt binary switch tarantool dev_gc64-3.8.1-0-r94
```

`list`, `switch` and `search --dev` show `dev_<full-version>`; selectors
without `dev_` match releases only.

## Enterprise credentials

Searching and downloading Enterprise, TCM and the SDK needs customer-zone
credentials. `--credentials-file` reads them from a file - the login on the
first line, the password on the second - that must not be readable by the
group or others:

```console
$ chmod 0600 ./credentials
$ tt binary search tarantool-ee --credentials-file ./credentials
$ tt binary install tcm 1.3.1-0-g074b5ffa --credentials-file ./credentials
```

Without the flag, the credentials come from `TT_CLI_EE_USERNAME` and
`TT_CLI_EE_PASSWORD`, or from a prompt.

## The Enterprise SDK

`tt binary install tarantool-sdk` installs Tarantool Enterprise, tt
Enterprise, TCM and the headers from one SDK archive. Its components are
installed under `sdk-<bundle-version>` identities, apart from standalone
versions - `versions/tarantool-ee/tarantool_sdk_<bundle-version>/`,
`versions/tcm/tcm_sdk_<bundle-version>/`,
`versions/tt-ee/tt_sdk_<bundle-version>/` - and a record in
`versions/tarantool-sdk/` tells which belong together.

```console
$ tt binary search sdk
$ tt binary install tarantool-sdk 3.7
$ tt binary switch sdk 3.7.1                # every component and the headers
$ tt binary switch tcm sdk-3.7.1            # one component
$ tt binary switch tt-ee sdk-3.7.1          # the tt Enterprise of the SDK
$ tt binary switch tt 3.0.0                 # back to an ordinary tt
```

Uninstalling an SDK removes its components and keeps the versions selected
apart from it. Switching or uninstalling an SDK needs an unambiguous
installed version.

## Install from local files

| Command | Takes |
| --- | --- |
| `tt binary install tt --local-repo DIR` | a Git clone of tt in `DIR/tt` |
| `tt binary install tarantool --local-repo DIR` | a Git clone of Tarantool in `DIR/tarantool` |
| `tt binary install tarantool 3ee --local-dir DIR` | SDK archives `tarantool-enterprise-sdk-*.tar.gz` in `DIR` |
| `tt binary install tt 2ee --local-dir DIR` | tt Enterprise archives `tt-ee-*.tar.gz` in `DIR` |
| `tt binary install tarantool-sdk --local-dir DIR` | SDK archives, every component |
| `tt binary install tcm --local-dir DIR` | TCM archives `tcm-*.tar.gz` in `DIR` |

`tt binary search <product>` takes the same flags. Put the flag after the
product name; a relative `DIR` is taken from the current directory.

## Search

```console
$ tt binary search tarantool
$ tt binary search tarantool-ee --credentials-file ./credentials
$ tt binary search sdk
```

`search` marks the versions installed and active in the selected scope; the
scope does not change where the available versions come from. An SDK is
shown as active when all of its components are.

## Headers for rocks with a C part

`tt rocks` and `tt package build`, `install` and `pack` build a rock with a
C part, and a `c` or `lua-c` component, against the headers of the
Tarantool they run, `<prefix>/include/tarantool`. The prefix is the first
of:

1. `.tarantool` for the Tarantool of the project, then
   `TT_CLI_TARANTOOL_PREFIX`: taken as they are, headers there or not.
2. A directory of the executable that holds `include/tarantool/module.h`:
   first along its real path, symbolic links resolved, then along the path
   it was found by. Of each, the directory above the one of the executable
   comes first (`<prefix>/bin/tarantool`), then the directory of the
   executable itself (an Enterprise SDK, or
   `<root>/versions/tarantool/tarantool_<version>/` of `tt binary install`).
   A `tarantool` of the user or system scope found on `PATH` as
   `<root>/bin/tarantool` gets the headers of its own version, and the
   active ones of `<root>/include/tarantool` when its version has none.
3. The `CMAKE_INSTALL_PREFIX` that `tarantool --version` reports, then
   `/usr`, when its `include/tarantool/module.h` is there and of the
   version of the `tarantool`, major.minor.patch. That prefix is where the
   build was to be installed, which a relocated or static build is not,
   and `/usr` may hold the headers of another Tarantool, installed from
   the packages of the system: headers of another version there are not
   taken.

Headers of another version than the `tarantool` taken from 1 or 2 are a
warning, naming both versions and the directory of the headers: a module
built against them may not load.

With no headers found, what has no C part builds all the same, and a `c`
or `lua-c` component fails before anything is built, listing the
directories it looked into, and why the headers of another version it
found were not taken:

```text
   ⨯ building component "ipc": tarantool development headers not found: cannot compile c/lua-c component: none found for /opt/bin/tarantool, no module.h in /opt/include/tarantool, /opt/bin/include/tarantool, /tarantool/static-build/tarantool-prefix/include/tarantool; /usr/include/tarantool: headers of 3.9.0-0-g6c3e9c0, the tarantool is 3.8.1-0-ge643f88a5e0c: set TT_CLI_TARANTOOL_PREFIX to the install prefix of Tarantool, the directory holding include/tarantool
```

A `tt rocks` command that builds a rock adds the same to its error. With no
Tarantool found at all, `tt package build`, `fetch` and `pack` build what
has no C part, and a rock or a `c` or `lua-c` component with one fails for
want of the headers.
