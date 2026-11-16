# Запуск внешнего процесса {#запуск-внешнего-процесса}

Приложение может запускать программы помимо Tarantool - шлюз, воркер,
экспортёр - рядом со своими инстансами Tarantool. Запись с полем `binary`
в `instances.yaml` задаёт такой внешний процесс: команды управления
инстансами запускают, останавливают и показывают его рядом с инстансами
Tarantool, под тем же watchdog'ом.

## Описание процесса {#описание-процесса}

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
  `PATH`: `binary: python3` запускает `python3` с хоста.
- `workdir` отсчитывается относительно каталога приложения. Без него
  процесс работает в своём каталоге данных,
  `<root>/var/lib/<app>/<instance>/`, который создаёт `tt instance start`.
- `env` накладывается поверх окружения tt.
- `args`, `workdir` и `logrotate_signal` относятся только к внешним
  процессам.

`tt instance start` сообщает о `binary`, который не удаётся запустить, или о
несуществующем каталоге `workdir` ещё до запуска чего-либо. Описание всех
полей приведено в [справочнике](../reference/instances-yaml.md).

## Сборка программы вместе с пакетом {#сборка-программы-вместе-с-пакетом}

Компонент, чья сборка создаёт программу, помещает её в `.rocks/bin/`, так что
пакет несёт её с собой, а запись указывает её по имени:

```toml
[components.gateway]
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

Backend'ы, собирающие программы:

- `go`: `go build -trimpath` пакета `package` (`.` по умолчанию) в программу
  с именем `name` (по умолчанию имя компонента); `tags`, `ldflags` и `cgo`
  (по умолчанию выключен). Исходники Go не попадают в состав пакета:
  включается только то, что указано в `include` компонента.
- `cargo`: `cargo build` бинарного файла `bin` в профиле `release`, если в
  `profile` не указано иное, в `_build/cargo/`; `package`, `features`.
  Блок, в котором также указан `module`, собирает и модуль библиотеки; см.
  [Build a module in Rust](rust-modules.md).
- `zig`: `zig build` файла `build.zig` из `cwd`, программа `bin` берётся из
  того, что сборка установила в свой префикс, `bin/`; `steps`, `optimize`,
  `options`. Блок, в котором также указан `module`, забирает и модуль; см.
  [Build a module in Zig](zig-modules.md).
- `cmake`: проект CMake из `cwd`, собранный вне дерева исходников в
  `_build/component/<component>/`, его программы перечислены в `output` с
  `to = "bin"` относительно дерева сборки; `variables`, `targets`,
  `build_type`.
- `make` и `shell`: собственная сборка, чьи программы перечислены в `output` с
  `to = "bin"` относительно `cwd`, а промежуточные файлы сборки попадают в
  `TT_BUILD_DIR`.

```toml
[components.tools.build]
backend = "make"
make_target = "tools"
output = [{ path = "build/gw-cli", to = "bin" }]
```

Один компонент может собирать модули и программы одновременно: строковый
элемент `output`  -  это модуль, а файл с `to = "share"`  -  файл данных.
Устаревшее поле `kind = "executable"` из `manifest_version = "0.1"` по-прежнему
работает, с предупреждением: его сборка создаёт только программы, а сборка
`make` или `shell` для такого компонента записывает их в `TT_OUTPUT_DIR`.

`static = true` запрашивает у `go` и `cargo` программу, которой не требуются
загрузчик и разделяемые библиотеки, а сборка проверяет, что получен именно
такой файл; программы `cmake`, `make` и `shell` только проверяются. Тулчейн
должен быть доступен в `PATH`. См.
[справочник по манифесту](../reference/app-manifest.md#componentsname).

## Как устроен запуск {#как-устроен-запуск}

- Процесс возглавляет собственную группу процессов, которой целиком
  доставляются его сигнал остановки и `tt instance kill`, поэтому
  скрипт-обёртка останавливается вместе с запущенной им программой.
- Вывод процесса направляется в `tt.log`. Watchdog сохраняет `tt.pid` и pid
  самого процесса, `process.pid`, в каталоге запуска.
- `tt instance status` показывает только то, работает ли процесс, и ничего
  сверх этого.
- `tt instance logrotate` заново открывает `tt.log` и отправляет процессу
  сигнал `logrotate_signal`, если он задан в записи. На Linux и macOS процесс,
  который не перехватывает, не игнорирует и не блокирует этот сигнал, не
  получает его, поскольку он привёл бы к его завершению; об этом пишется в
  `tt.log`.
- `tt instance clean` удаляет лог процесса и каталог его данных.
- `tt connect`, `tt data play`, `tt instance kill --dump` и команды кластера
  отклоняют внешний процесс: это не инстанс Tarantool.
- Инстансы запускаются в порядке, указанном в `instances.yaml`, и ни один не
  ждёт готовности другого.

Процесс, чей watchdog был завершён чем-то кроме `tt instance kill`,
продолжает работать без него. `tt instance status` не показывает его
работающим и называет его в alert'е, `tt instance start` отказывается
запускать второй экземпляр, а `tt instance stop` и `tt instance kill`
останавливают процесс по `process.pid`.
