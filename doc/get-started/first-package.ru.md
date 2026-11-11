# Первый пакет

В этом руководстве мы напишем небольшое приложение на Tarantool в виде пакета:
объявим зависимость, соберём пакет, запустим тест, упакуем приложение в архив
и установим этот архив для текущего пользователя.

## Создание манифеста {#create-the-manifest}

```console
$ mkdir hello && cd hello
$ git init
$ tt new
created /home/me/hello/app.manifest.toml for package hello
$ tt binary install tarantool
```

Команда `tt new` создаёт файл `app.manifest.toml`, названный по имени
каталога. Имя пакета состоит из строчных латинских букв, цифр и дефисов и
начинается с буквы; другое имя можно задать флагом `-n`. Команда
`tt binary install tarantool` даёт проекту Tarantool, на котором он
собирается и работает.

## Добавление зависимостей {#add-dependencies}

```console
$ tt package add checks '>=3.0.0'
added checks >=3.0.0 to [dependencies]
  checks 3.4.1-1
$ tt package add luatest --dev
added luatest * to [dev_dependencies]
  luatest 1.4.5-1
```

Команда `tt package add` объявляет зависимость в манифесте и разрешает её в
`app.manifest.lock`, фиксируя точные версии; оба файла коммитим в
репозиторий. Зависимость с флагом `--dev` нужна только для тестов и
инструментов: в архив пакета она не попадает.

## Написание кода {#write-the-code}

Код пакета состоит из его компонентов. Объявим компонент для каталога `hello`:

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

## Сборка и запуск {#build-and-run}

```console
$ tt package build
$ tt run -e "print(require('hello').greet('tt'))" - </dev/null
Hello, tt!
```

Команда `tt package build` устанавливает зависимости в `.rocks/` и раскладывает
файлы компонентов по каталогу `.rocks/share/tarantool/hello/` вместе с файлом
`version.lua`, поэтому код доступен через `require('hello')`. Команда `tt run`
запускает Tarantool проекта с переданными аргументами; без скрипта она открывает
интерактивную консоль.

## Тестирование {#test}

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

Команда `tt test` собирает пакет и запускает luatest для каталога `test/`.
Аргументы после `--` передаются в luatest:
`tt test test/hello_test.lua -- --verbose`.

## Описание инстансов {#describe-the-instances}

Файл `.templates/default/config.yaml` задаёт кластерную конфигурацию, с
которой запускается пакет:

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

Параметр `app.module` загружает код из `.rocks/`, где он находится как в
проекте, так и в установленном пакете. Шаблон с одним только `config.yaml`
запускает все инстансы своей топологии; команда `tt instance start` запускает
их и в проекте.

## Упаковка {#pack}

Коммитим проект и ставим тег с версией: версией пакета считается
содержимое файла `VERSION` или ближайший тег версии, `v1.0.0` или `1.0.0`.

```console
$ printf '.rocks/\n.tarantool/\n.env/\n_build/\n' > .gitignore
$ git add -A && git commit -m 'hello 1.0.0' && git tag v1.0.0
$ tt package pack
/home/me/hello/_build/pack/hello-1.0.0-linux-amd64.tt
```

Архив содержит сам пакет, его зависимости и, в каталоге `_runtime/`, бинарные
файлы Tarantool и tt, на которых работает приложение, поэтому пакет
устанавливается без доступа к сети. Незакоммиченные изменения, включая
неотслеживаемые файлы, добавляют к версии суффикс `+dirty`.

> **Замечание о предварительной версии.** Команда `tt package pack` вкладывает
> в архив тот tt, которым она запущена, а его версия должна подходить под
> `[platform].tt` в манифесте. Если tt собран из исходников, см. замечание в
> [Установка](install.md#build-from-source).

## Установка для пользователя {#install-for-the-user}

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

Пользовательский scope в Linux находится в `~/.local/share/tarantool`. Пакет
становится версией `versions/hello/hello_1.0.0/` внутри этого каталога, а
`apps/hello` указывает на неё; инстансы хранят свои данные в каталоге `var/`
этого scope. См.
[Projects and scopes](../concepts/projects-and-scopes.md) и
[Install an application for a user](../how-to/user-scope.md).

## Что дальше {#next}

- [An HTTP API in a WebAssembly component](../write-your-app/http-api-in-wasm.md):
  приложение с HTTP-сервером на Python, собранным в пакет.
- [Packages](../concepts/packages.md): манифест, lock-файл и архив.
- [`app.manifest.toml`](../reference/app-manifest.md): описание всех полей.
