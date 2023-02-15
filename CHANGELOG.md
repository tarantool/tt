# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Follow-up message support for application templates.
- tarantool_layout config option to enable compatibility mode with tarantoolctl artifacts layout.
  If this option is set to `true`, `tt` will not create sub-directories for runtime artifacts such
  as control socket, pid file and log file. This option affects only single instance applications.
- An ability to set different directories for WAL, vinyl and snapshots artifacts.
- ``tt instances`` command to print a list of enabled applications.

### Changed

- tt config is renamed to tt.yaml.

### Fixed

- Output of the ``help`` with all commands.

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
