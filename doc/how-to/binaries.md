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
another. The project scope is the `.tarantool` of the project the current
directory is in, from any of its subdirectories; outside a project it is the
`.tarantool` of the current directory, and the first `tt binary install`
there says so.

## Install and switch

```console
$ tt binary install tarantool                 # the latest Community release, into the project
$ tt binary install tarantool 3.8 --scope user
$ tt binary list
$ tt binary switch tarantool 3.8.1
$ tt binary uninstall tarantool 3.9.0
```

`tt binary list` prints one line per installed version, the product first,
the active version marked `[active]`:

```console
$ tt binary list
tarantool     3.9.0 [active]
tarantool     3.8.2
tarantool-ee  3.8.1
```

The list is the only thing on stdout, coloured only on a terminal and not
with `NO_COLOR` set; `-o json` and `-o yaml` give the same data.

`tt binary switch tarantool` with no version shows a menu of the installed
versions of both editions, Enterprise ones as `3.8.1ee`, and `tt binary
switch` alone asks for the product first. The menus need a terminal: in a
script, a missing version is an error that names the installed ones.

The version installed last becomes the active one: `<root>/bin/tarantool`
points to it, and `<root>/include/tarantool` to its headers. Each version is
kept in `<root>/versions/<product>/<product>_<version>/`, with the headers in
its `include/tarantool/`, the Lua files of Tarantool - the EmmyLua stubs of
its built-in modules, 3.9 and later, that `tt emmylua` finds beside the
executable - in its `share/tarantool/`, and the license files of the
release, `LICENSE` and `AUTHORS`, at its root, whichever way it was
installed. Tarantool Community and Enterprise share the
active `tarantool` link, and tt and tt Enterprise share `tt`.

To use the active binaries, put `<root>/bin` on `PATH` for the user and the
system scopes, and `source .env/activate.sh` in a project, or
`source .env/activate.fish` in fish. The first `tt binary install` of a
project writes `.env/activate.sh`, `.env/deactivate.sh` and their fish
twins `activate.fish` and `deactivate.fish`; existing scripts are kept.
The links of `<root>/bin` and `<root>/include` are relative,
`../versions/...`, and the scripts find the project from their own path in
bash, zsh and fish, so a project can be moved or copied and keeps working.
A shell that does not tell a sourced script its path, such as dash, gets the
directory the project had when the script was written. The instance commands of a
project do not need them: they run the project's Tarantool by themselves.
Nor do the builds of its components: a build backend and a hook find the
programs of `.tarantool/bin` ahead of `PATH`. Nor do the builds of rocks,
those of its dependencies and those of `tt rocks`: a rock that runs
`tarantool` by name, as the cmake of a rock that looks for it does, runs
the project's.

Community releases are installed from prebuilt binaries where there are
some - Tarantool on Linux amd64 and arm64 with glibc, from the static deb
packages of rws.tarantool.org, whose executable keeps its debug information
for `tt coredump inspect`, tt on Linux amd64 and arm64, tt 3 on macOS arm64
and tt 2 on macOS amd64, from the releases of tt on GitHub - and built from
source elsewhere. tt is the exception: the releases of tt on GitHub name
the platforms they publish, so on another one, macOS amd64 for tt 3 say,
`tt binary install tt` fails naming the platforms there are, and
`--source-build` builds it from source. A distribution of tt may have other
sources, or none
(`bundle_sources` in [tt.manifest.toml](../reference/tt-manifest.md)):
without a source of Tarantool or tt every version of it is built from
source. A commit,
`master`, a pull request (`pr/<number>`) and `--local-repo` are always built
from source, and `--source-build` builds a release from source too.
Enterprise search and installation of tt is available on Linux amd64 only.

A source build works in a temporary directory of `TMPDIR`. Ctrl-C or
`SIGTERM` stops the build and removes the directory and the build log
before tt exits; `--no-clean` keeps them and names them.

Uninstalling the active version selects the latest remaining one in the same
scope; without a version, `uninstall` removes the only installed one.

## Build dependencies

Before it builds Tarantool or tt from source, tt checks that the programs and
the packages of the build are installed. What is missing makes it stop and
name the packages with the command that installs them:

```console
$ tt binary install tarantool 3.9.0 --source-build --scope user
   • Installing tarantool in the user scope...
   • Searching in versions...
   • Checking existing...
   • Installing tarantool=3.9.0
   • Checking dependencies...
   ⨯ The operation requires some dependencies.
   ⨯ Missing packages: cmake autoconf automake libtool
     You can install them by running command:
      sudo apt install cmake autoconf automake libtool
     Use --force if you already have those packages installed
```

A build of Tarantool runs Git, CMake, `make`, a C and a C++ compiler, and
autoconf, automake and libtool, which configure the libunwind it bundles.
The static build, the one without `--dynamic`, also downloads and builds
OpenSSL, ICU, readline, ncurses and zlib, and runs perl and patch for them;
`--dynamic` links those of the system instead, and needs their development
packages. A build of tt runs Git and Mage.

tt knows the packages of Debian and Ubuntu, of RHEL, CentOS, Fedora, Rocky
Linux, AlmaLinux, Oracle Linux and Amazon Linux, of Gentoo, Arch Linux and
Alpine Linux, of the distributions derived from them, as `ID_LIKE` of
`/etc/os-release` tells, and of macOS with Homebrew. `--force` (`-f`) skips
the check, for the programs installed elsewhere than from packages.

