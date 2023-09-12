# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Use CLI arg connect string for the prompt line and the title to avoid too long
prompt line when using 'app:instance' target format.

### Added

- `tt install tarantool/tt`: ability to install tarantool and tt from an arbitrary commit.
The binary has the name tt/tarantool_ + seven-digit hash.
- New `tt pack` flag `--tarantool-version` is added to specify tarantool
  version for pack in docker. It is supported only with `--use-docker` enabled.

### Fixed

- Installation failure from a commit hash.
- Crash on `tt install <tool> master`.
- `--with-binaries` flag for `tt pack` not working while packing
  with `--cartridge-compat`.
- `tarantool` binary after `pack` now always named `"tarantool"`.

## [1.2.0] - 2023-08-18

### Changed

- `tt pack` now skips all `.git` files in packed environment, not only in main directory.
- `tt connect`: the reverse search function to work consistently with tarantool.

### Added

- `tt install tarantool-dev`: ability to install tarantool from the local build directory.
- `tt uninstall`: smart auto-completion. It shows installed versions of programs.
- `tt uninstall`: when removing symlinks and an existing installed version, the
  symlink will be switched to the latest installed version, so that `tt` can
  continue working with the program.
- `tt connect`: support for multi-line commands in the history.
- New `tt pack` flag `--cartridge-compat` is added to maintain backward compatibility
with the cartridge-cli. It is supported only by `tgz` type packing.
- `tt pack`: added option `--without-modules` allowing not to take external
  modules into the pack bundle.
- `tt connect`: added command `\shortcuts` listing all available
  shortcuts and hotkeys in go-prompt.

### Fixed

- `tt install tarantool`: symlink to the directory with tarantool headers is now updated
when installing an existing version.
- `tt connect`: terminal failure after throwing an error.

## [1.1.2] - 2023-06-16

### Changed

- Set `compat.fiber_slice_default` to `new` by default in cartridge application template.
- Treat the directory containing the instances file (instances.y[a]ml) as an application.

### Added

- `tt connect`: support for the `../` and `~/` at the beginning of the URI, when using unix sockets.

### Fixed

- Panic in tarantool 1.10.15 static build by `tt`.
- Removed `--use-docker` flag from `tt uninstall tarantool` since it was added by mistake.

## [1.1.1] - 2023-06-08

### Changed

- `tt build` now hides building output if `-V` is not provided.

### Fixed

- ``tt start`` now does not start an instance if it is already running.
- ``tt rocks`` uses rocks repo path relative to tt environment config location.
- ``tt connect`` now does not crash on `\q` input.

### Added

- smart auto-completion for `tt start`, `tt stop`, `tt restart`, `tt connect`, `tt build`,
`tt clean`, `tt logrotate`, `tt status`. It shows suitable apps, in case of the pattern doesn't
contain delimiter `:`, and suitable instances otherwise.
- support tt environment directories overriding using environment variables:
  * TT_CLI_REPO_ROCKS environment variable value is used as rocks repository path if it is set and
there is no tt.repo.rocks in tt configuration file or tt.repo.rocks directory does not include
repository manifest file.
  * TT_CLI_TARANTOOL_PREFIX environment variable value is used for as tarantool installation prefix
directory for rocks commands if it is set and tarantool executable is found in PATH.
- smart auto-completion for `tt create`. It shows a list of built-in templates and
templates from the config.
- `tt connect`: support for the timestamp format in the history file.

## [1.1.0] - 2023-05-02

### Changed
- ``tt install tarantool`` without version specification now installs the latest release.
- ``tt install/search tarantool-ee`` now uses credentials from `tarantool.io` customer zone.
  Also, installation now requires specifying the version.
- ``tt search tarantool-ee`` options changed. A new `--version` flag has been added to allow
  search for a specific release. The `--dev` and `--dbg` options have been merged into a
  single `--debug` option.
- ``tt search`` now uses subcommands for searching tarantool/tarantool-ee/tt binaries

### Added

- ``--dynamic`` option for `tt install tarantool` command to build non-static tarantool executable.

### Fixed

- ``tt connect`` command does not break a console after executing `os.exit()` command anymore.

## [1.0.2] - 2023-04-21

### Fixed

- ``tt cartridge`` command takes into account run dir path from the `tt` environment. So most
  of the `tt cartridge` sub-commands are able to work without specifying `--run-dir` option.
- ``tt install`` command checks it's write rights to binary and include directories before
  installing binaries.

### Changed
  - ``tt install/uninstall`` command line interface is updated. Program names have become
  sub-commands with their own options.

## [1.0.1] - 2023-04-04

### Added

- A configurable variable `cluster_cookie` for `tt create cartridge` template.
- ``tt build`` accepts application name for building.
- Creating wal, vinyl and memtx directories for `tt pack`. If these directories
  are not located in the same directory in the environment for packing, the result package
  will contain separate snap/vinyl/wal directories for corresponding artifacts.

