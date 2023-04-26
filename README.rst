.. _tarantool-cli:

=============
Tarantool CLI
=============

..  image:: https://img.shields.io/github/v/release/tarantool/tt?include_prereleases&label=Release&labelColor=2d3532
    :alt: TT CLI latest release on GitHub
    :target: https://github.com/tarantool/tt/releases

..  image:: https://github.com/tarantool/tt/workflows/Full%20CI/badge.svg
    :alt: TT CLI build status on GitHub Actions
    :target: https://github.com/tarantool/tt/actions/workflows/full-ci.yml


Tarantool CLI - command line utility for managing Tarantool packages and Tarantool-based applications.


.. contents:: **Contents**


---------------
Getting started
---------------

Installation
~~~~~~~~~~~~

TT can be installed from the deb / rpm repository "tarantool/modules".

Install the tarantool repositories:

https://www.tarantool.io/en/download/os-installation/

Install TT:

* Deb based distributions:

.. code-block:: bash

   apt-get install tt

* Rpm based distributions:

.. code-block:: bash

   yum install tt
   dnf install tt

On Gentoo Linux, the TT can be installed from the `Tarantool Gentoo Overlay <https://github.com/tarantool/gentoo-overlay>`_:

.. code-block:: bash

   emerge tt

On MacOS, the TT can be installed from brew:

.. code-block:: bash

   brew install tt

You can also install Tarantool CLI by downloading archive with pre-built binary
for your OS from GitHub's Releases page.

However, on MacOS to run that binary you will need to do additional steps:

1. After first try to run binary, you will encounter an error:

..  image:: doc/images/macOS_error.jpeg
    :width: 250
    :alt: MacOS error.

2. To fix it, you should go to 'system settings->privacy and security', then scroll down and find:

..  image:: doc/images/macOS_settings.jpeg
    :width: 350
    :alt: MacOS settings.

3. Click on 'Allow Anyway' and you should be able to use Tarantool Cli.

Build from source
~~~~~~~~~~~~~~~~~

Prerequisites
"""""""""""""

* `Go (version 1.18+) <https://golang.org/doc/install>`_
* `Mage <https://magefile.org/>`_
* `Git <https://git-scm.com/book/en/v2/Getting-Started-Installing-Git>`_

To run tests:

* `Python3 <https://www.python.org/downloads/>`_
* `pytest <https://docs.pytest.org/en/7.2.x/getting-started.html#get-started>`_
* `flake8 <https://pypi.org/project/flake8/>`_
* `flake8-isort <https://pypi.org/project/flake8-isort/>`_
* `flake8-unused-arguments <https://pypi.org/project/flake8-unused-arguments/>`_
* `golangci-lint <https://golangci-lint.run/usage/install/#local-installation>`_
* `lichen <https://github.com/uw-labs/lichen#install>`_
* `docker <https://docs.docker.com/engine/install/>`_


Build
"""""

.. code-block:: bash

   git clone https://github.com/tarantool/tt --recursive
   cd tt

You can build a binary without OpenSSL and TLS support for development
purposes:

.. code-block:: bash

   TT_CLI_BUILD_SSL=no mage build
   mage build

You can build a binary with statically linked OpenSSL. This build type is used
for releases:

.. code-block:: bash

   TT_CLI_BUILD_SSL=static mage build

Finally, you can build a binary with dynamically linked OpenSSL for development
purposes:

.. code-block:: bash

   TT_CLI_BUILD_SSL=shared mage build

Dependencies
""""""""""""

**tt rocks runtime dependencies:**

* `curl <https://curl.se>`_ or `wget <https://www.gnu.org/software/wget/>`_
* `zip <http://infozip.sourceforge.net/>`_
* `unzip <http://infozip.sourceforge.net/>`_

**tt install && search runtime dependencies:**

* `Git <https://git-scm.com/book/en/v2/Getting-Started-Installing-Git>`_

Run tests
"""""""""

To run default set of tests (excluding slow tests):

.. code-block::

   mage test

To run full set of tests:

.. code-block::

   mage testfull

-------------
Configuration
-------------

Taratool CLI can be launched in several modes:

* System launch (flag ``-S``) - the working directory is current, configuration
  file searched in ``/etc/tarantool`` directory.
