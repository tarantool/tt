import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
from etcd_helper import etcd_tool
from replicaset_helpers import (
    copy_application,
    create_template_project,
    eval_on_instance,
    parse_yml,
    start_template_project,
    stop_template_project,
    template_project_status,
)

from utils import run_command_and_get_output, wait_event, wait_file

# The ports of this file are 3601-3699: the cluster template projects take
# 361x, 362x and 363x, and the etcd of this file 3691 and 3692.
ETCD_CLIENT_PORT = 3691
ETCD_PEER_PORT = 3692

CLUSTER_INSTANCES = ["replicaset-001-a", "replicaset-001-b", "replicaset-002-a", "replicaset-002-b"]


@pytest.mark.parametrize(
    "case",
    [
        ["--config", "--custom"],
    ],
)
def test_expel_orchestrators_force_mix(tt_cmd, tmpdir_with_cfg, case):
    status_cmd = [tt_cmd, "replicaset", "expel", *case, "app:instance"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ only one type of orchestrator can be forced", out)


def test_expel_invalid_argument(tt_cmd, tmpdir_with_cfg):
    status_cmd = [tt_cmd, "replicaset", "expel", "app"]
    rc, out = run_command_and_get_output(status_cmd, cwd=tmpdir_with_cfg)
    assert rc == 1
    assert re.search(r"   ⨯ the command expects <APP>:<INSTANCE>", out)


def test_expel_no_instance(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)

    status_cmd = [tt_cmd, "replicaset", "expel", f"{app_name}:noexist"]
    rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
    assert rc == 1
    assert f'   ⨯ instance not found: "noexist" of "{app_name}"' in out


@pytest.mark.parametrize("flag", [None, "--custom"])
def test_expel_custom_app(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_custom_app"
    app_path = copy_application(tmpdir, app_name)
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        # Check for start.
        file = wait_file(app_path, "ready", [])
        assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append("test_custom_app:test_custom_app")

        rc, out = run_command_and_get_output(expel_cmd, cwd=app_path)
        assert rc == 1
        assert re.search(
            r"""  • Discovery application...*

Orchestrator:      custom
Replicasets state: bootstrapped

• .*
  Failover: unknown
  Master:   single
    • test_custom_app .* rw

   • Expel instance: test_custom_app
   ⨯ expel is not supported for an application by "custom" orchestrator
""",
            out,
        )
    finally:
        stop_cmd = [tt_cmd, "instance", "stop", "-y", f"{app_name}:*"]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=app_path)
        assert rc == 0


def wait_connected(tt_cmd, app_name, survivor, target, cwd):
    """Wait until survivor replicates from target and to it, both ways in
    follow: what expel takes for an instance that still runs."""
    lua = (
        "for _, r in pairs(box.info.replication) do "
        f"if r.name == '{target}' then "
        "return r.upstream ~= nil and r.upstream.status == 'follow' and "
        "r.downstream ~= nil and r.downstream.status == 'follow' end end "
        "return false"
    )

    def connected():
        return parse_yml(eval_on_instance(tt_cmd, app_name, survivor, cwd, lua)) == [True]

    assert wait_event(30, connected), f"{survivor} is not connected to {target}"


def connected_hint(app_name, inst):
    return (
        f'instance "{inst}" is still connected to the replicaset: stop it on its host '
        f"(tt instance stop {app_name}:{inst}) or pass --force"
    )


def cluster_names(tt_cmd, app_name, inst, cwd):
    """Return the names of the instances in _cluster, as inst has them."""
    out = eval_on_instance(tt_cmd, app_name, inst, cwd, "box.space._cluster:select()")
    tuples = parse_yml(out)
    assert isinstance(tuples, list), out
    return [t[2] for t in tuples[0]]


@pytest.mark.parametrize("flag", [None, "--config"])
def test_expel_cconfig(tt_cmd, tmpdir_with_cfg, flag):
    tmpdir = tmpdir_with_cfg
    app_name = "test_ccluster_app"
    app_path = copy_application(tmpdir, app_name)
    config_file = Path(app_path) / ".templates" / "default" / "config.yaml"
    try:
        # Start a cluster.
        start_cmd = [tt_cmd, "instance", "start", f"{app_name}:*"]
        rc, out = run_command_and_get_output(start_cmd, cwd=app_path)
        assert rc == 0

        for i in range(1, 6):
            file = wait_file(app_path, f"ready-instance-00{i}", [])
            assert file != ""

        expel_cmd = [tt_cmd, "replicaset", "expel"]
        if flag:
            expel_cmd.append(flag)
        expel_cmd.append(f"{app_name}:instance-003")

        # A running instance is refused, by what the others see of it: expel
        # does not stop it.
        wait_connected(tt_cmd, app_name, "instance-001", "instance-003", app_path)
        rc, out = run_command_and_get_output(expel_cmd, cwd=app_path)
        assert rc == 1, out
        assert connected_hint(app_name, "instance-003") in out
        assert "instance-003" in config_file.read_text()

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "stop", "-y", f"{app_name}:instance-003"],
            cwd=app_path,
        )
        assert rc == 0, out

        rc, out = run_command_and_get_output(expel_cmd, cwd=app_path)
        assert rc == 0, out
        assert re.search(
            """   • Discovery application...*

Orchestrator:      centralized config
Replicasets state: bootstrapped
(.|\n)*
   • Expel instance: instance-003
   • The instance instance-003 is removed from the config and from _cluster.*
   • Done.*
""",
            out,
        )
        assert "terminated" not in out

        # The instance is gone from the config. instances.yaml names it still,
        # as the expel says, until it is removed from there too.
        config = config_file.read_text()
        assert "instance-003" not in config
        assert "instance-002" in config
        assert "remove it from there" in out
        desc_file = config_file.with_name("instances.yaml")
        desc_file.write_text(desc_file.read_text().replace("instance-003:\n", ""))
        assert cluster_names(tt_cmd, app_name, "instance-001", app_path) == [
            "instance-001",
            "instance-002",
        ]

        # Check that the instance has been expelled.
        status_cmd = [tt_cmd, "replicaset", "status", app_name]
        rc, out = run_command_and_get_output(status_cmd, cwd=app_path)
        assert rc == 0
        assert (
            """Orchestrator:      centralized config
Replicasets state: bootstrapped

• replicaset-001
  Failover: off
  Master:   single
    • instance-001 unix/:./instance-001.iproto rw
    • instance-002 unix/:./instance-002.iproto read
• replicaset-002
  Failover: off
  Master:   multi
    • instance-004 unix/:./instance-004.iproto rw
    • instance-005 unix/:./instance-005.iproto rw
"""
            == out
        )

    finally:
        stop_cmd = [tt_cmd, "instance", "stop", "-y", f"{app_name}:*"]
        rc, _ = run_command_and_get_output(stop_cmd, cwd=app_path)
        assert rc == 0


