# Первый пакет {#первый-пакет}

В этом руководстве мы оформим небольшое приложение на Tarantool как пакет:
объявим зависимость, соберём пакет, запустим тест, упакуем приложение в архив
и установим архив для текущего пользователя.

## Создание манифеста {#создание-манифеста}

```console
$ mkdir hello && cd hello
$ git init
$ tt new
created /home/me/hello/app.manifest.toml for package hello
$ tt binary install tarantool
```

`tt new` записывает `app.manifest.toml`, названный по каталогу. Имя пакета
состоит из строчных латинских букв, цифр и дефисов, начинаясь с буквы; другое
имя можно задать флагом `-n`. `tt binary install tarantool` даёт проекту
Tarantool, под которым он собирается и работает.

## Добавление зависимостей {#добавление-зависимостей}

```console
$ tt package add checks '>=3.0.0'
added checks >=3.0.0 to [dependencies]
  checks 3.4.1-1
$ tt package add luatest --dev
added luatest * to [dev_dependencies]
  luatest 1.4.5-1
```

`tt package add` объявляет зависимость в манифесте и разрешает её в
`app.manifest.lock`, фиксируя точные версии; коммитим оба файла. Зависимость
`--dev` нужна только тестам и инструментам: в архив она не попадает.

## Написание кода {#написание-кода}

Код пакета состоит из его компонентов. Объявим компонент для каталога
`hello`:

```toml
[components.app]
path = "hello"
```

`hello/init.lua`:

```lua
local checks = require('checks')

local function greet(name)
    checks('string')
    return 'Hello, ' .. name .. '!'
end

return {greet = greet}
```

## Сборка и запуск {#сборка-и-запуск}

```console
$ tt package build
$ tt run -e "print(require('hello').greet('tt'))" - </dev/null
Hello, tt!
```

`tt package build` устанавливает зависимости в `.rocks/` и раскладывает файлы
компонентов в `.rocks/share/tarantool/hello/`, вместе с `version.lua`, поэтому
код подключается через `require('hello')`.

`tt run` запускает Tarantool проекта с переданными аргументами; без скрипта
запускается интерактивная консоль.

- Tarantool запускается в текущем каталоге, как если бы `tarantool` запустили
  там же. Путь к скрипту считается от этого каталога.
- Команда работает из любого подкаталога проекта: Tarantool находит `.rocks/`,
  поднимаясь вверх по дереву, а ниже корня tt ставит корень проекта в начало
  `LUA_PATH` и `LUA_CPATH`, чтобы находился исходный код корня.
- `-h` и `--help` перед именем скрипта выводят справку `tt run`. Все аргументы
  начиная с имени скрипта, включая `-h`, передаются скрипту.

## Тестирование {#тестирование}

`test/hello_test.lua`:

```lua
local t = require('luatest')
local hello = require('hello')

local g = t.group()

g.test_greet = function()
    t.assert_equals(hello.greet('world'), 'Hello, world!')
end
```

```console
$ tt test
.
Ran 1 tests in 0.001 seconds, 1 succeeded, 0 failed
```

`tt test` собирает пакет и запускает luatest для `test/`. Запуск можно сузить
путями относительно проекта, указав сколько угодно путей:
`tt test test/unit test/integration`. Аргументы после `--` передаются
в luatest: `tt test test/hello_test.lua -- --verbose`.

## Описание экземпляров {#описание-инстансов}

`.templates/default/config.yaml` - это конфигурация кластера, под которой
работает пакет:

```yaml
app:
  module: hello

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen:
              - uri: 127.0.0.1:3311
```

`app.module` загружает код из `.rocks/`, где он лежит как в проекте, так и в
установленном пакете. Шаблон из одного только `config.yaml` запускает каждый
экземпляр своей топологии; `tt instance start` запускает их и в проекте.

## Упаковка {#pack}

Коммитим проект и ставим тег с версией: версией пакета считается версия из его
файла `VERSION` либо из ближайшего тега версии, `v1.0.0` или `1.0.0`.

```console
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
$ git add -A && git commit -m 'hello 1.0.0' && git tag v1.0.0
$ tt package pack
/home/me/hello/_build/pack/hello-1.0.0-linux-amd64.tt
```

Архив содержит пакет, его зависимости и, в `_runtime/`, Tarantool и tt, под
которыми работает приложение, поэтому он устанавливается без сети.
Незакоммиченные изменения, включая неотслеживаемые файлы, добавляют к версии
суффикс `+dirty`.

> **Замечание о предварительной версии.** `tt package pack` вкладывает tt,
> подходящий под `[platform].tt` манифеста: тот, который
> `tt binary install tt` положил в проект, либо tt, запустивший команду
> (в разделе [Упаковка](../concepts/packages.md#packing) описан весь порядок
> выбора). Если tt собран из исходного кода, см. замечание в
> [Установка](install.md#build-from-source).

## Установка для пользователя {#установка-для-пользователя}

```console
$ tt package install _build/pack/hello-1.0.0-linux-amd64.tt --scope user
installed hello 1.0.0 into user scope
$ tt package list --scope user
NAME   ACTIVE  VERSIONS  DESCRIPTION
hello  1.0.0   1.0.0
$ tt instance start hello:instance-001 --scope user
$ echo "return require('hello').greet('user scope')" | tt connect hello:instance-001 --scope user -f-
---
- Hello, user scope!
...
$ tt instance stop hello:* --scope user --yes
```

Область установки пользователя (user scope) в Linux - это
`~/.local/share/tarantool`. Пакет становится в ней версией
`versions/hello/hello_1.0.0/`, и `apps/hello` указывает на неё; данные
экземпляров хранятся в каталоге `var/` этой области установки. См.
[Проекты и области установки](../concepts/projects-and-scopes.md) и
[Установка приложения для пользователя](../how-to/user-scope.md).

## Что дальше {#что-дальше}

- [An HTTP API in a WebAssembly component](../write-your-app/http-api-in-wasm.md):
  приложение с сервером HTTP на Python, собранным в пакет.
- [Publish a module as a rock](../how-to/rocks.md): модуль с C-частью,
  упакованный в виде rock-модулей, от которых зависят другие проекты.
- [Check and format Lua code with EmmyLua](../how-to/emmylua.md): линтер,
  форматтер и языковой сервер для кода пакета.
- [Пакеты](../concepts/packages.md): манифест, локфайл и архив.
- [`app.manifest.toml`](../reference/app-manifest.md): все поля.
