# Управление бинарными файлами

`tt binary` устанавливает, показывает, переключает и удаляет программы,
на которых работают приложения:

| Продукт | Что это |
| --- | --- |
| `tarantool` | Tarantool Community Edition, 3.0 и новее; Enterprise с версией `ee` |
| `tarantool-ee` | Tarantool Enterprise Edition |
| `tt` | tt; tt Enterprise с версией `ee` |
| `tt-ee` | tt Enterprise |
| `tcm` | Tarantool Cluster Manager |
| `tarantool-sdk` (`sdk`) | Enterprise SDK: Tarantool Enterprise, tt Enterprise, TCM и заголовочные файлы |

Каждая команда действует в одном [scope](../concepts/projects-and-scopes.md):
`--scope project` (по умолчанию), `user` или `system`, и никогда не переходит
в другой.

## Установка и переключение {#install-and-switch}

```console
$ tt binary install tarantool                 # самый свежий релиз Community, в проект
$ tt binary install tarantool 3.8 --scope user
$ tt binary list
$ tt binary switch tarantool 3.8.1
$ tt binary uninstall tarantool 3.9.0
```

Установленная последней версия становится активной: `<root>/bin/tarantool`
указывает на неё, а `<root>/include/tarantool` - на её заголовочные файлы.
Каждая версия хранится в `<root>/versions/<product>/<product>_<version>/`,
с заголовочными файлами в своём каталоге `include/tarantool/`. Tarantool
Community и Enterprise делят общую активную ссылку `tarantool`, а tt и
tt Enterprise - ссылку `tt`.

Чтобы использовать активные бинарные файлы, путь `<root>/bin` добавляют в
`PATH` для пользовательского и системного scope, а в проекте выполняют
`source .env/activate.sh`. Первый вызов `tt binary install` в проекте
создаёт `.env/activate.sh` и `.env/deactivate.sh`; существующие скрипты не
перезаписываются. Командам управления инстансами проекта эти скрипты не
нужны: они запускают Tarantool проекта сами. Не нужны они и сборке его
компонентов: бэкенд сборки и хук находят программы из `.tarantool/bin` раньше,
чем в `PATH`.

Релизы Community ставятся из готовых бинарных файлов там, где они есть, -
Tarantool на Linux amd64 и arm64 с glibc, tt на Linux amd64 и arm64 и macOS
amd64, - а на остальных платформах собираются из исходников. Коммит,
`master`, pull request (`pr/<number>`) и `--local-repo` собираются из
исходников всегда; `--source-build` тоже собирает релиз из исходников.
Поиск и установка Enterprise-сборок tt доступны только на Linux amd64.

Удаление активной версии делает активной самую свежую из оставшихся в том же
scope; вызов `uninstall` без указания версии удаляет единственную
установленную.

## Выбор версии {#choose-a-version}

- Community Edition: `3`, `3.8` или `3.8.1` выбирают самый свежий подходящий
  релиз; вызов без версии выбирает самый свежий релиз.
- Enterprise Edition: добавление `ee` - `3ee`, `3.8ee`, `3.8.1ee` - к
  `tt binary install tarantool` или `tt binary install tt`. Так выбирается
  самая свежая подходящая стабильная сборка; для Tarantool Enterprise по
  умолчанию берётся сборка с `gc64`. Полное имя архива, например
  `gc64-3.8.0-0-r94`, архив `nogc64` или release candidate выбирают именно эту
  сборку.
- TCM: `1`, `1.3` или `1.3.1` выбирают самую свежую подходящую стабильную
  сборку.
- SDK: как и для Enterprise - без версии, `X`, `X.Y` или `X.Y.Z` выбирают
  самый свежий стабильный SDK с `gc64`, а полное имя архива - точную сборку.

Отдельный релиз Tarantool Enterprise устанавливается под своей версией
Tarantool, `3.8.1`, без префикса `gc64`, номера ревизии и хеша:
`gc64-3.8.1-0-r93` и `gc64-3.8.1-0-r94` ставятся в
`versions/tarantool-ee/tarantool-ee_3.8.1/`, и установка другой ревизии не
трогает уже установленную, пока не передан флаг `--reinstall`.
Пре-релизы и отладочные сборки лежат отдельно: `3.8.1-rc1`, `debug-3.8.1`.

