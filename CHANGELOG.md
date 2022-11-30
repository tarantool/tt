# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- ``tt restart`` confirmation prompt. ``-y`` option is added to accept restart without prompting.
- ``tt pack`` will generate systemd unit for rpm and deb packages.

### Changed

- ``tt cartridge`` sub-commands ``create``, ``build``, ``pack`` are removed.
- ``remove`` command is renamed to ``uninstall``.

### Fixed

- Fixed internal collection of information about long commands.

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
