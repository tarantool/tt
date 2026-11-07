# An HTTP API in a WebAssembly component (experimental)

This tutorial writes an application that keeps notes in a Tarantool space
and serves them over HTTP:

| Request | |
| --- | --- |
| `PUT /notes/<id>` with `{"text": "..."}` | write the note |
| `GET /notes/<id>` | read it |
| `GET /notes` | list the notes |
| `DELETE /notes/<id>` | delete it |

The HTTP server and its router are a WebAssembly component written in
Python. It reads and writes the space with the box API of the
[tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs) SDK:
`box.space['notes'].replace(...)`, `.get(...)`, `.delete(...)`. The Lua part
of the application creates the space and starts the component.

The project:

```text
notes/
├── app.manifest.toml
├── app/
│   └── init.lua                # the Lua module: the space, the component
├── router/                     # the component, made by tt wasm new
│   ├── main.py
│   └── wit/app.wit
└── .templates/default/
    └── config.yaml             # the instance
```

It needs Linux x86-64 or macOS arm64 (Linux arm64 is not supported),
Tarantool 3.6 or newer and Python 3.10 or newer; tt installs the compilers of Python components into the
project. See [Run WebAssembly components](../how-to/wasm-components.md) for
what `tt wasm` does.

## Create the project

```console
$ mkdir notes && cd notes
$ git init
$ tt new
created /home/me/notes/app.manifest.toml for package notes
$ tt wasm prepare --lang python
...
declared wasm 0.1.0 from .tarantool/registry
...
prepared for tarantool-wasm 0.1.0 (linux-x86_64)
  rock:       .tarantool/registry/wasm-0.1.0-1.linux-x86_64.rock
  tarawasm:   .tarantool/bin/tarawasm
  toolchains: python
```

`tt wasm prepare` declares the `wasm` module as a dependency and installs
into `.tarantool/` tarawasm, which builds the component, and the compilers
of Python components. Raise the Tarantool the package needs in
`app.manifest.toml`:

```toml
[platform]
tarantool = ">=3.6.0"
```

## Write the router

```console
$ tt wasm new router --lang python --template http
...
added component router in router
next:
  # a Lua module of the package, at the root of its namespace, uses it so:
    local config = require('config')
    ...
  tt package build
```

`tt wasm new` has tarawasm create the component in `router/`, on the SDK
of the `wasm` release the package declares, declares it in the manifest and
lays the `http` template over it: the server below. It prints the Lua module
that runs it.

The world of the component, `router/wit/app.wit`, takes the Tarantool API
from the SDK and exports `wasi:cli/run`: the component is a program that
runs until it is stopped, not a set of functions to call.

```wit
package example:app@0.1.0;

world router {
  include tarantool:tarantool/guest@0.2.0;
  import docs:adder/ssl-bio@0.1.3;
  export wasi:cli/run@0.2.12;
}
```

`router/main.py` is the whole server:

