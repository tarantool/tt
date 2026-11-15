# Публикация модуля в виде rock'а {#publish-a-module-as-a-rock}

Модуль Tarantool, от которого зависят другие проекты, публикуется в виде
rock'а. На этой странице весь путь показан на примере одного модуля:
`hashkit`, модуль Lua с небольшой частью на C, описывается как пакет,
собирается и тестируется, версионируется тегами git, упаковывается в rock'и,
публикуется, и затем подключается как зависимость в приложение `kv-api`. Что
такое пакет, локфайл и rock-сервер, описано в разделах
[Пакеты](../concepts/packages.md) и
[Rock-серверы](../concepts/rock-servers.md); каждое поле манифеста разобрано
в [`app.manifest.toml`](../reference/app-manifest.md).

Страница представляет собой пошаговое руководство: команды выполняются по
порядку, каждая из указанного для неё каталога, начиная с пустого каталога.
Шаги, требующие другой платформы, CI или веб-сервера, помечены как не
выполняемые в рамках руководства. Показан вывод для Linux x86_64; пути, хеши
коммитов, составленные из них версии и дайджесты, а также время работы
отличаются на другой машине.

Понадобятся:

- tt 3, git, компилятор C (`cc`), `unzip` и `tar` с поддержкой zstd
  (`tar --zstd`, GNU tar 1.31 или новее с установленным `zstd`);
