# Запуск экземпляров под systemd

`tt service systemd` создаёт unit-файлы systemd, которые запускают экземпляры
приложения, установленного в область установки системы, `<root>/apps/<app>`, по
его паре файлов: паре файлов хоста в `<root>/etc/<app>/` или, если её нет,
шаблону по умолчанию установленной версии. Unit-файлы создаются только для
области установки системы; область установки пользователя и проекты отвергаются
как неподдерживаемые. `<app>` задаёт имя приложения так же, как команды
управления экземплярами: внутри проекта с тем же именем это проект, а
`--scope system` выбирает установленное приложение.

## Порядок действий {#steps}

Команды выполняются от пользователя root. На хосте нужны tt 3 и архив
приложения.

```console
# tt package install --scope system kv-api-1.0.0-linux-amd64.tt
# tt binary switch tt 3.0.0 --scope system     # tt из архива приложения
# mkdir -p /opt/tarantool/etc/kv-api           # пара файлов этого хоста
# $EDITOR /opt/tarantool/etc/kv-api/config.yaml
# $EDITOR /opt/tarantool/etc/kv-api/instances.yaml
# install -d -o tarantool -g tarantool /opt/tarantool/var
# tt service systemd install kv-api
# systemctl daemon-reload
# systemctl enable --now kv-api@kv-a.service kv-api@kv-b.service
```

