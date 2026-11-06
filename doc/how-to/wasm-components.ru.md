# Запуск компонентов WebAssembly (экспериментально)

[tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs) - модуль Tarantool, который запускает компоненты WebAssembly рядом с данными: код приложения на Python, JavaScript, Go, Rust, C или C#, вызываемый из Lua через `require('wasm')`. Компоненты собираются с помощью [tarawasm](https://github.com/mandesero/tarawasm).

`tt wasm prepare` подготавливает проект для работы обоих инструментов, пока tt не научится собирать компоненты самостоятельно, а `tt wasm new` добавляет компонент в пакет. Нужны Linux x86-64 или macOS arm64, Tarantool 3.6 или новее и Python 3.10 или новее для tarawasm.

На этой странице компонент Python `add(a, b)` собирается в пакет `myapp`. [HTTP API в компоненте WebAssembly](../write-your-app/http-api-in-wasm.md) - целое приложение: HTTP-сервер в компоненте, который записывает данные в space.

## Подготовка проекта {#prepare-the-project}

В каталоге пакета, где находится `app.manifest.toml`:

```console
$ tt wasm prepare --lang python
fetching tarantool-wasm-0.1.0-linux-x86_64.so
packed wasm-0.1.0-1.linux-x86_64.rock into .tarantool/registry
declared wasm 0.1.0 from .tarantool/registry
installing tarawasm
...
checking the python toolchain
python toolchain:
  python3          >=3.10.0           3.14.4     ok         /usr/bin/python3
  wasm-tools       1.261.0            1.261.0    ok         .../.tarantool/bin/wasm-tools (installed by tt)
  wkg              0.16.1             0.16.1     ok         .../.tarantool/bin/wkg (installed by tt)
  componentize-py  0.25.1             0.25.1     ok         .../.tarantool/bin/componentize-py (installed by tt)
...
prepared for tarantool-wasm 0.1.0 (linux-x86_64)
  rock:       .tarantool/registry/wasm-0.1.0-1.linux-x86_64.rock
  tarawasm:   .tarantool/bin/tarawasm
  toolchains: python
```

Вывод pip и tarawasm появляется между этими строками, вместо `...`. Команда:

- скачала нативный модуль релиза для этого хоста, проверила его по известному tt sha256 и упаковала в rock в `.tarantool/registry` - rock-сервер внутри проекта;
- объявила rock в манифесте и пересобрала lock:

    ```toml
    [dependencies]
    wasm = { version = "0.1.0", registry = ".tarantool/registry" }
    ```

- установила tarawasm в виртуальное окружение в `.tarantool/versions/` и создала ссылку `.tarantool/bin/tarawasm`;
- установила инструменты Python, см. ниже;
- записала `.env/activate.sh`, который добавляет `.tarantool/bin` в `PATH` оболочки.

`.tarantool` относится к этой машине и не попадает в коммит. После checkout, на другой машине или в CI команда `tt wasm prepare` восстанавливает всё: rock, tarawasm, набор инструментов каждого компонента и SDK, зафиксированный каждым компонентом.

## Наборы инструментов {#the-toolchains}

tarawasm запускает компиляторы для каждого языка. Из них tt устанавливает в проект инструменты, которые можно целиком взять из их релизов, проверяя каждый по известному tt sha256. Они размещаются в `.tarantool/versions/`, а ссылки на них - в `.tarantool/bin`:

| Инструмент | Для | |
| --- | --- | --- |
| wasm-tools 1.261.0, wkg 0.16.1 | все языки | релизы Bytecode Alliance |
| componentize-py 0.25.1 | `python` | wheel, который pip устанавливает в окружение tarawasm из загруженного tt файла |
| TinyGo 0.43.0 с Binaryen 133 | `go` | Binaryen добавляется поверх TinyGo, которому в версии 0.43 нужен `wasm-opt`; на него тоже создаётся ссылка |
| wit-bindgen 0.62.0 | `c` | |
| wasi-sdk 34 | `c` | не добавляется в `.tarantool/bin`, чтобы его `clang` не подменял системный компилятор при каждой сборке: для каждого C-компонента tarawasm получает путь к нему из `tarawasm.local.json` |

Остальные инструменты устанавливаются отдельно: tt проверяет их версии и сообщает, что нужно установить, но ничего не устанавливает в систему.

