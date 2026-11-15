# Проверка и форматирование кода на Lua с помощью EmmyLua (экспериментально) {#check-and-format-lua-code-with-emmylua-experimental}

[EmmyLua](https://github.com/EmmyLuaLs/emmylua-analyzer-rust) - анализатор
аннотированного Lua: он проверяет код на ошибки, форматирует его, генерирует
документацию по аннотациям и работает как language server для редактора.
`tt emmylua` запускает его программы, передавая им всё, что tt знает о коде:
версию Lua в Tarantool, его глобальные переменные, типы, поставляемые с
Tarantool, и модули, устанавливаемые rock'ами проекта. Все команды и флаги
описаны в [справочнике команд](../reference/commands/emmylua.md).

Страница представляет собой пошаговое руководство: команды выполняются по
порядку, каждая из указанного для неё каталога, начиная с пустого каталога вне
рабочего дерева git. Шаги, требующие редактора или сервиса CI, отмечены как не
выполняемые здесь. Приведён вывод для Linux x86_64 с EmmyLua 0.30.0pre1 -
релиза, который tt выбирает по умолчанию; пути, хеши и время выполнения на
вашей машине будут отличаться, а другой релиз EmmyLua может иначе формулировать
и считать диагностические сообщения.

Понадобятся:

- tt 3 и git;
- Tarantool 3.9 или новее в `PATH`, установленный из пакета или собранный из
  исходников: он устанавливает стабы EmmyLua для своих модулей в
  `share/tarantool/emmylua`. Со старыми версиями Tarantool или без него команды
  работают без этих типов (см. [Устранение неполадок](#troubleshooting));
- доступ к `raw.githubusercontent.com` и `github.com`, откуда загружается
  rock `emmylua`.

## Подготовка проекта {#set-up-the-project}

Проект представляет собой пакет `kv` с модулем Lua и скриптом, использующим его:

```console
$ mkdir kv && cd kv
$ git init -q
$ mkdir kv scripts
$ printf '.rocks/\n.tarantool/\n_build/\napi/\n' > .gitignore
```

`app.manifest.toml`:

```toml
manifest_version = "0.1"

[package]
name = "kv"

[platform]
tarantool = ">=3.0.0"
tt = ">=3.0.0"

[dependencies]

[components.kv]
path = "kv"
```

`kv/init.lua`, модуль, `require('kv')`:

```lua
local checks = require('checks')
local log = require('log')

--- A store of values by string keys, on a space of Tarantool.
---@class kv.Store
---@field space string the name of the space
local Store = {}
Store.__index = Store

--- The most keys a store holds.
local LIMIT = 1000000ULL

--- Open the store on a space, creating the space if there is none.
---@param name string the name of the space
---@return kv.Store
local function open(name)
    checks('string')
    box.schema.space.create(name, {if_not_exists = true})
    box.space[name]:create_index('pk', {parts = {{1, 'string'}}, if_not_exists = true})
    return setmetatable({space = name}, Store)
end

--- Put a value under a key.
---@param key string
---@param value any
function Store:put(key, value)
    if box.space[self.space]:len()>=LIMIT then
        error('the store is full')
    end
    box.space[self.space]:replace({key, value})
    log.info('put %s', key)
end

return {open = open, LIMIT = LIMIT}
```

`scripts/fill.lua`, скрипт с ошибкой:

```lua
local kv = require('kv')

box.cfg({})

local store = kv.open('demo')
for i = 1, 10 do
    store:set('key-' .. i, 'value ' .. i)
end
```

Компонент `kv` - это код пакета: `tt package build` раскладывает его файлы для
загрузки через `require('kv')`. Каталог `scripts/` содержит код проекта, не
входящий в пакет; EmmyLua проверяет и то, и другое. В каталог `api/` помещается
документация [ниже](#generate-the-documentation).

## Добавление EmmyLua в проект {#add-emmylua-to-the-project}

В каталоге проекта, внешнем `kv/`:

```console
$ tt emmylua prepare
declared emmylua 0.30.0pre1 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/ in [dev_dependencies]
  emmylua 0.30.0pre1-1
installing the rocks
EmmyLua 0.30.0pre1 from https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/
next: tt emmylua check
$ tail -2 app.manifest.toml
[dev_dependencies]
emmylua = { version = "0.30.0pre1", registry = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/" }
$ grep -A3 'lock.dev_dependencies' app.manifest.lock
[[lock.dev_dependencies]]
name = 'emmylua'
version = '0.30.0pre1-1'
source = 'registry'
$ ls -1 .rocks/bin
emmylua_check
emmylua_doc_cli
emmylua_ls
luafmt
```

`tt emmylua prepare` объявляет бинарный rock `emmylua` как
[зависимость для разработки](../reference/app-manifest.md#dependencies-and-dev_dependencies)
из rock-индекса
[tarantool-contrib/rocks-repo](https://github.com/tarantool-contrib/rocks-repo) -
инструменты проекта, которые не входят в архив пакета. Команда разрешает
локфайл, фиксирующий версию rock'а и sha256 его файла для каждой платформы
(строка `artifacts` после показанных выше), и устанавливает rock'и, в результате
чего в `.rocks/bin` появляются четыре программы EmmyLua: анализатор, генератор
документации, language server и форматтер. Объявление, уже присутствующее в
манифесте, оставляется без изменений. Параметр `--version` задаёт другой релиз,
раздаваемый сервером, `--rock-server` - другой сервер, а `--from` упаковывает
вместо этого локальную сборку EmmyLua; см.
[`tt emmylua prepare`](../reference/commands/emmylua.md#tt-emmylua-prepare).
О совместной работе rock-индекса и локфайла рассказано в разделе
[Rock-серверы](../concepts/rock-servers.md).

## Проверка кода {#check-the-code}

```console
$ tt emmylua check
--- scripts/fill.lua [2 warnings]
warning: function `store:set` may be nil [need-check-nil]
  --> scripts/fill.lua:7:5

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end

warning: Undefined field: `set` [undefined-field]
  --> scripts/fill.lua:7:11

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end


--- kv/init.lua [1 warning]
warning: Cannot resolve module `log`. [unresolved-require]
  --> kv/init.lua:2:21

  1 | local checks = require('checks')
  2 | local log = require('log')
  3 |



Summary
  3 warnings

Check completed with warnings
Check finished
```

`tt emmylua check` проверяет каждый файл Lua в проекте и выводит отчёт о них в
собственном порядке, который на вашей машине может отличаться. Команда
завершается с кодом 0 при наличии предупреждений и с кодом 1 при ошибках; флаг
`--warnings-as-errors` приводит к ошибке и при предупреждениях. Обнаружены две
проблемы:

- `store:set`: у хранилища нет метода `set`, используется `put`. EmmyLua знает
  из аннотаций в `kv/init.lua`, что `kv.open` возвращает `kv.Store`.
- `log`: стабы Tarantool 3.9 описывают модули `checks` и `metrics`, поэтому
  `require('checks')` разрешается, хотя стаб не задаёт модулю тип и вызов
  `checks` не проверяется; у встроенных модулей вроде `log`, `fiber` и
  остальных стабов пока нет. На `box` предупреждение не выдаётся: tt объявляет
  его и другие глобальные переменные Tarantool для EmmyLua.

Пути, каталоги или файлы проекта сужают область отчёта:

```console
$ tt emmylua check scripts/fill.lua
--- scripts/fill.lua [2 warnings]
warning: function `store:set` may be nil [need-check-nil]
  --> scripts/fill.lua:7:5

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end

warning: Undefined field: `set` [undefined-field]
  --> scripts/fill.lua:7:11

  6 | for i = 1, 10 do
  7 |     store:set('key-' .. i, 'value ' .. i)
  8 | end



Summary
  2 warnings

Check completed with warnings
Check finished
```

Остальная часть проекта по-прежнему читается как библиотека, поэтому
`require('kv')` разрешается и `store:set` по-прежнему обнаруживается; имена
модулей и пути в отчёте и в `.emmyrc.json` соответствуют корню. Путь вне
проекта отклоняется. `tt emmylua doc` принимает пути точно так же.

Исправим скрипт. `scripts/fill.lua`:

```lua
local kv = require('kv')

box.cfg({})

local store = kv.open('demo')
for i = 1, 10 do
    store:put('key-' .. i, 'value ' .. i)
end
```

Для `log` опишем функции, вызываемые проектом, в стабе - файле аннотаций,
который читает EmmyLua и который не исполняется кодом. Директива `---@meta log`
делает его модулем `log`. Стабы проекта размещаются в отдельном каталоге:

```console
$ mkdir types
```

`types/log.lua`:

```lua
---@meta log

--- The logger of Tarantool: the functions of it this project calls.
local log = {}

--- Write a message at the INFO level, formatted as string.format formats it.
---@param fmt string
---@param ... any
function log.info(fmt, ...) end

return log
```

и конфигурация EmmyLua для проекта, `.emmyrc.json`, объявляющая `types/`
библиотекой, которая читается ради типов и не проверяется и не документируется
как код проекта:

```json
{
  "workspace": {
    "library": ["types"]
  }
}
```

```console
$ tt emmylua check
No issues found
Check finished
```

tt запускает EmmyLua сначала с собственной конфигурацией (см.
[Что tt передаёт программам](#what-tt-gives-the-programs)), а затем с файлами
проекта `.luarc.json`, `.emmyrc.json` и `.emmyrc.lua`. EmmyLua объединяет их:
объекты сливаются, а списки конкатенируются, поэтому `library` выше добавляет
`types/` к библиотекам, переданным tt.

Соберём пакет и проверим его снова:

```console
$ tt package build
$ tt emmylua check
No issues found
Check finished
```

`tt package build` копирует файлы компонента в `_build/` и раскладывает пакет в
`.rocks/share/tarantool/kv/`. tt исключает `_build/` из кода, читаемого
EmmyLua. Каталог `.rocks/share/tarantool` читается как библиотека для модулей
rock'ов, от которых зависит проект, но собственный пакет проекта из него
исключается, чтобы модуль не считывался дважды.

## Форматирование кода {#format-the-code}

Форматтер - это `luafmt`. Он читает собственную конфигурацию, `.luafmt.toml`,
а не конфигурацию анализатора. В данном файле таблицы задаются без пробелов
внутри фигурных скобок, как это принято в коде Tarantool. `.luafmt.toml`:

```toml
[spacing]
space_inside_braces = false
```

```console
$ tt emmylua fmt
diff --git a/kv/init.lua b/kv/init.lua
index 8f517bc411362f42a4e0f4ec06a219b4cbd0abc2..2b21b6273f3be20a33e0caa189febf3c1734c3b0 100644
--- a/kv/init.lua
+++ b/kv/init.lua
@@ -21,10 +21,10 @@
 end
 
 --- Put a value under a key.
----@param key string
+---@param key   string
 ---@param value any
 function Store:put(key, value)
-    if box.space[self.space]:len()>=LIMIT then
+    if box.space[self.space]:len() >= LIMIT then
         error('the store is full')
     end
     box.space[self.space]:replace({key, value})
1 file(s) would be reformatted, 2 file(s) OK
```

Без флагов `tt emmylua fmt` выполняет проверку: выводит, как изменились бы файлы,
и завершается с кодом 1, если изменения требуются, или 2, если файл не удалось
отформатировать. Флаг `--write` перезаписывает файлы:

```console
$ tt emmylua fmt --write
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
```

Аргументы после `--` передаются в luafmt без изменений. Параметр
`--explain-config` показывает, с какими настройками форматируется файл:

```console
$ tt emmylua fmt -- --explain-config kv/init.lua
config file: .luafmt.toml
resolved for: kv/init.lua
settings differing from defaults:
  spacing.space_inside_braces = false
  syntax.level = LuaJIT
```

Параметр `spacing` берётся из `.luafmt.toml`, а `syntax.level` - из tt:
по умолчанию luafmt разбирает синтаксис Lua 5.5 и завершается с ошибкой на
конструкциях, поддерживаемых только в LuaJIT, таких как литерал `1000000ULL` в
`kv/init.lua`. tt передаёт `--level lua-jit`, если только найденная luafmt
конфигурация для этого файла не задаёт `[syntax] level` или если аргументы
после `--` не содержат `--level` либо `--config`.

Если путь не указан, внутри рабочего дерева git `tt emmylua fmt` форматирует
файлы Lua, которые git видит в проекте: отслеживаемые, а также неотслеживаемые,
но не входящие в `.gitignore`. Поэтому были отформатированы три файла: каталоги
`.rocks/`, `.tarantool/` и `_build/` игнорируются и исключаются, даже если
отслеживаются. Явно переданный путь форматируется вне зависимости от того,
игнорируется он или нет:

```console
$ printf 'local x=1\n' > scratch.lua
$ echo scratch.lua >> .git/info/exclude
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
$ tt emmylua fmt scratch.lua
diff --git a/scratch.lua b/scratch.lua
index 8e14037c58d17f33aed468463436b8b8b22bea4d..e728b6407be23b4963820447feda2060d3382c49 100644
--- a/scratch.lua
+++ b/scratch.lua
@@ -1 +1 @@
-local x=1
+local x = 1
1 file(s) would be reformatted, 0 file(s) OK
$ rm scratch.lua
```

Вне рабочего дерева git, в каталоге, игнорируемом git, или при отсутствии git
команда обходит весь каталог целиком, за исключением `_build/`. Скрытые файлы и
каталоги, а также перечисленные в `.luafmtignore`, luafmt пропускает сам.

## Генерация документации {#generate-the-documentation}

```console
$ tt emmylua doc -f markdown -o api
Documentation markdown exported to "api/docs"
$ find api/docs -name '*.md' | sort
api/docs/index.md
api/docs/modules/kv.md
api/docs/types/kv.Store.md
```

`tt emmylua doc` записывает документацию по аннотированному коду: модуль `kv` с
`open` и `LIMIT`, а также класс `kv.Store` с его методами и полями. По
умолчанию документация создаётся как HTML-сайт в `./output`; опция `-f markdown`
записывает страницы Markdown и файл `api/mkdocs.yml` для них, а `-f json` -
один JSON-файл. Стаб `types/log.lua` является библиотекой, поэтому не
документируется. Параметры `emmylua_doc_cli`, такие как `--site-name`,
передаются после `--`.

## Использование в редакторе {#use-it-in-an-editor}

`tt emmylua ls` - это language server, запускаемый редактором через стандартные
потоки ввода-вывода. Он вызывает `emmylua_ls` с конфигурацией `tt emmylua check`,
благодаря чему редактор отображает те же диагностические сообщения, а также
дополняет и документирует модули Tarantool, rock'ов и проекта. При запуске в
терминале он сообщает об ожидании редактора; останавливается сервер по Ctrl+C.

Для Neovim 0.11 или новее в `~/.config/nvim/init.lua` (здесь не запускается):

```lua
vim.lsp.config('emmylua_ls', {
    cmd = {'tt', 'emmylua', 'ls'},
    filetypes = {'lua'},
    root_markers = {'app.manifest.toml', '.emmyrc.json', '.git'},
})
vim.lsp.enable('emmylua_ls')
```

Neovim запускает сервер в своём текущем рабочем каталоге, если параметр
`cmd_cwd` не задаёт другой, и tt находит проект оттуда, как и любая команда
проекта: запускайте Neovim внутри проекта. Вне проектов сервер тоже работает, как
показано [ниже](#outside-a-project).

Редактору, который запускает `emmylua_ls` сам, например расширению EmmyLua для
VS Code, требуется конфигурация, формируемая tt, путь к которой передаётся через
переменную `EMMYLUALS_CONFIG`. `tt emmylua config` записывает её и выводит путь
к файлу:

```console
$ tt emmylua config
/home/me/work/kv/.tarantool/emmylua/emmyrc.json
$ cat .tarantool/emmylua/emmyrc.json
{
  "$comment": "Written by tt emmylua for this machine, anew on every run: put the settings of the project into .emmyrc.json.",
  "diagnostics": {
    "globals": [
      "box",
      "misc",
      "package",
      "tonumber64",
      "utf8",
      "_TARANTOOL"
    ]
  },
  "runtime": {
    "version": "LuaJIT"
  },
  "workspace": {
    "ignoreDir": [
      "/home/me/work/kv/.rocks/share/tarantool/kv"
    ],
    "ignoreGlobs": [
      ".rocks/**/*.lua",
      ".tarantool/**/*.lua",
      "_build/**"
    ],
    "library": [
      "/usr/share/tarantool/emmylua",
      "/home/me/work/kv/.rocks/share/tarantool"
    ]
  }
}
```

и редактор запускается с ней (здесь не выполняется):

```console
$ EMMYLUALS_CONFIG=$(tt emmylua config) code .
```

Уже запущенный экземпляр VS Code сохраняет окружение, с которым был запущен:
сначала завершите его работу.

Файл содержит пути текущей машины, поэтому хранится в `.tarantool/`, не
добавляемом в коммиты. Другие команды перезаписывают его при каждом запуске;
`tt emmylua config` используется, когда другие команды ещё не запускались, а
также после изменения проекта или его версии Tarantool.

## Проверка в CI {#check-in-ci}

В пайплайне не требуется `tt emmylua prepare`: локфайл фиксирует rock `emmylua`,
а `tt package fetch` устанавливает зафиксированные rock'и, включая зависимости
для разработки, без повторного разрешения. Зафиксируем проект коммитом и
выполним команды задачи в чистом клоне:

```console
$ git add -A
$ git commit -q -m 'kv: a store on a space'
$ cd ..
$ git clone -q kv kv-ci && cd kv-ci
$ tt package fetch
$ tt emmylua check --warnings-as-errors
No issues found
Check finished
$ tt emmylua fmt
0 file(s) would be reformatted, 3 file(s) OK
```

Обе команды приводят к сбою задачи при обнаружении проблем: `check` с
`--warnings-as-errors` - при любом предупреждении, `fmt` - при наличии файлов,
требующих изменений. Флаг `-f github` выводит диагностику `check` в виде
аннотаций GitHub Actions, `-f gitlab` - в виде отчёта о качестве кода для
GitLab (вместе с `-- --output FILE`), а `-f sarif` - для code scanning.
`tt package fetch` сверяет скачанный rock с sha256, зафиксированным в локфайле
для платформы задачи.

Workflow для GitHub Actions, `.github/workflows/lua.yml` (здесь не запускается):

```yaml
name: lua
on: [push, pull_request]
jobs:
  emmylua:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: tarantool/setup-tarantool@v4
        with:
          tarantool-version: '3.9'
      - name: Install tt
        run: |
          curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: tt package fetch
      - run: tt emmylua check --warnings-as-errors -f github
      - run: tt emmylua fmt
```

Задача выполняет проверку относительно установленного Tarantool: версия без
стабов, например старше 3.9, сообщает о `require('checks')` как о
неразрешённом модуле, и `--warnings-as-errors` завершается ошибкой. Скрипт
установки tt описан в разделе [Установка](../get-started/install.md).

## Вне проекта {#outside-a-project}

`tt emmylua check`, `doc`, `fmt` и `ls` работают в любом каталоге без манифеста
и без выполнения `prepare`: для отдельного скрипта или чекаута rock'а, не
являющегося пакетом tt. В начальном каталоге:

```console
$ cd ..
$ mkdir snippets && cd snippets
```

`hello.lua`:

```lua
local function greet(name)
    return 'Hello, '..nme
end

local calls = 0ULL
calls = calls + 1
print(greet('tt'), calls)
```

```console
$ tt emmylua check
outside a project: fetching EmmyLua 0.30.0pre1-1 for linux-x86_64 (21.4 MB) from https://github.com/tarantool-contrib/rocks-repo/releases/download/emmylua-0.30.0pre1-1/emmylua-0.30.0pre1-1.linux-x86_64.rock
--- hello.lua [1 error, 1 hint]
hint: name is never used, if this is intentional, prefix it with an underscore: _name [unused]
  --> hello.lua:1:22

  1 | local function greet(name)
  2 |     return 'Hello, '..nme

error: undefined global variable: `nme` [undefined-global]
  --> hello.lua:2:23

  1 | local function greet(name)
  2 |     return 'Hello, '..nme
  3 | end



Summary
  1 error
  1 hint

Check failed
Error: "exit code: 1"
$ tt emmylua fmt hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
$ ls -1 ~/.cache/tt/emmylua/programs/*
emmylua_check
emmylua_doc_cli
emmylua_ls
luafmt
$ ls -a
.
..
hello.lua
```

При первом запуске команда скачивает rock `emmylua` для данного хоста с
rock-сервера, сверяет его с sha256 из индекса сервера и распаковывает программы
в пользовательский кэш: подкаталог `tt/emmylua/` каталога `~/.cache`
(`~/Library/Caches` на macOS); последующие запуски берут их оттуда и не
обращаются к серверу, поэтому работают без доступа к сети. Конфигурация,
формируемая tt, помещается туда же - по одному файлу на каждый каталог,
благодаря чему в сам каталог ничего не записывается. Раскладка этого кэша
описана в разделе [Раскладка](../reference/layout.md#the-user-cache). Параметр
`--version` запускает другой релиз, а `--library DIR` читает модули каталога как
библиотеку, например чекаут rock'а, используемого кодом. В проекте релиз
определяется локфайлом, а параметры `--version` и `--rock-server` отклоняются.

Каталог выполнения команды выступает здесь корнем: `check` и `doc` принимают
пути внутри него, как и в проекте, а остальные пути отклоняют. `fmt` принимает
любой файл или каталог.

### Автономная работа {#work-offline}

На машине без доступа к сети при первом запуске сервер для загрузки недоступен.
Параметр `--rock-server` позволяет указать другой сервер, в том числе локальный
каталог с rock'ами. Создадим такой каталог на машине с доступом к сети, скачав
rock для данной платформы из того же индекса:

```console
$ tt package download emmylua --registry https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/ --dir ../emmylua-rocks
/home/me/work/emmylua-rocks/emmylua-0.30.0pre1-1.linux-x86_64.rock
indexed 1 rock(s) in /home/me/work/emmylua-rocks
```

Скопируем каталог на изолированную машину и передадим его командам:

```console
$ tt emmylua fmt --rock-server ../emmylua-rocks hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
```

Каталог перечитывается при каждом запуске, поэтому вновь добавленный туда rock
будет использован. В данном примере пользовательский кэш уже содержит программы
этого rock'а, чей sha256 совпадает со скачанным ранее, поэтому распаковка не
выполняется; на машине, где программ ещё нет, первый запуск распакует их и
сообщит об этом. В проекте автономная работа обеспечивается содержимым `.rocks/`
или зеркалом по его локфайлу: см.
[Зеркало для сборки без доступа к сети](../concepts/rock-servers.md#a-mirror-for-offline-builds).

## Что tt передаёт программам {#what-tt-gives-the-programs}

`check`, `doc` и `ls` запускаются с конфигурацией, формируемой tt при каждом
запуске: в `.tarantool/emmylua/emmyrc.json` для проекта и в пользовательском
кэше вне проектов (файл, путь к которому вывела команда `tt emmylua config`
выше):

- `runtime.version` имеет значение `LuaJIT` - диалект Lua в Tarantool: его
  синтаксис (например, `0ULL`) и стандартная библиотека, включая `bit` и `jit`.
- `diagnostics.globals` - глобальные переменные, определяемые Tarantool помимо
  LuaJIT: `box`, `misc`, `package`, `tonumber64`, `utf8` и `_TARANTOOL`.
- `workspace.library` - стабы того Tarantool, с которым tt запускает проект
  (`share/tarantool/emmylua` относительно его префикса), и модули,
  устанавливаемые rock'ами проекта (`.rocks/share/tarantool` и
  `.rocks/share/lua/5.1`); затем каталоги, указанные в `--library`.
- `workspace.ignoreGlobs` исключают `.rocks/`, `.tarantool/` и `_build/` из кода
  проекта. В каталоге `_build/` команда `tt package build` выполняет сборку
  проекта из копий его исходников и зависимостей по пути: если читать его как
  код, каждый модуль встретился бы дважды.
- `workspace.ignoreDir` исключает собственный пакет проекта из библиотеки
  `.rocks/share/tarantool`, куда его устанавливает `tt package build`: модули,
  устанавливаемые его rock'ом, абсолютные пути, здесь -
  `.rocks/share/tarantool/kv`. До сборки проекта этот каталог отсутствует.

Файлы проекта `.luarc.json`, `.emmyrc.json` и `.emmyrc.lua` считываются после
неё и дополняют её, как показано [выше](#check-the-code). `check`, `doc` и `ls`
читают все остальные файлы Lua в проекте, включая игнорируемые git: секции
`ignoreGlobs` или `ignoreDir` в файле `.emmyrc.json` позволяют исключить
дополнительные пути. При передаче путей `check` и `doc` формируют ещё одну
конфигурацию на время выполнения, которая исключает остальную часть корня из кода
и считывает её как библиотеку.

`fmt` запускает luafmt без этой конфигурации: luafmt считывает файл
`.luafmt.toml` или `luafmt.toml` (ближайший выше по дереву каталогов для каждого
файла) и `.luafmtignore`. Со своей стороны tt добавляет уровень синтаксиса,
`--level lua-jit`, для файлов, чья конфигурация не содержит `[syntax] level`,
если аргументы после `--` не содержат ни `--level`, ни `--config`; а также
список файлов при запуске без указания путей - файлы, видимые git, либо каталог
целиком, кроме `_build/`, вне git, как описано
[выше](#format-the-code).

## Устранение неполадок {#troubleshooting}

### Уровень Lua 5 нарушает форматирование кода на LuaJIT {#a-level-of-lua-5-breaks-the-formatting-of-luajit-code}

Команда `luafmt --init`, вызванная отдельно, записывает полную конфигурацию по
умолчанию, включая `[syntax] level = "Lua55"`; уровень синтаксиса может быть
задан и в конфигурации, взятой из проекта на стандартном Lua. Как только
конфигурация определяет уровень, tt не передаёт свой параметр `--level`, и разбор
кода, специфичного для LuaJIT, завершается ошибкой. Команда
`tt emmylua fmt -- --init` записывает конфигурацию в корень (каталог запуска
команды вне проекта) и выставляет в ней уровень LuaJIT. В каталоге `snippets/`:

```console
$ tt emmylua fmt -- --init
Wrote /home/me/work/snippets/luafmt.toml
   • set [syntax] level = "LuaJIT" in /home/me/work/snippets/luafmt.toml, the Lua of Tarantool, for "Lua55" luafmt writes module=emmylua
$ head -2 luafmt.toml
[syntax]
level = "LuaJIT"
```

Уровень синтаксиса Lua 5.5 в том виде, как его записывает `luafmt --init`:

```console
$ printf '[syntax]\nlevel = "Lua55"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
hello.lua: syntax error at 5:15: unexpected character 'U' after number literal
0 file(s) would be reformatted, 0 file(s) OK, 1 file(s) could not be formatted
```

Удалите таблицу `[syntax]` из файла или задайте уровень LuaJIT, который в
конфигурации записывается как `LuaJIT`:

```console
$ printf '[syntax]\nlevel = "LuaJIT"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
diff --git a/hello.lua b/hello.lua
index fbea083d59a45c492fa52dbc1bea179530ef19a0..0c155512782bbfa93aeb8cac3b09b5e9d3c1e306 100644
--- a/hello.lua
+++ b/hello.lua
@@ -1,5 +1,5 @@
 local function greet(name)
-    return 'Hello, '..nme
+    return 'Hello, ' .. nme
 end
 
 local calls = 0ULL
1 file(s) would be reformatted, 0 file(s) OK
```

### `unknown variant` для уровня синтаксиса {#unknown-variant-of-the-level}

В конфигурации и в параметре командной строки имя уровня записывается
по-разному: `LuaJIT` в `[syntax] level` и `lua-jit` для `--level`. Запись
варианта из флага командной строки в конфигурации приводит к ошибке при
обработке каждого файла:

```console
$ printf '[syntax]\nlevel = "lua-jit"\n' > luafmt.toml
$ tt emmylua fmt hello.lua
Failed to format hello.lua: syntax error: failed to parse config luafmt.toml: TOML parse error at line 2, column 9
  |
2 | level = "lua-jit"
  |         ^^^^^^^^^
unknown variant `lua-jit`, expected one of `Lua51`, `Lua52`, `Lua53`, `Lua54`, `Lua55`, `LuaJIT`, `LuaJIT3`

0 file(s) would be reformatted, 0 file(s) OK
$ rm luafmt.toml
```

В обратную сторону `tt emmylua fmt -- --level LuaJIT` отклоняется luafmt с
ошибкой `invalid value 'LuaJIT' for '--level <LEVEL>'`.

### `Cannot resolve module` для модуля Tarantool {#cannot-resolve-module-for-a-module-of-tarantool}

Стабы Tarantool 3.9 описывают только `checks` и `metrics`; встроенный модуль
вроде `log` или `fiber` выдаёт предупреждение `unresolved-require`. Опишите
вызываемые проектом функции этого модуля в стабе, как в файле `types/log.lua`
[выше](#check-the-code), либо отключите диагностику в `.emmyrc.json` с помощью
`"diagnostics": {"disable": ["unresolved-require"]}`, что скроет также и
ошибки в написании имён модулей самого проекта.

При отсутствии Tarantool или при использовании версии без стабов команды
`check`, `doc`, `ls` и `config` выводят предупреждение в stderr, а `checks` и
`metrics` также не разрешаются:

```text
   ⚠ no Tarantool found: checking without the types of the Tarantool API module=emmylua
```

или, для Tarantool без стабов: `Tarantool 3.8.1 has no EmmyLua stubs:
checking without the types of the Tarantool API`.

В версиях Tarantool старше 3.9 стабов нет; нет их и в сборках, установленных
через `tt binary install`, где сохраняются только исполняемый файл и
заголовочные файлы. tt выбирает Tarantool проекта так же, как и остальные
команды; см. [Управление бинарными файлами](binaries.md#install-and-switch).

### `not in the workspace` {#not-in-the-workspace}

```text
   ⨯ not in the workspace: /home/me/work/snippets is not in /home/me/work/kv
```

`check` и `doc` принимают пути внутри корня, в котором они запущены: проекта, а
вне проектов - каталога запуска команды. Запускайте их из каталога с кодом либо
передайте путь команде `fmt`, которая принимает любые пути.

### `EmmyLua is not installed in the project` {#emmylua-is-not-installed-in-the-project}

```text
   ⨯ EmmyLua is not installed in the project: no emmylua_check in .rocks/bin; the lock pins emmylua: run tt package fetch to install the rocks it pins
```

Локфайл фиксирует EmmyLua, но каталог `.rocks/` не установлен: например, в
свежем клоне репозитория. Команда `tt package fetch` устанавливает rock'и,
зафиксированные локфайлом. В проекте, чей локфайл не фиксирует EmmyLua, сообщение
предлагает выполнить `tt emmylua prepare`, которая сначала объявит rock.