* Local launch (flag ``-L``) - the working directory is the one you specified,
  configuration file is searched in this directory. If configuration file doesn't
  exists, config searched from the working directory to the root. If it is also
  not found, then take config from ``/etc/tarantool``. If tarantool or tt
  executable files are found in working directory, they will be used further.
* Default launch (no flags specified) - configuration file searched from the
  current directory to the root, going down the directory until file is found.
  Working directory - the one where the configuration file is found.
  If configuration file isn't found, config taken from ``/etc/tarantool`` directory.
  In this case working directory is current.


Configuration file
~~~~~~~~~~~~~~~~~~

By default, configuration file is named ``tt.yaml``. With the ``--cfg``
flag you can specify the path to configuration file. Example of configuration
file format:

.. code-block:: yaml

    tt:
      modules:
        directory: path/to/modules/dir
      app:
        instances_enabled: path/to/available/applications
        run_dir: path/to/run_dir
        log_dir: path/to/log_dir
        bin_dir: path/to/bin_dir
        inc_dir: path/to/inc_dir
        wal_dir: var/lib
        vinyl_dir: var/lib
        memtx_dir: var/lib
        log_maxsize: num (MB)
        log_maxage: num (Days)
        log_maxbackups: num
        restart_on_failure: bool
        tarantoolctl_layout: bool
      repo:
        rocks: path/to/rocks
        distfiles: path/to/install
      ee:
        credential_path: path/to/file
      templates:
        - path: path/to/templates_dir1
        - path: path/to/templates_dir2

**modules**

* ``directory`` (string) - the path to directory where the external modules are stored.

**app**

* ``instances_enabled`` (string) - path to directory that stores all applications.
* ``run_dir`` (string) - path to directory that stores various instance runtime
  artifacts like console socket, PID file, etc.
* ``log_dir`` (string) - directory that stores log files.
* ``bin_dir`` (string) - directory that stores binary files.
* ``inc_dir`` (string) - directory that stores header files.
  The path will be padded with a directory named include.
* ``wal_dir`` (string) - directory where write-ahead log (.xlog) files are stored.
* ``memtx_dir`` (string) - directory where memtx stores snapshot (.snap) files.
* ``vinyl_dir`` (string) - directory where vinyl files or subdirectories will be stored.
* ``log_maxsize`` (number) - the maximum size in MB of the log file before it gets
  rotated. It defaults to 100 MB.
* ``log_maxage`` (numder) - is the maximum number of days to retain old log files
  based on the timestamp encoded in their filename. Note that a day is defined
  as 24 hours and may not exactly correspond to calendar days due to daylight
  savings, leap seconds, etc. The default is not to remove old log files based
  on age.
* ``log_maxbackups`` (number) - the maximum number of old log files to retain.
  The default is to retain all old log files (though log_maxage may still cause
  them to get deleted.)
* ``restart_on_failure`` (bool) - should it restart on failure.
* ``tarantoolctl_layout`` (bool) - enable/disable tarantoolctl layout compatible mode for
  artifact files: control socket, pid, log files. Data files (wal, vinyl, snapshots) and
  multi-instance applications are not affected by this option.

**repo**

* ``rocks`` (string) - directory that stores rocks files.
* ``distfiles`` (string) - directory that stores installation files.

**ee**

* ``credential_path`` (string) - path to file with credentials for downloading tarantool-ee.
  File must contain login and password. Each parameter on a separate line.
  Alternatively credentials can be set via environment variables:
  `TT_CLI_EE_USERNAME` and `TT_CLI_EE_PASSWORD`.

**templates**

* ``path`` (string) - the path to templates search directory.

-----------------------
Creating tt environment
-----------------------

tt environment can be created using ``init`` command:

.. code-block:: bash

    $ tt init

``tt init`` searches for existing configuration files in current directory:

* ``.cartridge.yml``. If ``.cartridge.yml`` is found, it is loaded, and directory information
  from it is used for ``tt.yaml`` generation.
* ``.tarantoolctl``. If ``.tarantoolctl`` is found, it is invoked by Tarantool and directory
  information from ``default_cfg`` table is used for ``tt.yaml`` generation.
  ``.tarantoolctl`` will not be invoked by ``tt start`` command, so all variables defined in this
  script will not be available in application code.

