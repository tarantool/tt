"""End-to-end coverage of the applications installed into the user and the
system scopes: tt package pack -> tt package install --scope ->
tt instance start, status and stop -> tt package uninstall.

The application, myapp, has a Tarantool instance of the cluster config and an
external process in its templates. It is packed from a project the test
writes, without dependencies, so that it packs and installs offline. The
tarantool it runs on is installed into the scope the way tt binary lays it
out: by tt package install from a with-deps archive, or by hand, never
downloaded.
"""

import os
import platform
import re
import signal
import subprocess

import pytest
from integration.scopes.scopes_helpers import (
    DEFAULT_INSTANCES,
    DEFAULT_TEMPLATE,
    cluster_config,
    install_decoy_tarantool,
    install_tarantool,
    kill_instances,
    make_project,
    pack,
    running,
    runtime_cache,
    tarantool_installation,
    tarantool_version,
    wait,
)

import utils

pytestmark = pytest.mark.skipif(
    not utils.is_tarantool_3(),
    reason="the instances of myapp run on Tarantool 3",
)


def assert_runs_from(scope, inst, version, tarantool, template=None):
    """Assert that the Tarantool instance inst of myapp, of template, runs
    version of myapp in its version directory on tarantool, keeping its
    runtime artifacts under the root of scope."""
    version_dir = os.path.realpath(scope.version_dir(version))
    marker = f"myapp {version} runs in {version_dir} on {tarantool}"
    assert wait(10, lambda: marker in scope.log(inst)), scope.log(inst)

    status = scope.status(*(["--template", template] if template else []))
    assert scope.pid(inst) == status[f"myapp:{inst}"]["pid"]
    assert (scope.var("run", inst) / "tarantool.control").is_socket()
    assert list(scope.var("lib", inst).glob("*.snap")), "the data goes under the root"


def assert_worker_runs_from(scope, inst, version):
    """Assert that the external process inst of myapp runs from version of
    myapp in its data directory under the root of scope (decision 33)."""
    data = os.path.realpath(scope.var("lib", inst))
    marker = f"worker of myapp {version} runs in {data}"
    assert wait(10, lambda: marker in scope.log(inst)), scope.log(inst)
    assert scope.pid(inst, "process.pid") != 0


def test_user_scope(tt_cmd, tmp_path, user_scope):
    # A with-deps archive bundles the tarantool on PATH: tt package install
    # lays it into versions/ of the user root, and the application runs on
    # it, as its lock names it, never on the tarantool on PATH.
    project = make_project(tmp_path / "myapp", "1.0.0")
    archive = pack(tt_cmd, project, with_deps=True, env=runtime_cache(tmp_path / "cache"))
    user_scope.install(archive)

    root = user_scope.root
    assert os.readlink(root / "apps" / "myapp") == os.path.join(
        "..",
        "versions",
        "myapp",
        "myapp_1.0.0",
    )
    lock = (user_scope.version_dir("1.0.0") / "app.manifest.lock").read_text()
    bundled = re.search(r"bundled_tarantool_version = '([^']+)'", lock).group(1)
    assert tarantool_version() in bundled
    # The installation of Community or Enterprise, which PATH holds.
    installed = list(root.glob(f"versions/tarantool*/tarantool*{bundled}/tarantool"))
    assert len(installed) == 1, installed
    tarantool = installed[0]
    assert tarantool.is_file() and not tarantool.is_symlink()

    rc, _, err = user_scope.tt("instance", "start", "--scope", "user", "myapp:*")
    assert rc == 0, err
    assert wait(10, lambda: running(user_scope, "storage", "worker")), user_scope.status()

    # tarantool tells its executable by the real path.
    assert_runs_from(user_scope, "storage", "1.0.0", os.path.realpath(tarantool))
    assert_worker_runs_from(user_scope, "worker", "1.0.0")
    assert sorted(os.listdir(root / "var")) == ["lib", "log", "run"]
    assert not (user_scope.version_dir("1.0.0") / "var").exists()

    # The running instances keep the application installed.
    rc, _, err = user_scope.tt("package", "uninstall", "--scope", "user", "myapp", "--yes")
    assert rc == 1
    assert "the application has running instances" in err
    assert user_scope.version_dir("1.0.0").is_dir()

    rc, _, err = user_scope.tt("instance", "stop", "--scope", "user", "myapp:*", "--yes")
    assert rc == 0, err
    status = user_scope.status()
    assert status["myapp:storage"]["status"] == "NOT RUNNING"
    assert status["myapp:worker"]["status"] == "NOT RUNNING"

    # uninstall removes the application and keeps the data and the logs of
    # its instances, and the runtime other applications may run on.
    rc, _, err = user_scope.tt("package", "uninstall", "--scope", "user", "myapp", "--yes")
    assert rc == 0, err
    assert not (root / "apps" / "myapp").exists()
    assert not (root / "versions" / "myapp").exists()
    assert tarantool.is_file()
    assert list(user_scope.var("lib", "storage").glob("*.snap"))
    assert "worker got TERM" in user_scope.log("worker")