### Fixed

- Packing symlinks into RPM package.

### Changed

- ``tt uninstall`` does not ask version if only one version of a program is installed.
- ``tt rocks init`` is disabled.

## [1.0.0] - 2023-03-23

### Added

- Follow-up message support for application templates.
- tarantool_layout config option to enable compatibility mode with tarantoolctl artifacts layout.
  If this option is set to `true`, `tt` will not create sub-directories for runtime artifacts such
  as control socket, pid file and log file. This option affects only single instance applications.
- An ability to set different directories for WAL, vinyl and snapshots artifacts.
- ``tt instances`` command to print a list of enabled applications.
- SSL options for ``tt connect`` command.
- An ability to pass arguments to a connect command.
- ``tt binaries`` command. It shows a list of installed binaries and their versions.

### Changed

- tt config is renamed to tt.yaml.
- Do not use `make` command options for `tarantool` build if `MAKEFLAGS` environment variable
is set.
- `binaries`, `build`, `check`, `clean`, `create`, `install`, `instances`, `logrotate`, `pack`,
  `restart`, `run`, `start`, `status`, `stop`, `uninstall` require environment configuration file.

### Fixed

- Output of the ``help`` with all commands.
- Allow more characters for URI credentials.

## [0.4.0] - 2022-12-31

### Added

- Support of rocks repository specified in tt config.
- ``cfg dump`` module. It prints tt environment configuration.
- ``--use-docker`` option for ``tt pack`` for packing environments in docker container.
- Support of MacOS.

### Changed

- Updated cartridge-cli version to 2.12.4.

## [0.3.0] - 2022-12-05

### Added

- ``tt restart`` confirmation prompt. ``-y`` option is added to accept restart
  without prompting.
- ``tt pack`` will generate systemd unit for rpm and deb packages.
- ``--use-docker`` option for ``tt install`` to build Tarantool in
  Ubuntu 16.04 container.
- Ability to use the `start/stop/restart/status/check` commands without
  arguments to interact with all instances of the environment simultaneously.
- Starting from version 2.8.1, we can specify configuration parameters of
  tarantool via special environment variables.
  Added support for this feature when running older versions of tarantools via tt.

### Changed

- ``tt cartridge`` sub-commands ``create``, ``build``, ``pack`` are removed.
- ``remove`` command is renamed to ``uninstall``.
- Updated values in system ``tarantool.yaml`` for ``bin_dir``, ``inc_dir``
  and ``repo: distfiles``.

### Fixed

- Working of the ``help`` module with multi-level commands (commands with
  several subcommands).
- Using the system ``tarantool.yaml`` when installing from the repository.

## [0.2.1] - 2022-11-24

### Fixed

- Fixed building for MacOS.
- A unified error writing style has been introduced.

## [0.2.0] - 2022-11-21

### Added

- Module ``tt init``, to create tt environment configuration file.
- Module ``tt daemon``, to manage the ``tt`` daemon.
- Built-in application templates support. Cartridge application template is added.
- Using ``default_cfg`` from ``.tarantoolctl`` for ``tarantool.yaml`` generation in ``tt init``.

### Changed

- Modules ``tt start``, ``tt connect`` and ``tt catridge`` now use relative paths for unix sockets.
  It allows to use socket paths longer than sun_path limit.(108/106 on linux/macOS)
  e.g foo/bar.sock -> ./bar.sock

## [0.1.0] - 2022-10-12

### Added

- Module ``tt version``, to get information about the version of the CLI.
- Module ``tt completion``, to generate autocompletion for a specified shell.
- Module ``tt help``, to get information about the CLI and its modules.
- Module ``tt start``, responsible for launching the instance according to the
  application file.
- Module ``tt stop``, responsible for terminating the instance.
- Module ``tt status``, to get information about the state of the instance.
- Module ``tt restart``, responsible for restarting of the instance.
- Module ``tt logrotate``, to rotate instance logs.
- Module ``tt check``, to check an application file for syntax errors.
- Module ``tt connect``, used to connect to a running instance.
- Module ``tt rocks``, LuaRocks package manager.
- Module ``tt cat``, to print into stdout the contents of .snap/.xlog files.
- Module ``tt play``, to play the contents of .snap/.xlog files to another Tarantool instance.
- Module ``tt coredump``, to pack/unpack/inspect tarantool coredump.
- Module ``tt run``, to start tarantool instance using tt wrapper.
- Module ``tt search``, to show available tt/tarantool versions.
- Module ``tt create``, to create an application from a template.
- Module ``tt build``, to build an application.
- Module ``tt install``, to install tarantool/tt.
- Module ``tt remove``, to remove tarantool/tt.
