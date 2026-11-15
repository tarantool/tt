# app.manifest.toml {#app.manifest.toml}

Манифест [пакета](../concepts/packages.md) в корне его проекта. `tt new`
создаёт его заготовку.

```toml
manifest_version = "0.1"

[package]
name = "kv-api"
description = "A key-value API"
include = ["init.lua"]

[platform]
tarantool = ">=3.0.0,<4.0.0"
tt = ">=3.0.0"
registries = ["https://rocks.tarantool.org/", "./vendor-rocks"]

[dependencies]
checks = ">=3.0.0"
mylib = { source = "path", path = "../mylib" }

[dev_dependencies]
luatest = "*"

[components.app]
path = "kv"

[components.native]
path = "native"
[components.native.build]
backend = "lua-c"
module = "fast_hash"
sources = ["fast_hash.c"]

[products.default]
components = ["app", "native"]
default = true

[hooks.pre_build]
backend = "shell"
command = "./gen.sh"
```

Неизвестное поле считается ошибкой, если только в манифесте не объявлена более
новая минорная версия `manifest_version`, чем известна tt, - в этом случае
выводится предупреждение. Более новая мажорная версия отвергается.

## Верхний уровень {#top-level}

| Поле | Обязательно | |
| --- | --- | --- |
| `manifest_version` | да | версия формата: `"0.1"` |
| `manifest_kind` | нет | `"app"` по умолчанию; у дистрибутива tt свой манифест, `tt.manifest.toml` |

## `[package]` {#package}

| Поле | Обязательно | |
| --- | --- | --- |
| `name` | да | строчные буквы, цифры и дефисы, имя начинается с буквы; не `bin` и не `manifests` |
| `description` | нет | одна строка, отображается командой `tt package list` |
| `license` | нет | |
| `license_files` | нет | файлы, включаемые в пакет: в корень архива, в `doc/` rock'а, который упаковывает `--format rock`, по их путям в проекте, и в исходный rock, даже если git их не отслеживает |
| `authors` | нет | |
| `repository` | нет | |
| `include` | нет | файлы проекта, помещаемые в корень архива |
| `generate_version_lua` | нет | `false` исключает `version.lua` из сборки; по умолчанию `true` |

## `[platform]` {#platform}

| Поле | Обязательно | |
| --- | --- | --- |
| `tarantool` | да | версии Tarantool с редакцией: `">=3.0.0"` или `">=3.0.0[ce]"` для Community, `">=3.3.0[ee]"` для Enterprise |
| `tt` | да | версии tt |
| `tcm` | нет | версии TCM, включаемые в архив; без редакции |
| `platforms` | нет | платформы, для которых предназначен пакет: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64` или только `any` |
| `registries` | нет | rock-серверы в порядке обращения вместо серверов по умолчанию: URL HTTP(S) или каталоги, относительно манифеста или абсолютные. См. [Rock-серверы](../concepts/rock-servers.md) |

Ограничение версии - список сравнений через запятую, например
`">=3.0.0,<4.0.0"`.

Rock, который зависит от `tarantool`, проверяется по `tarantool`: такого rock'а
нет ни на одном сервере. Если ни одна из этих версий ему не подходит,
разрешение завершается ошибкой; если не подходит самая младшая, tt предупреждает,
что нижнюю границу стоит поднять. В локфайл для него ничего не попадает.

## `[dependencies]` и `[dev_dependencies]` {#dependencies-and-dev_dependencies}

Зависимость - rock в краткой форме:

```toml
checks = ">=3.0.0"           # ограничение; "*" для любой версии
```

или в полной форме:

| Поле | |
| --- | --- |
| `source` | `"registry"` (по умолчанию) или `"path"` |
| `version` | ограничение версии; обязательно для `registry` |
| `path` | каталог с rock'ом относительно манифеста или абсолютный; обязателен для `path`. См. [Зависимости по пути](#path-dependencies) |
| `registry` | rock-сервер, откуда брать этот rock: URL HTTP(S) или каталог, относительно манифеста или абсолютный |

Rock с указанным `registry` разрешается и устанавливается только с этого
сервера; rock'и, от которых он зависит, берутся из rock-серверов проекта.

Зависимости из `[dev_dependencies]` устанавливаются для тестов и инструментов
проекта и не упаковываются. Компонент может объявлять собственные зависимости в
`[components.<name>.dependencies]`.

### Зависимости по пути {#path-dependencies}

Зависимость `path` собирается из своего каталога в зависимости от того, что в нём
находится:

- `app.manifest.toml`: каталог представляет собой отдельный проект, и tt
  собирает его так же, как `tt package build` собирает пакет, - компоненты его
  продукта по умолчанию, их раскладку и `version.lua`, - и устанавливает в
  `.rocks/` как rock его пакета, с его `license_files` в `doc/` rock'а, как они
  размещены в его бинарном rock'е; если какого-то файла нет, выводится
  предупреждение. Зависимость называется по имени этого пакета или по `rock`
  другого его продукта, который в таком случае и собирается. Её версия
  определяется tt в её каталоге, а её зависимости - это зависимости её продукта
  по умолчанию, разрешаемые вместе с собственными зависимостями проекта:
  относительный `path` берётся относительно самой зависимости, а зависимость с
  `registry` сохраняет свой сервер. Её `[dev_dependencies]` и её локфайл не
  используются; её hook'и не запускаются, а rockspec рядом с манифестом
  игнорируется, каждый раз с предупреждением при разрешении локфайла проекта.
- `*.rockspec`: tt собирает rock, который он описывает.
- ни того, ни другого: собирать и устанавливать нечего, и локфайл лишь
  фиксирует каталог без версии; rock проекта от него не зависит.

Локфайл фиксирует зависимость по пути по её пути, относительному от проекта или
абсолютному, как указано в манифесте, и по хешу её файлов. Абсолютный путь
привязывает локфайл к машине: он действует только там, где каталог расположен по
этому пути. Хеш зависимости с манифестом не учитывает `.git/`, `_build/`,
`.rocks/`, `_runtime/`, её `app.manifest.lock` и то, что игнорируют её файлы
`.gitignore`, поэтому ни её собственная сборка, ни сборка проекта не приводят к
устареванию локфайла; новая версия зависимости приводит к устареванию, даже если
её создали только коммит или тег. Зависимость в подкаталоге репозитория, включая
собственный репозиторий проекта, версионируется исключительно по своему
каталогу (см. [Сборка](../concepts/packages.md#building)): коммит или изменение в
другом месте репозитория не сдвигает её версию. tt собирает такую зависимость в
`_build/path/<name>/` проекта - там располагаются её stage, `TT_BUILD_DIR` её
компонентов и её `CARGO_TARGET_DIR` - и оставляет её каталог без изменений, кроме
того, что туда записывают сами её backend'ы сборки.

```toml
[dependencies]
memcached-contrib = { source = "path", path = "../memcached-contrib" }
```

## `[components.NAME]` {#componentsname}

| Поле | |
| --- | --- |
| `path` | каталог компонента относительно манифеста; обязательно |
| `include`, `exclude` | файлы компонента, включаемые в раскладку; по умолчанию `*.lua` и `*.so`, для `go` по умолчанию ничего |
| `namespace` | куда помещаются файлы и модули компонента в `.rocks/share/tarantool/` и `.rocks/lib/tarantool/`: по умолчанию имя пакета, `""` для верхнего уровня |
| `dependencies` | зависимости компонента |
| `build` | его сборка, см. ниже |
| `kind` | устарело: `"module"` или `"executable"`, то, что компонент заявляет о результатах своей сборки; см. ниже |

Компонент создаёт то, что создаёт его сборка, и раскладывает свои файлы. Сборка
может создавать модули, файлы данных и программы, любые из них одновременно:

| Результат | Куда помещает сборка | Куда устанавливается |
| --- | --- | --- |
| модуль | `TT_OUTPUT_DIR`, `lib/<namespace>/` пакета | `.rocks/lib/tarantool/<namespace>/` |
| файл данных, например `.wasm` | `share/<namespace>/` пакета | `.rocks/share/tarantool/<namespace>/` |
| программа | `bin/` пакета | `.rocks/bin/`, откуда её запускает `tt instance` |

То, что создаёт сборка, определяется полями её backend'а: `module` и `bin` у
`cargo` и `zig` либо `output` у `make`, `shell` и `cmake`; `go` всегда создаёт
программу, `c` и `lua-c` - всегда модуль. tt записывает созданное каждым
компонентом, по типам, в `_build/results.json`. Один компонент пакета cargo с
библиотекой и CLI создаёт и то, и другое:

```toml
[components.fast]
path = "fast"

