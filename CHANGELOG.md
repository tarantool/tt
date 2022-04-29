# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
