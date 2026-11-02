# Install

tt 3 is not released yet: there are no packages of it. Build it from the
`v3` branch. The deb and rpm packages, Homebrew and the release archives
install tt 2.x.

<!-- TODO(writers): packages, Homebrew and release archives once tt 3 is
released; see package/README.md for what the release packages contain. -->

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