[components.fast.build]
backend = "cargo"
module = "fast"
bin = "fastctl"
```

Поле `kind` всё ещё принимается в `manifest_version = "0.1"` с предупреждением,
`components.<name>.kind is deprecated: a component makes what its build makes;
remove it`, как заявление компонента о результатах своей сборки, которое tt
проверяет. `"executable"` означает, что сборка создаёт программы и ничего больше:
компонент не раскладывает никаких своих файлов, поэтому `include`, `exclude` и
`namespace` вызывают ошибку, блок `build` обязателен, `TT_OUTPUT_DIR` его
сборки служит каталогом программ, простая строка в `output` считается программой,
а `cargo` или `zig` без `module` и `bin` собирают программу с именем
компонента. `"module"` означает, что сборка не создаёт программ. Backend,
который не умеет создавать то, что указано в `kind`, или блок, запрашивающий то,
что `kind` исключает, считаются ошибкой до начала сборки чего бы то ни было, в
`components.<name>.kind`: `is "executable", but backend "c" makes no programs`,
`is "module", but the build makes programs`. Сборка, которая всё же создаёт
такой результат, завершается с кодом возврата 2: `kind = "executable", but the
build made more than programs: <files>`, `kind = "module", but the build made
programs: <files>`. В `manifest_version = "0.2"` поля `kind` нет.

Файл `.wasm`, раскладываемый компонентом или записываемый его сборкой в
`share/`, должен быть бинарным файлом WebAssembly: файл другого вида приводит к
ошибке сборки, а core-модуль, который tarantool-wasm не загружает,
раскладывается с предупреждением. Проверка выполняется так же, как в
стандартном модуле `wasm`. `tt wasm new` объявляет компоненты, собираемые
tarawasm; см. [Запуск компонентов WebAssembly](../how-to/wasm-components.md).

### `[components.NAME.build]` {#componentsnamebuild}

| Поле | Backend'ы | |
| --- | --- | --- |
| `backend` | | `c`, `lua-c` (модули); `go` (программы); `cargo`, `zig` (модули и программы); `cmake`, `make`, `shell` (модули, файлы данных и программы) |
| `cwd`, `env` | все | каталог запуска сборки, её окружение |
| `output` | `make`, `shell`, `cmake` | файлы, создаваемые сборкой, которые tt копирует по местам назначения, см. ниже; обязательно для `cmake` |
| `incremental` | `make`, `shell`, `zig` | `true` пропускает сборку, когда её входные данные совпадают с прошлой сборкой и оставленное ею всё ещё на месте |
| `make_target`, `entrypoint`, `flags` | `make` | `make_target` обязательно |
| `command`, `args` | `shell` | `command` обязательно |
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `defines` | `c`, `lua-c` | `module` и `sources` обязательны |
| `cflags` | `c`, `lua-c` | флаги компиляции исходников на C и ассемблере, по одному в элементе, после собственных флагов tt и перед `CFLAGS` окружения; см. ниже |
| `cxxflags` | `c`, `lua-c` | флаги компиляции исходников на C++, по одному в элементе, после собственных флагов tt и перед `CXXFLAGS` окружения |
| `module` | `cargo` | собирает библиотеку пакета как модуль с этим именем; блок без `module` и `bin` собирает библиотеку под её собственным именем |
| `platforms.<os>` | `c`, `lua-c` | дополнительные `include_dirs`, `libraries`, `library_dirs`, `defines`, `cflags`, `cxxflags`, `ldflags` для одной ОС, `linux` или `darwin`, после списков сборки |
| `package` | `go`, `cargo` | пакет Go (по умолчанию `.`) или пакет cargo |
| `static` | все | `true`: то, что создаёт сборка, не требует ничего от машины установки, и сборка это проверяет; см. ниже |
| `ldflags` | `go`, `c`, `lua-c` | флаги компоновщика, по одному в элементе: для `go build -ldflags` - после собственных флагов tt; для компоновки `c` и `lua-c` - перед объектами; см. ниже |
| `name`, `tags`, `cgo` | `go` | по умолчанию `name` - имя компонента; cgo по умолчанию выключен |
| `bin`, `features`, `profile` | `cargo` | `bin` собирает бинарный файл с этим именем, программу с тем же именем; `profile` по умолчанию `release` |
| `module`, `bin` | `zig` | модуль и программа, которые сборка берёт из установленного `zig build`; блок указывает одно из них или оба |
| `steps`, `optimize`, `options` | `zig` | шаги `zig build`, по умолчанию его шаг по умолчанию; режим `Debug`, `ReleaseSafe`, `ReleaseFast` или `ReleaseSmall`, по умолчанию `ReleaseSafe`; `-D<name>=<value>` файла `build.zig` |
| `variables`, `targets`, `build_type` | `cmake` | `-D<name>=<value>` конфигурации, собираемые цели (по умолчанию все) и `CMAKE_BUILD_TYPE` (по умолчанию `RelWithDebInfo`); `output` обязательно |

#### `output` {#output}

`output` перечисляет файлы, создаваемые сборкой, которые должны войти в пакет.
Это поле предназначено для сборки, чей инструмент записывает файлы куда ему
угодно, - Makefile, скрипт, проект CMake, - и tt забирает каждый файл оттуда
после завершения сборки и помещает в пакет. Элемент представляет собой строку,
путь к файлу, либо таблицу `{ path = "...", to = "..." }`, указывающую место
назначения. Одиночный элемент можно записать без массива:

```toml
output = "dist/calc.wasm"                       # то же самое, что и ["dist/calc.wasm"]
output = { path = "build/tool", to = "bin" }    # то же самое, что и [{ ... }]
output = ["build/tool.so", { path = "build/tool", to = "bin" }]
```

`path` задаётся относительно того места, откуда backend забирает файл:

| Backend | `path` задаётся относительно |
| --- | --- |
| `make`, `shell` | рабочего каталога сборки: `cwd`, задаваемого относительно проекта, или каталога компонента, если у блока нет `cwd` |
| `cmake` | дерева сборки, `_build/component/<component>/` проекта (`TT_BUILD_DIR`) |
| backend модуля | того места, которое указано в его документации; backend, чей результат копирует tt, забирает его из рабочего каталога, как и `make` |

Файл забирается после успешного завершения сборки; файл, который сборка не
создала, приводит к ошибке с кодом возврата 2:

```text
⨯ building component "rates": copy declared output "dist/rates.json": opening the source: open /home/me/shop/rates/dist/rates.json: no such file or directory
```

Инкрементальная сборка, не требующая обновления, снова забирает файл из того же
места и запускает сборку заново, если файл оттуда пропал.

Параметр `to` указывает, куда направляется файл. tt помещает его туда под его
базовым именем - каталоги из `path` отбрасываются - внутри пакета. Команда
`tt package build` устанавливает пакет в `.rocks/` проекта, `tt package pack`
упаковывает этот `.rocks/` в архив, а `tt package install` устанавливает его по
тем же путям: в `.rocks/` проекта или версии приложения,
`<root>/versions/<name>/<name>_<version>/.rocks/`, в пользовательском и системном
scope:

| `to` | Чем является файл | Куда устанавливается в `.rocks/` |
| --- | --- | --- |
| `lib` | модуль, загружаемый через `require` | `lib/tarantool/<namespace>/<file>` |
| `share` | файл данных, например `.wasm` | `share/tarantool/<namespace>/<file>`, рядом с файлами Lua пакета |
| `bin` | программа, исполняемый файл компонента, по которому его находит `tt instance` | `bin/<file>` |

`<namespace>` - это `namespace` компонента, по умолчанию имя пакета. Элемент
без `to` считается модулем (`lib`), за исключением backend'а, создающего файлы
только одного типа, чей тип он тогда и принимает, - файл данных backend'а файлов
данных, - и компонента с `kind = "executable"`, чьи файлы считаются программами.
Программа проверяется так же, как и любая программа.

Это отличается от `include`, который раскладывает файлы из каталога компонента
по их исходным путям: `include = ["dist/calc.wasm"]` устанавливает
`share/tarantool/<namespace>/dist/calc.wasm`, а
`output = { path = "dist/calc.wasm", to = "share" }` устанавливает
`share/tarantool/<namespace>/calc.wasm`. Файл, указанный в `output`, не
раскладывается, если только его не захватывает `include`.

Пакет, чья сборка создаёт файл JSON, читаемый кодом на Lua:

```text
shop/
├── app.manifest.toml
├── app/init.lua
└── rates/build.sh
```

`shop/app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "shop"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[components.app]
path = "app"