If there are no existing configs in current directory, ``tt init`` generates default
``tt.yaml`` and creates a set of environment directories. Here is and example
of the default environment filesystem tree::

  .
  ├── bin
  ├── include
  ├── distfiles
  ├── instances.enabled
  ├── modules
  ├── tt.yaml
  └── templates

Where:

* ``bin`` - directory that stores binary files.
* ``include`` - directory that stores header files.
* ``distfiles`` - directory that stores installation files for local install.
* ``instances.enabled`` - directory that stores enabled applications or symlinks.
* ``modules`` - the directory where the external modules are stored.
* ``tt.yaml`` - tt environment configuration file generated by ``tt init``.
* ``templates`` - the directory where external templates are stored.

----------------
External modules
----------------

External module - any executable file stored in modules directory. Module
must be able to handle ``--description`` and ``--help`` flags. When calling
with ``--description`` flag, module should print a short description of
module to stdout. When calling with ``--help`` flag, module should print a
help information about module to stdout.

Tarantool CLI already contains a basic set of modules. You can overload these
with external ones, or extend functionality with your own module. Modules
getting from directory, which specified in ``directory`` field (see example above).

For example, you have an external ``version`` module. When you type ``tt version``,
the external ``version`` module will be launched. To run the internal implementation,
use the ``--internal (-I)`` flag. If there is no executable file with the same name,
the internal implementation will be started.

You can use any external module that doesn't have any internal implementation.
For example, you have module named ``example-module``. Just type ``tt example-module``
to run it.

To see list of available modules, type ``tt -h``.

--------
CLI Args
--------

Arguments of Tarantool CLI:

* ``--cfg | -c`` (string) - path to Tarantool CLI config.
* ``--internal | -I`` - use internal module.
* ``--local | -L`` (string) - run Tarantool CLI as local, in the specified directory.
* ``--system | -S`` - run Tarantool CLI as system.
* ``--help | -h`` - help.

Autocompletion
~~~~~~~~~~~~~~

You can generate autocompletion for ``bash`` or ``zsh`` shell:

.. code-block:: bash

   . <(tt completion bash)

Enter ``tt``, press tab and you will see a list of available modules with
descriptions. Also, autocomplete supports external modules.

--------
TT usage
--------

Working with a set of instances
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``tt`` can manage a set of instances based on one source file.

To work with a set of instances, you need:
a directory where the files will be located:
``init.lua`` and ``instances.yml``.

* ``init.lua`` - application source file.
* ``instances.yml`` - description of instances.

Instances are described in ``instances.yml`` with format:

.. code-block:: yaml

    instance_name:
      parameter: value

The dot and dash characters in instance names are reserved for system use.
if it is necessary for a certain instance to work on a source file other
than ``init.lua``, then you need to create a script with a name in the
format: ``instance_name.init.lua``.

The following environment variables are associated with each instance:

* ``TARANTOOL_APP_NAME`` - application name (the name of the directory
  where the application files are present).
* ``TARANTOOL_INSTANCE_NAME`` - instance name.

`Example <https://github.com/tarantool/tt/blob/master/doc/examples.rst#working-with-a-set-of-instances>`_

Working with application templates
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``tt`` can create applications from templates.

To work with application template, you need:

* A ``<path>`` where templates directories or archives are located.

* ``tt.yaml`` configured to search templates in <path>:

  .. code-block:: yaml

    tt:
      templates:
        - path: <path1>
        - path: <path2>

Application template may contain:

* ``*.tt.template`` - template files, that will be instantiated during application creation.

* ``MANIFEST.yaml`` - template manifest (see details below).

Template manifest ``MANIFEST.yaml`` has the following format:

.. code-block:: yaml

  description: Template description
  vars:
      - prompt: User name
        name: user_name
        default: admin
        re: ^\w+$

      - prompt: Retry count
        default: "3"
        name: retry_count
        re: ^\d+$
  pre-hook: ./hooks/pre-gen.sh
  post-hook: ./hooks/post-gen.sh
  include:
  - init.lua
  - instances.yml

Where:

* ``description`` (string) - template description.
* ``vars`` - template variables used for instantiation.

  * ``prompt`` - user prompt for variable value input.
  * ``name`` - variable name.
  * ``default`` - default value of the variable.
  * ``re`` - regular expression used for value validation.
