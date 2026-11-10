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

### Another Tarantool

The tests run against the `tarantool` on `PATH`. `TT_TEST_TARANTOOL` picks
another one for the targets that run Tarantool CE: `unit:full`,
`unit:fullSkipDocker`, `unit:coverage` and the `integration` ones but
`integrationEE` and `integrationNoTarantool`, `test` and `testfull` with
them:

```console
$ TT_TEST_TARANTOOL=3.6 mage integration       # the newest 3.6
$ TT_TEST_TARANTOOL=min mage unit:full         # the oldest version CI tests
$ TT_TEST_TARANTOOL=latest mage integration    # the newest version CI tests
$ TT_TEST_TARANTOOL=master mage integration    # built from the sources
$ TT_TEST_TARANTOOL=~/src/tarantool/build/src/tarantool mage integration
```

`min` and `latest` are the versions of `ci/tarantool-versions.env`. tt
installs a version the first time into a directory of its own in
`tarantool/` of the test cache, and puts it first on `PATH` with
`TT_CLI_TARANTOOL_PREFIX` at its headers. The test cache is `tt-test` in the
cache directory of the user, `~/.cache` on Linux and `~/Library/Caches` on
macOS, or `TT_TEST_CACHE`. A version stays there, `master` too: delete its
directory to install it again.

### In a container

`mage docker` runs targets in a container that has what CI installs: Go,
Python with the packages of `test/requirements.txt`, etcd, golangci-lint,
the shells of the completion tests and what a source build of Tarantool
needs. The machine needs Docker and what runs the magefile, Go and mage,
only. The image is built from `ci/Dockerfile` the first time; its tag
follows the Dockerfile, the requirements and the Go version of `go.mod`, so
a change to any of them builds a new one.

```console
$ mage docker integration
$ TT_TEST_TARANTOOL=min mage docker integrationFull
$ mage docker "lint:full unit:full"
$ PYTEST_ADDOPTS="-k test_status -x" mage docker integration
$ mage docker integrationNoTarantool
```

The image has no Tarantool: `TT_TEST_TARANTOOL`, `latest` unless set, picks
it as above, and a path names an executable in the container.
`integrationNoTarantool` installs none, so it runs with no Tarantool at all.

The container works in the checkout, mounted at its own path, as your user,
so what the tests write stays yours. It has the network of the host and its
Docker daemon, whose socket is mounted: the docker tests start containers
that mount the temporary directories of the tests, which are in
`/tmp/tt-test-<uid>` on the host for that, and listen on `localhost`. The
socket gives the tests what it gives you. `TT_TEST_TMPDIR` moves the
temporary directories, as when `/tmp` is a small `tmpfs`: a source build of
Tarantool takes gigabytes. Keep the path short, the unix sockets of the
instances are made under it. Go, mage and the versions of Tarantool stay in
`docker/` of the test cache between runs; delete it to start over. On
macOS, Docker Desktop needs host networking turned on in its settings.

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