def test_expel_cluster_template(tt_cmd, tmp_path):
    """Expel in a project of the cluster template, whose instances.yaml has
    "*". A running instance is refused; stopped by tt instance stop, it is
    removed from the config and from _cluster, the config stays valid, and
    the leader that remains alone in its replicaset is healthy. The leader
    itself is refused."""
    app_dir = create_template_project(tt_cmd, tmp_path, "cluster", "ex", "361")
    start_template_project(tt_cmd, app_dir, CLUSTER_INSTANCES)
    try:
        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "ex:replicaset-002-a"],
            cwd=app_dir,
        )
        assert rc == 1
        assert 'cannot expel the leader "replicaset-002-a" of "replicaset-002"' in out

        wait_connected(tt_cmd, "ex", "replicaset-002-a", "replicaset-002-b", app_dir)
        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "ex:replicaset-002-b"],
            cwd=app_dir,
        )
        assert rc == 1, out
        assert connected_hint("ex", "replicaset-002-b") in out
        status = template_project_status(tt_cmd, app_dir)
        assert status["replicaset-002-b"]["STATUS"] == "RUNNING"

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "stop", "-y", "ex:replicaset-002-b"],
            cwd=app_dir,
        )
        assert rc == 0, out

        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "ex:replicaset-002-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "The instance replicaset-002-b is removed from the config and from _cluster" in out
        assert "failed to reload" not in out

        config = (app_dir / ".templates" / "default" / "config.yaml").read_text()
        assert "replicaset-002-b" not in config
        assert "listen: {}" not in config

        rc, out = run_command_and_get_output(
            [tt_cmd, "cluster", "show", "--validate", "ex"],
            cwd=app_dir,
        )
        assert rc == 0, out

        # The expelled instance, stopped, is with "*" no longer one of the
        # project; the leader is configured without alerts.
        status = template_project_status(tt_cmd, app_dir)
        assert sorted(status) == sorted(i for i in CLUSTER_INSTANCES if i != "replicaset-002-b")
        assert status["replicaset-002-a"]["CONFIG"] == "ready"
        assert status["replicaset-002-a"]["MODE"] == "RW"

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", "-d", "ex:replicaset-002-a"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "Alerts for" not in out
        assert "Not in the config" not in out

        assert cluster_names(tt_cmd, "ex", "replicaset-002-a", app_dir) == ["replicaset-002-a"]

        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "status", "ex"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "replicaset-002-b" not in out
    finally:
        stop_template_project(tt_cmd, app_dir)