[components.rates]
path = "rates"

[components.rates.build]
backend = "shell"
command = "sh"
args = ["build.sh"]
output = { path = "dist/rates.json", to = "share" }
```

`shop/rates/build.sh`, запускаемый в `rates/`:

```sh
mkdir -p dist
printf '{"usd": 90}\n' > dist/rates.json
```

`shop/app/init.lua`, модуль `shop`, находящий файл рядом с собой:

```lua
local fio = require('fio')
local json = require('json')

local M = {}

-- rates.json is a data file of the package: tt installs it into the
-- directory of the package, next to this file.
function M.rates()
    local file = assert(fio.open(fio.pathjoin(debug.sourcedir(), 'rates.json')))
    local data = file:read()
    file:close()
    return json.decode(data)
end

return M
```

```console
$ cd shop
$ tt package build
$ find .rocks/share/tarantool/shop -type f | sort
.rocks/share/tarantool/shop/init.lua
.rocks/share/tarantool/shop/rates.json
.rocks/share/tarantool/shop/version.lua
$ tt run -e "print(require('shop').rates().usd) os.exit()"
90
```

`rates/build.sh` и `rates/dist/` в раскладку не попадают: по умолчанию `include`
берёт `*.lua` и `*.so`. `debug.sourcedir()` возвращает каталог файла Lua,
вызывающего функцию, поэтому модуль находит файл везде, где установлен пакет.
Код вне пакета читает его по пути от каталога приложения, служащего рабочим
каталогом его инстансов и команды `tt run`:
`.rocks/share/tarantool/shop/rates.json`.

Любое другое значение `to` считается ошибкой: `components.rates.build.output[0].to:
"etc" is not lib, share or bin`, как и таблица без `path`; значение, не
являющееся ни строкой, ни таблицей, ни массивом из них, даёт `output must be a
file, a table { path = ..., to = ... } or an array of them, got integer`.
Backend, не создающий файлов такого типа, отвергает элемент:
`components.a.build.output[0].to: backend "wasm-tool" makes no data files`.
Поле `output` у backend'а, не имеющего такого поля (например, `c`), отвергается
так же, как любое поле чужого backend'а ниже.

Поле backend'а, отличного от используемого в сборке, считается ошибкой, поскольку
оно ничего бы не делало: `cflags` в сборке `make`, `make_target` в сборке или
hook'е `shell`, `output` в сборке `c`. Поле принадлежит блоку независимо от его
значения, в том числе `make_target = ""`. Ошибка перечисляет backend'ы, у
которых есть это поле, модуль за модулем в порядке имён модулей, а внутри модуля
- в его порядке: для стандартного модуля `build` сначала `make` и `shell`, затем
остальные по имени: `components.a.build.ldflags: is a field of backend c, go or
lua-c, not of "cargo"`.

Описанные выше backend'ы принадлежат `build`, стандартному модулю tt
(`modules/build` его репозитория): в ядре собственных backend'ов нет, а в
официальном tt и в каждом дистрибутиве, включающем стандартные модули, они есть.
Модуль, с которым собран tt, может предоставлять дополнительные backend'ы (см.
[Написание backend'а сборки](../how-to/write-a-build-backend.md)) со своими
собственными полями, проверяемыми так же, как поля `build`, а их сборки проходят
через те же шаги: до начала любой сборки, в описанном ниже окружении, и
раскладываются, фиксируются и пропускаются через `incremental` точно так же, как
сборки `build`. В дистрибутиве, исключающем `build` (см.
[Стандартные модули](tt-manifest.md#standard-modules)), есть только backend'ы
его остальных модулей.

Блок сборки с backend'ом, который не предоставляет ни один модуль этого tt,
вызывает предупреждение: `no module of this tt provides backend "<name>"; its
fields are not checked` для команд, которые ничего не собирают, таких как
`tt package add` или `tt package pack --format rock-src`. Как только tt предстоит
собрать компонент - командами `tt package build` и `pack`, а также
`tt package fetch` для зависимости по пути или собираемого ею исходного rock'а, -
это приводит к ошибке ещё до начала сборки чего бы то ни было, и ошибка
называет зависимость. Поля блока сборки проверяются при проверке манифеста, а не
при его чтении: по правилам версии формата выше для полей, которых нет ни у
одного backend'а, и с путём поля для значения неверного типа:
`components.a.build.cflags: must be an array, not a string`.

Сборка получает свой контракт через переменные окружения: `TT_OUTPUT_DIR`, куда
она помещает создаваемые модули и который уже существует к началу сборки (для
компонента с `kind = "executable"` - программы), `TT_PROJECT_ROOT`,
`TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION`, `TT_PLATFORM_OS` и
`TT_PLATFORM_ARCH`. Если tt знает Tarantool, под который выполняется сборка,
сборка также получает `TT_TARANTOOL_PREFIX` (префикс установки этого Tarantool)
и `TT_TARANTOOL_INCLUDE_DIR` (каталог его файлов `module.h` и `lua.h`,
`<prefix>/include/tarantool`, под которые компилируют backend'ы `c` и `lua-c`);
если tt его не знает, обе переменные не заданы, как и
`TT_TARANTOOL_INCLUDE_DIR`, если `module.h` отсутствует (см.
[Заголовки для rock'ов с частью на C](../how-to/binaries.md#headers-for-rocks-with-a-c-part),
где описано, где tt их ищет). Поле `env` блока сборки не может их
переопределить. Если tt не находит Tarantool вовсе или не находит его
заголовков, компонент `c` или `lua-c` завершает сборку ошибкой ещё до её
начала из-за отсутствия заголовков с указанием каталогов, где tt их искал.

`TT_BUILD_DIR` - собственный каталог сборки для промежуточных результатов,
создаваемых из исходников: объектных файлов, дерева сборки. Это
`_build/component/<component>/`, существующий к началу сборки и сохраняющийся
между сборками, так что сборка `make` пересобирает только изменившееся; флаг
`--rebuild` сначала очищает его, и то же происходит при сборке блока, изменившегося
со времени прошлой сборки. Сборка `make` или `shell`, пишущая туда, а не рядом с
исходниками, оставляет каталог компонента нетронутым, как и каталог зависимости
по пути - другого рабочего дерева.

`c` и `lua-c` компилируют каждый исходник из `sources` относительно `cwd`
в объектный файл в `TT_BUILD_DIR` и компонуют объекты в один модуль,
`<module>.so` (`<module>.dylib` на macOS), в каталоге `TT_OUTPUT_DIR`.
Расширение исходного файла определяет его язык, а значит, компилятор и его
флаги:

| Расширение | Язык | Компилятор | Флаги языка |
| --- | --- | --- | --- |
| `.c` | C | `$CC`, `cc` по умолчанию | `cflags`, `$CFLAGS` |
| `.cc`, `.cpp`, `.cxx`, `.c++` | C++ | `$CXX`, `c++` по умолчанию | `cxxflags`, `$CXXFLAGS` |
| `.S`, `.s` | ассемблер, `.S` проходит через препроцессор | `$CC` | `cflags`, `$CFLAGS` |

Расширения чувствительны к регистру, а `.C` не поддерживается: на macOS `a.C`
и `a.c` - один и тот же файл. Исходник с другим расширением считается
ошибкой: `components.hashkit.build.sources[1]: "fnv.h" is not a source tt
compiles: want a name ending in .c, .cc, .cpp, .cxx, .c++, .S or .s; a
prebuilt library goes into libraries and library_dirs` - как и исходник,
указанный дважды. Каждый исходник компилируется отдельно, исходники модуля -
одновременно, в столько потоков, сколько у машины ядер процессора:

```console
$CC -O2 -fPIC <cflags> <$CFLAGS> -I<include dir of Tarantool> -D<defines> -I<include_dirs> \
    -MD -MF <object>.d -MQ <object> -c <source> -o <object>
