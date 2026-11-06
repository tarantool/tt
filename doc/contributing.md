# Contributing

## Build

```console
$ git clone --branch v3 https://github.com/tarantool/tt
$ cd tt
$ mage build
```

See [Install](get-started/install.md#build-from-source) for the TLS
backends.

## Test

The tests need:

- Python 3 with the packages of `test/requirements.txt`;
- Tarantool 3.3 or newer;
- [golangci-lint](https://golangci-lint.run/usage/install/#local-installation)
  2.14.0: `mage lint:golang` refuses another version, and `GOLANGCI_LINT`
  names the executable when it is not `golangci-lint` on `PATH`;
- [lichen](https://github.com/uw-labs/lichen#install), in `~/go/bin`;
- Docker;
- etcd 3 or newer, with its service stopped: the tests start their own.

```console
$ systemctl stop etcd && systemctl disable etcd
$ python3 -m venv .venv
$ source .venv/bin/activate
$ pip install -r test/requirements.txt
$ mage test          # the linters, the unit tests and the integration tests, slow ones excepted
$ mage testfull      # every test
```

## Before committing

CI runs the same checks:

```console
$ mage lint:golang
$ mage lint:python   # ruff check and ruff format --check over test/; --fix fixes
$ mage codespell
```

## The documentation

The site is built from `doc/` with MkDocs and Material for MkDocs
(`mkdocs.yml`, `doc/requirements.txt`) and published from the `v3` branch
to <https://tarantool.github.io/tt/>.

```console
$ pip install -r doc/requirements.txt
$ mage docs:serve    # http://127.0.0.1:8000/tt/, reloads on changes
$ mage docs:build    # site/, failing on a broken link
```

The [command reference](reference/commands/index.md) is generated from the
help of tt by `mage docs:reference`: edit the help of a command, not its
page. A new top-level command needs its page added to the nav of
`mkdocs.yml`; the build fails until it is.

### Translations

The site has an English and a Russian version, the English one at the root
and the Russian one under `/ru/`. A translation of a page lives next to it,
with the locale before the extension: `get-started/install.ru.md`
translates `get-started/install.md`. The Russian site shows the English page
wherever there is no translation, so a page can be translated at any time
and the nav of `mkdocs.yml` lists the English pages only; the titles the nav
sets explicitly are translated in `nav_translations` of the `i18n` plugin.
A translation keeps the headings of the English page and pins their anchors
to the English ones, `## Сборка из исходников {#build-from-source}`, so that
the links to a section work in both languages. The command reference stays
in English.

`mage docs:stale` lists the translations whose English page changed in git
after the translation last did, with the commits to catch up on, and the
translations whose English page is gone. CI runs it as a warning.