def test_expel_force_running(tt_cmd, tmp_path):
    """--force expels an instance that still runs. With "*" it is then an
    orphan: tt instance status shows it as not in the config, tt instance
    start does not start it, and logs and stop reach it by name."""
    app_dir = create_template_project(tt_cmd, tmp_path, "cluster", "ex", "362")
    start_template_project(tt_cmd, app_dir, CLUSTER_INSTANCES)
    try:
        wait_connected(tt_cmd, "ex", "replicaset-001-a", "replicaset-001-b", app_dir)
        rc, out = run_command_and_get_output(
            [tt_cmd, "replicaset", "expel", "--force", "ex:replicaset-001-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "is still connected to the replicaset, expelled by --force" in out
        assert "The instance replicaset-001-b is removed from the config and from _cluster" in out

        config = (app_dir / ".templates" / "default" / "config.yaml").read_text()
        assert "replicaset-001-b" not in config
        assert cluster_names(tt_cmd, "ex", "replicaset-001-a", app_dir) == ["replicaset-001-a"]

        # The orphan runs on, and shows as one.
        rc, out = run_command_and_get_output([tt_cmd, "instance", "status"], cwd=app_dir)
        assert rc == 0, out
        assert re.search(r"ex:replicaset-001-b\s+RUNNING\s+\d+", out), out
        assert "Not in the config: ex:replicaset-001-b." in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "status", "-o", "json", "ex:replicaset-001-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert '"not_in_config": true' in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "logs", "-n", "1", "ex:replicaset-001-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert "ex:replicaset-001-b:" in out

        # start does not start it, by name or with the others.
        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "start", "ex:replicaset-001-b"],
            cwd=app_dir,
        )
        assert rc == 1, out
        assert '"replicaset-001-b" still runs, though the config no longer has it' in out

        rc, out = run_command_and_get_output([tt_cmd, "instance", "start"], cwd=app_dir)
        assert rc == 0, out
        assert "replicaset-001-b" not in out

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "stop", "-y", "ex:replicaset-001-b"],
            cwd=app_dir,
        )
        assert rc == 0, out
        assert re.search(r"The instance ex:replicaset-001-b \(PID = \d+\) has been terminated", out)

        status = template_project_status(tt_cmd, app_dir)
        assert "replicaset-001-b" not in status
        assert status["replicaset-001-a"]["MODE"] == "RW"
    finally:
        stop_template_project(tt_cmd, app_dir)


