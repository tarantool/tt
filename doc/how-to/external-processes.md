# Run an external process

An application may run programs that are not Tarantool - a gateway, a
worker, an exporter - next to its Tarantool instances. A record with
`binary` in `instances.yaml` is such an external process: the instance
commands start, stop and show it next to the Tarantool instances, under the
same watchdog.

## Describe the process

```yaml
storage-001:
gateway:
  binary: gateway           # a program of .rocks/bin, or on PATH, or a path
  args: [--port, "8080"]
  env: {GATEWAY_MODE: production}
  workdir: ./gateway        # <root>/var/lib/<app>/<instance>/ by default
  stop_signal: SIGINT       # SIGTERM by default
  stop_timeout: 10s         # 30s by default
  logrotate_signal: SIGHUP  # none by default
  restart: on-failure       # no by default
```

- `binary` with a slash is a path, relative to the application directory.
  Without one it names a program, looked up first in `.rocks/bin/` of the
  application, where tt builds the programs of the package, and then on
  `PATH`: `binary: python3` runs the `python3` of the host.
- `workdir` is relative to the application directory. Without it the
  process runs in its data directory, `<root>/var/lib/<app>/<instance>/`,
  which `tt instance start` creates.
- `env` goes on top of the environment of tt.
- `args`, `workdir` and `logrotate_signal` are for external processes only.

`tt instance start` reports a `binary` it cannot run, or a `workdir` that does
not exist, before it starts anything. See the
[reference](../reference/instances-yaml.md) for every field.

## Build the program with the package

A component whose build makes a program puts it into `.rocks/bin/`, so the
package carries it, and the record names it by its name:

```toml
[components.gateway]
path = "gateway"            # a Go module

[components.gateway.build]
backend = "go"
```

```console
$ tt package build
$ ls .rocks/bin
gateway
$ tt instance start
$ tt instance logs myapp:gateway
myapp:gateway: gateway started, port [--port 8080]
```

The backends that build programs:

- `go`: `go build -trimpath` of `package` (`.` by default) into a program
  named `name`, the component's name by default; `tags`, `ldflags`, and
  `cgo` (off by default). The Go sources are not laid out into the package:
  only what `include` of the component names is.
- `cargo`: `cargo build` of the binary `bin`, in the `release` profile
  unless `profile` says otherwise, in `_build/cargo/`; `package`,
  `features`. A block that names `module` too builds the module of the
  library as well; see [Build a module in Rust](rust-modules.md).
- `zig`: `zig build` of the `build.zig` of `cwd`, the program `bin` taken
  from what it installed into its prefix, `bin/`; `steps`, `optimize`,
  `options`. A block that names `module` too takes the module as well; see
  [Build a module in Zig](zig-modules.md).
- `cmake`: the CMake project of `cwd`, built out of the source in
  `_build/component/<component>/`, its programs listed in `output` with
  `to = "bin"`, relative to the build tree; `variables`, `targets`,
  `build_type`.
- `make` and `shell`: a build of your own, its programs listed in `output`
  with `to = "bin"`, relative to `cwd`, and what it makes on the way in
  `TT_BUILD_DIR`.

```toml
[components.tools.build]
backend = "make"
make_target = "tools"
output = [{ path = "build/gw-cli", to = "bin" }]
```

A component may make modules and programs at once: a string of `output`
is a module, a file with `to = "share"` a data file. The deprecated
`kind = "executable"` of `manifest_version = "0.1"` still works, with a
warning: its build makes programs only, and a `make` or `shell` build of it
writes them into `TT_OUTPUT_DIR`.

`static = true` asks `go` and `cargo` for a program that needs no loader and
no shared library, and the build checks that it got one; the programs of
`cmake`, `make` and `shell` are only checked. The toolchain must be on
`PATH`. See the [manifest reference](../reference/app-manifest.md#componentsname).

## How it runs

- The process leads a process group of its own, which its stop signal and
  `tt instance kill` reach whole, so a wrapper script stops with the program
  it runs.
- Its output goes to `tt.log`. Its watchdog keeps `tt.pid` and the pid of
  the process, `process.pid`, in the run directory.
- `tt instance status` shows whether the process runs, and nothing more.
- `tt instance logrotate` reopens `tt.log` and sends the process
  `logrotate_signal`, if the record has one.
- `tt instance clean` removes its log and its data directory.
- `tt connect`, `tt play`, `tt instance kill --dump` and the cluster
  commands refuse an external process: it is not a Tarantool instance.
- The instances start in the order of `instances.yaml`, and none waits for
  another to be ready.

A process whose watchdog was killed by something else than
`tt instance kill` runs on without it. `tt instance status` does not show it
running and names it in an alert, `tt instance start` refuses to start a
second one, and `tt instance stop` and `tt instance kill` stop it by
`process.pid`.
