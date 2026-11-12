# Переход с предыдущих версий

На этой странице описано, как перевести существующие проекты на новую версию `tt`, и перечислены
несовместимые изменения.

## Содержание {#contents}

- [2.x -> 3.0.0](#2x---300)
- [1.3.1 -> 2.0.0](#131---200)

## 2.x -> 3.0.0 {#2x---300}

Главные отличия `tt` 3.0 от `tt` 2.x:

- **Нет окружения tt и файла `tt.yaml`.** Приложение стало [проектом](concepts/projects-and-scopes.md),
  а бинарные файлы и установленные приложения хранятся в [областях установки (scope)](concepts/projects-and-scopes.md) – проекта,
  пользователя или системы.
- **Команды объединены в группы:** `tt instance`, `tt binary`, `tt package`, `tt data` и другие.
- **Поддерживается только Tarantool 3.** Cartridge и раскладка tarantoolctl больше не поддерживаются.

Краткий план перехода:

1. [Преобразуйте окружение tt в проекты](#ttyaml-is-not-read) – по одному на каждое приложение.
2. Замените в скриптах команды `tt` 2.x на новые: [работа с экземплярами](#the-instance-commands-are-under-tt-instance),
   [цели команд](#a-target-names-an-instance), [бинарные файлы](#binaries-are-managed-by-tt-binary),
   [сборка и упаковка](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package).
3. Если приложения работают под systemd, [создайте юниты заново](#systemd-units-come-from-tt-service-systemd).
4. Проверьте, как скрипты обрабатывают [коды завершения](#exit-codes) и [подтверждения](#tt-cluster-publish-asks-for-a-confirmation).

Сначала описаны изменения, которые касаются большинства пользователей, в конце — те, что нужны в редких случаях.

### Файл `tt.yaml` больше не читается {#ttyaml-is-not-read}

`tt` 3.0 не читает `tt.yaml` – ни файл окружения, ни системный `/etc/tarantool/tt.yaml`. Вместе с ним удалены:

- флаги `-S` (`--system`), `-L` (`--local`), `-c` (`--cfg`) и `-s` (`--self`) команды `tt`;
- переменные окружения `TT_CLI_CFG` и `TT_SYSTEM_CONFIG_DIR`;
- флаг `-c` (`--config`) команд `tt backup start` и `tt backup finalize`. Он задавал `tt.yaml`, по которому
  находилась цель `<APP>:<INSTANCE>`. Теперь приложение находится по флагам `--scope` и `--template`;
- команды `tt cfg` и `tt env`.

`tt` больше не передаёт управление `tt` из каталога `bin_dir`. Чтобы использовать `tt` проекта, выполните
`source .env/activate.sh` – скрипт добавит его в `PATH`.

Если команда не находит проект, но находит `tt.yaml`, `tt.yml` или `instances.yml` в текущем или родительском
каталоге, она сообщает, что это окружение `tt` 2.x, и ссылается на эту страницу.

#### Как преобразовать окружение в проекты

Вместо приложения в окружении теперь используется проект – каталог, в котором есть `app.manifest.toml`
(его создаёт `tt new`) или шаблоны экземпляров `.templates/<name>/instances.yaml`. `tt` ищет проект,
поднимаясь от текущего каталога вверх.

Окружение tt с несколькими приложениями в `instances.enabled` превращается в несколько проектов – по одному
на каждое приложение. Пример окружения `tt` 2.x:

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

Остановите экземпляры приложения средствами `tt` 2.x. Затем для каждого приложения:

1. Перейдите в настоящий каталог приложения (не в символьную ссылку из `instances.enabled`) и выполните `tt new`.
   Этот каталог станет каталогом проекта.

   Теперь имя приложения берётся из `[package].name` манифеста, а не из имени каталога или ссылки.
   `tt new` использует имя каталога, только если оно допустимо для пакета: строчные латинские буквы, цифры
   и дефисы. Например, для каталога `my_app` выполните `tt new -n my-app` – цели и каталоги данных будут
   называться `my-app`.

2. Перенесите описание экземпляров и конфигурацию кластера:

   - `instances.yml` → `.templates/default/instances.yaml`;
   - `config.yaml` → `.templates/default/config.yaml`.

   Имена записей сохраняются. Запись без полей, созданная `tt` 2.x, читается без изменений.
   Экземпляр Tarantool из записи должен входить в топологию конфигурации кластера.
   Приложению, которое само вызывает `box.cfg`, топология и `config.yaml` не нужны – см.
   [Приложения на box.cfg](#boxcfg-applications-are-script-records).

   Политика перезапуска теперь задаётся полем `restart` записи (например, `restart: on-failure`),
   а не параметром `env.restart_on_failure`.

3. Перенесите данные **всех** экземпляров сразу: из `var/lib/<instance>` в `.tarantool/var/lib/<app>/<instance>`.
   Журналы, pid-файлы и сокеты теперь хранятся в `.tarantool/var/log` и `.tarantool/var/run`.

   > **Внимание.** Пока `var/lib` существует, а `.tarantool/var/lib` нет, `tt instance start` не запускает экземпляр
   > проекта, данные которого должны быть в `.tarantool/var/lib`, – чтобы он не стартовал с пустой базой.
   > Как только появится `.tarantool/var/lib`, эта проверка отключается: экземпляр, данные которого остались
   > в `var/lib`, запустится с пустой базой.

4. Установите бинарные файлы проекта командой `tt binary install`. Это заменяет параметры `env.bin_dir`
   и `env.inc_dir`:

   - версии хранятся в `.tarantool/versions/<product>/<product>_<version>/`;
   - активные версии связываются с `.tarantool/bin`, заголовочные файлы – с `.tarantool/include/tarantool`.

   Если бинарных файлов в проекте нет, используется первый `tarantool` из `PATH`.

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

#### Чем заменить остальные параметры `tt.yaml`

| `tt.yaml`                                                     | `tt` 3.0                                                                                                                   |
|---------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|
| `env.instances_enabled`                                       | Нет. Вместо этого – отдельный проект для каждого приложения или приложения, установленные в область установки пользователя или системы |
| `env.bin_dir`, `env.inc_dir`                                  | Каталоги `bin/` и `include/` области установки, в которые устанавливает `tt binary --scope`                                            |
| `env.restart_on_failure`                                      | Поле `restart` записи экземпляра                                                                                           |
| `env.tarantoolctl_layout`                                     | Нет: раскладка tarantoolctl не поддерживается                                                                              |
| `env.output_history_max`                                      | Аргумент `\history` в консоли `tt connect`, например `\history 15`. Без аргумента – 10                                     |
| `app.run_dir`, `log_dir`, `wal_dir`, `memtx_dir`, `vinyl_dir` | `<root>/var/{run,log,lib}/<app>/<instance>/` в корневом каталоге области установки. Конфигурация кластера может задавать другие пути   |
| `modules.directory`                                           | `TT_CLI_MODULES_PATH`, см. [Внешние модули](#external-modules-are-found-by-tt_cli_modules_path)                            |
| `repo.rocks`                                                  | Rock-серверы `tt registry`, см. ниже                                                                                       |
| `repo.distfiles`                                              | Флаги `--local-repo DIR` и `--local-dir DIR` команд `tt binary install` и `tt binary search`                               |
| `ee.credential_path`                                          | Флаг `--credentials-file` команды `tt binary`                                                                              |
| `templates`                                                   | Путь к собственному шаблону, который передаётся в `tt create`                                                              |

Об изменении `repo.rocks`:

- В `tt` 2.x `repo.rocks` добавлял rock-сервер, который LuaRocks опрашивал раньше стандартных.
- В `tt` 3.0 `TT_REGISTRIES` или `[platform].registries` манифеста **заменяют** стандартные серверы – и для
  `tt package`, и для `tt rocks`.
- `TT_CLI_REPO_ROCKS` по-прежнему добавляет сервер, который `tt rocks` опрашивает первым.

Подробнее – в разделе [Rock-серверы](concepts/rock-servers.md).

### Команды для экземпляров перенесены в `tt instance` {#the-instance-commands-are-under-tt-instance}

Команды для работы с экземплярами перенесены в группу `tt instance`. Прежние команды верхнего уровня удалены:

| `tt` 2.x                                                                       | `tt` 3.0                                                           |
|--------------------------------------------------------------------------------|--------------------------------------------------------------------|
| `tt start`, `stop`, `restart`, `kill`, `status`, `logrotate`, `check`, `clean` | `tt instance start`, `stop`, ...                                   |
| `tt log`, `tt log -f`                                                          | `tt instance logs`, `tt instance logs --follow`                    |
| `tt start -i`                                                                  | `tt instance start --foreground`                                   |
| `tt kill -f`, `tt clean -f`                                                    | `tt instance kill --yes`, `tt instance clean --yes`                |
| `tt status -f json`, `tt status --pretty`                                      | `tt instance status -o json`, `tt instance status -o pretty-table` |
| `tt connect`                                                                   | `tt connect` или `tt instance connect` – это одна и та же команда  |

Что ещё изменилось:

- Флаг `-f` в этих командах больше не означает подтверждение, слежение или формат.
- Подтверждение запрашивается только в терминале. Если скрипт передавал `y` в `tt stop` или запускал
  `tt --no-prompt stop`, теперь передавайте флаг `--yes`. Без него команда завершится с кодом 1.
- `tt instance start --foreground` запускает каждый экземпляр под отдельным watchdog, как и в фоновом режиме,
  дублирует их вывод в `tt.log` и не перезапускает экземпляры. Коды завершения:
  - `0` – экземпляры остановились вовремя;
  - `137` – экземпляр не остановился за `stop_timeout` и был принудительно завершён;
  - в остальных случаях – код первого экземпляра, который сам завершился с другим кодом.
- Команда для нескольких экземпляров продолжает работу после ошибки одного из них и завершается с кодом 3,
  если часть экземпляров завершилась с ошибкой.

### Цель команды – это экземпляр {#a-target-names-an-instance}

Команды для экземпляров принимают цель в одном из видов:

- `<app>:<instance>` – один экземпляр;
- `<app>:*` – все экземпляры приложения;
- список таких целей через запятую;
- без цели – все экземпляры текущего проекта.

Просто `<app>`, которое в `tt` 2.x означало все экземпляры приложения, теперь отклоняется, чтобы случайно
не остановить их все:

| `tt` 2.x                                           | `tt` 3.0                                                                                                                                |
|----------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------|
| `tt start app`                                     | `tt instance start app:*` или `tt instance start` в проекте                                                                             |
| `tt start app:storage-001`                         | `tt instance start app:storage-001`                                                                                                     |
| `tt start` – все приложения из `instances.enabled` | Нет. Выполните `tt instance start` в каждом проекте или `tt instance start app1:*,app2:*` для приложений, установленных в область установки системы |
| `tt connect app`                                   | `tt connect app:<instance>`                                                                                                             |

`<app>` – это `[package].name` из манифеста проекта или имя каталога проекта, если манифеста нет.
Вне проекта с таким именем `<app>` означает приложение, установленное в область установки системы. Чтобы задать область установки явно,
используйте флаг `--scope project|user|system`. Подробнее – в разделе [Цели](concepts/instances.md#targets).

### Прекращённая поддержка {#removed-support}

- **Tarantool 1.10 и 2.x.** `tt binary` не показывает и не устанавливает Tarantool, Tarantool Enterprise
  и Enterprise SDK версий ниже 3.
  - Tarantool Enterprise теперь устанавливается под именем своей версии, например `3.8.1`, а не под именами
    `gc64-` и `nogc64-`, как в 2.x.
  - Ранее установленный Tarantool Enterprise 2.x показывается, переключается и удаляется под прежним именем.
- **Раскладка tarantoolctl и приложения tarantoolctl.**
- **Cartridge:** `tt cartridge`, `tt replicaset --cartridge`, шаблон `cartridge` команды `tt create`
  и `tt pack --cartridge-compat`.

### Бинарными файлами управляет `tt binary` {#binaries-are-managed-by-tt-binary}

Команды для установки бинарных файлов перенесены в группу `tt binary`:

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt install tarantool 3.2.0` | `tt binary install tarantool 3.2.0` |
| `tt install tarantool-ee gc64-3.2.0-0-r40` | `tt binary install tarantool gc64-3.2.0-0-r40` или `3.2.0ee` для последней сборки 3.2.0 |
| `tt install tarantool-dev ~/src/tarantool/build` | `tt binary install tarantool --from-build ~/src/tarantool/build` |
| `tt uninstall tarantool 3.2.0` | `tt binary uninstall tarantool 3.2.0` |
| `tt search tarantool` | `tt binary search tarantool` |
| `tt binaries list` | `tt binary list` |
| `tt binaries switch tarantool 3.2.0` | `tt binary switch tarantool 3.2.0` |
| `tt download <version>` | `tt binary install tarantool-sdk <version>` – устанавливает SDK, а не только скачивает его |
| `tt install --local-repo tarantool` | `tt binary install tarantool --local-repo DIR` – исходный код из Git в `DIR/tarantool` |
| `tt install --local-repo tarantool-ee` | `tt binary install tarantool 3ee --local-dir DIR` – архивы SDK в `DIR` |

- Раньше `--local-repo` был переключателем и брал путь из `repo.distfiles` в `tt.yaml`. Теперь он, как
  и `--local-dir`, принимает путь к каталогу относительно текущего каталога.
- Каждая команда `tt binary` работает с одной областью установки: `--scope project` (по умолчанию), `user` или `system`.
  На другую область установки команда не переключается.

Подробнее – в разделе [Управление бинарными файлами](how-to/binaries.md).

### `tt pack`, `tt build` и `tt init` заменены на `tt package` {#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package}

Приложение собирается и упаковывается как [пакет](concepts/packages.md), описанный в `app.manifest.toml`.
Всем командам `tt package` нужен манифест – его создаёт `tt new`.

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt init` | `tt new`. Инструмента для преобразования приложений Cartridge или tarantoolctl нет |
| `tt build` | `tt package build` |
| `tt pack tgz`, `deb`, `rpm` | `tt package pack` – создаёт архив `.tt`, который устанавливается командой `tt package install` |
| `tt pack --with-integrity-check KEY` | `tt package pack --with-integrity-check KEY` в дистрибутиве с `integrity = "rsa"`, см. [Подписание пакетов](how-to/sign-packages.md) |
| `tt pack --unit-params-file` | `tt service systemd install`, см. [Юниты systemd](#systemd-units-come-from-tt-service-systemd) |
| `tt pack --use-docker`, `--cartridge-compat` | Нет |

В архив входят `.rocks/`, `.templates/`, среды выполнения и файлы из списка `[package].include`.

> **Примечание.** `init.lua` из корня проекта попадает в архив, только если он указан в `[package].include`.
> Если конфигурация кластера загружает его через `app.file`, добавьте файл в этот список или загружайте код
> компонента через `app.module`.

`tt run` запускает Tarantool для пакета в текущем каталоге, поэтому в нём должен быть `app.manifest.toml`.
Раньше команда читала `tt.yaml`. Аргументы, включая `--`, передаются Tarantool без изменений, как и раньше:
`tt run script.lua a -- -b` передаёт скрипту `a -- -b`, хотя справка `tt` 2.x описывала `--` иначе.

### Приложения на `box.cfg` описываются записями `script` {#boxcfg-applications-are-script-records}

В `tt` 2.x приложение, которое само вызывает `box.cfg`, запускалось из `init.lua`, `<instance>.init.lua` или
единственного файла `<name>.lua` в `instances.enabled`. `tt` 3.0 эти варианты не распознаёт: скрипт
запускается, только если в записи `instances.yaml` задано поле `script`:

```yaml
legacy-001:
  script: init.lua          # путь относительно каталога приложения
```

- Такие записи не входят в топологию конфигурации кластера. Приложению, которое состоит только из них,
  `config.yaml` не нужен.
- Переменная `TARANTOOL_CFG`, которая указывала на `instances.yml`, больше не задаётся. Нужные скрипту
  переменные окружения задавайте в поле `env` записи.
- Каталоги данных и порт, которые скрипт не задал сам, `box.cfg` берёт из переменных `TT_WAL_DIR`,
  `TT_MEMTX_DIR`, `TT_VINYL_DIR` и `TT_LISTEN`.

Подробнее – в разделе [Запуск приложения на box.cfg](how-to/script-instances.md).

### Системное окружение заменено областью установки системы {#the-system-environment-is-the-system-scope}

В `tt` 2.x команда `tt -S` работала с окружением из `/etc/tarantool/tt.yaml`: приложения хранились
в `/etc/tarantool/instances.enabled`, а данные, журналы и pid-файлы – в каталогах `sys_env` внутри
`/var/lib/tarantool`, `/var/log/tarantool` и `/var/run/tarantool`.

В `tt` 3.0 вместо этого есть область установки системы – каталог `/opt/tarantool` (или каталог из `TT_SYSTEM_ROOT`).
В нём хранятся приложения, установленные из [пакетов](#tt-pack-tt-build-and-tt-init-are-replaced-by-tt-package):

```console
# tt package install --scope system myapp-1.0.0-linux-amd64.tt
# tt instance start myapp:*
```

Где что хранится:

- `/opt/tarantool/apps/<app>` – ссылка на активную версию приложения;
- `/opt/tarantool/var/{lib,log,run}/<app>/<instance>/` – файлы экземпляров;
- `/opt/tarantool/etc/<app>/` – конфигурация этого хоста: `config.yaml` и `instances.yaml`.

До первого запуска перенесите данные каждого экземпляра из `/var/lib/tarantool/sys_env/<app>/<instance>`
в `/opt/tarantool/var/lib/<app>/<instance>`. Вне проекта `tt` не ищет данные в прежнем каталоге.

Пакеты deb и rpm `tt` 3.0 не устанавливают `/etc/tarantool/tt.yaml`, каталоги `sys_env`
и `/etc/logrotate.d/tt.conf`. При обновлении deb-пакета старые файлы остаются до полного удаления пакета (purge).

> **Внимание.** Старое правило logrotate вызывает `tt -S logrotate`, а эта команда теперь завершается с ошибкой.
> Удалите это правило. В собственном правиле переоткрывайте журналы командой `tt instance logrotate <app>:*` –
> пример приведён в разделе [Логи](how-to/systemd.md#logs).

### Юниты `systemd` создаёт `tt service systemd` {#systemd-units-come-from-tt-service-systemd}

Юниты, которые `tt pack` в `tt` 2.x добавлял в пакеты, запускали `tt -L <environment> start` как сервисы типа
forking. С `tt` 3.0 они не работают.

Для приложения, установленного в область установки системы, команда `tt service systemd install <app>` создаёт:

- шаблонный юнит `<app>@.service`, который запускает один экземпляр через `tt instance start --foreground`;
- drop-in для каждого экземпляра с полями `restart` и `stop_timeout` из его записи.

После этого экземплярами управляет `systemctl`, а `tt instance start`, `stop`, `restart` и `kill` отказываются
ими управлять.

В `tt` 2.x приложение с несколькими экземплярами тоже использовало юнит `<app>@.service`, но в
`/usr/lib/systemd/system`. В `tt` 3.0 юнит создаётся в `/etc/systemd/system` и перекрывает старый.
Поэтому переходите в таком порядке:

1. Остановите и отключите старые юниты.
2. Удалите пакет, который их установил.
3. Соберите приложение и установите его в область установки системы.
4. Установите юниты командой `tt service systemd install <app>`.

Команда работает только с областью установки системы – не с проектом и не с областью установки пользователя.

Параметры из `--unit-params-file` заменяются так:

| `systemd-unit-params.yml`      | `tt` 3.0                                                                                                           |
|--------------------------------|--------------------------------------------------------------------------------------------------------------------|
| `FdLimit`                      | В юните задано `LimitNOFILE=65535`. Другое значение задайте в собственном drop-in `<app>@.service.d/override.conf` |
| `instance-env`                 | Поле `env` записи экземпляра или записи `"*"`                                                                      |
| `TT`, `ConfigPath`, `ExecArgs` | Нет: юнит запускает `tt` из области установки приложения                                                        |

Подробнее – в разделе [Запуск инстансов под systemd](how-to/systemd.md).

### `tt cluster publish` запрашивает подтверждение {#tt-cluster-publish-asks-for-a-confirmation}

- `tt cluster publish` запрашивает подтверждение перед записью конфигурации, но только в терминале.
  В скрипте публикации передавайте `--yes` (`-y`), иначе команда завершится с кодом 1 и ничего не запишет.
- `--force` отключает проверку конфигурации, но не подтверждение.
- `tt cluster show --validate` и `tt cluster publish` проверяют конфигурацию по JSON Schema конфигурации
  кластера Tarantool. Проверка строже, чем в `tt` 2.x: неизвестные ключи верхнего уровня и областей видимости (scope) отклоняются.
  Ошибки выводятся в формате `<path> [code] <message>`.

### Удалённые команды {#removed-commands}

| `tt` 2.x | `tt` 3.0 |
|---|---|
| `tt env`, `. <(tt env)` | `source .env/activate.sh`. Файл создаёт первая команда `tt binary install` в проекте |
| `tt cfg dump` | Нет. Rock-серверы показывает `tt registry list` |
| `tt enable` | Нет: проект определяется по текущему каталогу, а приложение устанавливается командой `tt package install` |
| `tt instances` | `tt instance status` – для экземпляров, `tt package list --scope system` – для установленных приложений |
| `tt cartridge` | Нет |

### Команда `tt create` {#tt-create}

- Для `tt create` файл `tt.yaml` не нужен.
- Команда создаёт приложение в текущем каталоге или в каталоге из `--dst` и не создаёт ссылку
  в `instances.enabled`.
- Собственный шаблон задаётся путём к каталогу или архиву `.tgz`, например `tt create ./templates/basic`.
- Переменная шаблона `rundir` удалена.
- Встроенные шаблоны размещают `instances.yaml` и `config.yaml` в `.templates/default/`.
  Шаблон `single_instance` использует расширение `.yaml`, а не `.yml`. Шаблон `cartridge` удалён.
- Шаблон, который создаёт `app.manifest.toml`, отклоняет имена, недопустимые для пакета. Например,
  `vshard_cluster` преобразует `--name my_app` в `my-app`, а `My_App` отклоняет.

### Коды завершения {#exit-codes}

В `tt` 2.x любая ошибка, кроме ошибок `tt backup` и `tt restore`, завершала команду с кодом 1.
В `tt` 3.0 коды такие:

| Код | Значение                                                                                                                                                                                                                                                    |
|-----|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `1` | Запрос нельзя выполнить в заданном виде: ошибка в использовании, нет файла или проекта, нужно подтверждение, а терминала нет                                                                                                                                |
| `2` | Сбой системы, в которой выполнялась команда: сервер недоступен или не ответил вовремя, имя хоста не разрешается, файловой системе не хватает прав или места. Например, `tt connect` к порту, который никто не слушает, теперь завершается с кодом 2, а не 1 |
| `3` | Команда для нескольких целей выполнилась успешно только для части из них                                                                                                                                                                                    |

Если скрипт проверял код 1, теперь проверяйте любой код, отличный от 0. `tt backup` и `tt restore` сохраняют
собственные коды, начиная с 3. Все коды описаны в `tt help errors`.

### `tt backup-id` заменена на `tt backup id` {#tt-backup-id-is-tt-backup-id}

Команда, которая выводит идентификатор новой резервной копии, перенесена в группу `tt backup`. В скрипте
резервного копирования замените `id=$(tt backup-id)` на `id=$(tt backup id)`. Формат идентификатора не изменился.

### Внешние модули ищутся в `TT_CLI_MODULES_PATH` {#external-modules-are-found-by-tt_cli_modules_path}

- Внешние модули ищутся только в каталогах из переменной `TT_CLI_MODULES_PATH` (через двоеточие).
  Относительный путь отсчитывается от рабочего каталога.
- Каталог `modules.directory` из `tt.yaml` и стандартный каталог `modules` рядом с ним больше не проверяются.
  Модуль оттуда не будет найден, и команда, которую он заменял, снова будет выполняться как встроенная команда `tt`.
- Модуль с именем группы команд, например `cluster`, перехватывает всю группу: `tt cluster publish ...`
  запускает модуль с аргументами `publish ...`.
- Команды `tt modules`, `tt help`, `tt completion` и `tt version` заменить нельзя.
- Флаг `-I` запускает встроенную команду `tt`, даже если есть модуль с таким именем.

Подробнее – в разделе [Внешние модули](how-to/external-modules.md).

### `tt daemon` запускает команды tt 3.0 {#tt-daemon-runs-the-commands-of-tt-30}

Запрос к `tt daemon` теперь содержит команду `tt` 3.0. Например, запрос для `tt start test_app`:

- в `tt` 2.x: `{"command_name":"start", "params":["test_app"]}`;
- в `tt` 3.0: `{"command_name":"instance", "params":["start", "test_app:*"]}`.

Глобальные флаги `tt` 2.x, например `-L`, удалены. Для команд, которые запрашивают подтверждение, передавайте
`--yes`: daemon запускает команды без терминала. Подробнее – в разделе
[Работа с tt daemon](https://github.com/tarantool/tt/blob/v3/doc/examples.md#working-with-tt-daemon-experimental).

### Сборка tt и импорт его пакетов {#building-tt-and-importing-its-packages}

Переменная `TT_CLI_BUILD_SSL` принимает значения `default`, `openssl` и `openssl_static`. Значения `no`,
`shared` и `static` отклоняются:

| `tt` 2.x                     | `tt` 3.0                                            |
|------------------------------|-----------------------------------------------------|
| Не задана или `no` – без TLS | Не задана или `default` – TLS на чистом Go, без cgo |
| `shared`                     | `openssl`                                           |
| `static`                     | `openssl_static`                                    |

Для Go-кода, который импортирует `tt`:

- путь модуля – `github.com/tarantool/tt/v3`;
- `lib/cluster`, `lib/connect`, `lib/dial` и `lib/integrity` перенесены в `github.com/tarantool/tt/sdk/cluster`,
  `sdk/connect`, `sdk/dial` и `sdk/integrity`. Каждый из них стал отдельным модулем с API в статусе alpha;
- `InjectedCmds` из `cli/cmd` удалён. Дистрибутив добавляет свои команды как модули tt и выбирает поставщика
  integrity через `core.WithIntegrity`. Подробнее – в разделе [Дистрибутивы и модули](concepts/distributions.md).

### `tt cat` и `tt play` заменены на `tt data cat` и `tt data play` {#tt-cat-and-tt-play-are-tt-data-cat-and-tt-data-play}

`tt cat` и `tt play` удалены без псевдонимов – используйте `tt data cat` и `tt data play`. Новые команды читают
файлы средствами Go, поэтому локальный Tarantool не нужен. Имена флагов сохранились, но поведение изменилось:

| `tt` 2.x                                               | `tt` 3.0                                                                                                                                                                    |
|--------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--format yaml\|json\|lua`                             | `-o`/`--format`. `json` – формат JSON Lines, по одному объекту на строку                                                                                                    |
| `--to N` не включает N                                 | `--to N` включает N, как и `--from N`                                                                                                                                       |
| `--from`/`--to` сравнивают один LSN со всеми репликами | Ограничивают LSN одной реплики: она задаётся флагом `--replica` или берётся единственная реплика из файлов. Для нескольких реплик используйте `--from-vclock`/`--to-vclock` |
| Граница может разрезать транзакцию                     | Транзакция внутри границы берётся целиком                                                                                                                                   |
| Все `.snap` и `.xlog` каталога, сначала снимки         | Самый свежий снимок в заданных границах, затем журналы по vclock, без данных, которые уже есть в снимке                                                                     |
| `--show-system` фильтрует только изменения данных      | Также скрывает строки RAFT, синхронной репликации и NOP                                                                                                                     |
| `play`: один запрос на строку, без транзакций          | Один поток IPROTO, запросы `--batch` выполняются одновременно, доступны `--txn-mode`, `--on-error`, `--dry-run`                                                             |

Дополнительно:

- Если задан `--from` или `--from-vclock`, снимок не читается. Номера строк снимка не являются LSN, поэтому
  границы применяются только к журналам упреждающей записи (WAL).
- Вывод `cat` сохраняет `HEADER` и `BODY` каждой строки. Тела строк RAFT и синхронной репликации теперь
  декодируются.
- Снимок не содержит данных спейсов vinyl: они хранятся в файлах `.run`. `tt data cat` читает эти файлы, если
  они указаны явно или найдены с флагом `-r`. `tt data play` с ними не работает.
- `tt data play` завершается с кодом 3, если цель отклонила часть транзакций, и с кодом 2 при сбое соединения.
- Команда `tt coredump` не перенесена в группу.

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
