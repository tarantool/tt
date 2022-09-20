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