| Язык | Устанавливаемые инструменты |
| --- | --- |
| `python` | python3 3.10 или новее |
| `go` | Go 1.25 - 1.27 |
| `rust` | rustc и cargo 1.96 или новее с target `wasm32-wasip1`, cargo-component 0.21.1 |
| `js` | Node 24 или новее, jco 1.37.0 с componentize-js 0.23.0 и preview2-shim 0.17.8 |
| `c` | ничего дополнительного |
| `csharp` | .NET SDK 10.0.402, в Linux x86-64 |

Если инструмент отсутствует или его версия не подходит, команда останавливается и сообщает, что установить:

```console
$ tt wasm prepare --lang rust
...
   ⨯ the toolchain is not ready for rust; install, tt does not:
       rustc >=1.96.0 (missing): rustup (https://rustup.rs), then rustup target add wasm32-wasip1
       cargo >=1.96.0 (missing): rustup (https://rustup.rs)
       cargo-component >=0.21.1 (missing): cargo install --locked cargo-component --version 0.21.1
```

При сборке набор инструментов ищется сначала в `.tarantool/bin`, затем в `PATH`. Команда `tarawasm toolchain setup --install`, которая сама устанавливает инструменты, запускает `sudo apt-get install` в Linux и собирает `wasm-tools` и `wkg` из исходников; tt её не запускает.

## Добавление компонента {#add-a-component}

```console
$ tt wasm new calc --lang python
...
added component calc in calc
```

tarawasm создаёт компонент в `calc/` на SDK релиза `wasm`, объявленного пакетом, а `calc/.gitignore` исключает генерируемые файлы из git. Имя WIT world совпадает с именем компонента; так же называется результат сборки `dist/calc.wasm`, поэтому компоненты пакета не конфликтуют. Манифест объявляет компонент:

```toml
# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.calc]
path = "calc"
include = ["dist/calc.wasm"]

[components.calc.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
```

