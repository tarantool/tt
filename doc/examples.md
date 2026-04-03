# Examples

This file contains various examples of working with tt.

## Contents

- [Working with a set of instances](#working-with-a-set-of-instances)
- [Working with application templates](#working-with-application-templates)
- [Working with tt cluster (experimental)](#working-with-tt-cluster-experimental)
- [Packing environments](#packing-environments)
- [Working with tt daemon (experimental)](#working-with-tt-daemon-experimental)
- [Transition from tarantoolctl to tt](#transition-from-tarantoolctl-to-tt)
  + [System-wide configuration](#system-wide-configuration)
  + [Commands difference](#commands-difference)

## Working with a set of instances

For example, we want to launch two instances based on one `init.lua`
file and one instance based on the `router.init.lua` file. In order to
do this, we create a directory called `demo` with the content:

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

For starting all instances of environment, run:

``` console
$ tt start
• Starting an instance [demo:router]...
• Starting an instance [demo:master]...
• Starting an instance [demo:replica]...
• Starting an instance [demo_single_instance_app]...
```

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
    ├── Dockerfile.build.tt
    └── init.lua
```

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

The same works for an application configuration:

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

## Packing environments

For example, we want to pack a single application. Here is the content
of the sample application:

```text
    single_environment/
    ├── tt.yaml
    └── init.lua
```

`tt.yaml`:

``` yaml
app:
```

For packing it into tarball, call:

``` console
$ tt pack tgz
   • Apps to pack: single_environment
   • Generating new tt.yaml for the new package.
   • Creating tarball.
   • Bundle is packed successfully to /Users/dev/tt_demo/single_environment/single_environment_0.1.0.0.x86_64.tar.gz.
```

In the case of a single application, a sub-directory is created for it
within the resulting package. This directory contains the application's code,
`tt` configuration file, and the "bin" directory with executable files:
`tt` and `tarantool`. The file structure of the resulting package can be
seen below:

``` text
    unpacked_dir/
    └── single_environment
        ├── bin
        │   ├── tarantool
        │   └── tt
        ├── init.lua
        └── tt.yaml
```

`tt` also supports multiple applications environment. Here's how a typical
packed environment for multiple applications looks like:

```text
    bundle/
    ├── bin
    │   ├── tarantool
    │   └── tt
    ├── instances.enabled
    │   ├── multi -> ../multi
    │   ├── script_app.lua -> ../script.lua
    │   └── single -> ../single
    ├── multi
    │   ├── init.lua
    │   ├── instances.yaml
    │   └── var
    ├── script.lua
    ├── single
    │   ├── init.lua
    │   └── var
    └── tt.yaml
```

`tt.yaml`:

``` yaml
env:
  bin_dir: bin
  inc_dir: include
  instances_enabled: instances.enabled
  restart_on_failure: false
modules:
  directory: modules
app:
  run_dir: var/run
  log_dir: var/log
  wal_dir: var/lib
  vinyl_dir: var/lib
  memtx_dir: var/lib
ee:
  credential_path: ""
templates:
- path: templates
repo:
  rocks: ""
  distfiles: distfiles
```

Pay attention, that all absolute symlinks from
`instances_enabled` will be resolved, all
sources will be copied to the result package and the final
instances_enabled directory will contain only relative links.

For packing deb package call:

``` console
$ tt pack deb --name dev_bundle --version 1.0.0
• A root for package is located in: /var/folders/c6/jv1r5h211dn1280d7580000gp/T/2166098848
   • Apps to pack: app1 app2 app3 app4 app5

myapp scm-1 is now installed in /var/folders/c6/jv1r5h211dn1280d7580000gp/T/tt_pack4173588242/myapp/.rocks

   • myapp rocks are built successfully
   • Generating new tt.yaml for the new package
   • Initialize the app directory for prefix: data/usr/share/tarantool/bundle
   • Create data tgz
   • Created control in /var/folders/***/control_dir
   • Created result DEB package: /var/folders/***/T/tt_pack4173588242
```

Now the result package may be distributed and installed using dpkg
command. The package will be installed in
/usr/share/tarantool/package_name directory.

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

To send request to daemon you can use CURL. In this example the client
sends a request to start `test_app` instance on the server side. Note:
directory `test_app` (or file `test_app.lua`) exists on the server side.

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

### System-wide configuration

`tt` packages come with a system-wide environment configuration which
supports `tarantoolctl` configuration defaults. So, after installation
from repository `tt` can be used along with `tarantoolctl` for managing
applications instances. Here is an example:

``` text
$ sudo tt instances
List of enabled applications:
• example

$ tarantoolctl start example
Starting instance example...
Forwarding to 'systemctl start tarantool@example'

$ tarantoolctl status example
Forwarding to 'systemctl status tarantool@example'
● tarantool@example.service - Tarantool Database Server
    Loaded: loaded (/lib/systemd/system/tarantool@.service; enabled; vendor preset: enabled)
    Active: active (running)
    Docs: man:tarantool(1)
    Main PID: 6698 (tarantool)
. . .

$ sudo tt status
• example: RUNNING. PID: 6698.

$ sudo tt connect example
• Connecting to the instance...
• Connected to /var/run/tarantool/example.control

/var/run/tarantool/example.control>

$ sudo tt stop example
• The Instance example (PID = 6698) has been terminated.

$ tarantoolctl status example
Forwarding to 'systemctl status tarantool@example'
○ tarantool@example.service - Tarantool Database Server
    Loaded: loaded (/lib/systemd/system/tarantool@.service; enabled; vendor preset: enabled)
    Active: inactive (dead)
```

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

`tt` analog:

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

