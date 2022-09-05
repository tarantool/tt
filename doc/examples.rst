========
Examples
========

This file contains various examples of working with tt.

--------
Contents
--------
.. contents::
  :local:

Working with a set of instances
-------------------------------

For example, we want to launch two instances based on one ``init.lua`` file and one
instance based on the ``router.init.lua`` file. In order to do this, we create
a directory called ``demo`` with the content:

``init.lua``:

.. code-block:: lua

    local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
    local app_name = os.getenv('TARANTOOL_APP_NAME')

    while true do
        if app_name ~= nil and inst_name ~= nil then
            print(app_name .. ":" .. inst_name)
        else
            print("unknown instance")
        end
        require("fiber").sleep(1)
    end

``router.init.lua``:

.. code-block:: lua

    local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
    local app_name = os.getenv('TARANTOOL_APP_NAME')

    while true do
        print("custom init file...")
        if app_name ~= nil and inst_name ~= nil then
            print(app_name .. ":" .. inst_name)
        else
            print("unknown instance")
        end
        require("fiber").sleep(1)
    end

``instances.yml`` (The dot and dash characters in instance names
are reserved for system use.):

.. code-block:: yaml

    router:

    master:

    replica:

Now we can run all instances at once:

.. code-block:: bash

   $ tt start demo
   • Starting an instance [router]...
   • Starting an instance [master]...
   • Starting an instance [replica]...

Or just one of them:

.. code-block:: bash

   $ tt start demo:master
   • Starting an instance [master]...

Working with application templates
----------------------------------

For example, we want to create an application template. In order to do this, create a directory for the template:

.. code-block:: bash

    $ mkdir -p ./templates/simple

with the content:

``init.lua.tt.template``:

.. code-block:: lua

    local app_name = {{.name}}
    local login = {{.user_name}}

    require("fiber").sleep(1)

``MANIFEST.yaml``:

.. code-block:: yaml

    description: Simple app
    vars:
        - prompt: User name
          name: user_name
          default: admin
          re: ^\w+$

``init.lua.tt.template`` in this example contains an application code. After instantiation, ``.tt.template`` suffix is removed from the file name.

Create ``./tarantool.yaml`` and add templates search path to it:

.. code-block:: yaml

    tt:
        templates:
            - path: ./templates

Here is how the current directory structure looks like::

    ./
    ├── tarantool.yaml
    └── templates
        └── simple
            ├── init.lua.tt.template
            └── MANIFEST.yaml

Directory name ``simple`` can now be used as template name in create command.
Create an application from the ``simple`` template and type ``user1`` in ``User name`` prompt:

.. code-block:: bash

   $ tt create simple --name simple_app
   • Creating application in <current_directory>/simple_app
   • Using template from <current_directory>/templates/simple
   User name (default: admin): user1

Your application will appear in the ``simple_app`` directory with the following content::

    simple_app/
    ├── Dockerfile.build.tt
    └── init.lua

Instantiated ``init.lua`` content:

.. code-block:: lua

    local app_name = simple_app
    local login = user1

    require("fiber").sleep(1)

Packing environments
----------------------------------

For example, we want to pack a single application. Here is the content of the sample application::
      single_environment/
      ├── tarantool.yaml
      └── init.lua

``tarantool.yaml``:

.. code-block:: yaml

    tt:
        app:

For packing it into tarball, call:

.. code-block:: bash

   $ tt pack tgz
      • Apps to pack: single_environment
      • Generating new tarantool.yaml for the new package.
      • Creating tarball.
      • Bundle is packed successfully to /Users/dev/tt_demo/single_environment/single_environment_0.1.0.0.tar.gz.

The result directory structure::

      unpacked_dir/
      ├── tarantool.yaml
      ├── single_environment
      │   └── init.lua
      ├── env
      │   ├── bin
      │   └── modules
      ├── instances_enabled
      │   └── single_environment -> ../single_environment
      └── var
          ├── lib
          ├── log
          └── run

