# Запуск приложения на box.cfg

Приложение, настраивающее Tarantool через `box.cfg` в собственном скрипте,
как это делали приложения `init.lua` и `<instance>.init.lua` в tt 2.x,
запускается записями со `script`: это инстансы Tarantool, которые
настраиваются своим скриптом, а не кластерной конфигурацией. Такому
приложению не нужен `config.yaml`.

## Описание инстансов {#describe-the-instances}

`.templates/default/instances.yaml`:

```yaml
legacy-001:
  script: init.lua          # относительно каталога приложения
  env: {TT_MEMTX_MEMORY: "268435456"}
```

`init.lua`:

```lua
box.cfg{}
box.schema.space.create('kv', {if_not_exists = true})
```

```console
$ tt instance start
$ tt instance status
 INSTANCE           STATUS   PID      MODE  CONFIG         BOX      UPSTREAM
 legacy:legacy-001  RUNNING  1330686  RW    uninitialized  running  --
$ echo "return box.cfg.memtx_memory" | tt connect legacy:legacy-001 -f-
---
- 268435456
...
```

## Где инстанс хранит файлы {#where-the-instance-keeps-its-files}

Tarantool запускает скрипт в каталоге приложения. Параметры, которые скрипт
не передаёт сам, `box.cfg` берёт из окружения, а tt выставляет там
переменные раскладки:

- `TT_WAL_DIR`, `TT_MEMTX_DIR` и `TT_VINYL_DIR`: каталог данных,
  `<root>/var/lib/<app>/<instance>/`;
- `TT_LISTEN`: unix-сокет `tarantool.sock` в каталоге запуска, если только
  `TT_LISTEN` не задана в окружении tt.

Сокет консоли - `tarantool.control` в каталоге запуска, а вывод идёт в
`tt.log`. Поле `env` записи идёт поверх и может переместить данные теми же
переменными; в scope пользователя и системы относительный путь в нём
отклоняется.

Относительный путь, который скрипт передаёт в `box.cfg` сам, tt проверить не
может. В scope пользователя и системы он отсчитывается от установленной
версии, так что инстанс пишет в каталог версии, который заменяется при
обновлении: пусть скрипт берёт пути из окружения.

## Что с ними работает {#what-works-on-it}

- `tt connect`, `tt play`, `tt instance status` и `tt instance logrotate`
  работают как с любым инстансом Tarantool.
- `tt instance check` проверяет синтаксис скрипта.
- `tt instance clean` удаляет его данные и журнал.
- `tt cluster show` и `tt cluster publish` отклоняют его: кластерная
  конфигурация им не управляет.
- Цели уровня приложения в `tt replicaset` пропускают его, когда в приложении
  есть и инстансы кластерной конфигурации. Приложение только из скриптовых
  инстансов сохраняет их, и `tt replicaset` действует на него с custom
  оркестратором без `--custom`; `--config` и `--custom` по-прежнему задают
  оркестратор принудительно.

См. также [Переход с tt 2.x](../migration_from_older_versions.md).