- Tarantool 3 в `PATH` с заголовочными файлами там, где их находит tt (см.
  [Заголовочные файлы для rock-модулей с частью на C](binaries.md#headers-for-rocks-with-a-c-part));
- доступ к `https://rocks.tarantool.org/` для luatest.

Компилятор и заголовочные файлы нужны для сборок: бинарного rock'а, тестов
и проекта, собирающего модуль из его исходного rock'а. Для упаковки одного
только исходного rock'а сборка не выполняется, поэтому компилятор и
заголовки не требуются.

## Описание модуля {#describe-the-module}

Начните с пустого каталога и создайте в нём проект модуля:

```console
$ mkdir hashkit && cd hashkit
$ git init -q
$ mkdir hashkit test
```

`app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "hashkit"
description = "String hashing for Tarantool"
license = "BSD-2-Clause"
license_files = ["LICENSE"]
repository = "https://github.com/example/hashkit"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"
platforms = ["linux-amd64", "linux-arm64", "darwin-arm64"]

[dependencies]

[dev_dependencies]
luatest = "*"

# The Lua modules hashkit and hashkit.*, and the C module hashkit.fnv. {#the-lua-modules-hashkit-and-hashkit.-and-the-c-module-hashkit.fnv.}
[components.hashkit]
path = "hashkit"

[components.hashkit.build]
backend = "lua-c"
module = "fnv"
sources = ["fnv.c"]
cflags = ["-std=gnu99", "-Wall", "-Wextra", "-Werror"]

[components.hashkit.build.platforms.linux]
ldflags = ["-Wl,--as-needed"]

[components.hashkit.build.platforms.darwin]
cflags = ["-Wno-deprecated-declarations"]
```

- `[package]` задаёт имя rock'а и заполняет его rockspec: `description` и
  `license` описывают его, а `repository` служит домашней страницей и
  источником исходного кода. Файлы из `license_files` сопровождают модуль
  повсюду: попадают в каталог `doc/` его бинарного rock'а и rock'а,
  устанавливаемого проектом, а значит, и в каждый проект и архив с этим
  модулем; исходный rock хранит их по путям проекта.
- `[platform]`: `tarantool` становится зависимостью rock'а от `tarantool`;
  `platforms` перечисляет целевые платформы модуля и переходит в
  `supported_platforms` файла rockspec.
- `[dev_dependencies]` предназначены для тестов: сам rock от них не зависит.
- Компонент `hashkit` размещает файлы `*.lua` из своего `path`,
  `hashkit/hashkit/`, в `.rocks/share/tarantool/hashkit/`, а его сборка
  `lua-c` компилирует `fnv.c` в C-модуль `fnv`,
  `.rocks/lib/tarantool/hashkit/fnv.so` (`fnv.dylib` на macOS), который
  загружается вызовом `require('hashkit.fnv')`. `cflags` и `ldflags` задают
  флаги проекта по одному в элементе списка, а `platforms.linux` и
  `platforms.darwin` дополняют их только на соответствующей ОС. Backend'ы
  `c` и `lua-c` стандартного модуля `build` tt описаны в
  [справочнике](../reference/app-manifest.md#componentsnamebuild).

`hashkit/fnv.c`, часть на C:

```c
#include <stddef.h>
#include <stdint.h>

#include <lua.h>
#include <lauxlib.h>

/* hash(s): the 32-bit FNV-1a hash of the string s. */
static int
fnv_hash(lua_State *L)
{
	size_t len;
	const unsigned char *s =
		(const unsigned char *)luaL_checklstring(L, 1, &len);
	uint32_t h = 2166136261u;
	for (size_t i = 0; i < len; i++) {
		h ^= s[i];
		h *= 16777619u;
	}
	lua_pushnumber(L, h);
	return 1;
}

/* require('hashkit.fnv') */
int
luaopen_hashkit_fnv(lua_State *L)
{
	lua_newtable(L);
	lua_pushcfunction(L, fnv_hash);
	lua_setfield(L, -2, "hash");
	return 1;
}
```

`hashkit/init.lua`, сам модуль, `require('hashkit')`:

```lua
local fnv = require('hashkit.fnv')

local hashkit = {
    VERSION = require('hashkit.version').version,
}

-- The 32-bit FNV-1a hash of a string.
hashkit.hash = fnv.hash

-- The bucket of a key among n buckets, from 1 to n.
function hashkit.bucket(key, n)
    return fnv.hash(key) % n + 1
end

return hashkit
```

`hashkit.version` - файл `version.lua`, который tt генерирует для пакета
при каждой сборке. `test/hash_test.lua`:

```lua
local t = require('luatest')
local hashkit = require('hashkit')

local g = t.group('hash')

g.test_known_values = function()
    t.assert_equals(hashkit.hash(''), 2166136261)
    t.assert_equals(hashkit.hash('a'), 0xe40c292c)
end
```

`test/bucket_test.lua`:

```lua
local t = require('luatest')
local hashkit = require('hashkit')

local g = t.group('bucket')

g.test_range = function()
    for i = 1, 1000 do
        local b = hashkit.bucket('key-' .. i, 16)
        t.assert(b >= 1 and b <= 16)
    end
end
```

Файл лицензии, указанный в `license_files`, и `.gitignore` проекта; в
руководстве вместо полного текста лицензии достаточно одной строки:

```console
$ echo 'BSD 2-Clause License. Copyright (c) 2026, Example.' > LICENSE
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
```

## Сборка и тестирование {#build-and-test}

Команды модуля запускаются в корне его проекта - каталоге с
`app.manifest.toml`, а не в `hashkit/hashkit/`. В корне проекта:

```console
$ tt package build
$ tt run -e "local h = require('hashkit') print(h.VERSION, h.hash('a'), h.bucket('user:1', 4))" - </dev/null
0.0.0-dev.0+dirty	3826002220	4
```

`tt package build` разрешает локфайл, устанавливает luatest в `.rocks/`,
компилирует C-модуль и раскладывает пакет в `.rocks/` как rock `hashkit`.
Пока в репозитории нет коммитов, версия имеет вид `0.0.0-dev.0+dirty`; см.
[Версионирование](#version-it).

Сборка компилирует только то, что изменилось с прошлой сборки.
`tt -V package build` сообщает об этом после двух команд, определяющих,
каким компилятором является `cc`:

```console
$ tt -V package build 2>&1 | grep 'probing\|up to date'
   · probing the compiler backend=lua-c command="cc -dumpmachine"
   · probing the compiler backend=lua-c command="cc --version"
   · fnv.c is up to date: .../hashkit/_build/component/hashkit/obj/fnv.c.040409a9.o backend=lua-c
   · .../hashkit/_build/rock/default/lib/hashkit/fnv.so is up to date backend=lua-c
```

Сначала идут флаги самого tt, затем `cflags`, а после них `CFLAGS` из
окружения, которые при конфликте имеют приоритет; установка переменной
`CFLAGS`, даже в пустое значение, отменяет `-O2` от tt. Другие флаги дают
другой объектный файл, поэтому сборка для отладчика компилирует `fnv.c`
заново в `_build/component/hashkit/obj/` и компонует модуль из него;
`tt -V` выводит каждую команду перед её запуском:

```console
$ CFLAGS='-O0 -g' tt -V package build 2>&1 | grep running
   · running cc -fPIC -std=gnu99 -Wall -Wextra -Werror -O0 -g -I/usr/include/tarantool -MD -MF .../hashkit/_build/component/hashkit/obj/fnv.c.040409a9.o.d -MQ .../hashkit/_build/component/hashkit/obj/fnv.c.040409a9.o -c fnv.c -o .../hashkit/_build/component/hashkit/obj/fnv.c.040409a9.o.tmp backend=lua-c dir=.../hashkit/hashkit
   · running cc -shared -Wl,--as-needed -o .../hashkit/_build/rock/default/lib/hashkit/fnv.so .../hashkit/_build/component/hashkit/obj/fnv.c.040409a9.o dir=.../hashkit/hashkit
```

Следующая сборка без `CFLAGS` снова компилирует его с `-O2`. Файл
`_build/component/hashkit/compile_commands.json` содержит команду для каждого
исходника для редактора кода; см.
[справочник](../reference/app-manifest.md#componentsnamebuild).

`tt test` собирает пакет и запускает luatest по каталогу `test/`; пути
ограничивают набор тестов, их можно передать сколько угодно, а аргументы
после `--` передаются в luatest:

```console
$ tt test
Tarantool version is 3.9.0-0-g8b9a0f59a95b
started logging into a pipe, SIGHUP log rotation disabled
..
Ran 2 tests in 0.021 seconds, 2 succeeded, 0 failed
$ tt test test/hash_test.lua test/bucket_test.lua -- --verbose
Tarantool version is 3.9.0-0-g8b9a0f59a95b
started logging into a pipe, SIGHUP log rotation disabled
Started on Thu Nov 12 22:38:47 2026
    bucket.test_range ... (0.017s) Ok
    hash.test_known_values ... (0.000s) Ok
=========================================================
Ran 2 tests in 0.019 seconds, 2 succeeded, 0 failed
```

Иногда luatest выводит строку собственного лога: `luatest | ... I> Bootstrap
finished: ...`, что не означает ошибки. Несуществующий путь приводит к
ошибке запуска ещё до начала сборки.

## Версионирование {#version-it}

Если файл `VERSION` не задан, tt определяет версию пакета по git: берётся
ближайший тег версии и количество коммитов после него. При создании коммита
версия указывает на коммит; при установке тега версия совпадает с версией
из тега:

```console
$ git add -A
$ git commit -qm 'hashkit: string hashing'
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
0.0.0-dev.1+g1746c78
$ git tag v1.0.0
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.0.0
```

Тег имеет вид `v1.0.0` или `1.0.0` в полном формате SemVer без префикса `v`.
Версия rock'а не содержит символов `+` и `-`, кроме дефиса перед номером
ревизии, поэтому tt преобразует версию пакета:

| Дерево | Версия пакета | Версия rock'а | Проект, зависящий от rock'а |
| --- | --- | --- | --- |
| на теге `v1.0.0` | `1.0.0` | `1.0.0-1` | считает её релизом |
| коммит после тега | `1.0.1-dev.1+gd0b7479` | `1.0.1.dev.1.gd0b7479-1` | считает её релизом: версия указывает на конкретный коммит |
| коммит, тегов ещё нет | `0.0.0-dev.1+g1746c78` | `0.0.0.dev.1.g1746c78-1` | также считает её релизом |
| незакоммиченные изменения | `1.1.0+dirty`, `1.0.1-dev.1+gd0b7479.dirty` | `1.1.0.dirty-1`, `1.0.1.dev.1.gd0b7479.dirty-1` | пропускает её в пользу релиза и выбирает лишь тогда, когда больше ничего не подходит, с предупреждением о сборке из незакоммиченных изменений |

Изменение одного только локфайла не делает дерево dirty. Модуль,
представляющий собой один из проектов монорепозитория в подкаталоге,
версионируется только по своему каталогу: по последнему изменившему его
коммиту, а статус `dirty` получает только при изменениях внутри этого
каталога. Для выпуска релиза тег ставится на коммит, изменивший каталог.
Правила описаны в разделе [Сборка](../concepts/packages.md#building).

## Упаковка в rock'и {#pack-the-rocks}

В корне проекта модуля на теге `v1.0.0` упакуйте бинарный rock в
каталог `../dist` рядом с проектом:

```console
$ tt package pack --format rock --out ../dist
.../dist/hashkit-1.0.0-1.linux-x86_64.rock
.../dist/hashkit-1.0.0-1.rockspec
$ unzip -l ../dist/hashkit-1.0.0-1.linux-x86_64.rock
Archive:  ../dist/hashkit-1.0.0-1.linux-x86_64.rock
  Length      Date    Time    Name
---------  ---------- -----   ----
       51  1980-01-01 00:00   doc/LICENSE
      561  1980-01-01 00:00   hashkit-1.0.0-1.rockspec
    15832  1980-01-01 00:00   lib/hashkit/fnv.so
      309  1980-01-01 00:00   lua/hashkit/init.lua
      143  1980-01-01 00:00   lua/hashkit/version.lua
      422  1980-01-01 00:00   rock_manifest
---------                     -------
    17318                     6 files
```

Rock содержит всё, что разложила сборка, C-модуль, собранный под эту
платформу, и файлы лицензий в `doc/`. Проекту не нужен собственный rockspec:
tt формирует его из манифеста, одинаковым для rock'а любой платформы. Его
сборка уже выполнена, поэтому тип сборки - `none`; источником служит
репозиторий на теге версии, здесь `v1.0.0`:

```console
$ cat ../dist/hashkit-1.0.0-1.rockspec
-- The rock of the tt package hashkit 1.0.0, which tt packs from the
-- files it lays out: its build is done.
rockspec_format = "3.0"
package = "hashkit"
version = "1.0.0-1"
source = {
   url = "git+https://github.com/example/hashkit",
   tag = "v1.0.0",
}
description = {
   summary = "String hashing for Tarantool",
   homepage = "https://github.com/example/hashkit",
   license = "BSD-2-Clause",
}
supported_platforms = { "linux", "macosx" }
dependencies = {
   "tarantool >= 3.0.0",
}
build = {
   type = "none",
}
deploy = {
   wrap_bin_scripts = false,
}
```

Версия без тега, относящаяся к коммиту после тега, содержит вместо тега
полный sha коммита в поле `source.tag` (см.
[Подключение нового релиза](#take-a-new-release)); версия с незакоммиченными
изменениями указывает репозиторий без коммита.

Бинарный rock предназначен для собственной платформы. Для остальных
платформ служит исходный rock: он содержит отслеживаемые git файлы проекта,
манифест, файлы лицензий по их путям в проекте и файл `VERSION`. Проект,
зависящий от модуля, собирает его отсюда, если под его платформу нет
подходящего бинарного rock'а (см.
[Сборка из исходного rock'а](#build-from-the-source-rock)). При упаковке
одного только исходного rock'а сборка не запускается: tt проверяет манифест
и локфайл и ничего не компилирует, поэтому компилятор и заголовочные файлы
Tarantool не нужны:

```console
$ tt package pack --format rock-src --out ../dist
.../dist/hashkit-1.0.0-1.src.rock
.../dist/hashkit-1.0.0-1.rockspec
$ unzip -l ../dist/hashkit-1.0.0-1.src.rock
Archive:  ../dist/hashkit-1.0.0-1.src.rock
  Length      Date    Time    Name
---------  ---------- -----   ----
      561  1980-01-01 00:00   hashkit-1.0.0-1.rockspec
        6  1980-01-01 00:00   hashkit/VERSION
       34  1980-01-01 00:00   hashkit/.gitignore
       51  1980-01-01 00:00   hashkit/LICENSE
      409  1980-01-01 00:00   hashkit/app.manifest.lock
      766  1980-01-01 00:00   hashkit/app.manifest.toml
      563  1980-01-01 00:00   hashkit/hashkit/fnv.c
      309  1980-01-01 00:00   hashkit/hashkit/init.lua
      242  1980-01-01 00:00   hashkit/test/bucket_test.lua
      230  1980-01-01 00:00   hashkit/test/hash_test.lua
---------                     -------
     3171                     10 files
```

Оба вида rock'ов воспроизводимы: повторная упаковка того же коммита даёт
байт в байт тот же результат. Опубликованный rock не изменяется; новая
сборка уже выпущенной версии, выполненная, например, другим компилятором,
получает новый номер ревизии. Без флага `--out` rock'и сохраняются в
`_build/pack/` проекта, который индексируется как rock-сервер точно так же,
как и каталог `--out`:

```console
$ tt package pack --format rock --revision 2
.../hashkit/_build/pack/hashkit-1.0.0-2.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.0-2.rockspec
$ ls _build/pack
hashkit-1.0.0-2.linux-x86_64.rock
hashkit-1.0.0-2.rockspec
manifest
manifest-5.1
manifest-5.2
manifest-5.3
manifest-5.4
tt-rocks-index.json
```

Флаг `--format` принимает список значений: `tt,rock` собирает архив `.tt`
пакета и его rock за одну сборку, а `rock,rock-src` - оба вида rock'ов. Архив
включает в себя Tarantool и tt: из кеша runtime'ов, из каталога проекта (куда
их помещает `tt binary install`) или активные версии из системы с выводом
предупреждения. Текст предупреждений зависит от конкретной машины (см.
[Устранение неполадок](#troubleshooting)), а в rock не включается ни то, ни
другое:

```console
$ tt package pack --format tt,rock
   ⚠ no tarantool >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tarantool 3.9.0-0-g8b9a0f59a95b instead (tt binary install tarantool puts one into the project)
   ⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
   ⚠ no tt >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tt 3.0.0-alpha.2 instead (tt binary install tt puts one into the project)
   ⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
.../hashkit/_build/pack/hashkit-1.0.0-linux-amd64.tt
.../hashkit/_build/pack/hashkit-1.0.0-1.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.0-1.rockspec
```

Пакет, содержащий только код на Lua без части на C, упаковывает один общий
rock для всех платформ: `<name>-<version>.all.rock`, и исходный rock ему не
нужен. Содержимое rock'а и условия, приводящие к ошибкам упаковки, описаны в
разделе [Упаковка в rock](../concepts/packages.md#packing-a-rock).

## Публикация {#publish}

### Каталог с rock'ами {#a-directory-of-rocks}

Флаг `--out` индексирует каталог как rock-сервер сразу после записи rock'а,
сохраняя уже находящиеся там файлы. Теперь `../dist` представляет собой
полноценный rock-сервер: манифесты LuaRocks содержат список его rock'ов,
как и `tt-rocks-index.json` -
[rock-индекс](../concepts/rock-servers.md#a-rock-index) с контрольными
суммами sha256 для каждого файла:

```console
$ ls ../dist
hashkit-1.0.0-1.linux-x86_64.rock
hashkit-1.0.0-1.rockspec
hashkit-1.0.0-1.src.rock
manifest
manifest-5.1
manifest-5.2
manifest-5.3
manifest-5.4
tt-rocks-index.json
```

Проекты на этой машине могут подключать rock из каталога и фиксировать
дайджесты его файлов в своих локфайлах; HTTP-сервер, раздающий этот каталог,
предоставляет rock'и всем пользователям, включая LuaRocks.

Бинарный rock собирается под свою платформу, поэтому rock'и одного релиза
поступают с нескольких машин, например из матрицы сборки CI. Этот набросок
workflow для GitHub Actions не выполняется в рамках руководства: каждый job
в `pack` собирает rock под свою платформу, первый из них собирает также
исходный rock, а job `index` объединяет их в одном каталоге и индексирует:

```yaml
name: rocks
on:
  push:
    tags: ["v*", "[0-9]*"]
jobs:
  pack:
    strategy:
      matrix:
        include:
          - {runner: ubuntu-22.04, format: "rock,rock-src"}
          - {runner: ubuntu-22.04-arm, format: rock}
          - {runner: macos-14, format: rock}
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # the tags and the commits the version is made of
      - name: Install tt and Tarantool
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
          "$HOME/.local/bin/tt" binary install tarantool
      - run: tt package pack --format ${{ matrix.format }} --out dist
      - uses: actions/upload-artifact@v4
        with:
          name: rocks-${{ matrix.runner }}
          path: dist/hashkit-*
  index:
    needs: pack
    runs-on: ubuntu-22.04
    steps:
      - uses: actions/download-artifact@v4
        with:
          pattern: rocks-*
          path: dist
          merge-multiple: true
      - name: Install tt
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: tt package index dist
      - uses: actions/upload-artifact@v4
        with:
          name: rocks
          path: dist
```

`tt binary install tarantool` предоставляет проекту Tarantool вместе с его
заголовочными файлами; на macOS команда собирает Tarantool из исходников.
Бинарный rock собирают на самой старой системе из тех, где он должен
работать: C-модуль линкуется с библиотекой C той машины, на которой собран.
Собранные в job'ах rock'и, сведённые в один каталог, пока не
проиндексированы, поскольку каждый job выгружает только свои файлы: вызов
`tt package index dist` в job'е `index` формирует манифесты LuaRocks и
rock-индекс для всех находящихся в нём файлов rock и rockspec, превращая
каталог в rock-сервер. Файлы rock'ов можно также разместить на страницах
релизов, сохранив индекс отдельно.

### Rock-индекс для страниц релизов {#a-rock-index-for-release-pages}

Rock-индекс `tt-rocks-index.json` перечисляет каждый файл каждой версии по
его URL и контрольной сумме sha256. Благодаря этому файлы могут лежать в
другом месте - например, на страницах релизов репозитория, - а зависящий от
rock'а проект фиксирует дайджесты в своём локфайле и проверяет каждый
устанавливаемый файл по ним. Формат индекса и способ его формирования в tt
описаны в разделе [Rock-индекс](../concepts/rock-servers.md#a-rock-index).

URL может быть относительным - к файлу, расположенному глубже каталога с
индексом. В качестве страниц релизов здесь используется каталог `../site`,
где файлы каждого тега лежат в `../site/<tag>/`. `tt package index` с флагом
`--index` формирует только индекс, указывая файлы индексируемого каталога
относительно этого файла индекса. В корне проекта модуля:

```console
$ mkdir -p ../site/v1.0.0
$ cp ../dist/hashkit-1.0.0-1.* ../site/v1.0.0/
$ tt package index ../site/v1.0.0 --index ../site/tt-rocks-index.json
.../site/tt-rocks-index.json
indexed 1 version(s) of .../site/v1.0.0
$ cat ../site/tt-rocks-index.json
{
  "format": 1,
  "repository": {
    "hashkit": {
      "1.0.0-1": {
        "artifacts": [
          {
            "arch": "linux-x86_64",
            "sha256": "d6385ac73ecd90037ebb668607328aad2251a050665ffe5f83267384fa3bd0a2",
            "size": 4132,
            "url": "v1.0.0/hashkit-1.0.0-1.linux-x86_64.rock"
          },
          {
            "arch": "src",
            "sha256": "864b7ee6a85739c298abc0b62748387a8b6d8adfa35b96976e0e3077d652ccd8",
            "size": 3293,
            "url": "v1.0.0/hashkit-1.0.0-1.src.rock"
          }
        ],
        "rockspec": {
          "sha256": "dd1aae98df5c2cb94f0f3e6f272fecbe9f6cb574505a632a912267251595b959",
          "size": 561,
          "url": "v1.0.0/hashkit-1.0.0-1.rockspec"
        }
      }
    }
  }
}
```

Существующий индекс обновляется: следующий релиз добавляет свою версию,
сохраняя предыдущую, файлы которой лежат в другом месте.

На GitHub (этот шаг в руководстве не выполняется) файлы из каталога `dist`
загружаются в релиз соответствующего тега, флаг `--base-url` указывает URL,
по которому они доступны из релиза, а индекс хранится в отдельной ветке
(например, `index`), куда его коммитит CI:

```sh
gh release upload v1.0.0 dist/*.rock dist/*.rockspec
tt package index dist --index tt-rocks-index.json \
    --base-url https://github.com/example/hashkit/releases/download/v1.0.0
```

Проект подключает rock по прямому (raw) URL этой ветки:
`registry = "https://raw.githubusercontent.com/example/hashkit/index/"`.
LuaRocks индекс не читает; см.
[Для пользователей LuaRocks](#for-luarocks-users).

## Подключение rock'а {#depend-on-the-rock}

Приложение `kv-api` - это проект, расположенный рядом с `hashkit`. Из корня
проекта модуля:

```console
$ cd ..
$ mkdir kv-api && cd kv-api
$ git init -q
$ mkdir kv
```

`app.manifest.toml`, подключающий `hashkit` из индекса:

```toml
manifest_version = "0.1"

[package]
name = "kv-api"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[dependencies]
hashkit = { version = ">=1.0.0", registry = "../site" }

[components.app]
path = "kv"
```

`kv/init.lua`:

```lua
local hashkit = require('hashkit')

-- The shard of a key among 4.
local function shard(key)
    return hashkit.bucket(key, 4)
end

return {shard = shard}
```

```console
$ tt package build
$ tt run -e "print(require('kv-api').shard('user:1'))" - </dev/null
4
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE    ORIGIN
default  hashkit  >=1.0.0     1.0.0-1  registry  direct
$ cat app.manifest.lock
lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0-alpha.2'
manifest_hash = 'toml1-sha256:3bb122da9541b581f433f50754437bfd8a3a9914e171bd89e2ad5def90e2b781'

[lock]
[lock.products]
[lock.products.default]
[[lock.products.default.dependencies]]
name = 'hashkit'
version = '1.0.0-1'
source = 'registry'
artifacts = {linux-x86_64 = 'sha256:d6385ac73ecd90037ebb668607328aad2251a050665ffe5f83267384fa3bd0a2', rockspec = 'sha256:dd1aae98df5c2cb94f0f3e6f272fecbe9f6cb574505a632a912267251595b959', src = 'sha256:864b7ee6a85739c298abc0b62748387a8b6d8adfa35b96976e0e3077d652ccd8'}
$ ls .rocks/share/tarantool/rocks/hashkit/1.0.0-1/doc
LICENSE
```

Локфайл фиксирует версию rock'а и, поскольку сервер публикует индекс,
контрольные суммы sha256 каждого из его файлов: при любой установке rock'а
файл для соответствующей платформы проверяется по ним. Зависимость с
сервера без индекса (сервера только с манифестами LuaRocks вроде
rocks.tarantool.org) фиксируется только по версии. Лицензия модуля
устанавливается вместе с ним. Закоммитьте локфайл вместе с проектом:

```console
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
$ git add -A
$ git commit -qm 'kv-api: shards by hashkit'
```

### Подключение нового релиза {#take-a-new-release}

Вернитесь в проект модуля и выпустите релиз 1.1.0:

```console
$ cd ../hashkit
$ echo '# hashkit' > README.md
$ git add README.md
$ git commit -qm 'Add a README'
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.0.1-dev.1+gd0b7479
$ tt package pack --format rock
.../hashkit/_build/pack/hashkit-1.0.1.dev.1.gd0b7479-1.linux-x86_64.rock
.../hashkit/_build/pack/hashkit-1.0.1.dev.1.gd0b7479-1.rockspec
$ sed -n '/^source/,/^}/p' _build/pack/hashkit-1.0.1.*.rockspec
source = {
   url = "git+https://github.com/example/hashkit",
   -- The commit of the version, which no tag names. LuaRocks fetches
   -- a tag with git clone --branch, which takes no commit.
   tag = "d0b7479c5e1f3a9b8d2c6e4f0a7b9c1d3e5f7a92",
}
```

Коммит после тега получает версию, отсчитанную от него: по количеству
коммитов и хешу коммита. Его rock представляет собой релиз, к которому
может обращаться проект (например, ночную сборку). В поле `source.tag` файла
rockspec коммит указывается своим полным sha, поскольку в LuaRocks нет
отдельного поля для коммита: tt собирает rock из его файлов, а попытка
скачать исходный код средствами LuaRocks завершится ошибкой, вместо того
чтобы взять другой коммит. Поставьте на коммит тег, на этот раз без `v`, упакуйте оба rock'а
версии 1.1.0 в `../dist` и добавьте версию в индекс:

```console
$ git tag 1.1.0
$ tt package pack --format rock,rock-src --out ../dist
.../dist/hashkit-1.1.0-1.linux-x86_64.rock
.../dist/hashkit-1.1.0-1.src.rock
.../dist/hashkit-1.1.0-1.rockspec
$ mkdir ../site/1.1.0
$ cp ../dist/hashkit-1.1.0-1.* ../site/1.1.0/
$ tt package index ../site/1.1.0 --index ../site/tt-rocks-index.json
.../site/tt-rocks-index.json
indexed 1 version(s) of .../site/1.1.0, and kept 1 listed with files elsewhere
```

В каталоге `kv-api/` сборка сохраняет версию, зафиксированную в локфайле;
`tt package update` выбирает самую новую версию, допустимую ограничением,
для всех зависимостей или только для указанных:

```console
$ cd ../kv-api
$ tt package build
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE    ORIGIN
default  hashkit  >=1.0.0     1.0.0-1  registry  direct
$ tt package update hashkit
  hashkit 1.0.0-1 -> 1.1.0-1
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0
```

### Фиксация локфайла в CI {#keep-ci-on-the-lock}

Команды `tt package build --locked` и `tt package pack --locked` при
несоответствии локфайла манифесту завершаются с ошибкой вместо повторного
разрешения зависимостей. Именно это требуется в CI, чтобы собиралось ровно
то, что закоммичено. Поднимите ограничение версии в `kv-api` до нового
требуемого релиза; в `app.manifest.toml` измените строку `hashkit` на:

```toml
hashkit = { version = ">=1.1.0", registry = "../site" }
```

```console
$ tt package build --locked
   ⨯ lock is out of date: manifest changed since the lock was written (--locked); run tt package resolve to bring the lock back in step
$ tt package resolve
no version moved, but the lock changed:
  manifest hash refreshed
$ tt package build --locked
$ git commit -qam 'kv-api: hashkit 1.1.0'
```

Локфайл сверяется с манифестом по хешу его смыслового содержимого, а не его
байтов: комментарий или другое форматирование не делают его устаревшим.

```console
$ echo '# Keys are sharded by hashkit.' >> app.manifest.toml
$ tt package build --locked
$ git commit -qam 'kv-api: say what shards the keys'
```

### Поставка в составе архива {#ship-it-in-an-archive}

Команда `tt package pack` для `kv-api` собирает архив с зависимостями
приложения: самим модулем, его частью на C, собранной под текущую платформу,
и его лицензией.

```console
$ tt package pack
   ⚠ no tarantool >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tarantool 3.9.0-0-g8b9a0f59a95b instead (tt binary install tarantool puts one into the project)
   ⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
   ⚠ no tt >=3.0.0 in the runtime cache (.../tt/runtimes) or the project; bundling the active tt 3.0.0-alpha.2 instead (tt binary install tt puts one into the project)
   ⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
.../kv-api/_build/pack/kv-api-0.0.0-dev.3+gdca3f06-linux-amd64.tt
$ tar --zstd -tf _build/pack/kv-api-*.tt | grep hashkit
.rocks/lib/tarantool/hashkit/
.rocks/lib/tarantool/hashkit/fnv.so
.rocks/share/tarantool/hashkit/
.rocks/share/tarantool/hashkit/init.lua
.rocks/share/tarantool/hashkit/version.lua
.rocks/share/tarantool/rocks/hashkit/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc/
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc/LICENSE
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/hashkit-1.1.0-1.rockspec
.rocks/share/tarantool/rocks/hashkit/1.1.0-1/rock_manifest
```

### Сборка из исходного rock'а {#build-from-the-source-rock}

Проект на платформе, под которую бинарный rock не опубликован, собирает
модуль из его исходного rock'а. Работу этого механизма на текущей машине
показывает сервер, содержащий только исходный rock. В проекте модуля на
теге `1.1.0` упакуйте только исходный rock (что ничего не собирает) в
отдельный каталог:

```console
$ cd ../hashkit
$ tt package pack --format rock-src --out ../dist-src
.../dist-src/hashkit-1.1.0-1.src.rock
.../dist-src/hashkit-1.1.0-1.rockspec
$ cd ../kv-api
```

В файле `app.manifest.toml` приложения `kv-api` измените строку `hashkit` на:

```toml
hashkit = { version = ">=1.1.0", registry = "../dist-src" }
```

```console
$ tt package resolve
no version moved, but the lock changed:
  manifest hash refreshed
  file digests of "hashkit" pinned anew
$ tt -V package build 2>&1 | grep -E 'Download|running'
   · rocks.Download: downloaded library=go-luarocks name=hashkit version=1.1.0-1 arch=src path=.../kv-api/_build/src/hashkit-1.1.0-1.src.rock
   · running cc -O2 -fPIC -std=gnu99 -Wall -Wextra -Werror -I/usr/include/tarantool -shared -Wl,--as-needed -o .../kv-api/_build/src/hashkit/rock/default/lib/hashkit/fnv.so fnv.c dir=.../kv-api/_build/src/hashkit-1.1.0-1/hashkit/hashkit
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0
$ ls .rocks/share/tarantool/rocks/hashkit/1.1.0-1/doc
LICENSE
```

В каталоге `../dist-src`, проиндексированном при упаковке, бинарного rock'а
нет, поэтому локфайл теперь фиксирует только дайджесты исходного rock'а и
файла rockspec. tt собирает пакет из исходного rock'а в `_build/src/` по его
манифесту компилятором текущей машины и устанавливает его в зафиксированной
локфайлом версии вместе с лицензией; для сборки требуются заголовочные файлы
Tarantool, как и при сборке самого модуля.

### Параллельная работа над обоими проектами {#work-on-both-side-by-side}

Зависимость по пути собирает модуль прямо из его рабочего дерева, что
позволяет проверить изменения в приложении до выпуска релиза. В файле
`app.manifest.toml` приложения `kv-api` измените строку `hashkit` на:

```toml
hashkit = { source = "path", path = "../hashkit" }
```

```console
$ tt package build
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION  SOURCE  ORIGIN
default  hashkit  -           1.1.0-1  path    direct
$ grep -A5 "name = 'hashkit'" app.manifest.lock
name = 'hashkit'
version = '1.1.0-1'
source = 'path'
path = '../hashkit'
content_hash = 'sha256:0b20793689f3ace21b08133d84fc1e04dd5da2e57d46e7e1dcf7cb5485cebbd9'
```

Локфайл фиксирует каталог и хеш его файлов. При изменении модуля локфайл
устаревает: обычная сборка разрешает его заново, а сборка с `--locked`
завершается с ошибкой; модуль версионируется так же, как и его рабочее
дерево:

```console
$ echo '/* A change in progress. */' >> ../hashkit/hashkit/fnv.c
$ tt package build --locked
   ⨯ lock is out of date: path dependency "hashkit" changed on disk (--locked); run tt package resolve to bring the lock back in step
$ tt package build
$ tt run -e "print(require('hashkit').VERSION)" - </dev/null
1.1.0+dirty
$ tt package deps
PRODUCT  NAME     CONSTRAINT  VERSION        SOURCE  ORIGIN
default  hashkit  -           1.1.0.dirty-1  path    direct
$ git -C ../hashkit checkout -q hashkit/fnv.c
```

tt собирает зависимость в каталоге `_build/path/hashkit/` приложения
`kv-api`, не затрагивая каталог самого модуля. Путь может быть и
абсолютным: локфайл сохраняет его в абсолютном виде, и такой локфайл
действителен только на машине с указанным каталогом. Проект с зависимостью
по пути упаковывается в архив `.tt` вместе с лицензией модуля, но не в rock:
для упаковки в rock требуется зависимость от опубликованной версии.

## Для пользователей LuaRocks {#for-luarocks-users}

LuaRocks устанавливает бинарный rock из каталога с rock'ами или с
раздающего его HTTP-сервера. Команда `tt rocks` запускает сам LuaRocks; из
каталога `kv-api/`:

```console
$ cd ..
$ mkdir lr && cd lr
$ tt rocks install hashkit --only-server ../dist
Installing ../dist/hashkit-1.1.0-1.linux-x86_64.rock

hashkit 1.1.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
No existing manifest. Attempting to rebuild...
hashkit 1.1.0-1 is now installed in .../lr/.rocks (license: BSD-2-Clause)

$ tarantool -e "local h = require('hashkit') print(h.VERSION, h.bucket('user:1', 4))" - </dev/null
1.1.0	4
```

Для исходного rock'а требуется tt. Его rockspec совпадает с rockspec
бинарных rock'ов и имеет тип сборки `none`, поэтому LuaRocks устанавливает
rock без сборки и без модулей внутри. Платформа без бинарного rock'а
получает от LuaRocks исходный rock; `tt rocks` видит, что LuaRocks ничего
не собрал, удаляет полученное дерево и завершается с ошибкой уже после
того, как сам LuaRocks сообщил об успешной установке. Дерево `.rocks/`
создавалось под этот rock, поэтому оно тоже удаляется:

```console
$ cd ..
$ mkdir lr-src && cd lr-src
$ tt rocks install hashkit --only-server ../dist-src
Installing ../dist-src/hashkit-1.1.0-1.src.rock

hashkit 1.1.0-1 depends on tarantool >= 3.0.0 (3.9.0-1 provided by VM)
No existing manifest. Attempting to rebuild...
hashkit 1.1.0-1 is now installed in .../lr-src/.rocks (license: BSD-2-Clause)

   ⨯ LuaRocks built nothing of the source rock of a tt package: hashkit 1.1.0-1 is not installed, whatever LuaRocks printed above; tt removed it from the tree again. Install the binary rock of linux-x86_64 from a server that has one, or depend on the rock in app.manifest.toml and run tt package build, which builds the source rock with its manifest
$ ls -A
$ tarantool -e "require('hashkit')" - </dev/null 2>&1 | head -2
(command line):1: module 'hashkit' not found:
	no field package.preload['hashkit']
```

Обычный `luarocks` выполняет такую же установку без проверок: он сообщает,
что rock установлен, а последующий вызов `require` для модуля завершается с
ошибкой, аналогичной приведённой выше.

LuaRocks также не умеет читать rock-индекс:

```console
$ tt rocks install hashkit --only-server ../site
   ⨯ the rock server serves only a rock index: ../site has nothing but tt-rocks-index.json; LuaRocks reads only manifests; install from it with tt package
```

Поэтому бинарные rock'и для всех платформ, на которых работают пользователи
LuaRocks, следует публиковать в каталоге с rock'ами или на его HTTP-сервере,
рядом с rock-индексом, если он используется.

## Устранение неполадок {#troubleshooting}

Эти сообщения не относятся к шагам пошагового руководства.

Предупреждение при разрешении локфайла:

```text
⚠ checks resolved to scm-1, which tracks a branch; the lock will not pin fixed code
```

У rock'а нет другой версии, либо ограничение явно указывает на неё. Версии
`scm-1` и `dev-1` соответствуют коду ветки на момент установки.
Используйте зависимость от релиза или запросите релиз у автора: версия,
сформированная tt из коммита, тоже считается релизом.

```text
⚠ hashkit resolved to 1.1.0.dirty-1, which was built from uncommitted changes; the lock will not pin fixed code
```

Rock был собран из дерева с незакоммиченными изменениями, и любая подобная
сборка этого коммита получает такую версию; она выбирается лишь тогда,
когда ограничению не удовлетворяет ни одна другая версия. Собирайте rock'и
из чистого checkout'а репозитория, как это делает CI; на стороне
потребителя ограничение, допускающее релизную версию, выберет релиз.

Ошибка `--locked` после обновления tt:

```text
⨯ lock is out of date: manifest_hash of the lock is of an older tt; tt hashes the manifest differently now (--locked); run tt package resolve to bring the lock back in step
```

Текущая версия tt вычисляет хеш манифеста иначе, чем версия tt, записавшая
локфайл. Выполните команду `tt package resolve` или запустите сборку без
флага `--locked` и закоммитьте обновлённый локфайл: версии не изменятся, а
`tt package resolve` покажет, что именно было перезаписано.

```text
no version moved, but the lock changed:
  manifest hash refreshed
  generated_by is "tt 3.0.0-alpha.2", was "tt 3.0.0-alpha.1"
```

Локфайл более старой версии tt может содержать ключи `checksum` - контрольные
суммы md5 исходного кода rock'а, которые tt больше не фиксирует; команда
`resolve` удаляет их и выводит имена затронутых rock'ов:

```text
  checksum of "hashkit" dropped: tt no longer locks the md5 of the source of a rock
```

Предупреждения `tt package pack` при сборке архива:

```text
⚠ bundling tt 3.0.0-alpha.2, a prerelease build, to satisfy ">=3.0.0"
```

Архив включает в себя исполняемый файл tt: версию из кеша runtime'ов,
допустимую ограничением `[platform].tt`, либо версию, помещённую в проект
командой `tt binary install tt`, либо ту версию tt, которая выполняет
упаковку. В данном случае это предварительная версия (как и любая версия
tt 3 до официального релиза), которая удовлетворяет ограничению по своей
версии без суффикса предрелиза. Архив для production собирают релизной
версией tt. Rock не включает в себя tt и таких предупреждений не получает.

```text
⚠ bundling Tarantool without a license: looked beside .../bin/tarantool in ...
```

Архив упаковывает Tarantool, рядом с которым нет файла лицензии: отсутствуют
`LICENSE`, `LICENSE.md`, `LICENSE.txt` или `COPYING` в каталогах из списка
в сообщении (в префиксе установки и в его подкаталоге `share/tarantool/`).
К таким сборкам относится Tarantool, установленный командой
`tt binary install`, так как он поставляется без файла `LICENSE`; также это
может быть сборка из исходников, запускаемая из каталога сборки. Упаковка
продолжается, но архив не содержит лицензии Tarantool. Перед упаковкой
поставляемого архива положите файл `LICENSE` соответствующего релиза
Tarantool в один из перечисленных каталогов. Rock не включает в себя
Tarantool и таких предупреждений не получает.

Файл из rock-индекса, не соответствующий своему дайджесту, не
устанавливается:

```text
⨯ installing hashkit 1.1.0-1: ... sha256 of hashkit-1.1.0-1.linux-x86_64.rock is ded9d516..., want 0d72753c...: rocks: checksum mismatch; the file does not have the sha256 the lock or the rock server gives for it, so it was not installed; if the rock server published the rock anew on purpose, run tt package resolve to pin what it serves now
```

Опубликованный rock не должен меняться: вместо этого выпустите новую
сборку с новым значением `--revision`.

Манифест проверяется до начала сборки. Флаги компилятора указываются в
`cflags` и `ldflags`, а не в `env` блока сборки, откуда компилятор их не
читает:

```text
⨯ invalid manifest: components.hashkit.build.env.CFLAGS: does nothing on backend "lua-c": put the flags of the compiler into cflags; CFLAGS of the environment tt runs in comes after them
⨯ invalid manifest: components.hashkit.build.env.CPPFLAGS: does nothing on backend "lua-c": put the macros into defines, the directories of headers into include_dirs and other flags of the preprocessor into cflags and cxxflags
```

Поле другого backend'а не действует на текущем, а операционной системой для
`platforms.<os>` может быть только `linux` или `darwin`:

```text
⨯ invalid manifest: components.hashkit.build.make_target: is a field of backend make, not of "lua-c"
⨯ invalid manifest: components.hashkit.build.output: is a field of backend make, shell or cmake, not of "lua-c"
⨯ invalid manifest: components.hashkit.build.platforms.macos: "macos" is not an OS tt builds for (want linux or darwin)
```

Отсутствие файла лицензии, указанного в `license_files`, приводит к ошибке
упаковки любого из rock'ов:

```text
⨯ license_files entry "LICENSE": no such file
```