$CXX -O2 -fPIC <cxxflags> <$CXXFLAGS> -I<include dir of Tarantool> -D<defines> -I<include_dirs> \
    -MD -MF <object>.d -MQ <object> -c <source> -o <object>
```

а объектные файлы компонуются с помощью `$CXX`, если среди исходников есть C++,
и с помощью `$CC` в противном случае:

```console
$CXX -shared <ldflags> -o <module>.so <objects> \
    -L<library_dirs> -Wl,-rpath,<library_dirs> -l<libraries> <$LDFLAGS>
```

Флаги компиляции выстраиваются по цепочке: сначала собственные флаги tt,
затем проекта, затем пользователя: `-O2`, опускаемый, когда переменная
соответствующего языка, `CFLAGS` или `CXXFLAGS`, задана (даже пустой), и
`-fPIC`; затем `cflags` или `cxxflags`; затем разбитая по пробелам переменная
`CFLAGS` или `CXXFLAGS` окружения, которая идёт последней и потому разрешает
конфликты в свою пользу, аналогично тому, как `CFLAGS` идёт после `AM_CFLAGS`
в autotools: `CFLAGS="-O0 -g"` компилирует исходники на C для отладчика, а
`CFLAGS=-Wno-error` собирает проект, в чьих `cflags` указан `-Werror`,
компилятором, выдающим новые предупреждения. Флаги одного языка не доходят
до исходников другого (`-std=c11` привело бы к ошибке компиляции C++), тогда
как `defines`, `include_dirs`, `-fPIC` и заголовки Tarantool передаются обоим.
Компоновка не получает никаких флагов компиляторов: флаг, нужный как при
компиляции, так и при компоновке (`-pthread`, `-fsanitize=address`, `-flto`,
`-m32` или `--target=...`), указывается в `cflags` (и `cxxflags`) и в `ldflags`.
Флаги `ldflags` идут перед объектными файлами, как в вызове
`$(CC) $(LDFLAGS) -o out objs $(LDLIBS)` утилиты make, так что `-Wl,--as-needed`
действует на последующие `-l`; строка завершается переменной `LDFLAGS`
окружения. Список `platforms.<os>` идёт после списка самой сборки; `<os>` - это
`linux` или `darwin`, операционные системы, для которых собирает tt, а любая
другая (например, `macos`) считается ошибкой. На macOS вместо `-shared`
передаётся `-bundle -undefined dynamic_lookup -all_load`, а `-Wl,-rpath` не
передаётся.

Переменные `CC` и `CXX` делятся на слова так же, как shell делит команду, с
поддержкой `'...'`, `"..."` и `\` и без подстановок, поэтому компилятор можно
запускать через обёртку или с собственными аргументами: `CC="ccache cc"`,
`CC="zig cc" CXX="zig c++"`, `CC="'/opt/my tools/gcc'"`. Первое слово ищется в
`PATH` сборки. `CC`, `CXX`, `CFLAGS`, `CXXFLAGS` и `LDFLAGS` берутся из
окружения, в котором запущен tt, и компилятор не читает ни одну из них: в `env`
сборки каждая из них считается ошибкой, поскольку никуда бы не дошла, а флаги
проекта задаются в `cflags`, `cxxflags` и `ldflags`. То же относится к
`CPPFLAGS`, `LIBS` и `LDLIBS`, которые не читают ни tt, ни компилятор:
`defines`, `include_dirs`, `cflags` и `cxxflags` принимают то, что
передавалось бы через `CPPFLAGS`, а `libraries` и `library_dirs` - через `LIBS`.
`tt -V package build` показывает каждую команду перед её запуском и вывод
успешной компиляции (её предупреждения); без `-V` упавшая компиляция выводит
свою команду и то, что она напечатала, в тексте ошибки.

Модулю на C++ требуется библиотека C++. На Linux сборка с `static = true`
линкует `libstdc++` в модуль с флагами
`-static-libstdc++ -Wl,--exclude-libs,ALL`, причём второй флаг исключает её
символы из экспорта модуля: в сам Tarantool библиотека `libstdc++` уже
встроена, поэтому на машине выполнения `libstdc++.so.6` может отсутствовать.
Однако статическая `libstdc++` из нового gcc требует glibc той машины, на
которой она была собрана (так, gcc 15 требует glibc 2.38 там, где модулю на
C достаточно 2.14), поэтому такой модуль загружается только там, где
Tarantool работает на столь же новой glibc; сборка предупреждает, если эта
версия новее glibc того Tarantool, под который собирается модуль. Без `static`
модулю требуется `libstdc++.so.6` машины. На macOS модулю требуется
`/usr/lib/libc++.1.dylib` - системная библиотека C++, которая нужна и самому
Tarantool, и `static = true` её допускает. `zig c++` (`CXX="zig c++"` или
компилятор, среди слов которого есть `zig`, а сразу за ним `cc` или `c++`: так
его называют и через обёртку, `ccache zig cc`) линкует собственную `libc++` в
каждый модуль статически со скрытием символов, поэтому его компоновка не
получает ни одного из этих флагов (zig отклоняет `--exclude-libs`), а модуль не
требует библиотеки C++ независимо от значения `static`. Скрипт другого имени,
который запускает zig, за zig не считается.

```toml
[components.memcached.build]
backend = "lua-c"
module = "datapath"
sources = ["datapath.c", "ring.cpp"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]
cxxflags = ["-std=c++17", "-Wall"]