```python
import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import http.server
import json
import re
import socket
import sys

from twr_python_sdk import tarantool as box
from wit_world import exports


class Router:
    """Maps a method and a path pattern, /notes/<id:int>, to a handler."""

    def __init__(self):
        self.routes = []

    def route(self, method, pattern):
        def part(match):
            name, kind = match.group(1), match.group(2)
            return f'(?P<{name}>[0-9]+)' if kind == 'int' else f'(?P<{name}>[^/]+)'

        regex = re.compile('^' + re.sub(r'<(\w+)(?::(\w+))?>', part, pattern) + '$')

        def register(handler):
            self.routes.append((method, regex, pattern, handler))
            return handler

        return register

    def dispatch(self, method, path, body):
        known_path = False
        for route_method, regex, pattern, handler in self.routes:
            match = regex.match(path)
            if match is None:
                continue
            known_path = True
            if route_method == method:
                params = {
                    name: int(value) if f'<{name}:int>' in pattern else value
                    for name, value in match.groupdict().items()
                }
                return handler(body, **params)
        if known_path:
            return 405, {'error': f'{method} is not allowed on {path}'}
        return 404, {'error': f'no route for {path}'}


router = Router()


def note(row):
    return {'id': row[0], 'text': row[1]}


@router.route('GET', '/notes')
def list_notes(body):
    return 200, [note(row) for row in box.space['notes'].select(limit=100)]


@router.route('GET', '/notes/<id:int>')
def get_note(body, id):
    row = box.space['notes'].get(id)
    if row is None:
        return 404, {'error': f'no note {id}'}
    return 200, note(row)


@router.route('PUT', '/notes/<id:int>')
def put_note(body, id):
    text = json.loads(body)['text']
    return 200, note(box.space['notes'].replace((id, text)))


@router.route('DELETE', '/notes/<id:int>')
def delete_note(body, id):
    if box.space['notes'].delete(id) is None:
        return 404, {'error': f'no note {id}'}
    return 204, None


class Handler(http.server.BaseHTTPRequestHandler):
    """Parses one request with the standard library and answers with JSON."""

    def handle_one_request(self):
        self.close_connection = True
        super().handle_one_request()

    def serve(self):
        length = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(length)
        try:
            status, payload = router.dispatch(self.command, self.path.split('?')[0], body)
        except (ValueError, KeyError, TypeError) as error:
            status, payload = 400, {'error': f'bad request: {error!r}'}
        data = b'' if payload is None else json.dumps(payload).encode()
        self.send_response(status)
        if data:
            self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = serve

    def log_message(self, format, *args):
        box.log.write(box.LogLevel.INFO, 'notes: ' + format % args)


class Run(exports.Run):
    """The server: wasm.run runs it on a thread of its own."""

    def run(self) -> None:
        host, port = sys.argv[0], int(sys.argv[1])
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
            server.bind((host, port))
            server.listen()
            # A short accept timeout lets wasm.cancel stop the loop.
            server.settimeout(1)
            box.log.write(box.LogLevel.INFO, f'notes: listening on {host}:{port}')
            while True:
                try:
                    connection, address = server.accept()
                except TimeoutError:
                    continue
                with connection:
                    connection.settimeout(5)
                    try:
                        Handler(connection, address, server)
                    except Exception as error:
                        box.log.write(box.LogLevel.WARN, f'notes: {address}: {error!r}')
```

- `Run.run` is the program. It listens on the address the Lua module passes
  as its arguments and answers one connection after another. The
  `accept` timeout lets the runtime stop it between connections.
- `Handler` parses the request with `http.server` of the standard library,
  hands the method, the path and the body to the router and writes its
  answer as JSON.
- `Router` maps a method and a path pattern to a handler; a path the
  router knows under another method is `405`, an unknown one `404`.
- The handlers are the REST API. `box.space['notes']` is the space the Lua
  module creates; `replace`, `get`, `delete` and `select` run in the
  transaction thread of Tarantool, as the same calls of Lua do.

## Write the Lua module

`app/init.lua` is the module `tt wasm new` printed:

```lua
local config = require('config')
local fiber = require('fiber')
local fio = require('fio')
local log = require('log')
local wasm = require('wasm')

box.schema.space.create('notes', {
    if_not_exists = true,
    format = {
        {name = 'id', type = 'unsigned'},
        {name = 'text', type = 'string'},
    },
})
box.space.notes:create_index('primary', {if_not_exists = true})

-- app.cfg.listen of config.yaml: where the HTTP API listens.
local listen = (config:get('app.cfg') or {}).listen or '127.0.0.1:8080'
local host, port = listen:match('^(.+):(%d+)$')

-- The router is a file of this package, next to this module.
local router = wasm.load(fio.pathjoin(debug.sourcedir(), 'dist/router.wasm'), {
    args = {host, port},
    inherit_env = false,
    -- The addresses the component may use. The check applies to the peers
    -- of the connections it accepts as well, so it names no ports: a client
    -- connects from a port of its own.
    allowed_ips = {host},
})

-- The router serves on a thread of its own; Tarantool stops it on shutdown.
local server = wasm.run(router)
log.info('notes: HTTP API on http://%s', listen)

fiber.create(function()
    local ok, err = pcall(wasm.join, server)
    log.info('notes: HTTP API stopped: %s', ok and 'done' or err)
end)

return {}
```

