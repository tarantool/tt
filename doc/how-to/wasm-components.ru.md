# Запуск компонентов WebAssembly (экспериментально) {#запуск-компонентов-webassembly-экспериментально}

[tarantool-wasm](https://github.com/tarantool/tarantool-wasm-rs) - модуль
Tarantool, запускающий компоненты WebAssembly рядом с данными: код приложения
на Python, JavaScript, Go, Rust, C или C#, вызываемый из Lua через
`require('wasm')`. Компоненты собираются с помощью
[tarawasm](https://github.com/mandesero/tarawasm).

`tt wasm prepare` подготавливает проект для обоих инструментов, пока tt не
собирает компоненты сам, а `tt wasm new` добавляет компонент в пакет.
`tt wasm` - это модуль `wasm`, один из
[стандартных модулей](../concepts/distributions.md) tt, который проверяет в том
числе файлы `.wasm` пакета. Для работы требуются Linux x86-64 или macOS arm64,
Tarantool 3.6 или новее и Python 3.10 или новее для tarawasm. Linux arm64 не
поддерживается: сборки tarantool-wasm для него нет, поэтому ни на одном
rock-сервере нет rock'а `wasm` для установки на этой платформе, если только не
упаковать локальную сборку (`tt wasm rock --from`).

На этой странице компонент Python `add(a, b)` собирается в пакет `myapp`.
[HTTP API в компоненте WebAssembly](../write-your-app/http-api-in-wasm.md) -
целое приложение: HTTP-сервер в компоненте, записывающий данные в space.

## Подготовка проекта {#prepare-the-project}

В каталоге пакета, где находится `app.manifest.toml`:

```console
$ tt wasm prepare --lang python
declared wasm 0.1.0 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
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
  rock:       from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
  tarawasm:   .tarantool/bin/tarawasm
  toolchains: python
```

Вывод pip и tarawasm выводится между этими строками, на месте `...`. Что
сделала команда:

- объявила rock `wasm` - нативный модуль релиза - в манифесте и пересчитала
  локфайл:

    ```toml
    [dependencies]
    wasm = { version = "0.1.0", registry = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/" }
    ```

  Сервер - это [rock-индекс](../concepts/rock-servers.md#a-rock-index)
  tarantool-contrib/rocks-repo, раздающий rock для каждой платформы с его
  sha256, поэтому локфайл фиксирует их все, а следующий `tt package build`
  устанавливает rock этой машины, проверенный по локфайлу;
- установила tarawasm в виртуальное окружение в `.tarantool/versions/` и
  создала ссылку `.tarantool/bin/tarawasm`;
- развернула набор инструментов Python, см. ниже;
- записала `.env/activate.sh`, добавляющий `.tarantool/bin` в `PATH` оболочки.

`.tarantool` относится к этой машине и не коммитится. После checkout, на другой
машине или в CI `tt wasm prepare` восстанавливает всё: tarawasm, набор
инструментов каждого компонента и SDK, зафиксированный каждым компонентом.

## Наборы инструментов {#the-toolchains}

tarawasm управляет компиляторами для каждого языка. Из них tt устанавливает в
проект те инструменты, которые можно целиком взять из их релизов, проверяя
каждый по известному tt sha256, в `.tarantool/versions/` со ссылкой в
`.tarantool/bin`:

| Инструмент | Для | |
| --- | --- | --- |
| wasm-tools 1.261.0, wkg 0.16.1 | все языки | релизы Bytecode Alliance |
| componentize-py 0.25.1 | `python` | wheel, который pip устанавливает в окружение tarawasm из скачанного tt файла |
| TinyGo 0.43.0 с Binaryen 133 | `go` | Binaryen ставится поверх TinyGo, которому в версии 0.43 нужен `wasm-opt`; на него тоже создаётся ссылка |
| wit-bindgen 0.62.0 | `c` | |
| wasi-sdk 34 | `c` | не добавляется в `.tarantool/bin`, чтобы его `clang` не подменял системный компилятор при каждой сборке: для каждого C-компонента tarawasm получает путь к нему из `tarawasm.local.json` |

Остальные инструменты устанавливаются вручную: tt проверяет их версии, сообщает,
что установить, и ничего не ставит в систему.

| Язык | Устанавливаемые инструменты |
| --- | --- |
| `python` | python3 3.10 или новее |
| `go` | Go 1.25 - 1.27 |
| `rust` | rustc и cargo 1.96 или новее с target `wasm32-wasip1`, cargo-component 0.21.1 |
| `js` | Node 24 или новее, jco 1.37.0 с componentize-js 0.23.0 и preview2-shim 0.17.8 |
| `c` | ничего дополнительного |
| `csharp` | .NET SDK 10.0.402, в Linux x86-64 |

Отсутствующий инструмент или неподходящая версия останавливают команду с
сообщением о том, что именно нужно установить:

```console
$ tt wasm prepare --lang rust
...
   ⨯ the toolchain is not ready for rust; install, tt does not:
       rustc >=1.96.0 (missing): rustup (https://rustup.rs), then rustup target add wasm32-wasip1
       cargo >=1.96.0 (missing): rustup (https://rustup.rs)
       cargo-component >=0.21.1 (missing): cargo install --locked cargo-component --version 0.21.1
```

Набор инструментов проверяется так же, как его находит сборка: сначала в
`.tarantool/bin`, затем в `PATH`. Команда `tarawasm toolchain setup --install`,
устанавливающая инструменты сама, запускает `sudo apt-get install` на Linux и
собирает `wasm-tools` и `wkg` из исходников; tt никогда её не запускает.

## Добавление компонента {#add-a-component}

```console
$ tt wasm new calc --lang python
...
added component calc in calc
```

tarawasm создаёт компонент в `calc/` на SDK того релиза `wasm`, который
объявлен в пакете, а `calc/.gitignore` оставляет сгенерированные файлы вне git.
WIT world компонента назван по его имени; так же назван и результат сборки,
`dist/calc.wasm`: компоненты пакета не конфликтуют. Манифест объявляет
компонент:

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

`tarawasm all` заново генерирует bindings и собирает `dist/calc.wasm`; сборка
находит tarawasm и компиляторы в `.tarantool/bin` как с `.env/activate.sh`, так
и без него. `incremental = true` пропускает сборку, пока исходники, WIT и локфайл
SDK компонента остаются теми же, что и при прошлой сборке; после смены
компилятора `tt package build --rebuild` пересобирает компонент в любом случае.
См. [Инкрементальные сборки](../concepts/packages.md#incremental-builds).
Lua-компонент проекта с `path = "."` по умолчанию забирает все лежащие ниже
`*.so`: укажите для него `exclude = ["calc/"]`.

SDK компонента зафиксирован в его `tarawasm.tarantool.lock.json` и должен
соответствовать релизу, объявленному в пакете: компонент собирается под WIT
своего SDK, а модуль отдаёт собственный WIT. `tt wasm prepare` отклоняет
компонент на другом SDK, а `tt wasm status` перечисляет такие расхождения в
списке причин, мешающих сборке проекта; `tt package build` ничего о них не
сообщает.

## Написание компонента {#write-it}

Флаг `--template` создаёт компонент Python по образцу: `call` соответствует
примеру ниже, `http` - серверу из руководства
[HTTP API в компоненте WebAssembly](../write-your-app/http-api-in-wasm.md);
затем `tt wasm new` выводит Lua-модуль, использующий компонент.

```console
$ tt wasm new calc --lang python --template call
```

Без шаблона добавьте экспорт в world в файле `calc/wit/app.wit`, куда его
записывает tarawasm:

```wit
package example:app@0.1.0;

world calc {
  include tarantool:tarantool/guest@0.2.0;
  import docs:adder/ssl-bio@0.1.3;
  export add: func(a: s32, b: s32) -> s32;
}
```

и реализуйте его в `calc/main.py` ниже инициализации, созданной tarawasm:

```python
import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import wit_world


class WitWorld(wit_world.WitWorld):
    def add(self, a: int, b: int) -> int:
        return a + b
```

См. [руководство по tarawasm](https://github.com/mandesero/tarawasm/blob/master/docs/tarantool.md)
для остальных языков и API Tarantool в компоненте.

## Сборка и вызов из Lua {#build-it-and-call-it-from-lua}

```console
$ tt package build
$ find .rocks -name wasm.so -o -name '*.wasm'
.rocks/lib/tarantool/wasm.so
.rocks/share/tarantool/myapp/dist/calc.wasm
```

Файл `.wasm` пакета - размещаемый компонентом или записываемый его сборкой в
`share/` - обязан быть бинарным файлом WebAssembly: всё остальное приводит к
ошибке сборки, а core module, который `require('wasm')` не загружает,
раскладывается с предупреждением.

Компонент - это файл пакета в пространстве имён пакета. Загружайте его по пути
от каталога приложения (рабочего каталога инстансов) как в проекте, так и в
установленной версии:

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

`os.exit()` завершает выполнение: без него Tarantool продолжит читать скрипт из
стандартного ввода или откроет консоль в терминале.

Модуль держит скомпилированные компоненты в `~/.cache/wasmtime` пользователя, от
имени которого запущен Tarantool, и продолжает работу без кэша, если не может
туда писать.

## Проверка проекта {#check-the-project}

`tt wasm status` показывает, что подготовила команда `tt wasm prepare` и что
требуется для сборки, ничего не изменяя: rock `wasm` и его сборку для этой
машины, tarawasm, каждый компонент с зафиксированным в нём SDK и наборы
инструментов. При любых недостающих компонентах или инструментах команда
завершается с ошибкой, как при проверке в CI:

```console
$ tt wasm status
wasm:       0.1.0 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
rock:       from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
tarawasm:   .tarantool/bin/tarawasm
component:  calc (python, SDK 0.1.0) in calc
python toolchain:
  ...
ready
```

## Упаковка {#pack-it}

Упаковка с зависимостями выполняется по умолчанию: архив содержит модуль для
платформы, на которой собран пакет, и сам компонент.

```console
$ tt package pack
.../_build/pack/myapp-1.0.0-linux-amd64.tt
$ tt package install _build/pack/myapp-1.0.0-linux-amd64.tt --scope user --yes
installed myapp 1.0.0 into user scope
```

Архив, упакованный с `--without-deps`, получает суффикс `-any.tt`, если у пакета
нет собственных нативных файлов: при установке он скачивает rock `wasm` для
машины, куда устанавливается, с rock-сервера, сверяя его по локфайлу архива.

## Публикация rock {#share-the-rock}

Rock-индекс tarantool-contrib/rocks-repo раздаёт rock для каждой платформы.
Собственный rock-сервер делает то же самое: `tt wasm rock` упаковывает rock
каждой сборки релиза вместе с индексом, превращающим каталог в rock-сервер:

```console
$ tt wasm rock --out dist/rocks
.../dist/rocks/wasm-0.1.0-1.linux-x86_64.rock
.../dist/rocks/wasm-0.1.0-1.macosx-aarch64.rock
.../dist/rocks/manifest
.../dist/rocks/tt-rocks-index.json
```

Раздавайте этот каталог или загрузите его файлы рядом с ассетами релиза и
объявите зависимость оттуда:

```console
$ tt wasm prepare --rock-server https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
...
  rock:       from https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/
```

Объявляется любая версия, которую раздаёт сервер, независимо от того, знает ли
tt этот релиз. Локальная сборка модуля упаковывается только для этой машины - в
`.tarantool/registry`, rock-сервер внутри проекта, - и объявляется оттуда:

```console
$ tt wasm prepare --from target/release/libtarantool_wasm_rs.so
reading the build in target/release/libtarantool_wasm_rs.so
packed wasm-0.1.0-1.linux-x86_64.rock into .tarantool/registry
declared wasm 0.1.0 from .tarantool/registry
...
```

Этот реестр существует только на текущей машине: после checkout запустите
`tt wasm prepare --from` заново, а для установки на другой машине упакуйте пакет
вместе с зависимостями.

## Команды и параметры {#commands-and-options}

| Команда | |
| --- | --- |
| `tt wasm prepare` | rock `wasm`, tarawasm, наборы инструментов и SDK компонентов |
| `tt wasm new NAME --lang L` | компонент |
| `tt wasm status` | чего не хватает для сборки, без изменений; ошибка при отсутствии любого элемента |
| `tt wasm rock --out DIR` | rocks всех платформ релиза для rock-сервера |

- `--version` у `prepare` и `rock` выбирает релиз tarantool-wasm: любой,
  раздаваемый rock-сервером, для `prepare`, либо известный tt для
  `prepare --from` и `rock`; список известных выводит `tt wasm prepare --help`.
- `--rock-server URL` у `prepare` объявляет rock с указанного rock-сервера
  (по умолчанию - rock-индекс tarantool-contrib/rocks-repo).
- `--from FILE` у `prepare` упаковывает локальную сборку модуля в
  `.tarantool/registry` и объявляет rock оттуда; у `rock` - упаковывает её в
  `--out` как rock для `--arch`.
- `--python` у `prepare` задаёт Python для установки tarawasm, `--tarawasm` - то,
  что pip ставит вместо зафиксированного в tt tarawasm, `--no-tarawasm`
  пропускает его установку, `--reinstall` переустанавливает заново.
- `--lang` у `prepare` попутно разворачивает набор инструментов для `python`,
  `go`, `js`, `rust`, `c` или `csharp`; для `new` он обязателен.
- `--dir` у `new` задаёт каталог компонента, `--world` - имя его WIT world
  (по умолчанию совпадает с именем компонента), а `--template` - `http` или
  `call` для Python.

Загруженные файлы кэшируются в каталоге кэша пользователя, на Linux это
`~/.cache/tt/wasm`. `TT_WASM_RELEASES` задаёт JSON-файл с релизами, которые tt
использует вместо встроенных, вместе с `base_url` для их ассетов: это зеркало
для сетей без доступа к интернету; его формат описан в разделе
[Переменные окружения](../reference/environment.md#webassembly-components).
