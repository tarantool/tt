import json
import shutil
import subprocess
import time
import uuid
from pathlib import Path

import pytest
from integration.scopes.scopes_helpers import make_project, pack, runtime_cache
from integration.service.service_helpers import install_app

# The image of a host with systemd, built from systemd_image/.
IMAGE = "tt-test-systemd"
IMAGE_DIR = Path(__file__).parent / "systemd_image"

# The system root in the container: the default one, which the units name in
# TT_SYSTEM_ROOT all the same.
ROOT = "/opt/tarantool"

# The pair of the host: an instance systemd stops, one that fails on its own,
# which systemd restarts, and one that ignores its stop signal, which tt kills
# once its stop_timeout is up.
INSTANCES = """\
worker:
  binary: ./worker.sh
  restart: on-failure
  stop_timeout: 10s
crasher:
  binary: ./worker.sh
  env: {CRASH_AFTER: "2"}
  restart: on-failure
stubborn:
  binary: ./worker.sh
  env: {IGNORE_TERM: "1"}
  stop_timeout: 2s
"""

pytestmark = [pytest.mark.docker, pytest.mark.slow]


def docker(*args, timeout=120):
    """Run docker with args; return the exit code and the output."""
    p = subprocess.run(
        ["docker", *args],
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    return p.returncode, p.stdout + p.stderr


class Host:
    """A container that runs systemd."""

    def __init__(self, name):
        self.name = name

    def sh(self, script, timeout=120):
        """Run script in the container; return the exit code and the output."""
        rc, out = docker("exec", self.name, "sh", "-c", script, timeout=timeout)
        print(f"$ {script}\n{out}")
        return rc, out

    def tt(self, *args):
        """Run tt of the system scope in the container."""
        return self.sh(" ".join([f"{ROOT}/bin/tt", *args]))

    def unit(self, unit, prop):
        """Return the property prop of the unit unit."""
        _, out = self.sh(f"systemctl show -p {prop} --value {unit}")
        return out.strip()

    def status(self, app="myapp"):
        """Return tt instance status of app as JSON."""
        rc, out = docker(
            "exec",
            self.name,
            f"{ROOT}/bin/tt",
            "instance",
            "status",
            f"{app}:*",
            "-o",
            "json",
        )
        assert rc == 0, out
        return json.loads(out[out.index("{") :])


def wait(timeout, condition):
    """Wait timeout seconds for condition to hold."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if condition():
            return True
        time.sleep(0.5)
    return False


@pytest.fixture(scope="module")
def host(tt_cmd, tmp_path_factory):
    """A container with systemd, myapp installed in the system scope and tt
    of the scope: the built tt."""
    if shutil.which("docker") is None or docker("info")[0] != 0:
        pytest.skip("docker is not available")

    rc, out = docker("build", "-q", "-t", IMAGE, str(IMAGE_DIR), timeout=900)
    if rc != 0:
        pytest.skip(f"cannot build the image of a host with systemd: {out.strip()[-300:]}")

    name = f"tt-systemd-{uuid.uuid4().hex[:8]}"
    rc, out = docker(
        "run",
        "-d",
        "--name",
        name,
        "--privileged",
        "--cgroupns=private",
        "--tmpfs",
        "/run",
        "--tmpfs",
        "/run/lock",
        IMAGE,
    )
    if rc != 0:
        pytest.skip(f"cannot run systemd in a container: {out.strip()[-300:]}")

    try:
        host = Host(name)
        if not wait(
            60,
            lambda: host.sh("systemctl is-system-running")[1].strip() in ("running", "degraded"),
        ):
            pytest.skip("systemd did not come up in the container")

        root = tmp_path_factory.mktemp("systemd") / "root"
        install_app(root, None, INSTANCES)

        host.sh(f"mkdir -p {ROOT}/versions/tt/tt_test {ROOT}/bin")
        rc, out = docker("cp", f"{root}/.", f"{name}:{ROOT}")
        assert rc == 0, out
        rc, out = docker("cp", str(tt_cmd), f"{name}:{ROOT}/versions/tt/tt_test/tt")
        assert rc == 0, out

        # The instances run as tarantool, and write under the runtime
        # directories of the root, which the deployment gives them.
        rc, _ = host.sh(
            f"ln -s ../versions/tt/tt_test/tt {ROOT}/bin/tt && "
            f"mkdir -p {ROOT}/var && chown tarantool:tarantool {ROOT}/var",
        )
        assert rc == 0

        rc, out = host.tt("version")
        if rc != 0:
            pytest.skip(f"the built tt does not run in the container: {out.strip()[-300:]}")

        yield host
    finally:
        docker("rm", "-f", name)


def test_systemd_units_in_container(host):
    # install -> systemctl start -> tt instance status sees the instance and
    # its unit -> tt instance stop refuses -> systemctl stop -> uninstall. An
    # instance that fails under its unit is restarted by systemd, not by tt.
    rc, out = host.tt("service", "systemd", "install", "myapp")
    assert rc == 0, out
    assert (
        "systemctl enable --now myapp@worker.service myapp@crasher.service myapp@stubborn.service"
    ) in out
    assert "which this host does not have" not in out

    rc, out = host.sh(
        "systemctl daemon-reload && systemctl start myapp@worker myapp@crasher myapp@stubborn",
    )
    assert rc == 0, out

    assert wait(
        20,
        lambda: host.status()["myapp:worker"]["status"] == "RUNNING",
    ), host.sh("systemctl status myapp@worker; journalctl -u myapp@worker --no-pager")[1]
    status = host.status()
    assert status["myapp:worker"]["unit"] == "myapp@worker.service"
    assert host.unit("myapp@worker", "ActiveState") == "active"

    # The unit runs tt in the foreground and the watchdog of the instance as
    # tarantool, the watchdog keeps the pid file.
    main_pid = host.unit("myapp@worker", "MainPID")
    rc, out = host.sh(f"ps -o user= -p {main_pid}; cat {ROOT}/var/run/myapp/worker/tt.pid")
    assert rc == 0, out
    assert out.split()[0] == "tarantool"
    assert int(out.split()[1]) == status["myapp:worker"]["pid"]

    rc, out = host.tt("instance", "stop", "--yes", "myapp:worker")
    assert rc == 1, out
    assert "use systemctl stop myapp@worker.service" in out
    assert host.unit("myapp@worker", "ActiveState") == "active"

    # The crasher exits with 3 on its own: tt in the foreground does not
    # restart it and exits with its code, and systemd restarts it after
    # RestartSec=, as Restart=on-failure of its drop-in says.
    assert wait(30, lambda: int(host.unit("myapp@crasher", "NRestarts") or 0) >= 1), host.sh(
        "systemctl status myapp@crasher; journalctl -u myapp@crasher --no-pager",
    )[1]
    rc, out = host.sh(f"cat {ROOT}/var/log/myapp/crasher/tt.log")
    assert out.count("worker crashes") >= 1

    # The stop of systemd goes to tt alone, and tt kills the instance that
    # ignores it once its stop_timeout of 2 seconds is up, before
    # TimeoutStopSec= of 7 seconds: tt exits with 137, which tells the kill.
    assert wait(20, lambda: host.status()["myapp:stubborn"]["status"] == "RUNNING")
    started = time.time()
    rc, out = host.sh("systemctl stop myapp@stubborn")
    assert rc == 0, out
    assert time.time() - started < 6
    assert host.unit("myapp@stubborn", "ExecMainStatus") == "137"
    rc, out = host.sh(f"cat {ROOT}/var/log/myapp/stubborn/tt.log")
    assert out.count("worker ignores TERM") == 1

    rc, out = host.sh("systemctl stop myapp@worker myapp@crasher")
    assert rc == 0, out
    assert host.unit("myapp@worker", "ActiveState") == "inactive"
    assert host.unit("myapp@worker", "Result") == "success"

    rc, out = host.sh(f"cat {ROOT}/var/log/myapp/worker/tt.log")
    assert "worker got TERM" in out
    assert host.status()["myapp:worker"]["status"] == "NOT RUNNING"

    rc, out = host.tt("service", "systemd", "uninstall", "myapp")
    assert rc == 0, out
    rc, out = host.sh(
        "ls /etc/systemd/system/myapp@* /etc/systemd/system/myapp@*.service.d "
        f"{ROOT}/etc/myapp/systemd.yaml",
    )
    assert rc != 0, out
    assert "unit" not in host.status()["myapp:worker"]


# The pair of the host of tntapp: a Tarantool instance of the cluster config.
TNTAPP_INSTANCES = "storage:\n  restart: on-failure\n"
TNTAPP_CONFIG = """\
groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          storage: {}
"""

# The version of tntapp tt package install lays out in the container.
TNTAPP_VERSION = f"{ROOT}/versions/tntapp/tntapp_1.0.0"


@pytest.fixture(scope="module")
def tntapp(host, tt_cmd, tmp_path_factory):
    """tntapp, installed into the system scope of the host as a deployment
    does it: tt package install, run as root, of a with-deps archive, which
    bundles the tarantool on PATH, with the pair of the host in
    <root>/etc/tntapp/."""
    tmp = tmp_path_factory.mktemp("tntapp")
    project = make_project(tmp / "tntapp", "1.0.0", name="tntapp")
    archive = pack(tt_cmd, project, with_deps=True, env=runtime_cache(tmp / "cache"))
    rc, out = docker("cp", str(archive), f"{host.name}:/root/tntapp.tt")
    assert rc == 0, out

    rc, out = host.tt("package", "install", "--scope", "system", "/root/tntapp.tt")
    assert rc == 0, out

    pair = tmp / "etc"
    pair.mkdir()
    (pair / "instances.yaml").write_text(TNTAPP_INSTANCES)
    (pair / "config.yaml").write_text(TNTAPP_CONFIG)
    rc, out = docker("cp", f"{pair}/.", f"{host.name}:{ROOT}/etc/tntapp")
    assert rc == 0, out
    return host


@pytest.mark.xfail(
    strict=True,
    reason="tt package install creates the version directory with mode 0700 "
    "(os.MkdirTemp of swapVersion, cli/manifest/install/version.go), so the user "
    "the units run as cannot read an application root installed",
)
def test_systemd_installed_version_is_readable(tntapp):
    # The units run tt and the instances as tarantool, while the deployment
    # installs the application as root: the version must be readable to
    # others, as the tarantool it bundles is.
    rc, out = tntapp.sh(
        f"runuser -u tarantool -- cat {ROOT}/apps/tntapp/app.manifest.toml "
        f"&& runuser -u tarantool -- ls {ROOT}/versions/tarantool",
    )
    assert rc == 0, out


def test_systemd_tarantool_in_container(tntapp):
    # A Tarantool instance of an application tt package install laid out runs
    # under its unit on the tarantool the archive bundles, as tarantool, and
    # stops with the unit.
    host = tntapp
    rc, out = host.sh(f"{ROOT}/versions/tarantool/tarantool_*/tarantool --version")
    if rc != 0:
        pytest.skip(f"the tarantool of the host does not run in the container: {out[-300:]}")

    # Until install lays the version out readable to others
    # (test_systemd_installed_version_is_readable), the deployment opens it
    # for the user of the units.
    rc, out = host.sh(f"chmod -R o+rX {TNTAPP_VERSION}")
    assert rc == 0, out

    rc, out = host.tt("service", "systemd", "install", "tntapp")
    assert rc == 0, out
    assert "systemctl enable --now tntapp@storage.service" in out

    rc, out = host.sh("systemctl daemon-reload && systemctl start tntapp@storage")
    assert rc == 0, out
    assert wait(
        30,
        lambda: host.status("tntapp")["tntapp:storage"]["box"] == "running",
    ), host.sh("systemctl status tntapp@storage; journalctl -u tntapp@storage --no-pager")[1]
    status = host.status("tntapp")["tntapp:storage"]
    assert status["status"] == "RUNNING"
    assert status["unit"] == "tntapp@storage.service"

    # tarantool runs as tarantool, in the version, on the tarantool the
    # archive bundles, and keeps its data under the root.
    rc, out = host.sh(
        f"pid=$(cat {ROOT}/var/run/tntapp/storage/tarantool.pid) && "
        "ps -o user= -p $pid && readlink /proc/$pid/cwd /proc/$pid/exe && "
        f"stat -c %U {ROOT}/var/lib/tntapp/storage/*.snap",
    )
    assert rc == 0, out
    user, cwd, exe, owner = out.split()[:4]
    assert user == "tarantool"
    assert cwd == TNTAPP_VERSION
    assert exe.startswith(f"{ROOT}/versions/tarantool/tarantool_")
    assert owner == "tarantool"

    rc, out = host.sh("systemctl stop tntapp@storage")
    assert rc == 0, out
    assert host.unit("tntapp@storage", "ActiveState") == "inactive"
    assert host.unit("tntapp@storage", "Result") == "success"
    assert host.status("tntapp")["tntapp:storage"]["status"] == "NOT RUNNING"

    rc, out = host.tt("service", "systemd", "uninstall", "tntapp")
    assert rc == 0, out
    rc, out = host.tt("package", "uninstall", "--scope", "system", "tntapp", "--yes")
    assert rc == 0, out
    rc, out = host.sh(f"test ! -e {TNTAPP_VERSION} && ls {ROOT}/var/lib/tntapp/storage/*.snap")
    assert rc == 0, out