Команды `tt binary switch` и `tt binary uninstall` принимают `X.Y.Z` для
Enterprise, tt Enterprise и TCM, сопоставляя версии, установленные в scope;
значения `X` и `X.Y` не раскрываются. Точное совпадение с установленным
именем имеет приоритет; в противном случае должно совпасть ровно одно
установленное имя, а при неоднозначном запросе ничего не меняется.

### Сборки разработки {#development-builds}

`--dev` устанавливает сборки разработки Tarantool Enterprise, SDK или
TCM. Они сохраняют полную версию архива с префиксом `dev_` и лежат рядом с
релизами:

```console
$ tt binary install tarantool 3.8.1ee --dev
$ tt binary switch tarantool dev_gc64-3.8.1-0-r94
```

Команды `list`, `switch` и `search --dev` выводят `dev_<full-version>`;
селекторы без `dev_` находят только релизы.

## Учётные данные Enterprise {#enterprise-credentials}

Для поиска и скачивания Enterprise, TCM и SDK нужны учётные данные
customer-zone. Флаг `--credentials-file` читает их из файла (логин на первой
строке, пароль на второй), к которому закрыт доступ для группы и остальных
пользователей:

```console
$ chmod 0600 ./credentials
$ tt binary search tarantool-ee --credentials-file ./credentials
$ tt binary install tcm 1.3.1-0-g074b5ffa --credentials-file ./credentials
```

Без этого флага учётные данные берутся из `TT_CLI_EE_USERNAME` и
`TT_CLI_EE_PASSWORD` либо запрашиваются интерактивно.

## Enterprise SDK {#the-enterprise-sdk}

`tt binary install tarantool-sdk` устанавливает Tarantool Enterprise, tt
Enterprise, TCM и заголовочные файлы из одного архива SDK. Его компоненты
устанавливаются под именами вида `sdk-<bundle-version>`, отдельно от
автономных версий:
`versions/tarantool-ee/tarantool_sdk_<bundle-version>/`,
`versions/tcm/tcm_sdk_<bundle-version>/`,
`versions/tt-ee/tt_sdk_<bundle-version>/`, а запись в
`versions/tarantool-sdk/` хранит соответствие компонентов друг другу.

```console
$ tt binary search sdk
$ tt binary install tarantool-sdk 3.7
$ tt binary switch sdk 3.7.1                # все компоненты и заголовочные файлы
$ tt binary switch tcm sdk-3.7.1            # один компонент
$ tt binary switch tt-ee sdk-3.7.1          # tt Enterprise из SDK
$ tt binary switch tt 3.0.0                 # возврат к обычному tt
```

Удаление SDK удаляет его компоненты и сохраняет версии, выбранные отдельно от
него. Переключение или удаление SDK требует однозначно определённой
установленной версии.

## Установка из локальных файлов {#install-from-local-files}

| Команда | Что принимает |
| --- | --- |
| `tt binary install tt --local-repo DIR` | клон Git-репозитория tt в `DIR/tt` |
| `tt binary install tarantool --local-repo DIR` | клон Git-репозитория Tarantool в `DIR/tarantool` |
| `tt binary install tarantool 3ee --local-dir DIR` | архивы SDK `tarantool-enterprise-sdk-*.tar.gz` в `DIR` |
| `tt binary install tt 2ee --local-dir DIR` | архивы tt Enterprise `tt-ee-*.tar.gz` в `DIR` |
| `tt binary install tarantool-sdk --local-dir DIR` | архивы SDK, со всеми компонентами |
| `tt binary install tcm --local-dir DIR` | архивы TCM `tcm-*.tar.gz` в `DIR` |

`tt binary search <product>` принимает те же флаги. Флаг указывается после
имени продукта; относительный путь `DIR` отсчитывается от текущего каталога.

## Поиск {#search}

```console
$ tt binary search tarantool
$ tt binary search tarantool-ee --credentials-file ./credentials
$ tt binary search sdk
```

`search` отмечает установленные и активные версии в выбранном scope; сам scope
не влияет на то, откуда берутся доступные версии. SDK отображается как
активный, когда активны все его компоненты.

## Заголовочные файлы для rock-модулей с частью на C {#headers-for-rocks-with-a-c-part}

`tt rocks` и `tt package build` собирают rock-модуль с частью на C с
заголовочными файлами того Tarantool, под которым они запущены:
`.tarantool/include/tarantool` для Tarantool проекта,
`TT_CLI_TARANTOOL_PREFIX` для внешнего Tarantool, а в остальных случаях - по
префиксу каталога, куда был установлен Tarantool.