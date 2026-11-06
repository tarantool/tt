# Установка

## Скрипт установки {#install-script}

`install.sh` устанавливает tt 3 из релиза на GitHub на Linux (x86_64 и
arm64) и на macOS с процессорами Apple:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh
```

Скрипт берёт самый новый стабильный релиз tt 3, а пока такого нет — самый
новый пре-релиз, сверяет архив с `checksums.txt` этого релиза и кладёт `tt`
в `~/.local/bin`, а при запуске от root — в `/usr/local/bin`. Если этого
каталога нет в `PATH`, скрипт подскажет, как его добавить.
`wget -qO- <url> | sh` тоже работает.

Параметры передаются после `sh -s --`:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh -s -- --version 3.0.0-alpha.1 --dir /usr/local/bin
```

- `-v`, `--version <version>` — релиз для установки, например
  `3.0.0-alpha.1`. Его задаёт и `TT_VERSION`.
- `-c`, `--commit <commit>` — собрать tt из коммита репозитория вместо
  установки релиза, см. [ниже](#build-from-a-commit). Его задаёт и
  `TT_COMMIT`. Несовместим с `--version`.
- `-d`, `--dir <dir>` — каталог, куда положить `tt`. Его задаёт и
  `TT_INSTALL_DIR`.
- `--openssl` — установить сборку `tt-openssl`, в которой TLS реализован
  через OpenSSL системы, а не средствами Go. В релизах она есть только для
  Linux.
- `-h`, `--help` — вывести список параметров.

Если задан `GITHUB_TOKEN`, скрипт передаёт его GitHub API, через который
получает список релизов: без авторизации можно упереться в ограничение
частоты запросов. С `--version` список не запрашивается.

### Сборка из коммита {#build-from-a-commit}

`--commit` собирает tt 3 из коммита с помощью `go build`, поэтому нужны
`git` и [Go](https://go.dev/doc/install). Коммит задаётся хешем, полным или
сокращённым, веткой или тегом; коммит pull request, которого нет ни в одной
ветке, — полным хешем:

```console
$ curl -fsSL https://raw.githubusercontent.com/tarantool/tt/v3/install.sh | sh -s -- --commit 1508ce93
```

Бинарный файл собирается так же, как его собирает `mage build`, и сообщает
версию, которую `git describe` выдаёт для этого коммита:

```console
$ tt version
Tarantool CLI version 3.0.0-alpha.1, linux/amd64. commit: 1508ce9 (v3.0.0-alpha.1-20-g1508ce9)
```

Собрать можно везде, где работает Go, включая macOS на Intel, для которой
релизной сборки нет. С `--openssl` нужны компилятор C и заголовки OpenSSL.
Коммит tt 2 скрипт не принимает: tt 2 собирается с подмодулями и кодом,
который генерирует mage, как описано в
[README tt 2](https://github.com/tarantool/tt#build-from-source).

## Сборка из исходников {#build-from-source}

Понадобятся [Go](https://go.dev/doc/install) 1.27.3 или новее,
[Mage](https://magefile.org/) и Git.

```console
$ git clone --branch v3 https://github.com/tarantool/tt
$ cd tt
$ mage build
$ ./tt version
```

Положите бинарный файл `tt` в каталог из `PATH`.

> **Замечание о предварительной версии.** `mage build` прописывает в tt
> версию, которую выдаёт `git describe`, а ближайший тег в ветке `v3`
> относится к 2.x, поэтому собранный файл сообщает версию 2.x. `tt new`
> записывает в манифест `tt = ">=3.0.0"`, и `tt package pack` после этого
> отказывается класть такой tt в архив. Чтобы упаковывать приложение
> бинарным файлом, собранным из исходников, задайте ему версию 3.x:
>
> ```console
> $ go build -ldflags "-X github.com/tarantool/tt/v3/cli/version.gitTag=3.0.0" -o tt ./cli
> ```

### Реализации TLS {#tls-backends}

По умолчанию tt использует TLS на чистом Go и не требует OpenSSL. Другую
реализацию выбирает переменная `TT_CLI_BUILD_SSL`:

```console
$ TT_CLI_BUILD_SSL=openssl mage build         # OpenSSL, linked dynamically
$ TT_CLI_BUILD_SSL=openssl_static mage build  # OpenSSL, linked statically
```

Для статической сборки нужны статические библиотеки OpenSSL.

## Автодополнение в командной оболочке {#shell-completion}

```console
$ . <(tt completion bash)
```

Поддерживаются также `zsh` и `fish`. Автодополнение предлагает команды,
внешние модули и цели команд `tt instance`: приложения до `:` и их инстансы
после.

## Tarantool {#tarantool}

Устанавливать Tarantool заранее не нужно: `tt binary install tarantool`
ставит его в проект, для пользователя или для всей системы (см.
[Manage binaries](../how-to/binaries.md)). Поддерживается Tarantool
Community Edition 3.0 и новее.

## Зависимости времени выполнения {#runtime-dependencies}

- Git и Mage — для `tt binary install tt`, когда tt собирается из
  исходников, и Git — для `tt binary search tt`.
- Git, CMake, `make` и компиляторы C и C++ — для
  `tt binary install tarantool`, когда Tarantool собирается из исходников:
  на платформе без готового бинарного файла, для коммита или pull request,
  а также с `--source-build`.
- Компилятор C — для rock-модулей и компонентов с частью на C.
- `curl` или `wget`, `zip` и `unzip` — для `tt rocks`, который запускает
  LuaRocks.
