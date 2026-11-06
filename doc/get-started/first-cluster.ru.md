# Кластер на ноутбуке

В этом руководстве мы создадим проект из встроенного шаблона `cluster`,
запустим четыре его инстанса на localhost, подключимся к одному из них
и наведём порядок. Это займёт пару минут и не требует прав root.

## Создание проекта {#create-the-project}

```console
$ tt create cluster --name myapp
$ cd myapp
```

`tt create` запрашивает число наборов реплик, число реплик в каждом из них,
а также пользователя и пароль кластера; достаточно нажать Enter, чтобы оставить
значения по умолчанию: два набора реплик по два инстанса в каждом,
пользователь `client` с паролем `secret`. Флаг `-s` позволяет применить их
без лишних вопросов.

Проект представляет собой каталог с кодом приложения и шаблоном инстансов,
из которого оно запускается:

```text
myapp/
├── .templates
│   └── default
│       ├── config.yaml      # конфигурация кластера: пользователи, наборы реплик, порты
│       └── instances.yaml   # что запускается на этом хосте
└── app.lua
```

`instances.yaml` содержит одну запись `"*": {}`, которая обозначает каждый
инстанс топологии из `config.yaml`. См.
[Инстансы](../concepts/instances.md).

## Установка Tarantool в проект {#install-tarantool-into-the-project}

```console
$ tt binary install tarantool
```

Команда устанавливает свежий релиз Tarantool Community в каталог `.tarantool/`
проекта. Команды управления инстансами этого проекта будут использовать именно
его, независимо от того, какой `tarantool` находится в `PATH`. Чтобы запустить
его самостоятельно, команда `source .env/activate.sh` помещает его первым
в `PATH`.

## Запуск инстансов {#start-the-instances}

```console
$ tt instance start
   • Starting an instance [myapp:replicaset-001-a]...
   • Starting an instance [myapp:replicaset-001-b]...
   • Starting an instance [myapp:replicaset-002-a]...
   • Starting an instance [myapp:replicaset-002-b]...
$ tt instance status
 INSTANCE                STATUS   PID      MODE  CONFIG  BOX      UPSTREAM
 myapp:replicaset-001-a  RUNNING  1303991  RW    ready   running  --
 myapp:replicaset-001-b  RUNNING  1303992  RO    ready   running  --
 myapp:replicaset-002-a  RUNNING  1303993  RW    ready   running  --
 myapp:replicaset-002-b  RUNNING  1303994  RO    ready   running  --
```

Каждый инстанс работает в фоне под watchdog'ом tt. Внутри проекта команды
действуют на его инстансы; имя инстанса формируется как
`<application>:<instance>`, а само приложение называется по имени каталога
проекта.

Инстансы хранят свои данные, логи и сокеты в каталоге `.tarantool/var/`:

```text
.tarantool/var/
├── lib/myapp/replicaset-001-a/   # *.snap, *.xlog
├── log/myapp/replicaset-001-a/   # tt.log
└── run/myapp/replicaset-001-a/   # tt.pid, tarantool.pid, tarantool.control
```

## Подключение {#connect}

Команда `tt connect myapp:replicaset-001-a` открывает интерактивную консоль
к инстансу через его сокет управления. С флагом `-f-` она вместо этого
выполняет Lua-код, прочитанный со стандартного ввода:

```console
$ echo "box.info.ro" | tt connect myapp:replicaset-001-a -f-
---
- false
...
```

К инстансу можно подключиться и по сети, указав пользователя из
конфигурации кластера:

```console
$ echo "return box.space._space:count()" | tt connect client:secret@127.0.0.1:3301 -f-
```

<!-- TODO(writers): a screenshot or a transcript of the interactive console. -->


## Просмотр логов {#read-the-logs}

```console
$ tt instance logs myapp:replicaset-001-a -n 3
$ tt instance logs myapp:replicaset-001-a --follow
```

## Остановка и очистка {#stop-and-clean-up}

```console
$ tt instance stop --yes
$ tt instance clean --yes
```

Команды `stop` и `clean` просят подтверждения, которое пропускает флаг
`--yes`. `clean` удаляет данные и логи неработающих инстансов.

## Что дальше {#next}

- [Первый пакет](first-package.md): пишем собственное приложение с
  зависимостями и тестами.
- [Инстансы](../concepts/instances.md): как tt определяет, что запускать.
- [`tt instance`](../reference/commands/instance.md): полный список флагов.
