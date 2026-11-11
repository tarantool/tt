# Переменные окружения

## Scope и бинарные файлы {#scopes-and-binaries}

| Переменная | |
| --- | --- |
| `TT_USER_ROOT` | корневой каталог пользовательского scope вместо `$XDG_DATA_HOME/tarantool` или `~/.local/share/tarantool`; абсолютный путь |
| `TT_SYSTEM_ROOT` | корневой каталог системного scope вместо `/opt/tarantool`; абсолютный путь |
| `XDG_DATA_HOME` | пользовательский scope располагается в `$XDG_DATA_HOME/tarantool`, если значение является абсолютным путём |
| `TT_USE_SYSTEM_TARANTOOL` | любое значение, кроме пустого, `0` или `false`: проект запускается на `tarantool` из `PATH`, даже если в проекте установлен свой; на установленные приложения не влияет |
| `TT_CLI_TARANTOOL_PREFIX` | префикс каталога с заголовочными файлами Tarantool, под которые собираются rock-модули с частью на C; задаётся в `.env/activate.sh` |
| `TT_PROJECT_DIR` | каталог проекта; задаётся в `.env/activate.sh` |
| `TT_CLI_EE_USERNAME`, `TT_CLI_EE_PASSWORD` | учётные данные customer-zone для Enterprise, TCM и SDK, если не передан флаг `--credentials-file` |

См. [Projects and scopes](../concepts/projects-and-scopes.md) и
[Manage binaries](../how-to/binaries.md).

## Пакеты {#packages}

| Переменная | |
| --- | --- |
| `TT_REGISTRIES` | серверы rock-модулей через запятую вместо `[platform].registries` и значений по умолчанию; флаг `--registry` имеет приоритет |
| `SOURCE_DATE_EPOCH` | время, фиксируемое в `version.lua` и в архиве; по умолчанию - время коммита HEAD |

