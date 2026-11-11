# Структура файловой системы

## Корень scope {#the-root-of-a-scope}

У каждого [scope](../concepts/projects-and-scopes.md#scopes) корень устроен
одинаково: `.tarantool/` проекта, корень user scope или корень system scope.

```text
<root>/
├── bin/                                  # активные бинарные файлы (ссылки)
│   ├── tarantool
│   ├── tt
│   └── tcm
├── include/tarantool                     # заголовочные файлы активного tarantool (ссылка)
├── versions/
│   ├── tarantool/tarantool_3.8.1/        # tarantool, include/tarantool/
│   ├── tarantool-ee/tarantool-ee_3.8.1/
│   ├── tt/tt_3.0.0/
│   ├── tcm/tcm_1.3.1/
│   ├── tarantool-sdk/                    # какие компоненты SDK используются вместе
│   └── kv-api/kv-api_1.1.0/              # установленное приложение (user, system)
├── registry/                             # rock-сервер для tt wasm/emmylua prepare --from (project)
├── apps/
│   └── kv-api -> ../versions/kv-api/kv-api_1.1.0
├── etc/
│   └── kv-api/                           # пара хоста (user, system)
│       ├── config.yaml
│       ├── instances.yaml
│       └── systemd.yaml                  # инстансы, которыми управляют юниты systemd
└── var/
    ├── lib/<app>/<instance>/             # данные; рабочий каталог внешнего процесса
    ├── log/<app>/<instance>/tt.log       # вывод инстанса и сообщения его watchdog
    └── run/<app>/<instance>/             # tt.pid, tarantool.pid, tarantool.control,
                                          # tarantool.sock, process.pid
```

| Файл | |
| --- | --- |
| `run/.../tt.pid` | PID watchdog инстанса |
| `run/.../tarantool.pid` | pid-файл, который записывает Tarantool |
| `run/.../tarantool.control` | сокет консоли, к которому подключается `tt connect <app>:<instance>` |
| `run/.../tarantool.sock` | бинарный порт, если конфигурация кластера или скрипт не слушают другой адрес |
| `run/.../process.pid` | PID внешнего процесса |
| `log/.../tt.log` | всё, что выводит инстанс, и сообщения его watchdog |

`tt wasm prepare` добавляет в корень проекта всё необходимое для сборки
компонентов WebAssembly: tarawasm и утилиты, которые устанавливает tt, под
`versions/`, с символическими ссылками в `bin/`, а с `--from` - ещё и
`registry/`, rock-сервер с rock-пакетом `wasm` локальной сборки для текущей
машины:

```text
.tarantool/
├── bin/
│   ├── tarawasm -> <root>/versions/tarawasm/tarawasm_<commit>/bin/tarawasm
│   ├── wasm-tools -> <root>/versions/wasm-tools/wasm-tools_1.261.0/wasm-tools
│   ├── wkg
│   ├── componentize-py                   # в окружении tarawasm (Python)
│   ├── tinygo, wasm-opt                  # Go
│   └── wit-bindgen                       # C
├── registry/
│   ├── manifest
│   └── wasm-0.1.0-1.linux-x86_64.rock
└── versions/
    ├── tarawasm/tarawasm_<commit>/       # виртуальное окружение Python
    ├── wasm-tools/wasm-tools_1.261.0/
    ├── wkg/wkg_0.16.1/
    ├── tinygo/tinygo_0.43.0/             # с наложенным поверх Binaryen
    ├── wasi-sdk/wasi-sdk_34/             # без ссылок: см. tarawasm.local.json ниже
    └── wit-bindgen/wit-bindgen_0.62.0/
```

## Проект {#a-project}

```text
myapp/
├── app.manifest.toml          # манифест пакета
├── app.manifest.lock          # зафиксированные версии зависимостей; сохраняем в git
├── VERSION                    # опционально: версия пакета
├── .templates/
│   └── default/               # шаблон инстансов: пара проекта
│       ├── config.yaml
│       └── instances.yaml
├── .rocks/                    # зависимости и собранные компоненты (генерируется)
│   ├── share/tarantool/<name>/
│   └── bin/                   # исполняемые файлы компонентов
├── _build/pack/               # архивы, которые создаёт tt package pack (генерируется)
├── _build/rock/<product>/     # разложенные компоненты, устанавливаемые в .rocks/ (генерируется)
├── _build/rock/<product>.json # какой компонент положил туда каждый файл (генерируется)
├── _build/cache/              # хеши и артефакты предыдущих инкрементальных сборок (генерируется)
├── _build/cmake/<component>/  # деревья сборки cmake-компонентов (генерируется)
├── .env/                      # activate.sh, deactivate.sh (генерируется)
└── .tarantool/                # корень project scope (генерируется)
```

Сгенерированные каталоги не нужно добавлять в git:

```text
.rocks/
_build/
.env/
.tarantool/
```

## Компонент WebAssembly {#a-webassembly-component}

`tt wasm new router --lang python` создаёт проект tarawasm в каталоге `router/`:

```text
router/
├── .gitignore                    # список файлов, генерируемых tarawasm; создаётся tt wasm new
├── main.py                       # исходный код
├── wit/
│   ├── app.wit                   # world компонента с именем самого компонента
│   └── deps/                     # WIT из состава SDK
├── tarawasm.json                 # язык, world, результат сборки
├── tarawasm.tarantool.lock.json  # релиз SDK, под который собирается компонент
├── tarawasm.local.json           # пути к утилитам на текущей машине (C; генерируется)
├── .tarawasm/                    # SDK, распакованный по tt wasm prepare (генерируется)
├── wit_world/, poll_loop.py ...  # байндинги (генерируется)
└── dist/router.wasm              # компонент (генерируется)
```

В репозиторий коммитят исходный код, каталог `wit/` вместе с `deps/`,
`tarawasm.json`, `tarawasm.tarantool.lock.json` и `.gitignore`; всё остальное
генерируется автоматически и исключается через `.gitignore`.

## Архив {#an-archive}

`tt package pack` собирает архив `<name>-<version>-<os>-<arch>.tt` в формате
tar.zst:

```text
app.manifest.toml
app.manifest.lock
VERSION
.templates/...
.rocks/...
_runtime/tarantool/bin/tarantool
_runtime/tt/bin/tt
```

В подписанном архиве также находятся файлы `hashes.json`, `env_hashes.json` и
соответствующие файлы `.sig`; см.
[Подпись пакетов](../how-to/sign-packages.md).

## Пользовательский кэш {#the-user-cache}

`tt wasm` и `tt emmylua` хранят скачанное в подкаталоге `tt/`
пользовательского кэша: `$XDG_CACHE_HOME`, по умолчанию `~/.cache`, на
macOS - `~/Library/Caches`. Вне проектов `tt emmylua check`, `doc`, `fmt` и
`ls` запускают программы рока `emmylua` оттуда и пишут туда свою
конфигурацию, а не в текущий каталог:

```text
<user cache>/tt/emmylua/
├── <sha256>                          # файл релиза, скачанный tt emmylua rock
├── programs/<sha256>/                # bin/ рока с этим sha256
│   ├── emmylua_check
│   ├── emmylua_doc_cli
│   ├── emmylua_ls
│   └── luafmt
├── resolved/<key>.json               # рок, выбранный для сервера, версии и платформы
└── config/<key>/emmyrc.json          # конфигурация каталога, по его пути
```

Первый запуск скачивает рок и сообщает об этом; следующие находят его через
`resolved/` и к серверу не обращаются. Rock-сервер, который является
каталогом, опрашивается каждый раз. Чтобы скачать роки заново, удалите
этот каталог.
