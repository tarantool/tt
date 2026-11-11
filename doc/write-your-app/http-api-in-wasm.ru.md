# HTTP API в компоненте WebAssembly (экспериментально)

В этом руководстве создаётся приложение, которое хранит заметки в space Tarantool и предоставляет к ним доступ по HTTP:

| Запрос | |
| --- | --- |
| `PUT /notes/<id>` с `{"text": "..."}` | записать заметку |
| `GET /notes/<id>` | прочитать её |
| `GET /notes` | получить список заметок |
| `DELETE /notes/<id>` | удалить заметку |

HTTP-сервер и его маршрутизатор - компонент WebAssembly на Python. Он читает и записывает данные в space через box API SDK [tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs): `box.space['notes'].replace(...)`, `.get(...)`, `.delete(...)`. Lua-часть приложения создаёт space и запускает компонент.

Проект:

```text
notes/
├── app.manifest.toml
├── app/
│   └── init.lua                # модуль Lua: space и компонент
├── router/                     # компонент, созданный командой tt wasm new
│   ├── main.py
│   └── wit/app.wit
└── .templates/default/
    └── config.yaml             # инстанс
```

Нужны Linux x86-64 или macOS arm64 (Linux arm64 не поддерживается), Tarantool 3.6 или новее и Python 3.10 или новее; компиляторы Python-компонентов tt устанавливает в проект. Описание команды `tt wasm` см. в разделе [Запуск компонентов WebAssembly](../how-to/wasm-components.md).

## Создание проекта {#create-the-project}

```console
$ mkdir notes && cd notes
$ git init
$ tt new
created /home/me/notes/app.manifest.toml for package notes
$ tt wasm prepare --lang python
declared wasm 0.1.0 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
...
prepared for tarantool-wasm 0.1.0 (linux-x86_64)
  rock:       from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
  tarawasm:   .tarantool/bin/tarawasm
  toolchains: python
```

`tt wasm prepare` объявляет модуль `wasm` зависимостью с rock-сервера, который раздаёт его rock для каждой платформы, и устанавливает в `.tarantool/` tarawasm, который собирает компонент, а также компиляторы компонентов Python. В `app.manifest.toml` нужно повысить минимальную версию Tarantool, необходимую пакету:

```toml
[platform]
tarantool = ">=3.6.0"
```

## Написание маршрутизатора {#write-the-router}

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

Команда `tt wasm new` запускает tarawasm для создания компонента в `router/` на основе SDK выпуска `wasm`, объявленного зависимостью пакета, записывает компонент в манифест и применяет к нему шаблон `http` - сервер ниже. Команда также выводит Lua-модуль, который его запускает.

В world компонента `router/wit/app.wit` подключён API Tarantool из SDK и экспортирован `wasi:cli/run`: компонент - программа, которая работает до остановки, а не набор вызываемых функций.

```wit
package example:app@0.1.0;

world router {
  include tarantool:tarantool/guest@0.2.0;
  import docs:adder/ssl-bio@0.1.3;
  export wasi:cli/run@0.2.12;
}
```

`router/main.py` содержит весь сервер:

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

- `Run.run` - программа. Она слушает адрес, который Lua-модуль передаёт в аргументах, и последовательно отвечает на подключения. Таймаут `accept` даёт runtime возможность остановить программу между подключениями.
- `Handler` разбирает запрос с помощью стандартной библиотеки `http.server`, передаёт маршрутизатору метод, путь и тело запроса и записывает ответ в формате JSON.
- `Router` сопоставляет метод и шаблон пути с обработчиком; если путь известен маршрутизатору, но метод другой, возвращается `405`, если путь неизвестен - `404`.
- Обработчики составляют REST API. `box.space['notes']` - space, созданный Lua-модулем; `replace`, `get`, `delete` и `select` выполняются в потоке транзакций Tarantool, как и одноимённые вызовы Lua.

## Написание Lua-модуля {#write-the-lua-module}

`app/init.lua` - модуль, путь к которому вывела команда `tt wasm new`:

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

`wasm.load` загружает компонент с заданными возможностями: аргументами, без окружения инстанса и с доступом к сети только по одному IP-адресу - тому, на котором он слушает. `wasm.run` запускает компонент в отдельном потоке и сразу возвращает управление; fiber записывает в журнал сообщение при его остановке. При завершении Tarantool останавливает компонент.

`debug.sourcedir()` возвращает каталог модуля: компонент - файл пакета рядом с ним, и в проекте, и в установленной версии.

## Объявление компонентов {#declare-the-components}

В `app.manifest.toml` уже есть зависимость, добавленная командой `tt wasm prepare`, и компонент, объявленный командой `tt wasm new`; нужно добавить Lua-модуль как компонент:

```toml
[dependencies]
wasm = { version = "0.1.0", registry = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/" }

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

Оба компонента находятся в пространстве имён пакета: Lua-модуль загружается через `require('notes')`, а компонент - это `dist/router.wasm` рядом с ним. Имя файла соответствует world, которому команда `tt wasm new` присваивает имя компонента. Сборка `router` запускает `tarawasm all`, только если изменился исходный файл компонента; сборка находит tarawasm в `.tarantool/bin`.

## Описание инстанса {#describe-the-instance}

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

`app.module` загружает Lua-модуль; `app.cfg.listen` задаёт адрес HTTP API.

## Сборка и запуск {#build-and-run}

```console
$ tt package build
$ tt instance start
   • Starting an instance [notes:instance-001]...
```

Первый запуск занимает несколько секунд: runtime компилирует компонент и сохраняет скомпилированную версию в `~/.cache/wasmtime`. Затем:

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

Заметки хранятся в записях space, что видно в консоли инстанса:

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

Компонент пишет в журнал инстанса через `box.log`. После изменения компонента нужны `tt package build` и перезапуск инстанса.

## Примечания {#notes}

- Сервер обрабатывает по одному подключению за раз в потоке компонента: если клиент ничего не отправляет, остальные ждут 5 секунд - таймаут подключения.
- Доступ компонента к сети проверяется и для принимаемых подключений: `allowed_ips` должен включать адреса клиентов, а `allowed_ports` запретил бы всех клиентов, поскольку каждый подключается со своего порта. Сервер можно запустить на `127.0.0.1` и поставить перед ним proxy либо указать адреса клиентов.
- В проект не нужно включать `.rocks/`, `.tarantool/`, `.env/` и `_build/`; `router/.gitignore` исключает файлы, создаваемые tarawasm. После checkout команда `tt wasm prepare` устанавливает tarawasm, компиляторы и SDK компонента, а `tt package build` собирает его; `tt wasm status` показывает, чего не хватает, не меняя файлы.
- Rock-сервер раздаёт rock `wasm` всем машинам, поэтому пакет устанавливается в другом месте и упакованный вместе с зависимостями, как описано в руководстве [Первый пакет](../get-started/first-package.md#pack), и без них. Локальная сборка модуля (`tt wasm prepare --from`) упаковывается только для этой машины; см. [Запуск компонентов WebAssembly](../how-to/wasm-components.md#share-the-rock).