@pytest.fixture
def own_etcd(tmp_path):
    """An etcd of this file on ports of its own, ETCD_CLIENT_PORT and
    ETCD_PEER_PORT: the etcd of the session listens for its peers on the
    default 2380, which another etcd would clash with. Its endpoint."""
    if shutil.which(etcd_tool("etcd")) is None:
        pytest.skip("etcd is not found")

    endpoint = f"http://127.0.0.1:{ETCD_CLIENT_PORT}"
    peer = f"http://127.0.0.1:{ETCD_PEER_PORT}"
    popen = subprocess.Popen(
        [etcd_tool("etcd")],
        env={
            "ETCD_NAME": "expel",
            "ETCD_DATA_DIR": str(tmp_path / "etcd"),
            "ETCD_LISTEN_CLIENT_URLS": endpoint,
            "ETCD_ADVERTISE_CLIENT_URLS": endpoint,
            "ETCD_LISTEN_PEER_URLS": peer,
            "ETCD_INITIAL_ADVERTISE_PEER_URLS": peer,
            "ETCD_INITIAL_CLUSTER": f"expel={peer}",
            "PATH": os.getenv("PATH"),
        },
        cwd=tmp_path,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.STDOUT,
    )
    try:

        def healthy():
            p = subprocess.run(
                [etcd_tool("etcdctl"), "endpoint", "health", f"--endpoints={endpoint}"],
                capture_output=True,
            )
            return p.returncode == 0

        assert wait_event(10, healthy), "etcd did not start"
        yield endpoint
    finally:
        popen.kill()
        popen.wait()


def etcd_get(endpoint, key):
    p = subprocess.run(
        [etcd_tool("etcdctl"), "get", "--print-value-only", key, f"--endpoints={endpoint}"],
        capture_output=True,
        text=True,
        check=True,
    )
    return p.stdout


def test_cluster_expel_etcd(tt_cmd, tmp_path, own_etcd):
    """tt cluster replicaset expel refuses a running instance as
    tt replicaset expel does, and expels a stopped one: from the config in
    etcd and from _cluster, which the instances of the project it runs in
    are asked about and delete it from. Tarantool CE does not take its
    config from etcd: the instances run on a config file of the same
    topology. Outside of a project no instance is reached: the expel is
    refused, and --force removes the instance from the config alone."""
    key = "/prefix/config/all"
    app_dir = create_template_project(tt_cmd, tmp_path, "cluster", "ex", "363")
    config = (app_dir / ".templates" / "default" / "config.yaml").read_text()
    subprocess.run(
        [etcd_tool("etcdctl"), "put", key, f"--endpoints={own_etcd}"],
        input=config,
        text=True,
        check=True,
    )
    url = f"{own_etcd}/prefix?timeout=5"

    start_template_project(tt_cmd, app_dir, CLUSTER_INSTANCES)
    try:
        wait_connected(tt_cmd, "ex", "replicaset-002-a", "replicaset-002-b", app_dir)
        expel = [tt_cmd, "cluster", "replicaset", "expel", url, "replicaset-002-b"]
        rc, out = run_command_and_get_output(expel, cwd=app_dir)
        assert rc == 1, out
        assert connected_hint("ex", "replicaset-002-b") in out
        assert "replicaset-002-b" in etcd_get(own_etcd, key)

        rc, out = run_command_and_get_output(
            [tt_cmd, "instance", "stop", "-y", "ex:replicaset-002-b"],
            cwd=app_dir,
        )
        assert rc == 0, out

        rc, out = run_command_and_get_output(expel, cwd=app_dir)
        assert rc == 0, out
        assert f'Patching the config by the key: "{key}"' in out
        assert "The instance replicaset-002-b is removed from the config and from _cluster" in out
        stored = etcd_get(own_etcd, key)
        assert "replicaset-002-b" not in stored
        assert "replicaset-002-a" in stored
        assert cluster_names(tt_cmd, "ex", "replicaset-002-a", app_dir) == ["replicaset-002-a"]

        # Outside of the project there is nothing to check on.
        expel = [tt_cmd, "cluster", "replicaset", "expel", url, "replicaset-001-b"]
        rc, out = run_command_and_get_output(expel, cwd=tmp_path)
        assert rc == 1, out
        assert "no other instance of the replicaset is reached" in out
        assert "replicaset-001-b" in etcd_get(own_etcd, key)

        rc, out = run_command_and_get_output([*expel[:4], "-f", *expel[4:]], cwd=tmp_path)
        assert rc == 0, out
        assert '"replicaset-001-b" is removed from the config alone' in out
        assert "replicaset-001-b" not in etcd_get(own_etcd, key)
    finally:
        stop_template_project(tt_cmd, app_dir)
