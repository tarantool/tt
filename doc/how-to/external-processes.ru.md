# Запуск внешнего процесса

Приложение может запускать программы помимо Tarantool - шлюз, воркер,
экспортёр - рядом со своими инстансами Tarantool. Запись с полем `binary`
в `instances.yaml` задаёт такой внешний процесс: команды управления
инстансами запускают, останавливают и показывают его рядом с инстансами
Tarantool, под тем же watchdog'ом.

## Описание процесса {#describe-the-process}

```yaml
storage-001:
gateway:
  binary: gateway           # a program of .rocks/bin, or on PATH, or a path
  args: [--port, "8080"]
  env: {GATEWAY_MODE: production}
  workdir: ./gateway        # <root>/var/lib/<app>/<instance>/ by default
  stop_signal: SIGINT       # SIGTERM by default
  stop_timeout: 10s         # 30s by default
  logrotate_signal: SIGHUP  # none by default
  restart: on-failure       # no by default
```

- `binary` со слешем задаёт путь относительно каталога приложения. Без слеша
  поле задаёт имя программы, поиск которой выполняется сначала в
  `.rocks/bin/` приложения, куда tt собирает программы пакета, а затем в
  `PATH`: `binary: python3` запустит `python3` с хоста.
- `workdir` отсчитывается относительно каталога приложения. Если он не задан,
  процесс запускается в своём каталоге данных,
  `<root>/var/lib/<app>/<instance>/`, который создаёт `tt instance start`.
- `env` накладывается поверх переменных окружения tt.
- Поля `args`, `workdir` и `logrotate_signal` относятся только к внешним
  процессам.

`tt instance start` сообщает о бинарном файле из `binary`, который не удаётся
запустить, или о несуществующем каталоге `workdir` ещё до запуска чего-либо.
Описание всех полей приведено в
[справочнике](../reference/instances-yaml.md).

## Сборка программы вместе с пакетом {#build-the-program-with-the-package}

Компонент с `kind = "executable"` собирает программу в `.rocks/bin/`, так что
пакет несёт её с собой, а запись указывает её по имени:

```toml
[components.gateway]
kind = "executable"
path = "gateway"            # a Go module

[components.gateway.build]
backend = "go"
```

```console
$ tt package build
$ ls .rocks/bin
gateway
$ tt instance start
$ tt instance logs myapp:gateway
myapp:gateway: gateway started, port [--port 8080]
```

Бэкенды исполняемых компонентов:

- `go`: `go build -trimpath` пакета `package` (`.` по умолчанию) в программу
  с именем `name` (по умолчанию имя компонента); `tags`, `ldflags` и
  `cgo` (по умолчанию выключен).
- `cargo`: `cargo build` бинарного файла `bin` (по умолчанию имя
  компонента) в профиле `release`, если в `profile` не указано иное;
  `package`, `features`.
- `cmake`: сборка CMake-проекта из `cwd` вне исходников, в
  `_build/cmake/<component>/`; программы перечисляются в `output`
  относительно дерева сборки, `variables`, `targets`, `build_type`.
- `make` и `shell`: собственная сборка, записывающая программы в
  `TT_OUTPUT_DIR`.

`static = true` предписывает `go` и `cargo` собрать программу, которой не
требуются загрузчик и разделяемые библиотеки, а сборка проверяет, что получен
именно такой файл; программы `cmake`, `make` и `shell` только проверяются.
Тулчейн должен быть доступен в `PATH`. См.
[справочник по манифесту](../reference/app-manifest.md#componentsname).

## Как устроен запуск {#how-it-runs}

- Процесс возглавляет собственную группу процессов, которой целиком
  доставляются сигнал остановки и `tt instance kill`, поэтому
  скрипт-обёртка останавливается вместе с запущенной им программой.
- Вывод процесса направляется в `tt.log`. Watchdog сохраняет `tt.pid` и pid
  самого процесса, `process.pid`, в каталоге запуска.
- `tt instance status` показывает только то, работает ли процесс, и ничего
  сверх этого.
- `tt instance logrotate` заново открывает `tt.log` и отправляет процессу
  сигнал `logrotate_signal`, если он задан в записи.
- `tt instance clean` удаляет лог процесса и каталог его данных.
- `tt connect`, `tt play`, `tt instance kill --dump` и команды кластера
  отклоняют внешний процесс: это не инстанс Tarantool.
- Инстансы запускаются в порядке, указанном в `instances.yaml`, и ни один не
  ждёт готовности другого.

Процесс, чей watchdog был завершён чем-то кроме `tt instance kill`,
продолжает работать без него. `tt instance status` не показывает его как
работающий и выводит предупреждение о нём в alerts, `tt instance start`
отказывается запускать второй экземпляр, а `tt instance stop` и
`tt instance kill` останавливают процесс по `process.pid`.