`wasm.load` opens the component with what it may use: the arguments, no
environment of the instance, and the network of one IP address, the one it
listens on. `wasm.run` runs the component on a thread of its own and returns at
once; the fiber logs when it stops. On shutdown Tarantool stops it.

`debug.sourcedir()` is the directory of the module: the component is a file
of the package next to it, in the project and in an installed version alike.

## Declare the components

`app.manifest.toml` has the dependency `tt wasm prepare` wrote and the
component `tt wasm new` declared; add the Lua module as a component:

```toml
[dependencies]
wasm = { version = "0.1.0", registry = ".tarantool/registry" }

# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.router]
path = "router"
include = ["dist/router.wasm"]

[components.router.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true

[components.app]
path = "app"
```

Both components go under the namespace of the package: the Lua module is
`require('notes')`, the component is `dist/router.wasm` next to it, named
after its world, which `tt wasm new` names after the component. The build
of `router` runs `tarawasm all`, only when a source of the component
changed; a build finds tarawasm in `.tarantool/bin`.

## Describe the instance

`.templates/default/config.yaml`:

```yaml
app:
  module: notes
  cfg:
    listen: 127.0.0.1:8080

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen:
              - uri: 127.0.0.1:3301
```

`app.module` loads the Lua module; `app.cfg.listen` is the address of the
HTTP API.

## Build and run

```console
$ tt package build
$ tt instance start
   • Starting an instance [notes:instance-001]...
```

The first start takes some seconds: the runtime compiles the component and
keeps it compiled in `~/.cache/wasmtime`. Then:

```console
$ curl -X PUT http://127.0.0.1:8080/notes/1 -d '{"text": "buy milk"}'
{"id": 1, "text": "buy milk"}
$ curl -X PUT http://127.0.0.1:8080/notes/2 -d '{"text": "call mom"}'
{"id": 2, "text": "call mom"}
$ curl http://127.0.0.1:8080/notes
[{"id": 1, "text": "buy milk"}, {"id": 2, "text": "call mom"}]
$ curl -X DELETE http://127.0.0.1:8080/notes/1
$ curl http://127.0.0.1:8080/notes/1
{"error": "no note 1"}
$ curl -X POST http://127.0.0.1:8080/notes/1
{"error": "POST is not allowed on /notes/1"}
```

The notes are records of the space, as the console of the instance shows:

```console
$ echo "return box.space.notes:select()" | tt connect notes:instance-001 -f-
---
- - [2, 'call mom']
...
$ tt instance logs notes:instance-001 | grep 'I> notes:'
notes:instance-001: ... I> notes: HTTP API on http://127.0.0.1:8080
notes:instance-001: ... I> notes: listening on 127.0.0.1:8080
notes:instance-001: ... I> notes: "PUT /notes/1 HTTP/1.1" 200 -
...
$ tt instance stop --yes
```

The component writes to the log of the instance with `box.log`. A change to
the component is `tt package build` and a restart of the instance.

## Notes

- The server answers one connection at a time, on the thread of the
  component: a client that sends nothing holds the others for 5 seconds,
  the timeout of a connection.
- The network a component may use is checked for the connections it
  accepts as well: `allowed_ips` must cover the clients, and `allowed_ports`
  would refuse every client, since a client connects from a port of its
  own. Serve on `127.0.0.1` and put a proxy in front, or name the client
  addresses.
- Commit the project without `.rocks/`, `.tarantool/`, `.env/` and
  `_build/`; `router/.gitignore` keeps out what tarawasm generates. After a
  checkout, `tt wasm prepare` puts back tarawasm, the compilers and the SDK
  of the component, and `tt package build` builds it; `tt wasm status` says
  what is missing, changing nothing.
- The registry `tt wasm prepare` writes is for this machine: pack the
  package with its dependencies, as [Your first package](../get-started/first-package.md#pack)
  does, to install it elsewhere, or serve the rock to every machine with
  `tt wasm rock` and `tt wasm prepare --rock-server`, as
  [Run WebAssembly components](../how-to/wasm-components.md#share-the-rock)
  shows.
