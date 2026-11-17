# Сборка дистрибутива tt {#build-a-distribution-of-tt}

Модуль tt - это модуль Go, который компилируется внутрь tt, а не работает рядом
с ним: его пакет экспортирует конструктор `func New(sdk.Services) []sdk.Mount`,
возвращающий команды, которые он добавляет, или
`func New(sdk.Services) sdk.Module`, возвращающий также его бэкенды сборки и
проверки файлов пакета (см. [Write a tt module](write-a-module.md) и
[SDK](https://github.com/tarantool/tt/blob/v3/sdk/README.md)). Дистрибутив tt -
это ядро tt и набор таких модулей, собранных в один бинарный файл и описанных в
`tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt"

[core]
tt = "v3.0.0-alpha.3"

[distribution]
title = "Tarantool CLI EE"
edition = "ee"
integrity = "rsa"

[dependencies]
migrations = "./plugins/migrations"
foo = "github.com/acme/tt-foo@v1.2.0"
```

Обязательны только `manifest_version` и `manifest_kind`; каждое поле
описано в [tt.manifest.toml](../reference/tt-manifest.md). Без секции
`[core]` дистрибутив собирается на ядре того tt, который выполняет сборку;
`tt` принимает запрос версии Go для модуля ядра - версию, коммит или
`latest`, а `path` - локальный чекаут ядра. Версию или коммит надёжнее
зафиксировать: Go читает `v3` как "самый новый релиз v3.x.y", а не как
ветку `v3`, и пока у tt 3 есть только предварительные версии, под этот запрос
ничего не подходит. Секция `[distribution]` задаёт то, как бинарный файл
представляет себя (`title`, `edition`), провайдер контроля целостности
(`none` или `rsa`), наличие стандартных модулей ядра (`standard`, см.
[Стандартные модули](#standard-modules)), собственных команд tt (`builtin`)
и внешних модулей (`external_modules`), по умолчанию включённых, а также
откуда `tt binary install` берёт предсобранные продукты (`bundle_sources`:
по умолчанию `customer-zone`, личный кабинет tarantool.io, для Tarantool
Enterprise, SDK, TCM и tt-ee, `rws`, RPM-пакеты rws.tarantool.org, для
Tarantool Community и `github`, релизы tt на GitHub, для tt; `[]` - ни
одного источника, и тогда Tarantool Community и tt собираются из
исходного кода). Секция `[build]` задаёт реализацию TLS (`ssl`: `default`,
`openssl` или `openssl_static`, переопределяется переменной
`TT_CLI_BUILD_SSL`), сборку с `cgo` (по умолчанию только для вариантов с
OpenSSL), дополнительные теги сборки `tags` и флаги линковщика `ldflags`.

Зависимость - это локальный каталог (`./...`, `../...` или абсолютный путь),
путь к модулю либо путь к модулю с версией после `@`; длинная форма - это
таблица с полями `module`, `version`, `path`, `package` и `symbol`:
конструктором считается функция `New` в корневом пакете модуля, если
`package` и `symbol` не говорят об обратном. Модуль из репозитория ядра,
такой как `aeon`, собирается на коммите ядра; любой другой модуль без
версии - на самой свежей. Стандартным модулям запись здесь не нужна:
дистрибутив выше содержит `aeon`, `build`, `emmylua` и `wasm` с двумя его
собственными модулями.

```sh
tt binary build tt                  # собрать ./tt из ./tt.manifest.toml
tt binary build tt --update         # взять самые свежие версии ядра и модулей
tt binary build tt --locked -o tt   # собрать ровно то, что фиксирует локфайл
```

Тулчейн Go должен быть в `PATH`. Сборка генерирует модуль main,
требующий ядро и модули, компилирует его с теми тегами сборки, с которыми
собран сам tt, запускает `tt version` для результата - `tt --help` для
варианта без `builtin`, в котором находится `version`, - и записывает
`tt.manifest.lock` и `tt.manifest.sum` рядом с манифестом. Оба файла
коммитим в репозиторий: пока манифест говорит то же самое, следующая сборка
берёт версии из локфайла, а флаг `--locked` завершается с ошибкой, если
локфайл или модули Go расходятся. Локфайл связывается с манифестом через
хеш его таблиц, ключей и значений, как и локфайл пакета: комментарий или
переформатирование манифеста оставляют его актуальным. Локфайл фиксирует то,
из чего собирается бинарный файл, а не параметры компиляции: реализацию TLS,
теги и тулчейн Go каждая сборка выбирает сама, поэтому один локфайл
собирает любой из этих вариантов, а сборка, не фиксирующая ничего нового -
например, выполненная другим tt, - оставляет оба файла без изменений.

У локального модуля, локального ядра или каталога из `[replace]` нет версии,
которую можно зафиксировать, поэтому локфайл сохраняет `content_hash` -
хеш его файлов по путям и содержимому, так же как Go вычисляет хеш модуля для
`go.sum`. Это те файлы, которые бандл при `--vendor` копирует: каждый
обычный файл его каталога, кроме находящихся в каталогах VCS и `vendor`, во
вложенных модулях, `go.work` и `go.work.sum`, которые сборка не читает, и
файлов, игнорируемых `.gitignore`. Файлы `.gitignore` учитываются как
собственные для модуля, так и в вышестоящих каталогах вплоть до корня его
git-репозитория - вне репозитория вплоть до каталога манифеста, если модуль
находится в нём. Они читаются так, как их читает git, но без запуска git,
поэтому архив чекаута хешируется так же, как и сам чекаут; файлы
`.git/info/exclude` и `core.excludesFile`, различающиеся от машины к
машине, не читаются, а отслеживаемый git'ом файл, который `.gitignore`
игнорирует, не учитывается. Файлы, из которых Go может собрать модуль,
учитываются даже тогда, когда они проигнорированы: исходные файлы Go и cgo,
`go.mod`, `go.sum` и файлы, внедряемые через `//go:embed`. Сборка с
`--locked` завершается с ошибкой, если файлы изменились; обычная сборка
записывает их заново. Бинарный файл, собранный в чекауте ядра, например
`./tt`, изменением не считается: `.gitignore` самого tt его игнорирует.

Команда `tt version` для собранного файла показывает версию дистрибутива и
ядра tt, на котором он собран; `tt version --modules` перечисляет его модули
с версиями, зафиксированными Go для их модулей, или `(devel)` для
локального:

```sh
$ ./tt version --modules
Tarantool CLI EE version 3.0.0, linux/amd64. commit: 1a2b3c4
Tarantool CLI core version 3.0.0. commit: 9031d7ad96d9 (v3.0.1-0.20261028190206-9031d7ad96d9)
  aeon        v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/aeon
  build       v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/build
  builtin     v3.0.1-0.20261028190206-9031d7ad96d9  github.com/tarantool/tt/v3
  emmylua     v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/emmylua
  foo         v1.2.0                                github.com/acme/tt-foo
  migrations  (devel)                               example.com/tt-ee/plugins/migrations
  wasm        v0.0.0-20261028190206-9031d7ad96d9    github.com/tarantool/tt/modules/wasm
```

## Стандартные модули {#standard-modules}

Стандартные модули - это модули из репозитория ядра, с которыми собирается
официальный tt: сейчас это `aeon`, `build`, `emmylua` и `wasm`. Ядро
перечисляет их в своём пакете `standard`, и дистрибутив по умолчанию берёт
их все на коммите ядра, поэтому модули, добавленные более новым ядром,
попадают в сборку без правки манифеста. `build` содержит бэкенды сборки
tt, от `make` и `shell` до `cargo` и `go`: у самого ядра собственных
бэкендов нет, и дистрибутив без `build` раскладывает код Lua в пакете, но
не собирает ни один компонент с блоком сборки и не выполняет ни один хук.
`wasm` содержит команду `tt wasm` и проверку файлов `.wasm` пакета;
компоненты, добавляемые командой `tt wasm new`, собираются бэкендом `shell`
модуля `build`. Секция `[distribution].exclude` исключает некоторые из них
по имени:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[package]
name = "tt-lite"

[core]
path = "../tt"

[distribution]
exclude = ["emmylua", "wasm"]
```

При чекауте tt в `../tt` сборка берёт `aeon` и `build`, но не `emmylua` и
`wasm`:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   •   standard aeon, build (with the core)
   • Building tt-lite
   • Built /src/lite/tt-lite
/src/lite/tt-lite
$ ./tt-lite version --modules
Tarantool CLI version 3.0.0, linux/amd64. commit: 1a2b3c4
  aeon     (devel)  github.com/tarantool/tt/modules/aeon
  build    (devel)  github.com/tarantool/tt/modules/build
  builtin  (devel)  github.com/tarantool/tt/v3
```

Локфайл перечисляет вошедшие в бинарный файл стандартные модули в своей секции
`[core]`, а сборка с `--locked` завершается с ошибкой, если сборка взяла бы
другие:

```toml
[core]
module = 'github.com/tarantool/tt/v3'
path = '../tt'
content_hash = 'sha256:4367f681...'
standard = ['aeon', 'build']
```

Сборка читает этот список из исходного кода пакета `standard` и
импортирует каждый включённый стандартный модуль в сгенерированный файл
`main`, как и модули из `[dependencies]`, поэтому исключённый модуль не
компилируется вовсе:

```sh
$ go version -m tt-lite | grep 'dep.*tt/modules'
	dep	github.com/tarantool/tt/modules/aeon	v0.0.0-0
	dep	github.com/tarantool/tt/modules/build	v0.0.0-0
```

`builtin`, собственные команды tt, здесь считается стандартным модулем:
`exclude = ["builtin"]` означает `builtin = false`. Имя, которого нет в
списке ядра, приводит к ошибке с перечислением допустимых имён:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   ⨯ distribution.exclude: "tcm" is not a standard module of the core ../tt: they are aeon, build, emmylua, wasm, builtin
```

Модуль из `[dependencies]` с именем стандартного модуля занимает его
место - например, форк `aeon` или чекаут в `./aeon`. Укажите его и в
`exclude`, чтобы подтвердить это намерение явно; иначе сборка выдаст
предупреждение, а манифест, в котором `aeon` указан напрямую из репозитория
ядра, как делалось раньше, соберёт тот же самый бинарный файл, что и
манифест без него:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3 (path ../tt)
   ⚠ dependencies.aeon takes the place of the standard module aeon of the core: add "aeon" to distribution.exclude to say so, or drop the dependency to take the core's
   •   standard build (with the core)
   •   aeon github.com/tarantool/tt/modules/aeon (with the core)
   • Building tt-lite
```

У ядер до v3.0.0-alpha.3 пакета `standard` нет. Сборка дистрибутива на таком
ядре завершается с ошибкой, пока не задано `standard = false` и не
перечислены нужные модули:

```sh
$ tt binary build tt
   • Resolving the core github.com/tarantool/tt/v3
   •   core github.com/tarantool/tt/v3@v3.0.0-alpha.2
   ⨯ distribution.standard: the core github.com/tarantool/tt/v3@v3.0.0-alpha.2 has no package github.com/tarantool/tt/v3/standard that lists the standard modules: standard needs core v3.0.0-alpha.3 or later; set standard = false and list the modules in [dependencies]
```

```toml
[core]
tt = "v3.0.0-alpha.2"

[distribution]
standard = false

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
```

## Подмена модулей Go {#replacing-go-modules}

Модуль tt собирается с зависимостями, указанными в его собственном `go.mod`,
но Go применяет директивы `replace` только из модуля main, поэтому модуль,
зависящий от неопубликованной библиотеки или библиотеки не той версии, не
может задать подмену сам. Секция `[replace]` добавляет подмену в `go.mod`,
генерируемый сборкой перед вызовом `go mod tidy`:

```toml
[replace]
"gitlab.corp.mail.ru/tarantool/go-migration" = "../go-migration"
"github.com/tarantool/go-storage/v2" = "github.com/tarantool/go-storage/v2@v2.1.0-rc1"
"github.com/acme/old@v1.2.3" = "github.com/acme/new@main"
```

Ключ - это путь к модулю Go, при необходимости с `@version`, чтобы подменить
только эту версию, как в `go.mod`. Значение - это либо локальный каталог
(начинается с `./`, `../` или `/` и отсчитывается относительно манифеста),
содержащий указанный модуль, либо `module@version`, где версией может быть
любой запрос (ветка, коммит, `latest`); сборка разрешает его в точную версию,
как и для модулей, а локфайл фиксирует её. Ядро, модули из его репозитория и
модули tt из `[dependencies]` здесь подменять нельзя, это считается ошибкой:
для ядра есть `[core].path` и `--core`, а у модуля tt есть собственные `path`
и `version`. Подмена, из которой не берётся ни один пакет сборки, ни на что
не влияет, и сборка предупреждает об этом.

Локфайл фиксирует каждую запись в `[replace]`: каталог - через
`content_hash`, вычисленный так же, как для локального модуля (см. выше), а
модуль - по его точной версии. Сборка с `--locked` завершается с ошибкой при
изменении подменённого каталога, как и для локального модуля. Флаг `--vendor`
копирует подменённые каталоги в `local/replace/` бандла, а `go.mod`
подменяет их по относительному пути, поэтому бандл собирается без доступа к
сети.

## Сборка на другом ядре {#building-on-another-core}

Флаг `--core VALUE` или переменная окружения `TT_DIST_CORE` (флаг имеет
приоритет) собирает дистрибутив на другом ядре, отличном от указанного в
`[core]`, без правки манифеста: на локальном чекауте (существующий каталог
или путь, начинающийся с `./`, `../` или `/`, отсчитываемый от каталога
проекта) либо по запросу версии, как в `[core].tt`. Это нужно для сборки
дистрибутива на изменениях ядра, которые ещё не вышли в релиз, а также для CI
самого tt, собирающего дистрибутивы из своего рабочего дерева:

```sh
tt binary build tt --core ../tt
TT_DIST_CORE=. tt binary build tt --manifest dist/tt.manifest.toml
```

Такая сборка считается разрабатываемой. Её ядро и модули из репозитория
ядра не берутся из локфайла, а сам локфайл и файл sum не записываются;
остальные модули по-прежнему берутся из локфайла, пока манифест ему
соответствует. Сборка с `--locked` отклоняет этот режим.

Команда `tt binary workspace` с тем же параметром `--core` или `[core].path`
записывает `go.work`, собирающий локальные модули дистрибутива на этом чекауте
также для `go test` и редактора, какую бы версию SDK ни требовал их `go.mod`;
см. [Develop together with the core](write-a-module.md#develop-together-with-the-core).

## Сборка без доступа к сети {#offline-build}

Флаг `--vendor DIR` создаёт бандл вместо бинарного файла: всё то, из чего
собирается дистрибутив, и что один лишь тулчейн Go превращает в бинарный файл
на машине без сети, кэша модулей и tt.

```sh
tt binary build tt --locked --vendor dist/bundle
```

Бандл содержит сгенерированные `go.mod`, `go.sum` и `main.go`, каталог
`vendor/` со всеми пакетами, из которых собирается бинарный файл (ядро,
модули из его репозитория, модули tt, сторонние модули и внедряемые ими
файлы), каталог `local/` с копией каждого локального модуля - локального
ядра, зависимостей по пути и каталогов из `[replace]`, на которые `go.mod`
перенаправляет сборку по относительному пути, а также параметры сборки:
`build.env` задаёт теги, флаги линковщика и cgo для каждой реализации TLS,
реализацию, с которой собирался бандл, тулчейн и версию, а скрипт `build.sh`
выполняет сборку с этими параметрами. Бандл не содержит имён каталогов той
машины, на которой он создавался, поэтому его можно перенести куда угодно;
при создании он один раз собирается в автономном режиме.

На изолированной машине при наличии тулчейна Go, указанного в `build.env`
(`TT_DIST_TOOLCHAIN`), или более нового в `PATH`:

```sh
sh bundle/build.sh                         # TLS, с которым собирался бандл
sh bundle/build.sh openssl_static ./tt     # либо default, openssl; и выходной файл
```

Скрипт `build.sh` делает следующее:

```sh
cd bundle
. ./build.env
GOFLAGS=-mod=vendor GOPROXY=off GOWORK=off GOTOOLCHAIN=local \
CGO_ENABLED=$TT_DIST_CGO_DEFAULT \
    go build -trimpath -tags "$TT_DIST_TAGS_DEFAULT" \
        -ldflags "$TT_DIST_LDFLAGS_DEFAULT" -o tt .
```

Реализациям OpenSSL требуются компилятор C и заголовки OpenSSL на
изолированной машине; на macOS скрипт `build.sh` использует
`TT_DIST_LDFLAGS_DARWIN_*`.
