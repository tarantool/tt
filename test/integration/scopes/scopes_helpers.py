"""Helpers of the end-to-end tests of applications installed into the user
and the system scopes: a project of myapp the tests pack and install, the
scope roots they install into and the tarantool installations under them."""

import contextlib
import json
import os
import re
import shlex
import shutil
import signal
import subprocess
import time
from pathlib import Path

# The constraint of [platform].tarantool of myapp: a tarantool of the scope
# that does not satisfy it, 4.0.0, is installed next to the one that does to
# show that it is not run.
TARANTOOL_CONSTRAINT = ">=3.0.0,<4.0.0"

MANIFEST = """manifest_version = '0.1'

[package]
name = '{name}'
include = ['app.lua', 'bin/worker.sh']

[platform]
tarantool = '{tarantool}'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = 'src'
"""

# The application file of the Tarantool instances of myapp: it tells the
# tests which version of myapp an instance runs, in which working directory
# and on which tarantool. The tarantool is the executable tt runs, as
# tarantool sees it, or the installation a wrapper of tarantool_installation
# names.
APP_LUA = """\
local fio = require('fio')
local log = require('log')

local file = io.open('VERSION')
local version = file:read('*l')
file:close()

log.info('myapp %s runs in %s on %s', version, fio.cwd(),
         os.getenv('MYAPP_TARANTOOL') or arg[-1])
"""

# The external process of myapp: it tells the tests which version of myapp
# it comes from and in which working directory it runs, then waits for
# SIGTERM.
WORKER_SH = """\
#!/bin/sh
version=$(cat "$(dirname "$0")/../VERSION")
echo "worker of myapp $version runs in $(pwd -P)"
trap 'echo "worker got TERM"; exit 0' TERM
while :; do
\tsleep 1 &
\twait $!
done
"""

# The pair of the default template of myapp: a Tarantool instance and an
# external process. The instances bind no TCP port: tt gives a Tarantool
# instance a unix socket of its run directory.
DEFAULT_INSTANCES = """\
storage:
worker:
  binary: ./bin/worker.sh
"""


def cluster_config(*instances):
    """Return the cluster config of the Tarantool instances named instances,
    one replicaset each, which run app.lua."""
    replicasets = "".join(
        f"      {name}-rs:\n        instances:\n          {name}: {{}}\n" for name in instances
    )
    return f"app:\n  file: app.lua\ngroups:\n  group-001:\n    replicasets:\n{replicasets}"


DEFAULT_TEMPLATE = (DEFAULT_INSTANCES, cluster_config("storage"))


def make_project(path: Path, version: str, templates=None, name="myapp") -> Path:
    """Write the project of myapp version in path, without dependencies,
    with templates, a map of a template name to the pair of its
    instances.yaml and config.yaml, the default template by default. The
    package is called name."""
    templates = templates or {"default": DEFAULT_TEMPLATE}
    (path / "src").mkdir(parents=True)
    (path / "src" / "init.lua").write_text("return {}\n")
    (path / "bin").mkdir()
    (path / "bin" / "worker.sh").write_text(WORKER_SH)
    (path / "bin" / "worker.sh").chmod(0o755)
    (path / "app.lua").write_text(APP_LUA)
    (path / "VERSION").write_text(version + "\n")
    (path / "app.manifest.toml").write_text(
        MANIFEST.format(name=name, tarantool=TARANTOOL_CONSTRAINT),
    )
    for template_name, (instances, config) in templates.items():
        template = path / ".templates" / template_name
        template.mkdir(parents=True)
        (template / "instances.yaml").write_text(instances)
        (template / "config.yaml").write_text(config)
    return path


def runtime_cache(path: Path) -> dict:
    """Make path the user cache directory with a runtime cache holding a tt
    that satisfies [platform].tt of myapp, and return the environment of tt
    that uses it.

    A with-deps archive bundles tarantool and tt: the tarantool on PATH is
    bundled as the active one, while the tt under test reports no version
    a constraint could accept, so the cache supplies one. It is a stub: no
    test runs the tt an archive bundles."""
    stub = path / "tt" / "runtimes" / "tt" / "ce" / "3.1.0" / "bin" / "tt"
    stub.parent.mkdir(parents=True)
    stub.write_text("#!/bin/sh\necho 'the tt bundled into myapp is not to be run' >&2\nexit 1\n")
    stub.chmod(0o755)
    return dict(os.environ, XDG_CACHE_HOME=str(path))


def pack(tt_cmd, project: Path, with_deps=False, env=None) -> Path:
    """Pack project, with dependencies or without them, and return the
    archive."""
    args = [tt_cmd, "package", "pack"]
    if not with_deps:
        args.append("--without-deps")
    p = subprocess.run(args, cwd=project, env=env, capture_output=True, text=True)
    assert p.returncode == 0, p.stderr
    archives = list((project / "_build" / "pack").glob("*.tt"))
    assert len(archives) == 1, archives
    return archives[0]