def test_pack_bundles_a_release_quietly(tt_cmd, tmp_path):
    # The tarantool a with-deps archive bundles is a build of a release tag:
    # pack records it as the release and has nothing to warn about.
    version = subprocess.run(
        ["tarantool", "--version"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    if not re.match(r"Tarantool \d+\.\d+\.\d+-0-g[0-9a-f]+\n", version):
        pytest.skip("the tarantool on PATH is not a Community build of a release tag")

    project = make_project(tmp_path / "myapp", "1.0.0")
    p = subprocess.run(
        [tt_cmd, "package", "pack"],
        cwd=project,
        env=runtime_cache(tmp_path / "cache"),
        capture_output=True,
        text=True,
    )
    assert p.returncode == 0, p.stderr
    assert "prerelease" not in p.stderr


# The pair of the host of myapp: other instances than those of its templates.
HOST_INSTANCES = """\
host-storage:
host-worker:
  binary: ./bin/worker.sh
"""


def test_system_scope_host_pair(tt_cmd, tmp_path, system_scope):
    # The pair of the host in <root>/etc/myapp/ takes the place of the
    # templates of the installed version (decision 9). A --without-deps
    # archive bundles no tarantool: the application runs on the highest one
    # of the scope that satisfies its [platform].tarantool, not on the one the
    # scope selects in bin/, nor on the one on PATH (decision 17).
    project = make_project(tmp_path / "myapp", "1.0.0")
    system_scope.install(pack(tt_cmd, project))

    root = system_scope.root
    tarantool = install_tarantool(root, tarantool_version())
    decoy = install_decoy_tarantool(root, "4.0.0")
    (root / "bin").mkdir()
    os.symlink(decoy, root / "bin" / "tarantool")

    host = root / "etc" / "myapp"
    host.mkdir(parents=True)
    (host / "instances.yaml").write_text(HOST_INSTANCES)
    (host / "config.yaml").write_text(cluster_config("host-storage"))

    for command in [["start"], ["status"]]:
        rc, _, err = system_scope.tt("instance", *command, "myapp:*", "--template", "default")
        assert rc == 1
        assert "--template conflicts with the host pair" in err

    rc, _, err = system_scope.tt("instance", "start", "myapp:storage")
    assert rc == 1
    assert '"storage" of "myapp" not in' in err
    assert str(host / "instances.yaml") in err

    # Outside of a project myapp:* is the application of the system scope.
    rc, _, err = system_scope.tt("instance", "start", "myapp:*")
    assert rc == 0, err
    assert wait(10, lambda: running(system_scope, "host-storage", "host-worker"))
    assert sorted(system_scope.status()) == ["myapp:host-storage", "myapp:host-worker"]

    assert_runs_from(system_scope, "host-storage", "1.0.0", tarantool)
    assert_worker_runs_from(system_scope, "host-worker", "1.0.0")
    assert "is not to be run" not in system_scope.log("host-storage")

    rc, _, err = system_scope.tt("package", "uninstall", "--scope", "system", "myapp", "--yes")
    assert rc == 1
    assert "the application has running instances" in err

    rc, _, err = system_scope.tt("instance", "stop", "--scope", "system", "myapp:*", "--yes")
    assert rc == 0, err

    rc, _, err = system_scope.tt("package", "uninstall", "--scope", "system", "myapp", "--yes")
    assert rc == 0, err
    assert not (root / "versions" / "myapp").exists()
    assert (host / "instances.yaml").read_text() == HOST_INSTANCES
    assert (host / "config.yaml").is_file()
    assert list(system_scope.var("lib", "host-storage").glob("*.snap"))


# The second template of myapp, with instances of its own.
OTHER_TEMPLATE = (
    "replica:\nhelper:\n  binary: ./bin/worker.sh\n",
    cluster_config("replica"),
)


def test_system_scope_template(tt_cmd, tmp_path, system_scope):
    # Without the files of the host, --template chooses among the templates
    # of the installed version.
    project = make_project(
        tmp_path / "myapp",
        "1.0.0",
        templates={"default": DEFAULT_TEMPLATE, "other": OTHER_TEMPLATE},
    )
    system_scope.install(pack(tt_cmd, project))
    tarantool = install_tarantool(system_scope.root, tarantool_version())

    rc, _, err = system_scope.tt(
        "instance",
        "start",
        "--scope",
        "system",
        "--template",
        "other",
        "myapp:*",
    )
    assert rc == 0, err
    assert wait(10, lambda: running(system_scope, "replica", "helper", template="other"))

    assert_runs_from(system_scope, "replica", "1.0.0", tarantool, template="other")
    assert_worker_runs_from(system_scope, "helper", "1.0.0")

    # The default template is another pair: none of its instances runs.
    status = system_scope.status()
    assert sorted(status) == ["myapp:storage", "myapp:worker"]
    assert all(inst["status"] == "NOT RUNNING" for inst in status.values())

    rc, _, err = system_scope.tt(
        "instance",
        "stop",
        "--scope",
        "system",
        "--template",
        "other",
        "myapp:*",
        "--yes",
    )
    assert rc == 0, err
    status = system_scope.status("--template", "other")
    assert all(inst["status"] == "NOT RUNNING" for inst in status.values())


def crash(scope, inst, pid_file):
    """Kill the process of the instance inst of myapp that the pid file
    pid_file names, and wait for its watchdog to run it again."""
    failed = scope.pid(inst, pid_file)
    assert failed != 0
    os.kill(failed, signal.SIGKILL)

    def restarted():
        pid = scope.pid(inst, pid_file)
        return pid not in (0, failed) and os.path.exists(f"/proc/{pid}")

    assert wait(20, restarted), scope.log(inst)


@pytest.mark.skipif(platform.system() != "Linux", reason="the restart is seen through /proc")
def test_restart_takes_the_active_version(tt_cmd, tmp_path, user_scope):
    # An instance the watchdog restarts after a failure stays on the version
    # it started from; tt instance restart runs the active one (decision 29).
    restart = "  restart: on-failure\n"
    instances = DEFAULT_INSTANCES.replace("storage:\n", "storage:\n" + restart) + restart
    templates = {"default": (instances, cluster_config("storage"))}
    first = pack(tt_cmd, make_project(tmp_path / "1.0.0", "1.0.0", templates))
    second = pack(tt_cmd, make_project(tmp_path / "2.0.0", "2.0.0", templates))
    tarantool = install_tarantool(user_scope.root, tarantool_version())

    user_scope.install(first)
    rc, _, err = user_scope.tt("instance", "start", "--scope", "user", "myapp:*")
    assert rc == 0, err
    assert wait(10, lambda: running(user_scope, "storage", "worker"))
    assert_runs_from(user_scope, "storage", "1.0.0", tarantool)
    assert_worker_runs_from(user_scope, "worker", "1.0.0")
    watchdogs = {inst: user_scope.pid(inst) for inst in ["storage", "worker"]}

    user_scope.install(second)
    assert os.readlink(user_scope.root / "apps" / "myapp").endswith("myapp_2.0.0")

    # Both instances fail; their watchdogs run them again from 1.0.0.
    for inst, pid_file in [("storage", "tarantool.pid"), ("worker", "process.pid")]:
        crash(user_scope, inst, pid_file)
        assert user_scope.pid(inst) == watchdogs[inst], "the same watchdog restarts it"

    version_dir = os.path.realpath(user_scope.version_dir("1.0.0"))
    assert wait(
        10,
        lambda: user_scope.log("storage").count(f"myapp 1.0.0 runs in {version_dir}") == 2,
    )
    assert wait(10, lambda: user_scope.log("worker").count("worker of myapp 1.0.0 runs") == 2)
    assert "myapp 2.0.0" not in user_scope.log("storage") + user_scope.log("worker")
    storage = user_scope.pid("storage", "tarantool.pid")
    assert os.readlink(f"/proc/{storage}/cwd") == version_dir

    rc, _, err = user_scope.tt("instance", "restart", "--scope", "user", "myapp:*", "--yes")
    assert rc == 0, err
    assert wait(10, lambda: running(user_scope, "storage", "worker"))
    assert_runs_from(user_scope, "storage", "2.0.0", tarantool)
    assert_worker_runs_from(user_scope, "worker", "2.0.0")
    assert user_scope.pid("storage") != watchdogs["storage"]

    rc, _, err = user_scope.tt("instance", "stop", "--scope", "user", "myapp:*", "--yes")
    assert rc == 0, err


@pytest.mark.parametrize("scope_name", ["project", "user"])
def test_start_refuses_tarantool_not_executable(
    tt_cmd,
    tmp_path,
    request,
    user_scope,
    scope_name,
):
    # A tarantool without execute permission, the one the project selects or
    # the one the installed application runs on, is refused before anything
    # starts: tt does not report the instances started while their watchdogs
    # fail to run it.
    project = make_project(tmp_path / "myapp", "1.0.0")
    if scope_name == "project":
        cwd, root = project, project / ".tarantool"
        tarantool = root / "bin" / "tarantool"
    else:
        user_scope.install(pack(tt_cmd, project))
        cwd, root = user_scope.cwd, user_scope.root
        tarantool = tarantool_installation(root, tarantool_version())
    request.addfinalizer(lambda: kill_instances(root, settle=True))
    tarantool.parent.mkdir(parents=True)
    tarantool.write_text("#!/bin/sh\nexit 0\n")
    tarantool.chmod(0o644)

    p = subprocess.run(
        [tt_cmd, "instance", "start", "--scope", scope_name, "myapp:*"],
        cwd=cwd,
        env=user_scope.env,
        capture_output=True,
        text=True,
    )
    assert p.returncode == 1, p.stderr
    assert f"tarantool is not executable: {tarantool} has no execute permission" in p.stderr
    assert "Starting" not in p.stderr
    assert not (root / "var").exists(), "the external process does not start either"