1. Установите приложение в область установки системы, `/opt/tarantool` (см.
   [Пакеты](../concepts/packages.md#installing)). Его среда выполнения
   попадает в `/opt/tarantool/versions/`.
2. Настройте для области установки собственный tt, `/opt/tarantool/bin/tt`,
   который запускают unit-файлы: переключитесь на tt из архива или выполните
   `tt binary install tt --scope system`.
3. При необходимости создайте пару файлов хоста; без неё unit-файлы запускают
   шаблон по умолчанию установленной версии.
4. Откройте каталогу `var/` области установки системы права на запись
   пользователю, от которого работают unit-файлы (по умолчанию `tarantool`).
5. Установите unit-файлы и запустите их с помощью `systemctl`.

`tt instance status` показывает unit-файл каждого экземпляра:

```console
# tt instance status kv-api:*
 INSTANCE     STATUS   UNIT                 PID  MODE  CONFIG  BOX      UPSTREAM
 kv-api:kv-a  RUNNING  kv-api@kv-a.service  200  RW    ready   running  --
 kv-api:kv-b  RUNNING  kv-api@kv-b.service  207  RO    ready   running  --
# tt instance stop kv-api:kv-a --yes
   ⨯ kv-api:kv-a is managed by systemd through the unit kv-api@kv-a.service: use systemctl stop kv-api@kv-a.service
```

<!-- Verified on 2026-10-27 (1482dea6) in a container with systemd; re-check
before release. -->

## Команды {#the-commands}

```text
tt service systemd generate myapp                    # print the units
tt service systemd generate myapp --unit-dir ./units # write them into ./units
tt service systemd install myapp                     # into /etc/systemd/system
tt service systemd install myapp --run-as tt:tarantool
tt service systemd uninstall myapp
```

## Unit-файлы {#the-units}

Шаблонный unit-файл `myapp@.service` запускает один экземпляр `myapp`:

```ini
[Unit]
Description=Tarantool application myapp, instance %i
After=network.target

[Service]
Type=simple
User=tarantool
Group=tarantool
Environment=TT_SYSTEM_ROOT=/opt/tarantool
Environment=TT_SYSTEMD_UNIT=%n
ExecStart=/opt/tarantool/bin/tt instance start --foreground --scope system myapp:%i
KillMode=mixed
KillSignal=SIGTERM
SendSIGKILL=yes
Restart=no
RestartSec=5s
TimeoutStopSec=35s
LimitNOFILE=65535
LimitCORE=infinity

[Install]
WantedBy=multi-user.target
```

а каждый экземпляр получает drop-in, `myapp@<instance>.service.d/tt.conf`,
содержащий индивидуальные настройки экземпляра:

```ini
[Service]
Restart=on-failure
TimeoutStopSec=65s
```

- `tt instance start --foreground` не перезапускает экземпляр сам: он
  завершается с кодом возврата экземпляра, а systemd перезапускает его в
  соответствии с директивой `Restart=`, куда подставляется поле `restart`
  записи: `no`, `on-failure` и `always` - значения systemd.
- `tt` останавливает экземпляр самостоятельно: он отправляет экземпляру сигнал
  останова и принудительно завершает его по истечении `stop_timeout`. Поэтому
  сигнал останова от systemd направляется только самому `tt` (`KillMode=mixed`),
  а `TimeoutStopSec=` равен значению `stop_timeout` из записи (по умолчанию 30
  секунд) плюс 5 секунд на то, чтобы `tt` принудительно завершил экземпляр и
  вышел: systemd никого не убивает раньше `tt`. При своевременном останове
  unit-файл завершается с кодом 0, при принудительном завершении - с кодом 137.
- Экземпляры Tarantool, экземпляры со `script` и внешние процессы с `binary`
  работают под одним и тем же шаблоном.
- `TT_SYSTEM_ROOT` указывает на корень области установки системы, в котором
  созданы unit-файлы, чтобы `tt` под systemd находил там приложение.
  `ExecStart=` запускает `<root>/bin/tt`, собственный `tt` области установки
  системы, который ставится командой `tt binary install tt --scope system`; если
  его нет, unit-файлы запускают тот `tt`, который их создал, с предупреждением.
- Экземпляры запускаются от имени пользователя и группы, указанных в
  `--run-as <user>[:<group>]` (по умолчанию `tarantool:tarantool` -
  пользователь, создаваемый deb- и rpm-пакетами `tt`); если группа не задана,
  берётся группа с тем же именем, что и пользователь; числовые идентификаторы
  также поддерживаются. У них должен быть доступ на запись в `<root>/var`.
  Команда `install` сохраняет это значение вместе с unit-файлами, а повторный
  `install` поверх существующих unit-файлов без этого флага сохраняет прежнее
  значение; если на хосте нет такого пользователя или группы, выводится
  предупреждение. Собственные настройки добавляются в собственный drop-in,
  `myapp@.service.d/override.conf`; директивы `Restart=` и `TimeoutStopSec=`
  формируются на основе `instances.yaml`.
- Имена приложений и экземпляров, требующие экранирования в имени unit-файла,
  пользователи и группы, требующие экранирования в `User=` и `Group=`, а также
  корни и пути к `tt`, требующие кавычек в unit-файле, отвергаются.

## Установка и обновление unit-файлов {#install-and-update-the-units}

`install` записывает unit-файлы в `/etc/systemd/system` или в каталог, заданный
флагом `--unit-dir`, и фиксирует в `<root>/etc/<app>/systemd.yaml`, какими
экземплярами управляют unit-файлы. Команда не вызывает `systemctl`, а выводит
инструкции по запуску:

```text
systemctl daemon-reload
systemctl enable --now myapp@router-1.service myapp@storage-1.service
```

После изменения состава экземпляров приложения команду запускают снова: она
перезаписывает unit-файлы и удаляет drop-in'ы исчезнувших экземпляров. Файл
unit-файла с таким же именем, созданный не ею, а также экземпляр, уже работающий
без unit-файла, отвергаются: работающий экземпляр сначала останавливают с
помощью `tt instance stop`.

## Команды управления экземплярами {#the-instance-commands}

Команды `tt instance start`, `stop`, `restart` и `kill`, а также
`tt replicaset rebootstrap` отказываются работать с экземплярами, которыми
управляют unit-файлы, и выводят команду `systemctl`, которую следует
использовать взамен: процесс-наблюдатель `tt` рядом с unit-файлом контролировал
бы экземпляр дважды, а останов в обход unit-файла нарушил бы состояние systemd.
О запуске, выполняемом самим unit-файлом, сообщает переменная окружения
`TT_SYSTEMD_UNIT` с именем unit-файла. `tt instance status` читает те же
pid-файлы и показывает unit-файл в колонке `UNIT` и в поле `unit` при выводе
`-o json` и `-o yaml`; команды `logs`, `logrotate`, `check`, `clean` и `connect`
работают как обычно для любого экземпляра.

## Обновление приложения {#upgrade}

Установите новую версию, затем перезапустите unit-файлы по очереди, начиная с
реплик:

```console
# tt package install --scope system --upgrade kv-api-1.1.0-linux-amd64.tt
# systemctl restart kv-api@kv-b.service
# systemctl restart kv-api@kv-a.service
```

Если в `instances.yaml` изменились экземпляры, директива `restart` или
`stop_timeout`, выполните `tt service systemd install kv-api` и
`systemctl daemon-reload` снова.

## Журналы {#logs}

Вывод экземпляра поступает в его файл `tt.log` и через
`tt instance start --foreground` попадает в системный журнал:

```console
# journalctl -u kv-api@kv-a.service -f
# tt instance logs kv-api:kv-a -n 100
```

tt не устанавливает конфигурацию для logrotate. Собственное правило повторно
открывает `tt.log` с помощью `tt instance logrotate`:

```text
/opt/tarantool/var/log/kv-api/*/tt.log {
    daily
    rotate 14
    missingok
    notifempty
    compress
    delaycompress
    sharedscripts
    postrotate
        TT_SYSTEM_ROOT=/opt/tarantool /opt/tarantool/bin/tt instance logrotate kv-api:* >/dev/null
    endscript
}
```

<!-- TODO(writers): the logrotate rule was not run through logrotate itself. -->

## Удаление {#uninstall}

`uninstall` удаляет unit-файлы и файл записи из каталога, куда они были
установлены; команде требуется только файл записи, поэтому она работает и для
уже удалённого приложения. Без файла записи удалять нечего: команда сообщает,
что unit-файлы не установлены, и завершается с кодом 0, чтобы её можно было
вызывать повторно; нечитаемый файл записи считается ошибкой. Если экземпляр
запущен под своим unit-файлом, команда завершается с отказом: сначала unit-файлы
останавливают с помощью `systemctl disable --now`. Чужие файлы не удаляются.
`generate` только создаёт файлы unit-файлов и ничего не записывает в учётный
файл: команды управления экземплярами не отказываются работать с экземплярами
unit-файлов, установленных вручную.
