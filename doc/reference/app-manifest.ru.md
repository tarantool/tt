# app.manifest.toml

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
новая минорная версия `manifest_version`, чем известна tt,  -  в этом случае
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
| `license_files` | нет | файлы, включаемые в пакет |
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
| `registries` | нет | rock-серверы в порядке обращения вместо серверов по умолчанию: URL HTTP(S) или каталоги относительно манифеста. См. [Rock-серверы](../concepts/rock-servers.md) |

Ограничение версии  -  список сравнений через запятую, например
`">=3.0.0,<4.0.0"`.

## `[dependencies]` и `[dev_dependencies]` {#dependencies-and-dev_dependencies}

Зависимость  -  rock в краткой форме:

```toml
checks = ">=3.0.0"           # ограничение; "*" для любой версии
```

или в полной форме:

| Поле | |
| --- | --- |
| `source` | `"registry"` (по умолчанию) или `"path"` |
| `version` | ограничение версии; обязательно для `registry` |
| `path` | каталог с rock относительно манифеста; обязателен для `path` |
| `registry` | rock-сервер, откуда брать этот rock: URL http(s) или каталог относительно манифеста |

Rock с указанным `registry` разрешается и устанавливается только с этого
сервера; зависимости этого rock берутся из реестров проекта.

Зависимости из `[dev_dependencies]` устанавливаются для тестов и инструментов
проекта и не включаются в пакет. Компонент может объявлять собственные
зависимости в `[components.<name>.dependencies]`.

## `[components.NAME]` {#componentsname}

| Поле | |
| --- | --- |
| `path` | каталог компонента относительно манифеста; обязателен |
| `kind` | `"module"` (по умолчанию) или `"executable"` |
| `include`, `exclude` | файлы компонента-модуля; по умолчанию `*.lua` и `*.so` |
| `namespace` | куда помещается компонент-модуль в `.rocks/share/tarantool/`: по умолчанию имя пакета, `""` для верхнего уровня |
| `dependencies` | зависимости компонента |
| `build` | сборка компонента, см. ниже; обязательна для исполняемого компонента |

Файл `.wasm`, раскладываемый компонентом-модулем, должен быть двоичным файлом
WebAssembly: файл другого типа приводит к ошибке сборки, а core-модуль,
который tarantool-wasm не загружает, раскладывается с предупреждением. `tt wasm
new` объявляет компоненты, собираемые tarawasm; см.
[Запуск компонентов WebAssembly](../how-to/wasm-components.md).

### `[components.NAME.build]` {#componentsnamebuild}

| Поле | Backend'ы | |
| --- | --- | --- |
| `backend` | | `c`, `lua-c` (компоненты-модули); `go` (исполняемые компоненты); `cargo`, `make`, `shell` (и те, и другие) |
| `cwd`, `env`, `output` | все | каталог запуска сборки, её окружение, создаваемые файлы |
| `incremental` | `make`, `shell` | `true` пропускает сборку, если входные данные те же, что при прошлой сборке, и её результаты ещё на месте |
| `make_target`, `entrypoint`, `flags` | `make` | `make_target` обязателен |
| `command`, `args` | `shell` | `command` обязателен |
| `module`, `sources`, `include_dirs`, `libraries`, `library_dirs`, `defines` | `c`, `lua-c` | `module` и `sources` обязательны |
| `module` | `cargo` | имя модуля, собираемого компонентом-модулем: по умолчанию имя библиотеки |
| `platforms.<os>` | `c`, `lua-c` | дополнительные `include_dirs`, `libraries`, `library_dirs`, `defines` для одной ОС |
| `package` | `go`, `cargo` | пакет Go (по умолчанию `.`) или пакет cargo |
| `static` | `go`, `cargo` | программа, которой не нужны загрузчик и разделяемая библиотека; для исполняемого компонента |
| `name`, `tags`, `ldflags`, `cgo` | `go` | по умолчанию `name`  -  имя компонента; cgo по умолчанию выключен |
| `bin`, `features`, `profile` | `cargo` | для исполняемого компонента `bin` по умолчанию  -  имя компонента; `profile` по умолчанию `release` |

Сборка получает свой контракт через окружение: `TT_OUTPUT_DIR`, куда
помещаются результаты сборки, `TT_PROJECT_ROOT`, `TT_PACKAGE`,
`TT_COMPONENT_NAME`, `TT_VERSION`, `TT_PLATFORM_OS` и `TT_PLATFORM_ARCH`.
`env` сборки не может переопределить их.

`cargo` собирает бинарный файл `bin` исполняемого компонента и библиотеку
пакета для компонента-модуля: `cdylib`, то есть нативный модуль `<module>.so`
(`<module>.dylib` на macOS). Каталог `target/` компонента, собираемого cargo,
не раскладывается. См. [Сборка модуля на Rust](../how-to/rust-modules.md).

Входные данные `incremental`-сборки  -  её блок сборки и файлы компонента,
кроме скрытых файлов, `_build/`, `_runtime/` и того, что игнорируют файлы
`.gitignore` проекта и компонента: то, что сборка генерирует, а git не
хранит, входными данными не является. Хеш входных данных её последней сборки
находится в `_build/cache/<component>.json`, а флаг `--rebuild` команд
`tt package build` и `tt package pack` запускает сборку в любом случае; см.
[Инкрементальные сборки](../concepts/packages.md#incremental-builds).

Каталог `.tarantool/bin` проекта, куда `tt binary` и `tt wasm prepare`
помещают программы проекта, идёт первым в `PATH` сборки и hook'а: в `command`
такая программа указывается без предварительного выполнения
`.env/activate.sh`.

## `[products.NAME]` {#productsname}

| Поле | |
| --- | --- |
| `components` | компоненты продукта; обязательно |
| `default` | `true` задаёт продукт, который `build` и `pack` берут без `--product`; продукт по умолчанию ровно один |

В манифесте без `[products]` есть один продукт, `default`, состоящий из всех
компонентов.

## `[hooks.pre_build]` и `[hooks.post_build]` {#hookspre_build-and-hookspost_build}

Команда, запускаемая до или после сборки компонентов, с backend'ом `make` или
`shell` и полями сборки. Hook получает окружение сборки без `TT_OUTPUT_DIR` и
`TT_COMPONENT_NAME`.

<!-- TODO(writers): app.manifest.lock - what it records, such as
bundled_tarantool_version - is not described yet. -->
