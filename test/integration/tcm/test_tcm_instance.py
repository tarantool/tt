"""TCM, the Tarantool Cluster Manager, as an instance of its application:
tt tcm init declares it, tt instance runs it.

TCM is Tarantool Enterprise only, so the tests run tcm_shim.py in its place:
it records what tt started it with into tcm-shim.json in its working
directory and answers HTTP on the address of its config."""

import json
import os
import socket
import stat
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import pytest
import yaml
from integration.service.service_helpers import install_app

import utils

SHIM = Path(__file__).parent / "tcm_shim.py"

APP = "shop"

EE_MANIFEST = f"""manifest_version = "0.1"

[package]
name = "{APP}"

[platform]
tarantool = ">=3.0.0[ee]"
tt = ">=3.0.0"
"""

CE_MANIFEST = EE_MANIFEST.replace("[ee]", "")


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def write_shim(path):
    """Write the shim at path as a tcm, run by the Python of the tests, which
    has PyYAML."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"#!{sys.executable}\n" + SHIM.read_text())
    path.chmod(0o755)


@pytest.fixture
def env(tmp_path):
    """The environment of tt, with scope roots of its own, so that no TCM of
    the host is found."""
    env = dict(os.environ)
    env["TT_USER_ROOT"] = str(tmp_path / "user-root")
    env["TT_SYSTEM_ROOT"] = str(tmp_path / "system-root")
    env.pop("TCM_SECURITY_BOOTSTRAP_PASSWORD", None)
    env.pop("TT_SYSTEMD_UNIT", None)
    return env


@pytest.fixture
def project(tmp_path):
    """A project of Tarantool Enterprise with the shim as its tcm."""
    project = tmp_path / APP
    project.mkdir()
    (project / "app.manifest.toml").write_text(EE_MANIFEST)
    write_shim(project / ".tarantool" / "bin" / "tcm")
    return project


def tt(tt_cmd, cwd, env, *args):
    return utils.run_command_and_get_output([tt_cmd, *args], cwd=cwd, env=env)


def lib_dir(project, inst="tcm"):
    return Path(utils.inst_lib_dir(project, APP, inst))


def run_dir(project, inst="tcm"):
    return Path(utils.inst_run_dir(project, APP, inst))


def shim_record(project):
    record = lib_dir(project) / "tcm-shim.json"
    assert utils.wait_files(10, [str(record)])
    return json.loads(record.read_text())


def wait_until(predicate, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(0.1)
    return predicate()


def http_status(url):
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            return response.status
    except urllib.error.HTTPError as err:
        return err.code


def test_tcm_lifecycle(tt_cmd, project, env):
    port = free_port()
    url = f"http://127.0.0.1:{port}"

    init = ["tcm", "init", "--version", "1.3", "--http", f"127.0.0.1:{port}"]
    rc, out = tt(tt_cmd, project, env, *init)
    assert rc == 0, out
    assert 'Declared TCM as "tcm" in .templates/default/instances.yaml.' in out
    assert 'Set [platform].tcm = ">=1.3" in app.manifest.toml.' in out
    assert f"tt instance start {APP}:tcm" in out
    assert "tt binary install tcm" not in out, "the project has a tcm"

    desc = yaml.safe_load((project / ".templates" / "default" / "instances.yaml").read_text())
    assert desc == {"tcm": {"kind": "tcm", "http": f"127.0.0.1:{port}"}}
    assert 'tcm = ">=1.3"' in (project / "app.manifest.toml").read_text()

    secret = lib_dir(project) / "admin.secret"
    try:
        # A bare start starts every instance of the pair, TCM among them.
        rc, out = tt(tt_cmd, project, env, "instance", "start")
        assert rc == 0, out
        assert f"The instance {APP}:tcm is up." in out
        assert f"TCM: {url} (user admin, password in {secret})" in out

        password = secret.read_text().strip()
        assert len(password) == 32
        assert stat.S_IMODE(secret.stat().st_mode) == 0o600

        record = shim_record(project)
        config_file = run_dir(project) / "tcm.yaml"
        assert record["args"] == [f"-c={config_file}"]
        assert record["env"]["TCM_SECURITY_BOOTSTRAP_PASSWORD"] == password
        assert record["cwd"] == str(lib_dir(project))
        assert record["config_mode"] == "0o600"

        config = record["config"]
        assert config["http"] == {"host": "127.0.0.1", "port": port}
        embed = config["storage"]["etcd"]["embed"]
        assert embed["enabled"] is True
        assert embed["workdir"] == str(lib_dir(project) / "etcd")
        assert config["storage"]["etcd"]["prefix"] == f"/tcm/{APP}"
        clusters = config["initial-settings"]["clusters"]
        assert [c["name"] for c in clusters] == [APP]

        assert http_status(url) == 401

        # The password is in its file alone.
        for path in [
            config_file,
            run_dir(project) / "launch.json",
            Path(utils.inst_log_dir(project, APP, "tcm")) / "tt.log",
        ]:
            assert password not in path.read_text(), path

        rc, out = tt(tt_cmd, project, env, "instance", "status")
        assert rc == 0, out
        assert f"{APP}:tcm" in out and "RUNNING" in out

        rc, out = tt(tt_cmd, project, env, "instance", "status", "--details")
        assert rc == 0, out
        assert f"TCM {APP}:tcm: {url} (user admin, password in {secret})" in out
        assert "storage: the embedded etcd of TCM" in out
        assert "tt cluster publish" in out
        assert password not in out

        rc, out = tt(tt_cmd, project, env, "instance", "status", "-o", "json")
        assert rc == 0, out
        # The PID of the status is TCM, which process.pid names; its
        # watchdog holds tt.pid.
        status = json.loads(out)[f"{APP}:tcm"]
        assert status["pid"] == record["pid"]
        assert status["pid"] == int((run_dir(project) / "process.pid").read_text())
        assert status["watchdog_pid"] == int((run_dir(project) / utils.pid_file).read_text())
        access = status["tcm"]
        assert access["url"] == url
        assert access["user"] == "admin"
        assert access["password_file"] == str(secret)

        rc, out = tt(tt_cmd, project, env, "instance", "logs", f"{APP}:tcm")
        assert rc == 0, out
        assert f"{APP}:tcm: INF tcm shim: listening on 127.0.0.1:{port}" in out

        rc, out = tt(tt_cmd, project, env, "instance", "check", f"{APP}:tcm")
        assert rc == 0, out
        assert f"{APP}:tcm: TCM, its binary" in out

        # A restart keeps the password.
        pid = record["pid"]
        rc, out = tt(tt_cmd, project, env, "instance", "restart", "-y", f"{APP}:tcm")
        assert rc == 0, out
        assert wait_until(lambda: shim_record(project)["pid"] != pid, 10)
        assert secret.read_text().strip() == password
        assert shim_record(project)["env"]["TCM_SECURITY_BOOTSTRAP_PASSWORD"] == password
        assert http_status(url) == 401
    finally:
        tt(tt_cmd, project, env, "instance", "stop", "-y")

    rc, out = tt(tt_cmd, project, env, "instance", "status", f"{APP}:tcm")
    assert rc == 0, out
    assert "NOT RUNNING" in out
    with pytest.raises(urllib.error.URLError):
        urllib.request.urlopen(url, timeout=2)


def test_tcm_init_is_idempotent(tt_cmd, project, env):
    rc, out = tt(tt_cmd, project, env, "tcm", "init", "--version", "1.3")
    assert rc == 0, out
    desc_file = project / ".templates" / "default" / "instances.yaml"
    desc = desc_file.read_text()
    manifest = (project / "app.manifest.toml").read_text()

    rc, out = tt(tt_cmd, project, env, "tcm", "init", "--http", "127.0.0.1:9999")
    assert rc == 0, out
    assert 'declares TCM already, as "tcm": it is left as it is.' in out
    assert "--http 127.0.0.1:9999 is not applied" in out
    assert '[platform].tcm of app.manifest.toml is ">=1.3".' in out
    assert desc_file.read_text() == desc
    assert (project / "app.manifest.toml").read_text() == manifest


def test_tcm_init_next_to_config(tt_cmd, project, env):
    # A template of a config alone runs its topology: tt tcm init keeps it so.
    template = project / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "config.yaml").write_text("groups: {g: {replicasets: {r: {instances: {i: {}}}}}}\n")

    rc, out = tt(tt_cmd, project, env, "tcm", "init", "--version", ">=1.3,<2")
    assert rc == 0, out
    desc = yaml.safe_load((template / "instances.yaml").read_text())
    assert desc == {"*": {}, "tcm": {"kind": "tcm", "http": "127.0.0.1:8080"}}
    assert 'tcm = ">=1.3,<2"' in (project / "app.manifest.toml").read_text()


def test_tcm_init_version(tt_cmd, project, env):
    # Without --version and [platform].tcm, the version comes from a TCM
    # tt binary installed; with none, the command says why it needs one.
    rc, out = tt(tt_cmd, project, env, "tcm", "init")
    assert rc == 1, out
    assert "pass --version" in out
    assert "customer zone of tarantool.io" in out
    assert not (project / ".templates").exists(), "nothing is written"

    versions = project / ".tarantool" / "versions" / "tcm"
    (versions / "tcm_1.2.0").mkdir(parents=True)
    (versions / "tcm_1.3.1-0-g074b5ffa").mkdir()

    rc, out = tt(tt_cmd, project, env, "tcm", "init")
    assert rc == 0, out
    assert 'tcm = ">=1.3.1"' in (project / "app.manifest.toml").read_text()


def test_tcm_community_refused(tt_cmd, project, env):
    (project / "app.manifest.toml").write_text(CE_MANIFEST)

    rc, out = tt(tt_cmd, project, env, "tcm", "init", "--version", "1.3")
    assert rc == 1, out
    assert "TCM is Tarantool Enterprise only" in out
    assert 'tarantool = ">=3.0.0[ee]"' in out
    assert not (project / ".templates").exists()

    template = project / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text("tcm:\n  kind: tcm\n")

    for command in (["instance", "start"], ["instance", "check"]):
        rc, out = tt(tt_cmd, project, env, *command)
        assert rc == 1, out
        assert 'instance "tcm" of "shop": TCM is Tarantool Enterprise only' in out
    assert not run_dir(project).joinpath("tt.pid").exists()


def test_tcm_without_binary(tt_cmd, project, env):
    (project / ".tarantool" / "bin" / "tcm").unlink()
    template = project / ".templates" / "default"
    template.mkdir(parents=True)
    (template / "instances.yaml").write_text("tcm:\n  kind: tcm\n")

    rc, out = tt(tt_cmd, project, env, "instance", "start")
    assert rc == 1, out
    assert "no TCM to run" in out
    assert "install it with tt binary install tcm <version>" in out


def test_tcm_systemd_unit(tt_cmd, tmp_path, env):
    # tt service systemd makes a unit for TCM as for any instance.
    root = tmp_path / "system-root"
    install_app(
        root,
        tt_cmd,
        instances="worker:\n  binary: ./worker.sh\n"
        "tcm:\n  kind: tcm\n  restart: always\n  stop_timeout: 20s\n",
    )

    unit_dir = tmp_path / "units"
    generate = ["service", "systemd", "generate", "myapp"]
    rc, out = tt(tt_cmd, root, env, *generate, "--unit-dir", str(unit_dir))
    assert rc == 0, out

    conf = unit_dir / "myapp@tcm.service.d" / "tt.conf"
    assert conf.exists(), sorted(p.name for p in unit_dir.iterdir())
    assert "Restart=always\nTimeoutStopSec=25s\n" in conf.read_text()
    assert (unit_dir / "myapp@worker.service.d" / "tt.conf").exists()

    rc, out = tt(tt_cmd, root, env, *generate)
    assert rc == 0, out
    assert "==> myapp@tcm.service.d/tt.conf <==" in out
