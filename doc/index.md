# Tarantool CLI

`tt` is the command-line utility for developing, packaging, deploying and
operating Tarantool applications.

> **Preview.** This is the documentation of tt 3, which is not released yet,
> built from the `v3` branch. tt 2.x is documented in the
> [Tarantool documentation](https://www.tarantool.io/en/doc/latest/tooling/tt_cli/).

## What tt does

- **Develops** an application as a package: `app.manifest.toml` declares its
  dependencies and components, `tt package build` builds them, `tt run` and
  `tt test` run the code and the tests under the Tarantool of the project.
- **Runs** the instances of an application on a host: Tarantool instances
  configured by the cluster config, Tarantool instances that run a script of
  their own, and external processes, all under a watchdog of tt, in the
  background, in the foreground or under systemd.
- **Packages and installs** an application: `tt package pack` makes an
  archive with its dependencies and the Tarantool it runs on, and
  `tt package install` installs it for a user or system-wide, offline.
- **Manages** the binaries an application runs on: Tarantool Community and
  Enterprise, tt, TCM and the Enterprise SDK, per project, per user or
  system-wide.
- **Operates** a cluster: its configuration, replica sets and failover,
  backups and point-in-time recovery.
- **Runs WebAssembly components** in Tarantool (experimental): code in
  Python, Go, Rust, C, JavaScript or C# built into the package and called
  from Lua; see [Run WebAssembly components](how-to/wasm-components.md) and
  [An HTTP API in a WebAssembly component](write-your-app/http-api-in-wasm.md).

## Try it

```console
$ tt create cluster --name myapp
$ cd myapp
$ tt binary install tarantool
$ tt instance start
$ tt instance status
```

[A cluster on your laptop](get-started/first-cluster.md) walks through it.

## Where to go

- [What's new in tt 3](whats-new.md): what tt 3 does that tt 2.x did not.
- [Get started](get-started/install.md): install tt, run a cluster, make a
  package.
- [Concepts](concepts/projects-and-scopes.md): projects and scopes, packages,
  instances - the model the commands share.
- [Write your application](write-your-app/http-api-in-wasm.md): a whole
  application, step by step.
- [How-to](how-to/binaries.md): deploy, run under systemd, sign packages,
  extend tt, build a module in Rust, run WebAssembly components.
- [Reference](reference/commands/index.md): every command, the files tt
  reads, the environment variables.
- [Migrate from tt 2.x](migration_from_older_versions.md).