Example of packing a multi-app environment. The source tree::

     bundle/
     ├── tarantool.yaml
     ├── env
     │   ├── bin
     │   │   ├── tt
     │   │   └── tarantool
     │   └── modules
     ├── myapp
     │   ├── Dockerfile.build.cartridge
     │   ├── Dockerfile.cartridge
     │   ├── README.md
     │   ├── app
     │   ├── bin
     │   ├── deps.sh
     │   ├── failover.yml
     │   ├── init.lua
     │   ├── instances.yml
     │   ├── myapp-scm-1.rockspec
     │   ├── pack-cache-config.yml
     │   ├── package-deps.txt
     │   ├── replicasets.yml
     │   ├── stateboard.init.lua
     │   ├── systemd-unit-params.yml
     │   ├── tarantool.yaml
     │   ├── test
     │   └── tmp
     ├── myapp2
     │   ├── app.lua
     │   ├── data
     │   ├── etc
     │   ├── myapp2
     │   ├── queue
     │   ├── queue1.lua
     │   └── queue2.lua
     ├── myapp3.lua
     ├── app4.lua
     ├── instances_enabled
     │   ├── app1 -> ../myapp
     │   ├── app2 -> ../myapp2
     │   ├── app3.lua -> ../myapp3.lua
     │   ├── app4.lua -> /Users/dev/tt_demo/bundle1/app4.lua
     │   └── app5.lua -> ../myapp3.lua
     └── var
         ├── lib
         ├── log
         └── run

``tarantool.yaml``:

.. code-block:: yaml

    tt:
      modules:
        directory: env/modules
      app:
        instances_enabled: instances_enabled
        run_dir: var/run
        log_dir: var/log
        log_maxsize: 1
        log_maxage: 1
        log_maxbackups: 1
        restart_on_failure: true
        data_dir: var/lib
        bin_dir: env/bin

Pay attention, that all absolute symlinks from `instances_enabled` will be resolved, all sources will be copied
to the result package and the final instances_enabled directory will contain only relative links.

For packing deb package call:

.. code-block:: bash

   $ tt pack deb --name dev_bundle --version 1.0.0
   • A root for package is located in: /var/folders/c6/jv1r5h211dn1280d75pmdqy80000gp/T/2166098848
      • Apps to pack: app1 app2 app3 app4 app5

   myapp scm-1 is now installed in /var/folders/c6/jv1r5h211dn1280d75pmdqy80000gp/T/tt_pack4173588242/myapp/.rocks

      • myapp rocks are built successfully
      • Generating new tarantool.yaml for the new package
      • Initialize the app directory for prefix: data/usr/share/tarantool/bundle
      • Create data tgz
      • Created control in /var/folders/***/control_dir
      • Created result DEB package: /var/folders/***/T/tt_pack4173588242

Now the result package may be distributed and installed using dpkg command.
The package will be installed in /usr/share/tarantool/package_name directory.

Working with tt daemon (experimental)
-------------------------------------

``tt daemon`` module is used to manage ``tt`` running
on the background on a given machine. This way instances
can be operated remotely.
Daemon can be configured with ``tt_daemon.yaml`` config.

You can manage TT daemon with following commands:

* ``tt daemon start`` - launch of a daemon
* ``tt daemon stop`` - terminate of the daemon
* ``tt daemon status`` - get daemon status
* ``tt daemon restart`` - daemon restart

Work scenario:

First, TT daemon should be started on the server side:

.. code-block:: bash

   $ tt daemon start
   • Starting tt daemon...

After daemon launch you can check its status on the server side:

.. code-block:: bash

   $ tt daemon status
   • RUNNING. PID: 6189.

To send request to daemon you can use CURL. In this example the
client sends a request to start ``test_app`` instance on the server side.
Note: directory ``test_app`` (or file ``test_app.lua``) exists
on the server side.

.. code-block:: bash

   $ curl --header "Content-Type: application/json" --request POST \
   --data '{"command_name":"start", "params":["test_app"]}' \
   http://127.0.0.1:1024/tarantool
   {"res":"   • Starting an instance [test_app]...\n"}

Below is an example of running a command with flags.

Flag with argument:

.. code-block:: bash

   $ curl --header "Content-Type: application/json" --request POST \
   --data '{"command_name":"version", "params":["-L", "/path/to/local/dir"]}' \
   http://127.0.0.1:1024/tarantool
   {"res":"Tarantool CLI version 0.1.0, darwin/amd64. commit: bf83f33\n"}

Flag without argument:

.. code-block:: bash

   $ curl --header "Content-Type: application/json" --request POST \
   --data '{"command_name":"version", "params":["-V"]}' \
   http://127.0.0.1:1024/tarantool
   {"res":"Tarantool CLI version 0.1.0, darwin/amd64. commit: bf83f33\n
    • Tarantool executable found: '/usr/local/bin/tarantool'\n"}