* ``pre-hook`` (string) - executable to run before template instantiation.
* ``post-hook`` (string) - executable to run after template instantiation.
* ``include`` (list) - list of files to keep in application directory after create.

There are pre-defined variables that can be used in template text:
``name`` - application name. It is set to ``--name`` CLI argument value.

Don't include the .rocks directory in your application template. To specify application dependencies,
use the .rockspec.

`Custom template example <https://github.com/tarantool/tt/blob/master/doc/examples.rst#working-with-application-templates>`_

Working with tt daemon (experimental)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

``tt daemon`` module is used to manage ``tt``
daemon on a given machine. This way instances
can be operated remotely.
Daemon can be configured with ``tt_daemon.yaml`` config.

``tt_daemon.yaml`` file format:

.. code-block:: yaml

  daemon:
        run_dir: path
        log_dir: path
        log_maxsize: num (MB)
        log_maxage: num (Days)
        log_maxbackups: num
        log_file: string (file name)
        listen_interface: string
        port: num
        pidfile: string (file name)

Where:

* ``run_dir`` (string) - path to directory that stores various instance
  runtime artifacts like console socket, PID file, etc. Default: ``run``.
* ``log_dir`` (string) - directory that stores log files. Default: ``log``.
* ``log_maxsize`` (number) - the maximum size in MB of the log file before it gets
  rotated. Default: 100 MB.
* ``log_maxage`` (numder) - is the maximum number of days to retain old log files
  based on the timestamp encoded in their filename. Note that a day is defined
  as 24 hours and may not exactly correspond to calendar days due to daylight
  savings, leap seconds, etc. Default: not to remove old log files based
  on age.
* ``log_maxbackups`` (number) - the maximum number of old log files to retain.
  Default: to retain all old log files (though log_maxage may still cause
  them to get deleted).
* ``log_file`` (string) - name of file contains log of daemon process.
  Default: ``tt_daemon.log``.
* ``listen_interface`` (string) - network interface the IP address
  should be found on to bind http server socket.
  Default: loopback (``lo``/``lo0``).
* ``port`` (number) - port number to be used for daemon http server.
  Default: 1024.
* ``pidfile`` (string) - name of file contains pid of daemon process.
  Default: ``tt_daemon.pid``.

`TT daemon example <https://github.com/tarantool/tt/blob/master/doc/examples.rst#working-with-tt-daemon-experimental>`_

Setting Tarantool configuration parameters via environment variables
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Using ``tt``, you can specify configuration parameters
via special environment variables even on Tarantool versions that does not natively support it.
The name of a variable should have the following pattern: ``TT_<NAME>``,
where ``<NAME>`` is the uppercase name of the corresponding `box.cfg <https://www.tarantool.io/en/doc/latest/reference/configuration/#box-cfg-params-ref>`_ parameter.

--------
Commands
--------
Common description. For a detailed description, use ``tt help command`` .

* ``start`` - start a tarantool instance(s).
* ``stop`` - stop the tarantool instance(s).
* ``status`` - get current status of the instance(s).
* ``restart`` - restart the instance(s).
* ``version`` - show Tarantool CLI version information.
* ``completion`` - generate autocomplete for a specified shell.
* ``help`` - display help for any command.
* ``logrotate`` - rotate logs of a started tarantool instance(s).
* ``check`` - check an application file for syntax errors.
* ``connect`` -  connect to the tarantool instance.
* ``rocks`` - LuaRocks package manager.
* ``cat`` - print into stdout the contents of .snap/.xlog files.
* ``play`` - play the contents of .snap/.xlog files to another Tarantool instance.
* ``coredump`` - pack/unpack/inspect tarantool coredump.
* ``run`` - start a tarantool instance.
* ``search`` - show available tt/tarantool versions.
* ``clean`` -  clean instance(s) files.
* ``create`` - create an application from a template.
* ``build`` - build an application.
* ``install`` - install tarantool/tt.
* ``uninstall`` - uninstall tarantool/tt.
* ``init`` - create tt environment configuration file.
* ``daemon (experimental)`` - manage tt daemon.
* ``cfg dump`` - print tt environment configuration.
* ``pack`` - pack an environment into a tarball/RPM/Deb.
* ``instances`` - show enabled applications.
* ``binaries`` - show a list of installed binaries and their versions.
