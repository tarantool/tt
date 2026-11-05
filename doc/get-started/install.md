# Install

## Install script

`install.sh` installs tt 3 from its GitHub release on Linux (x86_64 and
arm64) and on macOS with Apple silicon:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
```

The script takes the newest stable tt 3 release, or the newest pre-release
while there is none, checks the archive against `checksums.txt` of the
release and puts `tt` into `~/.local/bin`, or into `/usr/local/bin` when run
by root. When the directory is not on `PATH`, the script says how to add it.
`wget -qO- <url> | sh` works as well.

Options go after `sh -s --`:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh -s -- --version 3.0.0-alpha.1 --dir /usr/local/bin
```

- `-v`, `--version <version>` - the release to install, such as
  `3.0.0-alpha.1`. `TT_VERSION` sets it too.
- `-c`, `--commit <commit>` - build tt from a commit of the repository
  instead of installing a release; see [below](#build-from-a-commit).
  `TT_COMMIT` sets it too. It excludes `--version`.
- `-d`, `--dir <dir>` - the directory to put `tt` into. `TT_INSTALL_DIR`
  sets it too.
- `--openssl` - install the `tt-openssl` build, whose TLS uses the OpenSSL
  of the machine instead of Go. A release has it for Linux only.
- `-h`, `--help` - list the options.

`GITHUB_TOKEN`, when set, is sent to the GitHub API the script lists the
releases with: an unauthenticated client may hit its rate limit.
`--version` skips the lookup.

### Build from a commit

`--commit` builds tt 3 of a commit with `go build`, so it needs `git` and
[Go](https://go.dev/doc/install). It takes a hash, full or short, a branch or
a tag; the commit of a pull request, which no branch has, by its full hash:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh -s -- --commit 1508ce93
```

The binary is built as `mage build` builds it and reports the version
`git describe` gives the commit:

```console
$ tt version
Tarantool CLI version 3.0.0-alpha.1, linux/amd64. commit: 1508ce9 (v3.0.0-alpha.1-20-g1508ce9)
```

A build runs wherever Go does, Intel macOS included, which has no release
build. With `--openssl` it needs a C compiler and the OpenSSL headers. A
commit of tt 2 is refused: it builds with submodules and code that mage
generates, as the
[README of tt 2](https://github.com/tarantool/tt#build-from-source) tells.

<!-- TODO(writers): Homebrew and the package repositories once tt 3 is
published there; see package/README.md for what the release packages
contain. -->

## Build from source

You need [Go](https://go.dev/doc/install) 1.27.3 or newer,
[Mage](https://magefile.org/) and Git.

```console
$ git clone --branch v3 https://github.com/tarantool/tt
$ cd tt
$ mage build
$ ./tt version
```

Put the `tt` binary into a directory on `PATH`.

> **Preview note.** `mage build` stamps tt with the version `git describe`
> gives, and the nearest tag on the `v3` branch is a 2.x one, so the binary
> reports a 2.x version. `tt new` writes `tt = ">=3.0.0"` into a manifest,
> and `tt package pack` then refuses to bundle such a tt into an archive. To
> pack with a binary built from source, stamp it with a 3.x version:
>
> ```console
> $ go build -ldflags "-X github.com/tarantool/tt/v3/cli/version.gitTag=3.0.0" -o tt ./cli
> ```

### TLS backends

By default tt uses the pure-Go TLS backend and does not need OpenSSL.
`TT_CLI_BUILD_SSL` selects another one:

```console
$ TT_CLI_BUILD_SSL=openssl mage build         # OpenSSL, linked dynamically
$ TT_CLI_BUILD_SSL=openssl_static mage build  # OpenSSL, linked statically
```

The static build needs the OpenSSL static libraries.

## Shell completion

```console
$ . <(tt completion bash)
```

`zsh` and `fish` are supported too. Completion offers the commands, external
modules, and the targets of `tt instance` commands: applications before the
`:` and their instances after it.

## Tarantool

tt does not need Tarantool to be installed: `tt binary install tarantool`
installs it into a project, for a user or system-wide (see
[Manage binaries](../how-to/binaries.md)). Tarantool Community Edition 3.0
and newer is supported.

## Runtime dependencies

- Git and Mage, for `tt binary install tt` when it builds tt from source,
  and Git for `tt binary search tt`.
- Git, CMake, `make` and a C and C++ compiler, for
  `tt binary install tarantool` when it builds Tarantool from source: on a
  platform with no prebuilt binary, for a commit or a pull request, or with
  `--source-build`.
- A C compiler, for rocks and components with a C part.
- `curl` or `wget`, `zip` and `unzip`, for `tt rocks`, which runs LuaRocks.

<!-- TODO(writers): check whether tt rocks still needs curl/zip/unzip: it
runs the LuaRocks backend of go-luarocks. tt package does not need them. -->
