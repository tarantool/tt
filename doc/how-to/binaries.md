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

Community releases are installed from prebuilt binaries where there are
some - Tarantool on Linux amd64 and arm64 with glibc, tt on Linux amd64 and
arm64 and macOS amd64 - and built from source elsewhere. A commit,
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

`tt rocks` and `tt package build` build a rock with a C part against the
headers of the Tarantool they run: `.tarantool/include/tarantool` for the
Tarantool of the project, `TT_CLI_TARANTOOL_PREFIX` for another one, and
otherwise the prefix that Tarantool was installed into.
