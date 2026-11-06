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
  2.14.0: `mage lint:golang` отказывается работать с другой версией, а
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

## Перед коммитом {#before-committing}

CI запускает те же проверки:

```console
$ mage lint:golang
$ mage lint:python   # ruff check и ruff format --check для test/; --fix исправляет ошибки
$ mage codespell
```

## Документация {#the-documentation}

Сайт собирается из `doc/` с помощью MkDocs и Material for MkDocs
(`mkdocs.yml`, `doc/requirements.txt`) и публикуется из ветки `v3` по адресу
<https://tarantool.github.io/tt/>.

```console
$ pip install -r doc/requirements.txt
$ mage docs:serve    # http://127.0.0.1:8000/tt/, перезагрузка при изменениях
$ mage docs:build    # site/, сборка завершается ошибкой при неработающей ссылке
```

[Справочник команд](reference/commands/index.md) генерируется из справки tt
командой `mage docs:reference`: редактировать нужно справку команды, а не её
страницу. Для новой команды верхнего уровня страницу нужно добавить в
навигацию `mkdocs.yml`; иначе сборка завершится с ошибкой.

### Переводы {#translations}

У сайта есть английская и русская версии: английская находится в корне, а
русская - в `/ru/`. Перевод страницы лежит рядом с ней; локаль указывается
перед расширением: `get-started/install.ru.md` - перевод
`get-started/install.md`. Если перевода нет, русская версия сайта показывает
английскую страницу. Поэтому перевод можно добавить в любое время, а в
навигации `mkdocs.yml` перечислены только английские страницы; заголовки,
которые навигация задаёт явно, переводятся в `nav_translations` плагина `i18n`.
В переводе сохраняются заголовки английской страницы, а их якоря задаются
по английскому варианту, чтобы ссылки на разделы работали на обоих языках.
Справочник команд остаётся на английском.

`mage docs:stale` выводит переводы, чьи английские страницы менялись в git
после последнего обновления перевода, вместе с коммитами, которые нужно
учесть, а также переводы удалённых английских страниц. CI запускает эту
команду как предупреждение.
