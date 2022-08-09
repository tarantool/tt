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
connected instance based on the ``router.lua`` file. In order to do this, we create
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
