# Examples

This file contains various examples of working with tt.

## Contents

- [Working with a set of instances](#working-with-a-set-of-instances)
- [Migrating from instances.enabled](#migrating-from-instancesenabled)
- [Working with application templates](#working-with-application-templates)
- [Working with tt cluster (experimental)](#working-with-tt-cluster-experimental)
- [Packaging applications](#packaging-applications)
- [Working with tt daemon (experimental)](#working-with-tt-daemon-experimental)
- [Transition from tarantoolctl to tt](#transition-from-tarantoolctl-to-tt)
  + [Commands difference](#commands-difference)

## Working with a set of instances

For example, we want to launch two instances based on one `init.lua`
file and one instance based on the `router.init.lua` file. In order to
do this, we create an application directory called `demo` with the content:

`tt.yaml`:

```yaml
{}
```

`init.lua`:

``` lua
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
```

`router.init.lua`:

``` lua
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
```

`instances.yml` (The dot character in instance names is
reserved for system use):

``` yaml
router:

master:

replica:
```

Run the commands from the application root:

```console
cd demo
```

Now we can run all instances at once:

``` console
$ tt start demo
• Starting an instance [demo:router]...
• Starting an instance [demo:master]...
• Starting an instance [demo:replica]...
```

Or just one of them:

``` console
$ tt start demo:master
• Starting an instance [demo:master]...
```

To start all instances of the current application without specifying its name,
run:

``` console
$ tt start
• Starting an instance [demo:router]...
• Starting an instance [demo:master]...
• Starting an instance [demo:replica]...
```

## Migrating from instances.enabled

See the [2.x to 3.0 migration guide](migration_from_older_versions.md#2x---300)
for the legacy layout and the complete upgrade procedure.

## Working with application templates

For example, we want to create an application template. In order to do
this, create a directory for the template:

``` sh
mkdir -p ./templates/simple
```

with the content:

`init.lua.tt.template`:

``` lua
local app_name = {{.name}}
local login = {{.user_name}}

require("fiber").sleep(1)
```

`tt.yaml`:

```yaml
{}
```

`MANIFEST.yaml`:

``` yaml
description: Simple app
vars:
    - prompt: User name
      name: user_name
      default: admin
      re: ^\w+$
```

`init.lua.tt.template` in this example contains an application code.
After instantiation, `.tt.template` suffix is removed from the file
name.

Create `./tt.yaml` and add templates search path to it:

``` yaml
  templates:
    - path: ./templates
```

Here is how the current directory structure looks like:

``` text
    ./
    ├── tt.yaml
    └── templates
        └── simple
            ├── init.lua.tt.template
            ├── tt.yaml
            └── MANIFEST.yaml
```

Directory name `simple` can now be used as template name in create
command. Create an application from the `simple` template and type
`user1` in `User name` prompt:

``` console
$ tt create simple --name simple_app
• Creating application in <current_directory>/simple_app
• Using template from <current_directory>/templates/simple
User name (default: admin): user1
```

Your application will appear in the `simple_app` directory with the
following content:

``` text
    simple_app/
    ├── init.lua
    └── tt.yaml
```

The generated `tt.yaml` makes `simple_app` an independent application root.
Change into that directory before running lifecycle commands for the new
application.

Instantiated `init.lua` content:

``` lua
local app_name = simple_app
local login = user1

require("fiber").sleep(1)
```

## Working with tt cluster (experimental)

`tt cluster` module is used to manage a Tarantool 3 cluster configuration.
Since Tarantool 3 has not yet been released, this module may still change
in the future.

The module has following commands:

- `tt cluster show SOURCE` - to show a cluster configuration from the
    `SOURCE`.
- `tt cluster publish SOURCE config.yaml` - to publish a cluster
    configuration to the `SOURCE`.

The `SOURCE` could be:

- An application name or application:instance name.
- An etcd URI. In this case you could specify an instance name as an URI
  argument `name`.

The simplest logic in case of the etcd `SOURCE`. `tt cluster` just shows or
publishes a cluster from etcd configuration within a specified prefix.

As an example, let's assume we are running etcd on `localhost:2379` (the
default host and port). We also has two files with a cluster and an instance
configuration:

`cluster.yaml`:

```yaml
groups:
  group_name:
    replicasets:
      replicaset_name:
        instances:
          instance1:
            iproto:
              listen:
              - uri: 127.0.0.1:3384
          instance2:
            iproto:
              listen:
              - uri: 127.0.0.1:3385
```

`instance.yaml`:

```yaml
iproto:
  listen:
  - uri: 127.0.0.1:3389
  threads: 10
```

Let's publish and show the configurations with a prefix `/tt`:

```text
$ tt cluster publish "http://localhost:2379/tt" cluster.yaml
$ tt cluster show "http://localhost:2379/tt"
groups:
  group_name:
    replicasets:
      replicaset_name:
        instances:
          instance1:
            iproto:
              listen:
              - uri: 127.0.0.1:3384
          instance2:
            iproto:
              listen:
              - uri: 127.0.0.1:3385
$ tt cluster show "http://localhost:2379/tt?name=instance2"
iproto:
  listen:
  - uri: 127.0.0.1:3385
```

At now we could update an instance configuration and show the result:

```text
$ tt cluster publish "http://localhost:2379/tt?name=instance2" instance.yaml
$ tt cluster show "http://localhost:2379/tt"
groups:
  group_name:
    replicasets:
      replicaset_name:
        instances:
          instance1:
            iproto:
              listen:
              - uri: 127.0.0.1:3384
          instance2:
            iproto:
              listen:
              - uri: 127.0.0.1:3389
              threads: 10
$ tt cluster show "http://localhost:2379/tt?name=instance2"
iproto:
  listen:
  - uri: 127.0.0.1:3389
  threads: 10
```

You could see the configuration in etcd with the command:

```sh
etcdctl get --prefix "/tt/"
```

The same works for an application configuration. The following commands are
run from the `test_app` application root, which contains `tt.yaml`:

```text
$ tt cluster publish test_app cluster.yaml
$ tt cluster publish test_app:instance2 instance.yaml
$ tt cluster show test_app
groups:
  group_name:
    replicasets:
      replicaset_name:
        instances:
          instance1:
            iproto:
              listen:
              - uri: 127.0.0.1:3384
          instance2:
            iproto:
              listen:
              - uri: 127.0.0.1:3389
              threads: 10
$ tt cluster show test_app:instance2
iproto:
  listen:
  - uri: 127.0.0.1:3389
  threads: 10
```

The configuration is published to `config.yaml` in the application directory.

But `tt cluster show` is a little more complicated for the application. It
collects configuration from all data sources (environment variables, etcd) and
shows a combined configuration with the same logic as Tarantool:

```text
$ TT_APP_FILE=init.lua tt cluster show test_app:instance2
app:
  file: init.lua
iproto:
  listen:
  - uri: 127.0.0.1:3389
  threads: 10
```

This is done to help the user see the actual configuration that will be used
by Tarantool.

To view all available options for the commands, use the help command:

```sh
tt cluster show --help
tt cluster publish --help
```

## Packaging applications

`tt package pack` builds a manifest-based application and writes a
reproducible `.tt` archive. The application root must contain
`app.manifest.toml`; for example:

```toml
manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=2.11.0'

[components.app]
path = '.'
include = ['*.lua']

[products.default]
components = ['app']
default = true
```

Run the command from the application root:

```console
$ tt package pack
_build/pack/my-app-1.0.0-linux-amd64.tt
```

The command performs the same dependency resolution and component build as
`tt package build`; a separate build is not required.

## Working with tt daemon (experimental)

`tt daemon` module is used to manage `tt` running on the background on a
given machine. This way instances can be operated remotely. Daemon can
be configured with `tt_daemon.yaml` config.

You can manage TT daemon with following commands:

- `tt daemon start` - launch of a daemon
- `tt daemon stop` - terminate of the daemon
- `tt daemon status` - get daemon status
- `tt daemon restart` - daemon restart

Work scenario:

First, TT daemon should be started on the server side:

``` console
$ tt daemon start
• Starting tt daemon...
```

After daemon launch you can check its status on the server side:

``` console
$ tt daemon status
• RUNNING. PID: 6189.
```

To send request to daemon you can use CURL. In this example the client sends a
request to start the `test_app` application. The daemon is started in the
`test_app` application root, which contains `tt.yaml` and the application
files.

``` sh
curl --header "Content-Type: application/json" --request POST \
--data '{"command_name":"start", "params":["test_app"]}' \
http://127.0.0.1:1024/tarantool
{"res":"   • Starting an instance [test_app]...\n"}
```

Below is an example of running a command with flags.

Flag with argument:

``` sh
curl --header "Content-Type: application/json" --request POST \
--data '{"command_name":"version", "params":["-L", "/path/to/local/dir"]}' \
http://127.0.0.1:1024/tarantool
{"res":"Tarantool CLI version 0.1.0, darwin/amd64. commit: bf83f33\n"}
```

Flag without argument:

``` sh
curl --header "Content-Type: application/json" --request POST \
--data '{"command_name":"version", "params":["-V"]}' \
http://127.0.0.1:1024/tarantool
{"res":"Tarantool CLI version 0.1.0, darwin/amd64. commit: bf83f33\n
 • Tarantool executable found: '/usr/local/bin/tarantool'\n"}
```

## Transition from tarantoolctl to tt

### Commands difference

`tarantoolctl enter/connect/eval` functionality is covered by
`tt connect` command.

`tarantoolctl`:

```text
    $ tarantoolctl enter app1
    connected to unix/:./run/tarantool/app1.control
    unix/:./run/tarantool/app1.control>

    $ tarantoolctl connect localhost:3301
    connected to localhost:3301
    localhost:3301>

    $ tarantoolctl eval app1 eval.lua
    connected to unix/:./run/tarantool/app1.control
    ---
    - 42
    ...

    $ cat eval.lua | tarantoolctl eval app1
    connected to unix/:./run/tarantool/app1.control
    ---
    - 42
    ...
```

`tt` analog follows. The name-based commands are run from the `app1`
application root containing `tt.yaml`; URI-based connections do not require
application discovery:

```text
    $ tt connect app1
    • Connecting to the instance...
    • Connected to /home/user/run/tarantool/app1/app1.control

    /home/user/run/tarantool/app1/app1.control>

    $ tt connect localhost:3301
    • Connecting to the instance...
    • Connected to localhost:3301

    localhost:3301>

    $ tt connect app1 -f eval.lua
    ---
    - 42
    ...

    $ cat eval.lua | tt connect app1 -f -
    ---
    - 42
    ...
```