def tarantool_version() -> str:
    """Return the version of the tarantool on PATH: the release it is built
    from, 3.8.1 of 3.8.1-0-ge643f88a5e0c."""
    out = subprocess.run(
        ["tarantool", "--version"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    match = re.match(r"Tarantool\s+(Enterprise\s+)?(\d+\.\d+\.\d+)", out)
    assert match is not None, out
    return match.group(2)


def tarantool_installation(root: Path, version: str) -> Path:
    """Return the executable of the installation of tarantool version under
    the scope root root, as tt binary lays it out."""
    return root / "versions" / "tarantool" / f"tarantool_{version}" / "tarantool"


def install_tarantool(root: Path, version: str) -> Path:
    """Install tarantool version into the scope root root by hand, as
    tt binary would, and return its executable: a wrapper of the tarantool on
    PATH that tells the application which installation it is."""
    path = tarantool_installation(root, version)
    path.parent.mkdir(parents=True)
    path.write_text(
        f"#!/bin/sh\nMYAPP_TARANTOOL={shlex.quote(str(path))} "
        f'exec {shlex.quote(shutil.which("tarantool"))} "$@"\n',
    )
    path.chmod(0o755)
    return path


def install_decoy_tarantool(root: Path, version: str) -> Path:
    """Install a tarantool version into the scope root root by hand that
    fails, telling so, if it is ever run, and return its executable."""
    path = tarantool_installation(root, version)
    path.parent.mkdir(parents=True)
    path.write_text(f"#!/bin/sh\necho 'tarantool {version} is not to be run' >&2\nexit 1\n")
    path.chmod(0o755)
    return path


class Scope:
    """An installed scope, user or system, of the root root: runs tt with the
    root, and stops what runs under it once the test is over."""

    def __init__(self, tt_cmd, name: str, root: Path, cwd: Path, env=None):
        self.tt_cmd = tt_cmd
        self.name = name
        self.root = root
        self.cwd = cwd
        # The other scope gets a root of its own, so that nothing is looked
        # up or installed outside of the test.
        other = root.parent / ("system" if name == "user" else "user")
        self.env = dict(
            env or os.environ,
            TT_USER_ROOT=str(root if name == "user" else other),
            TT_SYSTEM_ROOT=str(root if name == "system" else other),
        )
        self.env.pop("TT_SYSTEMD_UNIT", None)

    def tt(self, *args):
        """Run tt with args; return the exit code, the output and the error
        output."""
        p = subprocess.run(
            [self.tt_cmd, *args],
            cwd=self.cwd,
            env=self.env,
            capture_output=True,
            text=True,
        )
        print(f"$ tt {' '.join(args)}\n{p.stdout}{p.stderr}")
        return p.returncode, p.stdout, p.stderr

    def install(self, archive: Path):
        rc, _, err = self.tt("package", "install", "--scope", self.name, str(archive))
        assert rc == 0, err

    def status(self, *args):
        """Return tt instance status of myapp:* with args as JSON."""
        rc, out, err = self.tt(
            "instance",
            "status",
            "--scope",
            self.name,
            "myapp:*",
            "-o",
            "json",
            *args,
        )
        assert rc == 0, err
        return json.loads(out)

    def var(self, kind, inst):
        """Return the directory of the runtime artifacts of kind, run, log or
        lib, of the instance inst of myapp."""
        return self.root / "var" / kind / "myapp" / inst

    def log(self, inst):
        """Return what tt.log of the instance inst of myapp holds."""
        path = self.var("log", inst) / "tt.log"
        return path.read_text(errors="replace") if path.exists() else ""

    def pid(self, inst, name="tt.pid"):
        """Return the pid the pid file name of the instance inst of myapp
        names, 0 when there is none."""
        try:
            return int((self.var("run", inst) / name).read_text().strip())
        except (OSError, ValueError):
            return 0

    def version_dir(self, version):
        return self.root / "versions" / "myapp" / f"myapp_{version}"

    def cleanup(self):
        kill_instances(self.root)


def kill_instances(root: Path, settle=False):
    """Kill the process groups of the watchdogs and the external processes
    that still run under the scope root root: a failed test leaves no
    instance behind. With settle, a second is given first to the watchdogs
    a test did not expect to start, which write their pid files once they
    are up."""
    if settle:
        time.sleep(1)
    for pid_file in [*root.glob("var/run/*/*/tt.pid"), *root.glob("var/run/*/*/process.pid")]:
        with contextlib.suppress(OSError, ValueError):
            os.killpg(int(pid_file.read_text().strip()), signal.SIGKILL)


def wait(timeout, condition, interval=0.2):
    """Wait timeout seconds for condition to hold; return whether it did."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if condition():
            return True
        time.sleep(interval)
    return condition()


def running(scope: Scope, *instances, template=None):
    """Return whether the instances of myapp all run, as tt instance status
    reports."""
    args = ["--template", template] if template else []
    status = scope.status(*args)
    return all(status.get(f"myapp:{inst}", {}).get("status") == "RUNNING" for inst in instances)