On another system, openSUSE or Void Linux say, tt cannot check the
dependencies and asks whether to build Tarantool without the check.
`--yes` (`-y`) answers yes. The question is asked on a terminal only: in a
script, a pipe or a container run without `-t`, and under the global
`tt --no-prompt`, the installation fails at once, with nothing read from
the standard input, unless `--yes` or `--force` is given:

```console
$ tt binary install tarantool 3.9.0 --source-build --scope user </dev/null
   • Installing tarantool in the user scope...
   • Searching in versions...
   • Checking existing...
   • Installing tarantool=3.9.0
   • Checking dependencies...
   ⨯ cannot check the build dependencies on an unknown OS: no answer to the confirmation: the standard input is not a terminal; pass --yes to confirm without a prompt, or --force to skip the check
```

Installing `master` over an installed `master` asks whether to look for
newer commits and rebuild, which `--yes` answers yes too. Off a terminal or
under `--no-prompt`, without `--yes`, the installed `master` is kept, and
tt says that it was not checked.

## Choose a version

- Community Edition: `3`, `3.8` or `3.8.1` select the latest matching
  release; no version selects the latest one. For tt, the latest is the
  newest release of tt 3, the major version of the tt that installs it:
  the newest stable one, or the newest pre-release while tt 3 has no stable
  release; tt 2 is installed only by its version, `2.15.0`. A tt release is
  installed as `versions/tt/tt_<version>/`, `tt_3.0.0`, without the `v` of
  its tag; one installed before as `tt_v<version>` is used as it is.
- Enterprise Edition: append `ee` - `3ee`, `3.8ee`, `3.8.1ee` - to
  `tt binary install tarantool` or `tt binary install tt`. This selects the
  latest matching stable build; Tarantool Enterprise is a `gc64` one by
  default. A complete archive name, such as `gc64-3.8.0-0-r94`, a `nogc64`
  one or a release candidate, selects that build.
- TCM: `1`, `1.3` or `1.3.1` select the latest matching stable build.
- The SDK: like Enterprise - no version, `X`, `X.Y` or `X.Y.Z` select the
  latest stable `gc64` SDK, and a complete archive name an exact one.

A Community release to be installed from a prebuilt binary is looked up
among the prebuilt binaries, so it needs no Git: the deb packages of
rws.tarantool.org for Tarantool, the releases of tt on GitHub for tt, which
tt lists with the GitHub API (`GITHUB_TOKEN` lifts its rate limit). `3`
there is the latest release of Tarantool 3 that has a prebuilt binary. What
they do not have - a development tag, a release with no prebuilt binary
for the host yet, a commit - and a build from source are looked up in the
Git repository of Tarantool or tt, with `git`. Without `git`, tt says the
lookup needs it and names a version the prebuilt binaries have:

```console
$ tt binary install tarantool 3.10.0-entrypoint
   ⨯ 'git' is required for 'tt binary install' to find tarantool 3.10.0-entrypoint: prebuilt package unavailable: rws has no prebuilt tarantool 3.10.0-entrypoint; install git, or ask for a version rws has, such as 3.9.0
```

Tarantool, Community or Enterprise, and the SDK are installed from 3.0 on.
A version older than that - `2.11.5`, `1.10.15`, `2.11ee` - is refused with
a message that names it, whether it is to be downloaded or built from
source; install it with tt 2.

A standalone Tarantool Enterprise release is installed under its Tarantool
version, `3.8.1`, without the `gc64` prefix, the revision, or the hash:
`gc64-3.8.1-0-r93` and `gc64-3.8.1-0-r94` both install into
`versions/tarantool-ee/tarantool-ee_3.8.1/`, and installing another
revision keeps the one there unless `--reinstall` is given. Prereleases and
debug builds stay apart: `3.8.1-rc1`, `debug-3.8.1`.

`tt binary switch` takes `X`, `X.Y` or `X.Y.Z` of every product, matching
the versions installed in the scope: `tt binary switch tarantool 3.9`
selects the 3.9.0 installed. `tt binary uninstall`, which removes what it
matches, takes `X.Y.Z` for Enterprise, tt Enterprise and TCM, and `X` and
`X.Y` are not expanded. An exact installed name wins; otherwise exactly one
installation must match, and an ambiguous request changes nothing. A
version that is not installed is an error that lists the installed ones.

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
`TT_CLI_EE_PASSWORD`, or from a prompt on a terminal. In a script, with
stdin not a terminal, there is no prompt: credentials given nowhere are an
error that names the flag and the variables, and the command fails.

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

`tt binary search tarantool` lists the releases: the `X.Y.Z-entrypoint`
tags of the Tarantool repository mark where a version starts, are no
releases, and are left out unless `--all` is given. Installing one builds
it from source, with a warning that says so.

`tt binary search tarantool` and `tt binary search tt` read the tags of
the Git repository, which needs `git`. Without it they list the releases
that `tt binary install` can install from a prebuilt binary without it:
those of rws.tarantool.org for the host, the releases of tt on GitHub for
the host, with no `master`. A warning says so:

```console
$ tt binary search tarantool
   ⚠ git is not found, so only the releases of tarantool with a prebuilt build in rws for linux/amd64 are listed; install git to list every tag
   • Available versions of tarantool:
3.0.0
...
3.9.0
```

A host with none of them, macOS for Tarantool, or a source that does not
answer, is an error that says why and to install `git`.

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
built against them may not load. It is shown once, by a command that
compiles against them: a build of a component whose backend makes native
modules, a rock that an install compiled (a build or fetch shows it once
the rock is in `.rocks/`), and `tt rocks install`, `build` and `make`. A
command that compiles nothing - `tt package resolve`, `pack --format
rock-src`, a build of a package of Lua alone - shows none.

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