[components.memcached.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]

[components.memcached.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
```

Исходник на C++ для `lua-c` подключает Lua API через `#include <lua.hpp>`:
заголовочные файлы `lua.h`, `lauxlib.h` и `lualib.h` Tarantool не содержат
`extern "C"`, поэтому исходник C++, подключающий их напрямую, объявляет Lua
API с именами C++, и модуль не загружается: `undefined symbol:
_Z15luaL_checkstackP9lua_StateiPKc`. Файл `module.h` объявляет собственные
функции как `extern "C"`. Точка входа модуля на C++ также объявляется как
`extern "C"`.

`lua-c` проверяет модуль после компоновки: сборка завершается ошибкой, если
модуль не экспортирует `luaopen_<name>` - функцию, которую вызывает
`require('<name>')` (`<name>` - это `<namespace>.<module>` или `<module>`
при `namespace = ""`, где точки заменены на `_`, а часть до первого `-`
отброшена, как это делает Lua: `require('my-app.fast')` вызывает
`luaopen_app_fast`), а также если модуль требует функции Lua API по их именам
в C++. Проверяется каждый срез универсального файла Mach-O. Модуль `c`, чьи
функции Tarantool вызывает по именам `box.schema.func`, этим проверкам не
подлежит.

```text
⨯ building component "hashkit": .../hashkit/_build/rock/default/lib/hashkit/fnv.so: the module needs functions of the Lua API by their C++ names: _Z17luaL_checkintegerP9lua_Statei, _Z15lua_pushintegerP9lua_Statel: lua.h, lauxlib.h and lualib.h of Tarantool declare no extern "C", so a C++ source includes them through <lua.hpp>, which does, or within extern "C" { }
⨯ building component "hashkit": .../hashkit/_build/rock/default/lib/hashkit/fnv.so: the module exports no luaopen_hashkit_fnv, the function require("hashkit.fnv") calls: define int luaopen_hashkit_fnv(lua_State *L), extern "C" in C++, or set module to the name the module is for
```

Сборка компилирует только то, что изменилось с прошлой сборки. Объектный
файл компилируется заново, когда изменилась его командная строка, компилятор
(его бинарный файл, размер и время изменения этого файла, вывод `-dumpmachine`
и `--version`), переменная окружения, которую читает компилятор (`CPATH`,
`SOURCE_DATE_EPOCH`, `SDKROOT`, `MACOSX_DEPLOYMENT_TARGET` и подобные,
`C_INCLUDE_PATH` для исходников на C или ассемблере, `CPLUS_INCLUDE_PATH` для
C++; но не `TMPDIR`), его исходник или файл, прочитанный прошлой компиляцией:
каждый заголовок из списка `-MD`, включая заголовки Tarantool и системы. Модуль
компонуется заново, когда изменился объектный файл, команда компоновки,
компилятор компоновки, `LIBRARY_PATH` или файл из `libraries` в `library_dirs`,
либо когда сам модуль отсутствует. Файл, указанный только во флаге (например,
version script в `ldflags`), и библиотеки, найденные в других местах, не
отслеживаются: `tt package build --rebuild` пересобирает всё заново, как и
изменение блока сборки. Объектные файлы и записи сборки о них хранятся в
`_build/component/<component>/obj/`.

Каждая сборка также записывает
`_build/component/<component>/compile_commands.json` - базу данных компиляции
исходников компонента с командой для каждого из них, предназначенную для clangd
и других читающих её инструментов. clangd ищет этот файл только в каталогах
выше исходника и в их `build/`, поэтому для каждого компонента на C его
указывают в файле `.clangd` проекта:

```yaml
If:
  PathMatch: memcached/.*
CompileFlags:
  CompilationDatabase: _build/component/memcached
```

`CompilationDatabase` задаёт каталог этого файла относительно каталога
`.clangd`. Для проекта с одним таким компонентом подходит и ссылка в корне
проекта, которую clangd находит над любым исходником:
`ln -s _build/component/memcached/compile_commands.json .` с добавлением
`/compile_commands.json` в `.gitignore`.

`cmake` собирает проект CMake из `cwd`, где расположен его `CMakeLists.txt`, вне
исходников, в каталоге `TT_BUILD_DIR`:

```console
cmake -S <cwd> -B _build/component/<component> -DCMAKE_BUILD_TYPE=<build_type> \
    -D<name>=<value>... -DCMAKE_PREFIX_PATH=<prefix of Tarantool>
cmake --build _build/component/<component> --config <build_type> --parallel <CPUs> \
    --target <targets>...
```

и копирует перечисленные в `output` файлы относительно дерева сборки туда, куда
направляется каждый из них; файл, который сборка не создала, приводит к ошибке.
Префикс Tarantool добавляется после префиксов, заданных переменной
`CMAKE_PREFIX_PATH` в `variables`, так что вызов `find_path(... tarantool/module.h)`
из типового `FindTarantool.cmake` находит его заголовки. Дерево сборки
сохраняется между сборками, и cmake пересобирает изменившееся в нём; флаг
`--rebuild` конфигурирует его заново, как и сборка перемещённого проекта.
Переменная `CMAKE_BUILD_PARALLEL_LEVEL`, если она задана, определяет число
задач, а `CMAKE_GENERATOR` в `env` выбирает другой генератор. На macOS модуль
`.so` копируется как `.dylib`, который загружает там Tarantool. Программа
сборки задаётся элементом `output` с `to = "bin"`. На `PATH` должен присутствовать
CMake 3.15 или новее. Компонент конфигурируется с версией политик, запрошенной
его `cmake_minimum_required`: CMake 4 отвергает версии старше 3.5, а переменная
`CMAKE_POLICY_VERSION_MINIMUM`, устанавливаемая tt для собираемых им rock'ов, не
пробрасывается дальше, тогда как переменная, заданная в `env` или в окружении
запуска tt, пробрасывается.

```toml
[components.memcached]
path = "memcached"
include = ["init.lua"]
[components.memcached.build]
backend = "cmake"
cwd = "."
variables = { ENABLE_BUNDLED_SMALL = "ON" }
targets = ["internalso"]
output = ["memcached/internal.so"]
```

`cargo` собирает библиотеку пакета для `module`: `cdylib`, являющуюся нативным
модулем `<module>.so` (`<module>.dylib` на macOS); бинарный файл `bin` для
`bin`, программу `bin`; оба при указании обоих; и библиотеку под её собственным
именем, если не указано ни то, ни другое. Сборка получает `CARGO_TARGET_DIR`,
`_build/cargo/`, общий для всех cargo-компонентов проекта, подобно тому как
пакеты workspace разделяют его `target/`: то, от чего они зависят,
компилируется один раз. Настройка `target-dir` конфигурации cargo уступает этой
переменной, но `CARGO_TARGET_DIR` или `CARGO_BUILD_TARGET_DIR`, заданные в `env`
или в окружении запуска tt, сохраняются. Библиотека и бинарный файл забираются
оттуда, куда cargo сообщает об их сборке. `--rebuild` запускает cargo как
обычно, и cargo пересобирает изменившееся, включая смену компилятора. Каталог
`target/` собираемого cargo компонента в раскладку не попадает. См.
[Сборка модуля на Rust](../how-to/rust-modules.md).

`zig` запускает `zig build` для `build.zig` из `cwd`, устанавливая результаты в
`_build/component/<component>/zig-out/` и сохраняя кеш в соседнем каталоге
`zig-cache/`:

```console
zig build <steps>... -p _build/component/<component>/zig-out \
    --cache-dir _build/component/<component>/zig-cache \
    [--pkg-dir _build/component/<component>/zig-pkg] [--release=<mode>] -D<name>=<value>...
```

Флаг `--pkg-dir`, указывающий каталог, где zig 0.17 сохраняет скачиваемые для
`build.zig.zon` пакеты (по умолчанию соседний `zig-pkg/`), передаётся zig версии
0.17 и новее, если только не задана переменная `ZIG_LOCAL_PKG_DIR`,
экспортированная в окружении запуска tt или в `env` блока; zig 0.16 хранит их в
своём глобальном кеше и отвергает этот флаг.

Параметр `optimize` передаётся как `--release=safe`, `fast` или `small`, а
`Debug` передаётся без флага, так что `build.zig`, не объявляющий `-Doptimize`,
тоже работает; `options` передаются в порядке своих имён, и `optimize` среди них
быть не должно. Модуль `module` берётся из `lib/` префикса: `<module>.so` или
`.dylib`, как его устанавливает `installModule` из zig-tarantool, либо
`lib<module>.so` или `.dylib`, как библиотеку устанавливает `b.installArtifact`,
и копируется как `<module>.so` (`<module>.dylib` на macOS); модуль с точкой в
имени, `a.b`, берётся там как `lib/a/b.so` и помещается как `a/b.so` в
namespace компонента. Программа `bin` берётся из `bin/` префикса. Блок, не
указывающий ни того, ни другого, считается ошибкой, как и модуль или программа,
которые сборка не установила, - при этом перечисляется то, что она установила.
Флаг `--rebuild` очищает префикс, кеш и пакеты. Каталоги `zig-out/` и `zig-pkg/`
компонента, собираемого zig, в раскладку не попадают. Входными данными
инкрементальной сборки `incremental` также служат версия zig и файлы `build.zig`
и `build.zig.zon` из `cwd`. zig сам скачивает зависимости из `build.zig.zon` в
процессе сборки. См. [Сборка модуля на Zig](../how-to/zig-modules.md).

Значение `static = true` требует, чтобы результат сборки не нуждался ни в чём от
машины, на которую устанавливается пакет, и сборка это проверяет: файл,
требующий большего, завершает сборку ошибкой с кодом возврата 2, называя файл и
то, что ему требуется.

- Программе не нужны загрузчик и динамические библиотеки. Сборкам `go`,
  `cargo` и `zig` даётся указание собирать её именно так только под Linux:
  `cargo` собирает бинарный файл под musl, а модуль из того же блока - как
  обычный модуль; `zig` собирает программу из блока без модуля под musl,
  `-Dtarget=<arch>-linux-musl`, когда `build.zig` принимает `-Dtarget` и в
  `options` не указан `target`, а программу из блока с модулем - под target
  модуля; то, что собирают `cmake`, `make` и `shell`, только проверяется.
- Модулю не нужны никакие динамические библиотеки, кроме библиотек стандартной
  библиотеки C, которые сам Tarantool уже загрузил при старте: на Linux это
  `libc.so.6`, `libm.so.6`, `libdl.so.2`, `libpthread.so.0`, `librt.so.1`,
  загрузчик `ld-linux-*` или (для musl) `ld-musl-*` и `libc.musl-*`, а также
  `libgcc_s.so.1` - среда выполнения GCC, необходимая любой сборке Tarantool; на
  macOS - `/usr/lib/libSystem.B.dylib` и `/usr/lib/libc++.1.dylib` (системная
  библиотека C++, которая требуется и самому Tarantool) и каждый срез
  универсального бинарного файла. Нативными файлами считаются те, которые сборка
  записывает в `TT_OUTPUT_DIR`, и файлы компонента, захватываемые раскладкой. От
  backend'а ничего не требуется, кроме `c` и `lua-c`, которые линкуют
  `libstdc++` в модуль на C++ под Linux: библиотека, нужная модулю (например,
  `libsasl2.so.2`), линкуется статически из своего `.a` либо загружается во
  время выполнения через `dlopen`, а модуль на C++ другого backend'а линкует
  `libstdc++` с флагом `-static-libstdc++`. Модуль, требующий более новую
  glibc, чем Tarantool, под который он собран, собирается с предупреждением: он
  не загрузится там, где этот Tarantool работает со старой glibc.

Входными данными инкрементальной сборки `incremental` являются её блок сборки и
файлы компонента, кроме скрытых, `_build/`, `_runtime/` и того, что игнорируют
файлы `.gitignore` проекта и компонента: то, что сборка генерирует сама и что git
не отслеживает, входными данными не считается. Хеш входных данных её последней
сборки сохраняется в `_build/cache/<component>.json`, а флаг `--rebuild` команд
`tt package build` и `tt package pack` всё равно запускает сборку; так же
поступает и следующая сборка после `tt package clean <component>`, удаляющей хеш
и созданное сборкой. См. [Инкрементальная сборка](../concepts/packages.md#incremental-builds)
и [Очистка](../concepts/packages.md#cleaning).

Каталог `.tarantool/bin` проекта, куда команды `tt binary` и `tt wasm prepare`
помещают программы проекта, идёт первым в `PATH` сборки и hook'а: параметр
`command` может ссылаться на такую программу без выполнения
`.env/activate.sh`. Программа ищется только в этом `PATH`: блок, чей `env`
задаёт `PATH`, получает программы из этого `PATH` и из `.tarantool/bin`, а
программа, присутствующая только в собственном `PATH` tt, найдена не будет.

`tt -V package build` выводит в лог каждую команду, запускаемую сборкой или
hook'ом, и каталог её выполнения перед её запуском, а также показывает её
вывод; без флага `-V` эта информация вместе с самой командой выводится только при
ошибке.

## `[products.NAME]` {#productsname}

| Поле | |
| --- | --- |
| `components` | компоненты продукта; обязательно |
| `default` | `true` для продукта, выбираемого `build` и `pack` без флага `--product`; ровно один продукт является продуктом по умолчанию |
| `rock` | имя rock'а, под которым выпускается продукт, `[a-z][a-z0-9-]*`; по умолчанию имя пакета для продукта по умолчанию, отсутствует для остальных |

Манифест без секции `[products]` содержит один продукт, `default`, состоящий из
всех компонентов.

Пакет из нескольких rock'ов, например модуль и сопутствующие утилиты, задаёт
каждому продукту, от которого зависят другие проекты, собственный `rock`. Два
продукта, выпускаемые как два rock'а, не имеют общих компонентов, поскольку
дерево установки обоих содержало бы их файлы дважды, а файл `version.lua`
продукта помещается под именем его rock'а, `<rock>/version.lua`, а не под именем
пакета. Команда `tt package pack --format rock --product NAME` упаковывает
продукт как его rock; продукт без указания rock упаковать нельзя. Зависимость по
пути представляет собой продукт пакета в её каталоге: продукт по умолчанию по
имени пакета, другой продукт - по имени его rock'а.

## `[hooks.pre_build]`, `[hooks.post_build]` и `[hooks.clean]` {#hookspre_build-hookspost_build-and-hooksclean}

Команда, запускаемая до или после сборки компонентов с помощью backend'а `shell`
(по умолчанию) или `make` и полей сборки. Hook получает окружение сборки без
`TT_OUTPUT_DIR`, `TT_COMPONENT_NAME` и `TT_BUILD_DIR`, а файлы из его `output`
не копируются. Hook указывает backend, поддерживающий запуск hook'ов: `make` или
`shell` стандартного модуля `build` либо backend модуля, объявляющего такую
поддержку; любой другой считается ошибкой: `hooks.pre_build: backend "cmake" is
not supported (want make or shell)`. tt без backend'а, поддерживающего hook'и
(например, дистрибутив без `build`), отклоняет любые hook'и, в том числе не
указывающие backend вовсе: `hooks.pre_build: backend "shell" is not supported: no
module of this tt provides a backend that runs hooks`.

`[hooks.clean]` запускается командой `tt package clean` для пакета целиком, без
указания конкретного компонента, после того как backend'ы компонентов очистили
оставленное своими инструментами и до того, как tt удалит `_build/`. Этот hook
предназначен для того, что проект генерирует рядом со своими исходниками
самостоятельно и что tt сам не удаляет: например, для созданного hook'ом
`pre_build`.

```toml
[hooks.pre_build]
command = "sh"
args = ["-c", "mkdir -p src/gen && date +'return %Y' > src/gen/year.lua"]

[hooks.clean]
command = "rm"
args = ["-rf", "src/gen"]
```

Завершившийся неудачей hook приводит к завершению команды с кодом возврата 2:
до начала сборки чего бы то ни было для `pre_build` и до удаления чего бы то ни
было для `clean`. Версия tt, выпущенная раньше появления этого hook'а, пропускает
`[hooks.clean]` с предупреждением: `unknown hook "clean" skipped`.

<!-- TODO(writers): app.manifest.lock - what it records, such as
bundled_tarantool_version - is not described yet. -->
