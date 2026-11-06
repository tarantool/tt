# Tarantool CLI

`tt` — утилита командной строки для разработки, упаковки, развёртывания и
эксплуатации приложений на Tarantool.

> **Предварительная версия.** Это документация tt 3, который ещё не вышел;
> она собрана из ветки `v3`. Документация tt 2.x — в
> [документации Tarantool](https://www.tarantool.io/ru/doc/latest/tooling/tt_cli/).

## Что делает tt {#what-tt-does}

- **Разработка.** Приложение оформляется как пакет: `app.manifest.toml`
  описывает его зависимости и компоненты, `tt package build` собирает их, а
  `tt run` и `tt test` запускают код и тесты под Tarantool проекта.
- **Запуск.** tt запускает инстансы приложения на хосте: инстансы Tarantool,
  настроенные кластерной конфигурацией, инстансы Tarantool со своим
  собственным скриптом и внешние процессы. Все они работают под watchdog tt —
  в фоне, на переднем плане или под systemd.
- **Упаковка и установка.** `tt package pack` собирает архив приложения
  вместе с зависимостями и Tarantool, на котором оно работает, а
  `tt package install` устанавливает его для пользователя или для всей
  системы, без доступа к сети.
- **Управление бинарными файлами**, на которых работает приложение: Tarantool
  Community и Enterprise, tt, TCM и Enterprise SDK — для проекта, для
  пользователя или для всей системы.
- **Эксплуатация** кластера: его конфигурация, наборы реплик и failover,
  резервные копии и восстановление на момент времени (PITR).
- **Компоненты WebAssembly** в Tarantool (экспериментально): код на Python,
  Go, Rust, C, JavaScript или C#, собранный в пакет и вызываемый из Lua; см.
  [Run WebAssembly components](how-to/wasm-components.md) и
  [An HTTP API in a WebAssembly component](write-your-app/http-api-in-wasm.md).

## Попробовать {#try-it}

```console
$ tt create cluster --name myapp
$ cd myapp
$ tt binary install tarantool
$ tt instance start
$ tt instance status
```

Подробный разбор — в разделе
[Кластер на ноутбуке](get-started/first-cluster.md).

## Что читать дальше {#where-to-go}

- [Что нового в tt 3](whats-new.md): что умеет tt 3 и чего не умел tt 2.x.
- [Начало работы](get-started/install.md): установка tt, запуск кластера,
  создание пакета.
- [Концепции](concepts/projects-and-scopes.md): проекты и scope, пакеты,
  инстансы — модель, общая для всех команд.
- [Пишем приложение](write-your-app/http-api-in-wasm.md): целое приложение,
  шаг за шагом.
- [Руководства](how-to/binaries.md): развёртывание, запуск под systemd,
  подпись пакетов, расширение tt, сборка модуля на Rust, запуск компонентов
  WebAssembly.
- [Справочник](reference/commands/index.md): все команды, файлы, которые
  читает tt, переменные окружения.
- [Переход с tt 2.x](migration_from_older_versions.md).
