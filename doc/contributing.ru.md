# Участие в разработке

## Сборка {#build}

```console
$ git clone --branch v3 https://github.com/tarantool/tt
$ cd tt
$ mage build
```

TLS-бэкенды описаны в разделе [Установка](get-started/install.md#build-from-source).

## Тестирование {#test}

Для тестов нужны:

- Python 3 с пакетами из `test/requirements.txt`;
- Tarantool 3.3 или новее;
- [golangci-lint](https://golangci-lint.run/usage/install/#local-installation)
  2.15.0: `mage lint:golang` отказывается работать с другой версией, а
  `GOLANGCI_LINT` задаёт имя исполняемого файла, если он называется не
  `golangci-lint` в `PATH`;
- [lichen](https://github.com/uw-labs/lichen#install) в `~/go/bin`;
- Docker;
- etcd 3 или новее с остановленной службой: тесты запускают собственную.

```console
$ systemctl stop etcd && systemctl disable etcd
$ python3 -m venv .venv
$ source .venv/bin/activate
$ pip install -r test/requirements.txt
$ mage test          # линтеры, модульные и интеграционные тесты, кроме медленных
$ mage testfull      # все тесты
```

### Другая версия Tarantool {#another-tarantool}

Тесты работают с `tarantool` из `PATH`. `TT_TEST_TARANTOOL` выбирает другой
для целей, которые запускают Tarantool CE: `unit:full`,
`unit:fullSkipDocker`, `unit:coverage` и целей `integration:`, кроме
`integration:ee` и `integration:noTarantool`, а с ними и для `test` и
`testfull`. `mage integration` — это `integration:default`, так же как
`mage unit` — это `unit:default`:

```console
$ TT_TEST_TARANTOOL=3.6 mage integration       # последняя 3.6
$ TT_TEST_TARANTOOL=min mage unit:full         # самая старая версия из CI
$ TT_TEST_TARANTOOL=latest mage integration    # самая новая версия из CI
$ TT_TEST_TARANTOOL=master mage integration    # сборка из исходников
$ TT_TEST_TARANTOOL=~/src/tarantool/build/src/tarantool mage integration
```

`min` и `latest` — версии из `ci/tarantool-versions.env`. При первом запуске
tt устанавливает версию в отдельный каталог в `tarantool/` тестового кэша,
ставит её первой в `PATH` и указывает `TT_CLI_TARANTOOL_PREFIX` на её
заголовки. Тестовый кэш — это `tt-test` в каталоге кэша пользователя
(`~/.cache` в Linux и `~/Library/Caches` в macOS) или `TT_TEST_CACHE`.
Установленная версия остаётся в кэше, `master` тоже: чтобы установить её
заново, удалите её каталог.

### В контейнере {#in-a-container}

`mage docker` запускает цели в контейнере, где есть всё, что ставит CI: Go,
Python с пакетами из `test/requirements.txt`, etcd, golangci-lint, оболочки
для тестов автодополнения и всё, что нужно для сборки Tarantool из
исходного кода. От машины нужны только Docker и то, что запускает magefile: Go
и mage. Образ собирается из `ci/Dockerfile` при первом запуске; его тег
зависит от Dockerfile, от зависимостей и от версии Go из `go.mod`, так что
после изменения любого из них собирается новый.

```console
$ mage docker integration
$ TT_TEST_TARANTOOL=min mage docker integration:full
$ mage docker "lint:full unit:full"
$ PYTEST_ADDOPTS="-k test_status -x" mage docker integration
$ mage docker integration:noTarantool
```

В образе нет Tarantool: его выбирает `TT_TEST_TARANTOOL`, как описано выше,
по умолчанию `latest`, а путь указывает на исполняемый файл внутри
контейнера. `integration:noTarantool` Tarantool не устанавливает и работает
совсем без него.

Контейнер работает прямо в рабочей копии, смонтированной по тому же пути, от
вашего пользователя, поэтому файлы, которые пишут тесты, остаются вашими. У
него сеть хоста и Docker-демон хоста, сокет которого монтируется: тесты
docker запускают контейнеры, которые монтируют временные каталоги тестов
(для этого они лежат в `/tmp/tt-test-<uid>` на хосте) и слушают на
`localhost`. Сокет даёт тестам те же права, что и вам. `TT_TEST_TMPDIR`
переносит временные каталоги в другое место, например если `/tmp` —
маленький `tmpfs`: сборка Tarantool из исходного кода занимает несколько
гигабайт. Путь должен быть коротким, потому что в нём создаются unix-сокеты
экземпляров. Go, mage и версии Tarantool сохраняются между запусками в
`docker/` тестового кэша; чтобы начать с чистого листа, удалите этот
каталог. В macOS в настройках Docker Desktop нужно включить host networking.

## Перед коммитом {#before-committing}

CI запускает те же проверки:

```console
$ mage lint:full     # всё, что ниже
$ mage lint:golang
$ mage lint:python   # ruff check и ruff format --check для test/; --fix исправляет ошибки
$ mage lint:spell
$ mage lint:modules  # go.mod и go.sum приведены в порядок и проверены
```

## Документация {#the-documentation}

Сайт собирается из `doc/` с помощью [Zensical](https://zensical.org/)
(`zensical.toml`, `doc/requirements.txt`) и публикуется из ветки `v3` по адресу
<https://tarantool.github.io/tt/>.

```console
$ pip install -r doc/requirements.txt
$ mage docs:serve    # http://127.0.0.1:8000/tt/, перезагрузка при изменениях
$ mage docs:build    # site/, сборка завершается ошибкой при неработающей ссылке
```

[Справочник команд](reference/commands/index.md) генерируется из справки tt
командой `mage docs:reference`: редактировать нужно справку команды, а не её
страницу. Для новой команды верхнего уровня страницу нужно добавить в
навигацию `zensical.toml`; иначе сборка завершится с ошибкой.

### Переводы {#translations}

У сайта есть английская и русская версии: английская находится в корне, а
русская - в `/ru/`. Перевод страницы лежит рядом с ней; локаль указывается
перед расширением: `get-started/install.ru.md` - перевод
`get-started/install.md`. Если перевода нет, русская версия сайта показывает
английскую страницу. Поэтому перевод можно добавить в любое время, а в
навигации `zensical.toml` перечислены только английские страницы. Заголовки,
которые навигация задаёт явно, и описание сайта переводятся в
`zensical.ru.toml` рядом с `zensical.toml`.
В переводе сохраняются заголовки английской страницы, а их якоря задаются
по английскому варианту, `## Сборка из исходников {#build-from-source}`,
чтобы ссылки на разделы работали на обоих языках; заголовку первого уровня
якорь не нужен. Справочник команд остаётся на английском.

Термины переводятся так, как их задаёт
[глоссарий](translations/glossary.md): там же пунктуация и конструкции,
которых в переводах избегают.

Zensical собирает сайт на одном языке, поэтому русский сайт собирает
`ci/doc_site.py`, его запускают `mage docs:build` и `mage docs:serve`: из
копии `doc/` в `.zensical/ru/doc/`, где страница берётся из перевода, если он
есть, и из `zensical.toml` с изменениями из `zensical.ru.toml`, и кладёт его
в `site/ru/`. Сборка завершается ошибкой на неработающей ссылке любого из
двух сайтов и на ссылке переведённой страницы на якорь, которого на русском
сайте нет. Английская страница, показанная на русском сайте, может ссылаться
на раздел, до которого перевод ещё не дошёл: это предупреждение, как и
устаревший перевод. `mage docs:serve` перезагружает английскую страницу в
браузере при изменении и пересобирает русский сайт за несколько секунд, что
видно при перезагрузке страницы.

`mage docs:stale` выводит переводы, чьи английские страницы менялись в git
после последнего обновления перевода, вместе с коммитами, которые нужно
учесть, а также переводы удалённых английских страниц. CI запускает эту
команду как предупреждение.
