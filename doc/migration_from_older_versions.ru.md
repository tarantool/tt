# Переход с предыдущих версий

В этом файле описан переход существующих проектов на более новую версию
`tt`. Помимо рекомендаций по миграции, здесь перечислены несовместимые
изменения.

## Содержание {#contents}

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0 {#2x---300}

В `tt` 3.0 нет окружения tt: приложение стало проектом, бинарные файлы и
установленные приложения размещаются в [scope](concepts/projects-and-scopes.md),
а большинство команд перешло в группы. Разделы идут от изменений, с которыми
столкнётся почти каждый, к тем, которые нужны немногим.

### Файл tt.yaml больше не читается {#ttyaml-is-not-read}

`tt` 3.0 не читает `tt.yaml` - ни файл окружения, ни системный
`/etc/tarantool/tt.yaml`. Вместе с ним удалены:

- флаги `-S` (`--system`), `-L` (`--local`), `-c` (`--cfg`) и `-s`
  (`--self`) команды `tt`;
- переменные `TT_CLI_CFG` и `TT_SYSTEM_CONFIG_DIR`;
- флаг `-c` (`--config`) команд `tt backup start` и `tt backup finalize`,
  который задавал `tt.yaml` для разрешения цели `<APP>:<INSTANCE>`;
  приложение теперь находится через `--scope` и `--template`;
- команды `tt cfg` и `tt env`.

`tt` больше не запускает вместо себя `tt` из `bin_dir`:
`source .env/activate.sh` добавляет `tt` проекта в `PATH`. Если команда не
находит проект, но находит `tt.yaml`, `tt.yml` или `instances.yml` в текущем
каталоге или одном из родительских, она сообщает, что это окружение `tt` 2.x,
и указывает на эту страницу.

Вместо приложения теперь используется проект: каталог с `app.manifest.toml`
(его создаёт `tt new`) или с шаблонами инстансов
`.templates/<name>/instances.yaml`. `tt` ищет проект, поднимаясь от текущего
каталога вверх. Окружение tt с несколькими приложениями в `instances.enabled`
превращается в отдельный проект для каждого приложения:

```text
<environment>/
├── tt.yaml
├── instances.available/
│   ├── app1/
│   │   ├── init.lua
│   │   └── instances.yml
│   └── app2/
└── instances.enabled/
    ├── app1 -> ../instances.available/app1
    └── app2 -> ../instances.available/app2
```

Для каждого приложения, когда его инстансы остановлены средствами `tt` 2.x:

1. Используйте настоящий каталог приложения, а не символьную ссылку
   `instances.enabled`, как каталог проекта и запустите там `tt new`. С этого
   момента имя приложения берётся из `[package].name` манифеста, а не из
   имени каталога или ссылки. `tt new` использует имя каталога, только если
   оно допустимо для пакета: строчные латинские буквы, цифры и дефисы. Для
   `my_app` выполните `tt new -n my-app`; цели и каталоги данных будут
   называться `my-app`.
2. Переместите описание инстансов `instances.yml` в
   `.templates/default/instances.yaml`, а конфигурацию кластера `config.yaml`
   - в `.templates/default/config.yaml`. Имена записей сохраняются; запись
   без полей, созданная `tt` 2.x, читается без изменений. Инстанс Tarantool,
   указанный в записи, должен входить в топологию конфигурации. Приложению,
   которое самостоятельно вызывает `box.cfg`, топология не нужна, как и
   `config.yaml`: см. [Приложения box.cfg](#boxcfg-applications-are-script-records).
   Политика перезапуска задаётся полем `restart` записи, например
   `restart: on-failure`, а не `env.restart_on_failure`.
3. Переместите данные всех инстансов из `var/lib/<instance>` в
   `.tarantool/var/lib/<app>/<instance>` - все сразу. Пока существует
   `var/lib`, а `.tarantool/var/lib` нет, `tt instance start` отказывается
   запускать инстанс проекта, который должен хранить данные в
   `.tarantool/var/lib`, вместо запуска с пустой базой. После появления
   `.tarantool/var/lib` проверка отключается, и инстанс, чьи данные остались
   в `var/lib`, запустится пустым. Логи, pid-файлы и сокеты также переходят в
   `.tarantool/var/log` и `.tarantool/var/run`.
4. Установите бинарные файлы, с которыми работает проект, командой
   `tt binary install`. Версии хранятся в
   `.tarantool/versions/<product>/<product>_<version>/`, а активные версии
   связываются с `.tarantool/bin`, заголовочные файлы - с
   `.tarantool/include/tarantool`. Это заменяет `env.bin_dir` и `env.inc_dir`.
   Если бинарных файлов там нет, проект использует первый `tarantool` из
   `PATH`.
5. Удалите `tt.yaml`.

Получившийся проект:

```text
/srv/apps/app1/
├── app.manifest.toml
├── init.lua
├── .templates/
│   └── default/
│       ├── config.yaml
│       └── instances.yaml
└── .tarantool/
    ├── bin/
    └── var/
```

Остальные настройки `tt.yaml` заменяются так:

| `tt.yaml` | `tt` 3.0 |
|---|---|
| `env.instances_enabled` | нет: отдельный проект для каждого приложения или приложения, установленные в пользовательский либо системный scope |
| `env.bin_dir`, `env.inc_dir` | каталоги `bin/` и `include/` scope, куда устанавливает `tt binary --scope` |
| `env.restart_on_failure` | поле `restart` записи инстанса |
| `env.tarantoolctl_layout` | нет: раскладка tarantoolctl не поддерживается |
| `env.output_history_max` | аргумент `\history` в консоли `tt connect`, например `\history 15`; без аргумента - 10 |
| `app.run_dir`, `log_dir`, `wal_dir`, `memtx_dir`, `vinyl_dir` | `<root>/var/{run,log,lib}/<app>/<instance>/` в корневом каталоге scope; конфигурация кластера может задавать другие пути |
| `modules.directory` | `TT_CLI_MODULES_PATH`; см. [Внешние модули](#external-modules-are-found-by-tt_cli_modules_path) |
| `repo.rocks` | rock-серверы `tt registry`, см. ниже |
| `repo.distfiles` | `--local-repo DIR` и `--local-dir DIR` команд `tt binary install` и `tt binary search` |
| `ee.credential_path` | `--credentials-file` команды `tt binary` |
| `templates` | путь к собственному шаблону, который передаётся в `tt create` |

`repo.rocks` добавлял rock-сервер, который LuaRocks опрашивал до своих
стандартных серверов. Вместо этого `TT_REGISTRIES` или
`[platform].registries` манифеста заменяют стандартные серверы и для
`tt package`, и для `tt rocks`; `TT_CLI_REPO_ROCKS` по-прежнему добавляет
сервер, который `tt rocks` опрашивает первым. См. [Rock-серверы](concepts/rock-servers.md).

### Команды инстансов перенесены в tt instance {#the-instance-commands-are-under-tt-instance}

Команды для работы с инстансами перенесены в `tt instance`, прежние команды
верхнего уровня удалены:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt start`, `stop`, `restart`, `kill`, `status`, `logrotate`, `check`, `clean` | `tt instance start`, `stop`, ... |
| `tt log`, `tt log -f` | `tt instance logs`, `tt instance logs --follow` |
| `tt start -i` | `tt instance start --foreground` |
| `tt kill -f`, `tt clean -f` | `tt instance kill --yes`, `tt instance clean --yes` |
| `tt status -f json`, `tt status --pretty` | `tt instance status -o json`, `tt instance status -o pretty-table` |
| `tt connect` | `tt connect` или `tt instance connect`, одна и та же команда |

В этих командах `-f` больше не является сокращением подтверждения,
слежения или формата. Подтверждение запрашивается только в терминале:
скрипту, который передавал `y` в `tt stop` или запускал
`tt --no-prompt stop`, теперь нужно передать `--yes`, иначе команда завершится
с кодом 1. `tt instance start --foreground` запускает каждый инстанс под
собственным watchdog, как и в фоновом режиме, записывает их вывод также в
`tt.log` и не перезапускает инстансы. Команда завершается с кодом 0, если
инстансы остановились вовремя, с 137 - если инстанс не остановился за
`stop_timeout` и был убит, а в остальных случаях - с кодом первого инстанса,
который сам завершился с другим кодом. Команда для нескольких инстансов
продолжает работу после ошибки отдельного инстанса и завершается с кодом 3,
если часть инстансов завершилась с ошибкой.

### Цель команды задаёт инстанс {#a-target-names-an-instance}

Команда для инстансов принимает `<app>:<instance>`, `<app>:*`, список таких
целей через запятую или не принимает цель - тогда она действует на все
инстансы текущего проекта. Просто `<app>`, обозначавшее в `tt` 2.x все
инстансы приложения, теперь отклоняется, чтобы случайно не остановить их
все:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt start app` | `tt instance start app:*` или `tt instance start` в проекте |
| `tt start app:storage-001` | `tt instance start app:storage-001` |
| `tt start`: все приложения из `instances.enabled` | нет: `tt instance start` в каждом проекте или `tt instance start app1:*,app2:*` для приложений, установленных в системный scope |
| `tt connect app` | `tt connect app:<instance>` |

`<app>` - это `[package].name` из манифеста проекта или имя каталога
проекта, если манифеста нет. За пределами проекта с таким именем `<app>`
обозначает приложение, установленное в системный scope; параметр
`--scope project|user|system` задаёт scope напрямую. См. [Цели](concepts/instances.md#targets).

### Удалённая поддержка {#removed-support}

- Tarantool 1.10 и 2.x. `tt binary` не показывает и не устанавливает
  Tarantool, Enterprise или Enterprise SDK ниже версии 3; Enterprise теперь
  устанавливается под именем своей версии, например `3.8.1`, а не под
  именами `gc64-` и `nogc64-` из 2.x. Ранее установленная Enterprise 2.x
  показывается, переключается и удаляется под прежним именем.
- Раскладка tarantoolctl и приложения tarantoolctl.
- Cartridge: `tt cartridge`, `tt replicaset --cartridge`, шаблон `cartridge`
  команды `tt create` и `tt pack --cartridge-compat`.

### Бинарными файлами управляет tt binary {#binaries-are-managed-by-tt-binary}

Команды установки бинарных файлов перенесены в `tt binary`:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt install tarantool 3.2.0` | `tt binary install tarantool 3.2.0` |
| `tt install tarantool-ee gc64-3.2.0-0-r40` | `tt binary install tarantool gc64-3.2.0-0-r40` или `3.2.0ee` для последней сборки 3.2.0 |
| `tt install tarantool-dev ~/src/tarantool/build` | `tt binary install tarantool --from-build ~/src/tarantool/build` |
| `tt uninstall tarantool 3.2.0` | `tt binary uninstall tarantool 3.2.0` |
| `tt search tarantool` | `tt binary search tarantool` |
| `tt binaries list` | `tt binary list` |
| `tt binaries switch tarantool 3.2.0` | `tt binary switch tarantool 3.2.0` |
| `tt download <version>` | `tt binary install tarantool-sdk <version>`, устанавливает SDK, а не скачивает его |
| `tt install --local-repo tarantool` | `tt binary install tarantool --local-repo DIR`: исходники Git в `DIR/tarantool` |
| `tt install --local-repo tarantool-ee` | `tt binary install tarantool 3ee --local-dir DIR`: архивы SDK в `DIR` |

Раньше `--local-repo` был переключателем, который читал `repo.distfiles` из
`tt.yaml`; теперь он, как и `--local-dir`, принимает каталог относительно
текущего каталога. Каждая команда `tt binary` работает с одним scope:
`--scope project` (по умолчанию), `user` или `system`, и не переключается на
другой. См. [Управление бинарными файлами](how-to/binaries.md).

### tt pack, tt build и tt init заменены на tt package {#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package}

Приложение собирается и упаковывается как [пакет](concepts/packages.md),
описанный в `app.manifest.toml`. Для любой команды `tt package` нужен
манифест; его создаёт `tt new`.

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt init` | `tt new`; преобразовать приложение Cartridge или tarantoolctl нечем |
| `tt build` | `tt package build` |
| `tt pack tgz`, `deb`, `rpm` | `tt package pack`: архив `.tt`, который устанавливает `tt package install` |
| `tt pack --with-integrity-check KEY` | `tt package pack --with-integrity-check KEY` в дистрибутиве со значением `integrity = "rsa"`; см. [Подписание пакетов](how-to/sign-packages.md) |
| `tt pack --unit-params-file` | `tt service systemd install`; см. [Юниты systemd](#systemd-units-come-from-tt-service-systemd) |
| `tt pack --use-docker`, `--cartridge-compat` | нет |

Архив содержит `.rocks/`, `.templates/`, среды выполнения и файлы,
перечисленные в `[package].include`. `init.lua` в корне проекта не попадёт в
архив, если его нет в этом списке: конфигурация кластера, которая загружает
его через `app.file`, должна либо включить файл в список, либо загрузить код
компонента через `app.module`.

`tt run` запускает Tarantool для пакета в текущем каталоге, где должен быть
`app.manifest.toml`; раньше команда читала `tt.yaml`. Аргументы, включая
`--`, передаются Tarantool без изменений, как и раньше: `tt run script.lua a
-- -b` передаёт скрипту `a -- -b`, несмотря на то, что говорила справка
`tt` 2.x о `--`.

### Приложения box.cfg становятся записями script {#boxcfg-applications-are-script-records}

В `tt` 2.x приложение, самостоятельно вызывающее `box.cfg`, запускалось из
`init.lua`, `<instance>.init.lua` или единственного `<name>.lua` в
`instances.enabled`. `tt` 3.0 не распознаёт ни один из этих вариантов:
скрипт запускается, если в записи `instances.yaml` задано поле `script`.

```yaml
legacy-001:
  script: init.lua          # relative to the application directory
```

Такие записи не входят в топологию конфигурации кластера, и приложению,
состоящему только из них, не нужен `config.yaml`. Переменная `TARANTOOL_CFG`,
указывавшая на `instances.yml`, больше не задаётся; нужные скрипту переменные
задаёт `env` записи. Для параметров, не переданных самим скриптом,
`box.cfg` берёт каталог данных и бинарный порт раскладки из `TT_WAL_DIR`,
`TT_MEMTX_DIR`, `TT_VINYL_DIR` и `TT_LISTEN`. См. [Запуск приложения box.cfg](how-to/script-instances.md).

### Системное окружение стало системным scope {#the-system-environment-is-the-system-scope}

`tt -S` запускал окружение из `/etc/tarantool/tt.yaml`: приложения из
`/etc/tarantool/instances.enabled` с данными, логами и pid-файлами в
каталогах `sys_env` в `/var/lib/tarantool`, `/var/log/tarantool` и
`/var/run/tarantool`. В `tt` 3.0 вместо этого есть системный scope,
`/opt/tarantool`, если не задан `TT_SYSTEM_ROOT`; в нём хранятся приложения,
установленные из [пакетов](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package):

```console
# tt package install --scope system myapp-1.0.0-linux-amd64.tt
# tt instance start myapp:*
```

Установленное приложение находится в `/opt/tarantool/apps/<app>` - это
ссылка на активную версию. Файлы инстансов хранятся в
`/opt/tarantool/var/{lib,log,run}/<app>/<instance>/`, а пара конфигураций
этого хоста - `config.yaml` и `instances.yaml` - в `/opt/tarantool/etc/<app>/`.
До первого запуска переместите данные каждого инстанса из
`/var/lib/tarantool/sys_env/<app>/<instance>` в
`/opt/tarantool/var/lib/<app>/<instance>`: за пределами проекта `tt` не ищет
данные в прежнем каталоге.

Пакеты deb и rpm `tt` 3.0 не устанавливают `/etc/tarantool/tt.yaml`,
каталоги `sys_env` и `/etc/logrotate.d/tt.conf`. При обновлении deb-пакета
старые файлы остаются до его полной очистки, а старое правило logrotate
запускает `tt -S logrotate`, что теперь завершается ошибкой; это правило
нужно удалить. Собственное правило может переоткрывать логи командой
`tt instance logrotate <app>:*`; пример приведён в разделе [Логи](how-to/systemd.md#logs).

### Юниты systemd создаёт tt service systemd {#systemd-units-come-from-tt-service-systemd}

Юниты, которые `tt pack` в `tt` 2.x помещал в пакеты, запускали
`tt -L <environment> start` как forking-сервисы и не работают с `tt` 3.0.
Для приложения, установленного в системный scope, команда
`tt service systemd install <app>` создаёт шаблонный юнит `<app>@.service`,
который запускает один инстанс через `tt instance start --foreground`, и
drop-in для каждого инстанса с полями `restart` и `stop_timeout` его записи.
После этого запуском и остановкой инстансов управляет `systemctl`, а команды
`tt instance start`, `stop`, `restart` и `kill` отказываются их запускать.

В `tt` 2.x приложение с несколькими инстансами тоже использовало имя юнита
`<app>@.service`, расположенного в `/usr/lib/systemd/system`; в `tt` 3.0
юнит размещается в `/etc/systemd/system` и скрывает старый. Сначала остановите
и отключите старые юниты, затем удалите установивший их пакет. После этого
соберите приложение, установите его в системный scope и установите юниты;
команда не работает с проектом и пользовательским scope.

Параметры `--unit-params-file` соответствуют следующим настройкам:

| `systemd-unit-params.yml` | `tt` 3.0 |
|---|---|
| `FdLimit` | `LimitNOFILE=65535` в юните; другое значение задаётся в собственном drop-in `<app>@.service.d/override.conf` |
| `instance-env` | `env` записи инстанса или записи `"*"` |
| `TT`, `ConfigPath`, `ExecArgs` | нет: юнит запускает `tt` из scope для установленного приложения |

См. [Запуск инстансов под systemd](how-to/systemd.md).

### tt cluster publish запрашивает подтверждение {#tt-cluster-publish-asks-for-a-confirmation}

`tt cluster publish` запрашивает подтверждение перед записью конфигурации,
но только в терминале: скрипту публикации нужно передать `--yes` (`-y`),
иначе команда завершится с кодом 1 и ничего не запишет. `--force` пропускает
проверку, но не подтверждение.

`tt cluster show --validate` и `tt cluster publish` проверяют конфигурацию по
JSON Schema конфигурации кластера Tarantool. Проверка строже, чем в
`tt` 2.x: неизвестные ключи верхнего уровня и scope отклоняются, а ошибки
выводятся в формате `<path> [code] <message>`.

### Удалённые команды {#removed-commands}

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt env`, `. <(tt env)` | `source .env/activate.sh`; этот файл создаёт первая команда `tt binary install` в проекте |
| `tt cfg dump` | нет; rock-серверы показывает `tt registry list` |
| `tt enable` | нет: проект определяется по текущему каталогу, приложение устанавливает `tt package install` |
| `tt instances` | `tt instance status` для инстансов, `tt package list --scope system` для установленных приложений |
| `tt cartridge` | нет |

### Команда tt create {#tt-create}

Для `tt create` файл `tt.yaml` не нужен. Команда создаёт приложение в
текущем каталоге или в каталоге `--dst` и не создаёт ссылку в
`instances.enabled`. Собственный шаблон задаётся путём к каталогу или архиву
`.tgz`: `tt create ./templates/basic`. Переменная шаблона `rundir` удалена.
Встроенные шаблоны размещают `instances.yaml` и `config.yaml` в
`.templates/default/`; шаблон `single_instance` использует имена с `.yaml`,
а не `.yml`, а шаблон `cartridge` удалён. Шаблон, создающий
`app.manifest.toml`, отклоняет имя, недопустимое для пакета: `vshard_cluster`
преобразует `--name my_app` в `my-app` и отклоняет `My_App`.

### Коды завершения {#exit-codes}

В `tt` 2.x любая ошибка, кроме ошибок `tt backup` и `tt restore`, завершала
команду с кодом 1. В `tt` 3.0 используются коды:

- 1 - запрос нельзя выполнить в заданном виде: ошибка использования, нет
  файла или проекта, либо требуется подтверждение без терминала;
- 2 - произошёл сбой системы, на которой выполнялась команда: сервер
  недоступен или не ответил вовремя, имя хоста не разрешается, файловая
  система отклонила операцию из-за недостатка прав или места. `tt connect` к
  порту, на котором никто не слушает, завершается с кодом 2, а не 1;
- 3 - команда для нескольких целей выполнилась успешно лишь для некоторых.

Скрипту, который проверяет код 1, теперь нужно проверять любой код, отличный
от 0. `tt backup` и `tt restore` сохраняют собственные коды, начиная с 3.
Коды описаны в `tt help errors`.

### tt backup-id стало tt backup id {#tt-backup-id-is-tt-backup-id}

Команда, которая печатает идентификатор новой резервной копии, теперь входит
в группу `tt backup`: вместо `id=$(tt backup-id)` скрипт резервного
копирования запускает `id=$(tt backup id)`. Идентификатор остаётся прежним.

### Внешние модули ищутся через TT_CLI_MODULES_PATH {#external-modules-are-found-by-tt_cli_modules_path}

Внешние модули ищутся только в каталогах, перечисленных в
`TT_CLI_MODULES_PATH` через двоеточие. Каталог `modules.directory` из
`tt.yaml` и стандартный каталог `modules` рядом с ним не проверяются:
сохранённый там модуль не найдётся, а заменённая им команда снова будет
выполняться как собственная команда `tt`. Относительный путь в
`TT_CLI_MODULES_PATH` отсчитывается от рабочего каталога.

Модуль с именем группы команд, например `cluster`, перехватывает всю группу:
`tt cluster publish ...` запускает модуль с аргументами `publish ...`.
Заменить команды `tt modules`, `tt help`, `tt completion` и `tt version`
нельзя. Флаг `-I` запускает собственную команду `tt`. См. [Внешние модули](how-to/external-modules.md).

### tt daemon запускает команды tt 3.0 {#tt-daemon-runs-the-commands-of-tt-30}

Запрос к `tt daemon` теперь называет команду из `tt` 3.0: запрос
`tt start test_app`, ранее имевший вид `{"command_name":"start", "params":["test_app"]}`,
теперь имеет вид `{"command_name":"instance", "params":["start", "test_app:*"]}`.
Глобальные флаги `tt` 2.x, например `-L`, удалены; для команды, требующей
подтверждения, нужно передать `--yes`, поскольку daemon запускает её без
терминала. См. [Работа с tt daemon](https://github.com/tarantool/tt/blob/v3/doc/examples.md#working-with-tt-daemon-experimental).

### Сборка tt и импорт его пакетов {#building-tt-and-importing-its-packages}

`TT_CLI_BUILD_SSL` принимает `default`, `openssl` и `openssl_static`; значения
`no`, `shared` и `static` отклоняются:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| не задана или `no`: без TLS | не задана или `default`: TLS на чистом Go, без cgo |
| `shared` | `openssl` |
| `static` | `openssl_static` |

Для Go-кода, импортирующего `tt`:

- путь модуля - `github.com/tarantool/tt/v3`;
- `lib/cluster`, `lib/connect`, `lib/dial` и `lib/integrity` перенесены в
  `github.com/tarantool/tt/sdk/cluster`, `sdk/connect`, `sdk/dial` и
  `sdk/integrity`; каждый из них стал отдельным модулем с alpha API;
- `InjectedCmds` из `cli/cmd` удалён: дистрибутив добавляет свои команды как
  модули tt и выбирает поставщик integrity через `core.WithIntegrity`.
  См. [Дистрибутивы и модули](concepts/distributions.md).

### tt cat и tt play стали tt data cat и tt data play {#tt-cat-and-tt-play-are-tt-data-cat-and-tt-data-play}

`tt cat` и `tt play` удалены без псевдонимов: вместо них используются
`tt data cat` и `tt data play`. Они читают файлы на Go и не требуют локального
Tarantool. Имена флагов сохранились, но поведение изменилось:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `--format yaml\|json\|lua` | `-o`/`--format`; `json` - JSON Lines, по одному объекту в строке |
| `--to N` не включает N | `--to N` включает N, как и `--from N` |
| `--from`/`--to` сравнивают один LSN со всеми репликами | они ограничивают LSN одной реплики: задаётся `--replica` или берётся единственная реплика из файлов; для нескольких используются `--from-vclock`/`--to-vclock` |
| граница может разрезать транзакцию | транзакция внутри границы берётся целиком |
| все `.snap` и `.xlog` каталога, сначала снимки | самый новый снимок в заданных границах, затем логи по vclock, без данных, уже содержащихся в снимке |
| `--show-system` фильтрует только изменения данных | также скрывает строки RAFT, синхронной репликации и NOP |
| `play`: один запрос на строку, без транзакций | один поток IPROTO, запросы `--batch` выполняются одновременно, доступны `--txn-mode`, `--on-error`, `--dry-run` |

Если задан `--from` или `--from-vclock`, снимок не читается; номера строк
снимка не являются LSN, поэтому границы применяются только к журналам
предварительной записи. Вывод `cat` сохраняет `HEADER` и `BODY` каждой
строки; тела строк RAFT и синхронной репликации теперь декодированы. Снимок
не содержит данных vinyl-пространств: они находятся в файлах `.run`, которые
читает `tt data cat`, если они названы явно или найдены с `-r`; команда
`tt data play` отказывается с ними работать. `tt data play` завершается с
кодом 3, если цель отклонила часть транзакций, и с кодом 2 при сбое
соединения. `tt coredump` не перемещена.

## 1.3.1 -> 2.0.0 {#131---200}

### Новый формат файла конфигурации tt {#new-format-of-tt-config-file}

Формат файла конфигурации `tt` 2.0.0 несовместим с предыдущей версией:

- корневой раздел `tt` удалён;
- общие настройки окружения перенесены в раздел `env`. Все относительные
  пути в нём отсчитываются от каталога файла конфигурации;
- относительные пути в разделе `app` отсчитываются от каталога приложения.

### Новая раскладка артефактов времени выполнения {#new-runtime-artifacts-layout}

Раскладка артефактов времени выполнения изменилась:

- относительные пути отсчитываются от каталога приложения. Для инстанса,
  заданного одним скриптом, каталог создаётся в каталоге `instances.enabled`;
- поскольку относительные пути уже содержат имя приложения, к результирующему
  каталогу добавляется только имя инстанса. Ниже показана стандартная
  раскладка локального окружения в версии 2.0.0:

```text
instances.enabled/app/
├── init.lua
├── instances.yml
└── var
    ├── lib
    │   ├── inst1
    │   └── inst2
    ├── log
    │   ├── inst1
    │   └── inst2
    └── run
        ├── inst1
        └── inst2
```

Перенос артефактов из версий 1.*:

- создайте каталоги артефактов в каталоге приложения: var/lib, var/log,
  var/run;
- скопируйте подкаталоги инстансов из окружения версии 1.* в каталог
  приложения. Пример копирования данных:
  `cp -r <env_dir>/var/lib/app/* <instances_enabled>/app/var/lib/`

Абсолютные пути не затрагиваются этими изменениями: к ним всегда добавляется
имя приложения.

### Изменился рабочий каталог {#working-directory-is-changed}

Рабочим каталогом процесса инстанса стал каталог приложения. Раньше это был
текущий каталог `tt`. Поэтому файлы, с которыми код инстанса работает по
относительным путям, нужно переместить или скопировать в каталог приложения.
