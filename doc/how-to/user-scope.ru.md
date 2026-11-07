# Установка приложения для пользователя

Пользовательский scope позволяет запускать приложение без прав root: на
тестовом стенде, на машине разработчика или на хосте, где tt не установлен
в масштабе всей системы. Его корневой каталог в Linux находится в
`~/.local/share/tarantool` или задаётся через `TT_USER_ROOT`. Те же шаги
подходят и для системного scope с флагом `--scope system`, запущенным от
root.

Для работы понадобится архив приложения, собранный командой `tt package pack`
(см. [Ваш первый пакет](../get-started/first-package.md)).

## Установка {#install}

```console
$ tt package install --scope user kv-api-1.0.0-linux-amd64.tt
installed kv-api 1.0.0 into user scope
$ tt package list --scope user
NAME    ACTIVE  VERSIONS  DESCRIPTION
kv-api  1.0.0   1.0.0
```

Архив становится версией `versions/kv-api/kv-api_1.0.0/` scope'а, а ссылка
`apps/kv-api` указывает на неё. Tarantool и tt, входящие в состав архива,
попадают в каталог `versions/` scope'а; доступ к сети не требуется.

## Запуск {#run}

Проект не требуется: команды можно выполнять из любого каталога.

```console
$ tt instance start kv-api:* --scope user
$ tt instance status kv-api:* --scope user
$ tt instance logs kv-api:kv-1 --scope user --follow
$ tt connect kv-api:kv-1 --scope user
```

Инстансы запускаются по шаблону `default` этой версии и хранят свои данные в
каталоге `var/lib/kv-api/` scope'а.

## Описание этого хоста {#describe-this-host}

Шаблоны версии входят в состав пакета; обновление заменяет их. Чтобы задать,
что именно работает на этом хосте - какие инстансы, какие порты и какие
пароли, - в каталог `etc/<app>/` scope'а записывают пару хоста. Она имеет
приоритет над шаблонами:

```console
$ mkdir -p ~/.local/share/tarantool/etc/kv-api
$ cd ~/.local/share/tarantool/etc/kv-api
$ $EDITOR config.yaml       # кластерная конфигурация этого хоста
$ $EDITOR instances.yaml    # что здесь работает, например: kv-1: {restart: on-failure}
$ tt instance start kv-api:* --scope user
```

Для пары хоста обязателен файл `instances.yaml`: в нём указано, какие именно
инстансы кластера работают на этом хосте. См.
[Инстансы](../concepts/instances.md#the-pair).

## Обновление {#upgrade}

```console
$ tt package install --scope user --upgrade kv-api-1.1.0-linux-amd64.tt
$ tt package list --scope user
NAME    ACTIVE  VERSIONS      DESCRIPTION
kv-api  1.1.0   1.0.0, 1.1.0
$ tt instance restart kv-api:* --scope user --yes
```

Флаг `--upgrade` устанавливает только версию новее всех уже установленных.
Новая версия становится активной сразу, но работающий инстанс продолжает
выполнять ту версию, с которой был запущен, в том числе при перезапуске
watchdog'ом после падения; команда `tt instance restart` переводит его на
новую версию.

## Откат {#roll-back}

Отдельной команды отката нет. Повторная установка уже установленной версии
требует флага `--force`, который отклоняется при работающих инстансах
приложения, поэтому для отката инстансы останавливают:

```console
$ tt instance stop kv-api:* --scope user --yes
$ tt package install --scope user --force kv-api-1.0.0-linux-amd64.tt
$ tt instance start kv-api:* --scope user
```

## Удаление {#uninstall}

```console
$ tt instance stop kv-api:* --scope user --yes
$ tt package uninstall kv-api --scope user --yes
removed kv-api from user scope: versions 1.0.0, 1.1.0
```

Команда `uninstall` отклоняется, пока работает хотя бы один инстанс. Она
удаляет все версии и ссылку `apps/`, сохраняя данные в `var/`, пару хоста в
`etc/kv-api/` и runtime-окружения в `versions/`, которые могут использоваться
другими приложениями.
