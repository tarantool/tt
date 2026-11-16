# Run an application in a Docker container

An application packed by `tt package pack` carries its dependencies and the
Tarantool it runs on, so an image of it needs only a base image with glibc,
a tt and the archive. The image installs the archive into the system scope,
`/opt/tarantool`, and runs its instances in the foreground with
`tt instance start --foreground`, the way a unit of systemd does (see
[Run instances under systemd](systemd.md)).

You need:

- the archive of the application with its dependencies, such as
  `hello-1.0.0-linux-amd64.tt` from
  [Your first package](../get-started/first-package.md), packed without
  `--without-deps`;
- a tt 3 for Linux of the architecture of the image, the one in `PATH`
  for example: it installs the archive and runs the instances. A tt built
  from source needs a base image whose glibc is not older than the one of
  the host it was built on.

## Build the image

Put the tt and the archive next to the `Dockerfile`:

```console
$ mkdir image && cd image
$ cp "$(command -v tt)" ../hello/_build/pack/hello-1.0.0-linux-amd64.tt .
```

`Dockerfile`:

```dockerfile
FROM debian:stable-slim
COPY tt /usr/local/bin/tt
COPY hello-1.0.0-linux-amd64.tt /tmp/
RUN tt package install --scope system /tmp/hello-1.0.0-linux-amd64.tt \
 && rm /tmp/hello-1.0.0-linux-amd64.tt
VOLUME /opt/tarantool/var
ENTRYPOINT ["tt", "instance", "start", "--foreground", "--scope", "system"]
CMD ["hello:*"]
```

```console
$ docker build -t hello .
```

`tt package install` needs no network: the Tarantool and the tt the archive
bundles go into `/opt/tarantool/versions/`. `VOLUME` keeps the data of the
instances, `/opt/tarantool/var/lib/hello/`, out of the layers of the
container.

## Run the container

```console
$ docker run -d --name hello hello
$ docker logs hello 2>&1 | tail -1
... main main.cc:1077 I> entering the event loop
$ docker exec hello tt instance status 'hello:*'
 INSTANCE            STATUS   PID  MODE  CONFIG  BOX      UPSTREAM
 hello:instance-001  RUNNING  20   RW    ready   running  --
$ echo "return require('hello').greet('docker')" | docker exec -i hello tt connect hello:instance-001 -f-
---
- Hello, docker!
...
$ docker stop hello && docker inspect -f '{{.State.ExitCode}}' hello
hello
0
$ docker rm hello
```

The output of the instances goes to the log of the container.
`docker stop` sends SIGTERM, which stops every instance; tt exits 0 when they
stopped in time (see [Instances](../concepts/instances.md)). `docker exec`
runs the other instance commands, `tt connect` among them, inside the
container. `CMD` selects the instances: `docker run -d hello
hello:instance-001` runs one instance of the application, which is the way
to run one container for each instance.

## Keep the data

Give the volume a name to keep the data of the instances across containers:

```console
$ docker run -d --name hello -v hello-data:/opt/tarantool/var hello
$ echo "box.schema.space.create('greetings'):create_index('pk') return box.space.greetings:insert({1, 'hi'})" | docker exec -i hello tt connect hello:instance-001 -f-
---
- [1, 'hi']
...
$ docker rm -f hello
hello
$ docker run -d --name hello -v hello-data:/opt/tarantool/var hello
$ echo "return box.space.greetings:select()" | docker exec -i hello tt connect hello:instance-001 -f-
---
- - [1, 'hi']
...
```

## Publish a port

The instances listen where the cluster config of the application says. The
config of `hello` listens on `127.0.0.1:3311`, which is not reachable from
outside the container, so `-p` alone publishes nothing. Give the container
the pair of the host, `/opt/tarantool/etc/hello/`, which wins over the
templates of the installed version (see
[Instances](../concepts/instances.md#the-pair)), with a config that listens
on every address and a user to connect as, whose password comes from the
environment of the container rather than from the file.

`etc/config.yaml`:

```yaml
app:
  module: hello

config:
  context:
    password:
      from: env
      env: HELLO_PASSWORD

credentials:
  users:
    hello:
      password: '{{ context.password }}'
      roles: [super]

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            iproto:
              listen:
              - uri: 0.0.0.0:3311
```

`etc/instances.yaml`, the instances that run in this container:

```yaml
instance-001:
```

```console
$ docker run -d --name hello -p 3311:3311 -e HELLO_PASSWORD=secret \
    -v "$PWD/etc:/opt/tarantool/etc/hello:ro" hello
$ echo "return require('hello').greet('the host')" | tt connect hello:secret@127.0.0.1:3311 -f-
---
- Hello, the host!
...
$ docker rm -f hello
```

<!-- Verified on 2026-11-16 with debian:stable-slim, Tarantool 3.9.0 and tt
built from bigbes/gh-no-demo-qa-cli (8250b7c1 + fixes). -->