Сборка компонента и хуки получают переменные `TT_OUTPUT_DIR`, `TT_PROJECT_ROOT`,
`TT_PACKAGE`, `TT_COMPONENT_NAME`, `TT_VERSION`, `TT_PLATFORM_OS` и
`TT_PLATFORM_ARCH`, а если tt знает Tarantool, для которого идёт сборка,
то и `TT_TARANTOOL_PREFIX` и `TT_TARANTOOL_INCLUDE_DIR`, каталог его
`module.h` и `lua.h`; см. [app.manifest.toml](app-manifest.md#componentsnamebuild).
Каталог `.tarantool/bin` проекта ставится первым в передаваемом им `PATH`.
Сборка компонента получает также `TT_BUILD_DIR` -
`_build/component/<component>/`, собственный каталог для того, что она
создаёт по пути, вне исходников, - а сборка `cargo` получает
`CARGO_TARGET_DIR` - `_build/cargo/`, - если не заданы `CARGO_TARGET_DIR` или
`CARGO_BUILD_TARGET_DIR`.

## Компоненты WebAssembly {#webassembly-components}

| Переменная | |
| --- | --- |
| `TT_WASM_RELEASES` | JSON-файл с релизами tarantool-wasm, которые `tt wasm` использует вместо встроенных: например, файлы с зеркала |
| `XDG_CACHE_HOME` | `tt wasm` кэширует скачанные файлы в подкаталоге `tt/wasm/`, по умолчанию `~/.cache/tt/wasm`; на macOS - в `~/Library/Caches/tt/wasm`. `tt emmylua` хранит программы, которые запускает вне проекта, и их конфигурацию в подкаталоге `tt/emmylua/` (см. [Раскладку](layout.md#the-user-cache)) |

Файл из `TT_WASM_RELEASES` сопоставляет версию с её релизом: нативный модуль
для каждой платформы со своей контрольной суммой sha256 и архив SDK, на
который указывает rockspec rock-модуля `wasm`. Скачивание идёт по адресу
`<base_url>/v<version>/<asset>`; без `base_url` - из релизов
tarantool-wasm-rs на GitHub.

```json
{
  "0.1.0": {
    "version": "0.1.0",
    "rock_revision": 1,
    "base_url": "https://mirror.example.com/tarantool-wasm",
    "sdk_asset": "tarantool-wasm-0.1.0-sdk.tar.gz",
    "sdk_md5": "f986e42dd8d430110c06e9fb2963209b",
    "platforms": [
      {
        "goos": "linux",
        "goarch": "amd64",
        "asset": "tarantool-wasm-0.1.0-linux-x86_64.so",
        "sha256": "65866215ce6424389381644ef65adc3906f896d46a03843f213baf16df758585",
        "rock_arch": "linux-x86_64",
        "module": "wasm.so"
      },
      {
        "goos": "darwin",
        "goarch": "arm64",
        "asset": "tarantool-wasm-0.1.0-macos-arm64.dylib",
        "sha256": "bf2960db7dfe3535f7b6facecd2b33c4dbd9479f90b691e1b964a2499ef5a38d",
        "rock_arch": "macosx-aarch64",
        "module": "wasm.dylib"
      }
    ]
  }
}
```

Поле `rock_arch` задаёт архитектуру LuaRocks для rock-модуля (её
go-luarocks считает целевой для бинарного rock-модуля), а `module` - файл,
который rock-модуль кладёт в `lib/`: `wasm.dylib` на macOS, где Tarantool
загружает модули C только как `?.dylib`.
См. [Run WebAssembly components](../how-to/wasm-components.md).

## Подключения {#connections}

| Переменная | |
| --- | --- |
| `TT_CLI_USERNAME`, `TT_CLI_PASSWORD` | имя пользователя и пароль, с которыми tt подключается к инстансам Tarantool и хранилищам конфигураций |
| `TT_CLI_ETCD_USERNAME`, `TT_CLI_ETCD_PASSWORD` | имя пользователя и пароль, с которыми tt подключается к etcd |

Учётные данные в URI имеют приоритет над флагами команды, а те - над
переменными окружения.

## Модули и дистрибутивы {#modules-and-distributions}

| Переменная | |
| --- | --- |
| `TT_CLI_MODULES_PATH` | каталоги с внешними модулями, разделённые двоеточием |
| `TT_DIST_CORE` | `tt binary build tt`: ядро, на котором ведётся сборка, вместо `[core]`; флаг `--core` имеет приоритет |
| `TT_CLI_BUILD_SSL` | `mage build` и `tt binary build tt`: реализация TLS, `default`, `openssl` или `openssl_static` |

## Что tt передаёт инстансу {#what-tt-sets-for-an-instance}

Инстанс Tarantool получает пути своей [раскладки](layout.md) в виде
переменных `TT_*_DEFAULT` - `TT_SNAPSHOT_DIR_DEFAULT`, `TT_WAL_DIR_DEFAULT`,
`TT_VINYL_DIR_DEFAULT`, `TT_PROCESS_PID_FILE_DEFAULT`,
`TT_CONSOLE_SOCKET_DEFAULT` и других; кластерная конфигурация переопределяет
их. Инстанс со `script` получает `TT_WAL_DIR`, `TT_MEMTX_DIR`,
`TT_VINYL_DIR` и `TT_LISTEN`. Под systemd переменная `TT_SYSTEMD_UNIT`
задаёт имя юнита.

Сам Tarantool читает настройки из переменных вида `TT_<NAME>`: параметр
кластерной конфигурации - из пути параметра в верхнем регистре с `_` вместо
`.` (например, `TT_IPROTO_LISTEN`), а параметр `box.cfg` инстанса со скриптом
- по его имени (например, `TT_MEMTX_MEMORY`). tt ничего к этому не добавляет;
их задают в секции `env` соответствующей записи в
[instances.yaml](instances-yaml.md).

<!-- TODO(writers): the full list of TT_*_DEFAULT variables tt sets; see
cli/running/instance_spec.go. -->
