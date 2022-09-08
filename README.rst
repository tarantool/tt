.. _tarantool-cli:

=============
Tarantool CLI
=============

Tarantool CLI - command line utility for managing Tarantool packages and Tarantool-based applications.

-----------------
Getting started
-----------------

~~~~~~~~~~~~~
Prerequisites
~~~~~~~~~~~~~

* `Go (version 1.18+) <https://golang.org/doc/install>`_
* `Mage <https://magefile.org/>`_
* `Git <https://git-scm.com/book/en/v2/Getting-Started-Installing-Git>`_

To run tests:

* `Python3 <https://www.python.org/downloads/>`_
* `golangci-lint <https://golangci-lint.run/usage/install/#local-installation>`_

~~~~~
Build
~~~~~

.. code-block:: bash

   git clone https://github.com/tarantool/tt --recursive
   cd tt
   mage build

~~~~~~~~~~~~
Dependencies
~~~~~~~~~~~~

**tt rocks runtime dependencies:**

* `curl <https://curl.se>`_ or `wget <https://www.gnu.org/software/wget/>`_
* `zip <http://infozip.sourceforge.net/>`_
* `unzip <http://infozip.sourceforge.net/>`_

~~~~~~~~~
Run tests
~~~~~~~~~

.. code-block:: bash

   mage test

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
------------------

By default, configuration file is named ``tarantool.yaml``. With the ``--cfg``
flag you can specify the path to configuration file. Example of configuration
file format:

.. code-block:: yaml

    tt:
      modules:
        directory: path/to/modules/dir
      app:
        instances_available: path/to/available/applications
        run_dir: path/to/run_dir
        log_dir: path/to/log_dir
        log_maxsize: num (MB)
        log_maxage: num (Days)
        log_maxbackups: num
        restart_on_failure: bool

**modules**

* ``directory`` (string) - the path to directory where the external modules are stored.

**app**

* ``instances_available`` (string) - path to directory that stores all available applications.
* ``run_dir`` (string) - path to directory that stores various instance runtime
  artifacts like console socket, PID file, etc.
* ``log_dir`` (string) - directory that stores log files.
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

CLI Args
--------

Arguments of Tarantool CLI:

* ``--cfg | -c`` (string) - path to Tarantool CLI config.
* ``--internal | -I`` - use internal module.
* ``--local | -L`` (string) - run Tarantool CLI as local, in the specified directory.
* ``--system | -S`` - run Tarantool CLI as system.
* ``--help | -h`` - help.

Autocompletion
--------------

You can generate autocompletion for ``bash`` or ``zsh`` shell:

.. code-block:: bash

   . <(tt completion bash)

Enter ``tt``, press tab and you will see a list of available modules with
descriptions. Also, autocomplete supports external modules.

Working with a set of instances
-------------------------------

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