`tarawasm all` заново создаёт bindings и собирает `dist/calc.wasm`; при сборке tarawasm и компиляторы находятся в `.tarantool/bin` независимо от наличия `.env/activate.sh`. Параметр `incremental = true` пропускает сборку, если исходники, WIT и SDK lock компонента не изменились с последней сборки; после установки нового компилятора `tt package build --rebuild` всё равно собирает компонент. См. [Инкрементальные сборки](../concepts/packages.md#incremental-builds). Lua-компонент проекта с `path = "."` по умолчанию включает все `*.so` внутри каталога: для исключения указывается `exclude = ["calc/"]`.

SDK компонента задаётся в его `tarawasm.tarantool.lock.json` и должен соответствовать релизу, объявленному пакетом: компонент собирается по WIT своего SDK, а модуль предоставляет собственный WIT. `tt package build` предупреждает о компоненте на другом SDK, а `tt wasm prepare` отказывается продолжать.

## Написание компонента {#write-it}

Параметр `--template` создаёт компонент Python на основе примера: `call` соответствует примеру ниже, `http` - серверу из материала [HTTP API в компоненте WebAssembly](../write-your-app/http-api-in-wasm.md). Затем `tt wasm new` выводит Lua-модуль для работы с компонентом.

```console
$ tt wasm new calc --lang python --template call
```

Без шаблона экспорт добавляется в world файла `calc/wit/app.wit`, который создаёт tarawasm:

```wit
package example:app@0.1.0;

world calc {
  include tarantool:tarantool/guest@0.2.0;
  import docs:adder/ssl-bio@0.1.3;
  export add: func(a: s32, b: s32) -> s32;
}
```

Реализация записывается в `calc/main.py` после кода инициализации, созданного tarawasm:

```python
import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import wit_world


class WitWorld(wit_world.WitWorld):
    def add(self, a: int, b: int) -> int:
        return a + b
```

См. [руководство tarawasm](https://github.com/mandesero/tarawasm/blob/master/docs/tarantool.md) о других языках и API Tarantool для компонента.

## Сборка и вызов из Lua {#build-it-and-call-it-from-lua}

```console
$ tt package build
$ find .rocks -name wasm.so -o -name '*.wasm'
.rocks/lib/tarantool/wasm.so
.rocks/share/tarantool/myapp/dist/calc.wasm
```

Компонент, который размещается в `.wasm`, должен быть бинарным файлом WebAssembly: любой другой формат приводит к ошибке сборки, а core module, который `require('wasm')` не загружает, размещается с предупреждением.

Компонент становится файлом пакета в пространстве имён пакета. Его загружают по пути от каталога приложения - рабочего каталога инстансов - как в проекте, так и в установленной версии:

```lua
local wasm = require('wasm')

local calc = wasm.load('.rocks/share/tarantool/myapp/dist/calc.wasm')
assert(wasm.call(calc, 'add', 20, 22) == 42)

local bound = wasm.bind(calc)
assert(bound.add(1, 2) == 3)

wasm.drop(calc)
```

```console
$ tt run -e "local wasm = require('wasm') \
    local calc = wasm.load('.rocks/share/tarantool/myapp/dist/calc.wasm') \
    print(wasm.call(calc, 'add', 20, 22)) os.exit()"
42
```

`os.exit()` завершает запуск: без него Tarantool продолжает читать скрипт из стандартного ввода или открывает консоль в терминале.

Модуль хранит скомпилированные компоненты в `~/.cache/wasmtime` пользователя, от имени которого запущен Tarantool, и продолжает работу без кэша, если не может туда записать.

## Проверка проекта {#check-the-project}

`tt wasm status` показывает, что подготовила команда `tt wasm prepare` и что нужно для сборки, ничего не меняя: rock `wasm` и его сборку для этой машины, tarawasm, каждый компонент и указанный им SDK, а также наборы инструментов. При отсутствии чего-либо команда завершается с ошибкой, как и проверка в CI:

```console
$ tt wasm status
wasm:       0.1.0 from .tarantool/registry
rock:       .tarantool/registry/wasm-0.1.0-1.linux-x86_64.rock
tarawasm:   .tarantool/bin/tarawasm
component:  calc (python, SDK 0.1.0) in calc
python toolchain:
  ...
ready
```

## Упаковка {#pack-it}

Упаковка с зависимостями используется по умолчанию: архив содержит модуль для платформы, на которой он собран, и компонент.

```console
$ tt package pack
.../_build/pack/myapp-1.0.0-linux-amd64.tt
$ tt package install _build/pack/myapp-1.0.0-linux-amd64.tt --scope user --yes
installed myapp 1.0.0 into user scope
```

Архив без зависимостей, созданный с `--without-deps`, получает имя `-any.tt`, если у пакета нет собственных нативных файлов. Однако при установке он загружает rock `wasm` из `.tarantool/registry`, доступного только на машине, где архив был собран; rock-сервер ниже делает его доступным везде.

## Публикация rock {#share-the-rock}

В `.tarantool/registry` хранится rock для одной машины. `tt wasm rock` упаковывает rocks всех сборок релиза вместе с индексом, который превращает каталог в rock-сервер:

```console
$ tt wasm rock --out dist/rocks
.../dist/rocks/wasm-0.1.0-1.linux-x86_64.rock
.../dist/rocks/wasm-0.1.0-1.macosx-aarch64.rock
.../dist/rocks/manifest
```

Каталог можно публиковать как есть или загрузить файлы рядом с ресурсами релиза, а затем объявить зависимость из этого источника:

```console
$ tt wasm prepare --rock-server https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
...
  rock:       from https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
```

Каждая машина получает с сервера свой rock, архив с `--without-deps` устанавливается где угодно, а в проект ничего не упаковывается.

## Команды и параметры {#commands-and-options}

| Команда | |
| --- | --- |
| `tt wasm prepare` | rock `wasm`, tarawasm, наборы инструментов и SDK компонентов |
| `tt wasm new NAME --lang L` | компонент |
| `tt wasm status` | недостающие для сборки элементы, без изменений; ошибка при отсутствии любого из них |
| `tt wasm rock --out DIR` | rocks всех платформ релиза для rock-сервера |

- `--version` команд `prepare` и `rock` выбирает релиз tarantool-wasm; список известных tt релизов выводит `tt wasm prepare --help`.
- `--rock-server URL` команды `prepare` объявляет rock с rock-сервера вместо его упаковки в `.tarantool/registry`.
- `--python` команды `prepare` задаёт Python для установки tarawasm, `--tarawasm` - версию tarawasm для установки через pip вместо закреплённой tt, `--no-tarawasm` исключает его установку, `--reinstall` устанавливает заново.
- `--lang` команды `prepare` также устанавливает набор инструментов `python`, `go`, `js`, `rust`, `c` или `csharp`; для `new` параметр обязателен.
- `--dir` команды `new` задаёт каталог компонента, `--world` - имя его WIT world (по умолчанию имя компонента), а `--template` - `http` или `call` для Python.

Загрузки кэшируются в каталоге кэша пользователя: в Linux это `~/.cache/tt/wasm`. Переменная `TT_WASM_RELEASES` задаёт JSON-файл с релизами, используемыми вместо известных tt, и `base_url` их ресурсов: это зеркало для сети без доступа к интернету. Формат описан в разделе [Переменные окружения](../reference/environment.md#webassembly-components).
