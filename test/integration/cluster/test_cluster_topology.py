import json
import os
import shutil

import pytest

from utils import get_tarantool_version, run_command_and_get_output, wait_file

tarantool_major_version, _ = get_tarantool_version()

# The cconfig test app lives in the replicaset test directory.
CCONFIG_APP_NAME = "test_ccluster_app"
CCONFIG_APP_DIR = os.path.join(
    os.path.dirname(__file__),
    "..",
    "replicaset",
    CCONFIG_APP_NAME,
)

INSTANCES = [f"instance-00{i}" for i in range(1, 6)]

# Expected modes for a fully running cluster: masters are rw, replicas are ro.
EXPECTED_MODES = {
    "instance-001": "rw",
    "instance-002": "ro",
    "instance-003": "ro",
    "instance-004": "rw",
    "instance-005": "rw",
}


@pytest.fixture
def ccluster_app(request, tt_cmd, tmpdir_with_cfg, port_factory):
    """Start the centralized-config cluster app and tear it down after."""
    tmpdir = tmpdir_with_cfg
    app_path = os.path.join(tmpdir, CCONFIG_APP_NAME)
    shutil.copytree(CCONFIG_APP_DIR, app_path)

    transport = getattr(request, "param", "unix")
    assert transport in ("unix", "tcp")

    config_path = os.path.join(app_path, "config.yaml")
    with open(config_path) as config_file:
        config = config_file.read()

    config = config.replace(
        "  listen:\n    - uri: 'unix/:./{{ instance_name }}.iproto'\n",
        "",
    )
    for instance in INSTANCES:
        if transport == "unix":
            uri = f"unix/:./{instance}.iproto"
        else:
            uri = f"127.0.0.1:{port_factory()}"

        instance_marker = f"          {instance}:\n"
        instance_iproto = (
            instance_marker
            + "            iproto:\n"
            + "              listen:\n"
            + f"                - uri: '{uri}'\n"
        )
        assert instance_marker in config
        config = config.replace(instance_marker, instance_iproto, 1)

    with open(config_path, "w") as config_file:
        config_file.write(config)

    rc, _ = run_command_and_get_output(
        [tt_cmd, "start", CCONFIG_APP_NAME],
        cwd=tmpdir,
    )
    assert rc == 0
    for inst in INSTANCES:
        assert wait_file(os.path.join(tmpdir, CCONFIG_APP_NAME), f"ready-{inst}", []) != ""

    yield tmpdir

    run_command_and_get_output(
        [tt_cmd, "stop", "-y", CCONFIG_APP_NAME],
        cwd=tmpdir,
    )


def _topology_cmd(tt_cmd, config_path, tmpdir, fmt=None):
    """Build and run a topology command, return (rc, output)."""
    cmd = [
        tt_cmd,
        "cluster",
        "topology",
        "-c",
        config_path,
        "-u",
        "client",
        "-p",
        "secret",
    ]
    if fmt:
        cmd += ["--format", fmt]
    return run_command_and_get_output(cmd, cwd=tmpdir)


def _parse_json_output(output):
    """Extract and parse JSON from combined stdout+stderr output.

    The JSON payload is printed to stdout while log messages go to stderr;
    they may be interleaved in the combined output. Scan for the first '{'
    that begins a valid JSON object and decode it.
    """
    decoder = json.JSONDecoder()
    search_from = 0
    while True:
        idx = output.find("{", search_from)
        if idx == -1:
            raise ValueError("no JSON object found in output")
        try:
            obj, _ = decoder.raw_decode(output[idx:])
            return obj
        except json.JSONDecodeError:
            search_from = idx + 1


def _instance_names_from_json(data):
    """Collect all instance names from a parsed topology JSON."""
    names = set()
    for instances in data["replicasets"].values():
        for inst in instances:
            names.add(inst["instance_name"])
    return names


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_table(tt_cmd, ccluster_app):
    """tt cluster topology -c <config.yaml> — table output."""
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0

    # Two replicasets.
    assert "replicaset-001" in out
    assert "replicaset-002" in out

    # All instances present.
    assert "instance-001" in out
    assert "instance-002" in out
    assert "instance-003" in out
    assert "instance-004" in out
    assert "instance-005" in out

    # Mode is shown: rw for masters, ro for replicas.
    lines = out.splitlines()
    inst001 = [line for line in lines if "instance-001" in line and "rw" in line]
    assert inst001, "instance-001 rw line not found"

    inst002 = [line for line in lines if "instance-002" in line and "ro" in line]
    assert inst002, "instance-002 ro line not found"

    for instance in INSTANCES:
        instance_lines = [line for line in lines if instance in line]
        assert len(instance_lines) == 1
        assert "OK" in instance_lines[0]
        assert "not reachable" not in instance_lines[0]

    # No 'M' marker in the output.
    assert "M instance" not in out


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
@pytest.mark.parametrize(
    "ccluster_app",
    ["unix", "tcp"],
    indirect=True,
    ids=["unix-per-instance", "tcp-per-instance"],
)
def test_topology_json(tt_cmd, ccluster_app):
    """tt cluster topology -c <config.yaml> --format json."""
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0

    data = _parse_json_output(out)
    assert "replicasets" in data
    replicasets = data["replicasets"]
    assert len(replicasets) == 2

    names = _instance_names_from_json(data)
    assert names == {
        "instance-001",
        "instance-002",
        "instance-003",
        "instance-004",
        "instance-005",
    }

    for rs_uuid, instances in replicasets.items():
        assert rs_uuid, "replicaset UUID must be non-empty"
        assert len(instances) > 0
        for inst in instances:
            assert inst["instance_uuid"], "instance_uuid must be non-empty"
            assert inst["instance_name"], "instance_name must be non-empty"
            assert inst["hostname"], "hostname must be non-empty"
            assert inst["mode"] == EXPECTED_MODES[inst["instance_name"]]
            assert inst["status"] == "OK"


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
@pytest.mark.parametrize("config_storage_type", ["etcd", "tcs"])
def test_topology_config_storage(
    tt_cmd,
    ccluster_app,
    config_storage_type,
    request,
):
    tmpdir = ccluster_app
    app_path = os.path.join(tmpdir, CCONFIG_APP_NAME)
    config_path = os.path.join(app_path, "config.yaml")
    with open(config_path) as config_file:
        config = config_file.read()

    for instance in INSTANCES:
        listen = (
            "            iproto:\n"
            "              listen:\n"
            f"                - uri: 'unix/:./{instance}.iproto'\n"
        )
        advertise = (
            "            iproto:\n"
            "              listen:\n"
            f"                - uri: 'unix/:/nonexistent/{instance}.iproto'\n"
            "              advertise:\n"
            f"                client: 'unix/:{app_path}/{instance}.iproto'\n"
        )
        assert listen in config
        config = config.replace(listen, advertise, 1)

    config_storage = request.getfixturevalue(config_storage_type)
    connection = config_storage.conn()
    if config_storage_type == "etcd":
        connection.put("/prefix/config/all", config)
    else:
        connection.call("config.storage.put", "/prefix/config/all", config)

    credentials = f"{config_storage.connection_username}:{config_storage.connection_password}@"
    uri = f"http://{credentials}{config_storage.host}:{config_storage.port}/prefix?timeout=5"

    try:
        if config_storage_type == "etcd":
            config_storage.enable_auth()

        rc, out = _topology_cmd(tt_cmd, uri, tmpdir, fmt="json")
    finally:
        if config_storage_type == "etcd":
            config_storage.disable_auth()

    assert rc == 0, out
    data = _parse_json_output(out)
    assert _instance_names_from_json(data) == set(INSTANCES)
    assert len(data["replicasets"]) == 2
    assert "replicaset-001" not in data["replicasets"]
    assert "replicaset-002" not in data["replicasets"]
    for instances in data["replicasets"].values():
        for instance in instances:
            assert instance["instance_uuid"]
            assert instance["hostname"]
            assert instance["mode"] == EXPECTED_MODES[instance["instance_name"]]
            assert instance["status"] == "OK"


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_unreachable_instance_included(tt_cmd, ccluster_app):
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")

    # Stop one instance from replicaset-001.
    rc, out = run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-003"],
        cwd=tmpdir,
    )
    assert rc == 0, out

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0
    assert "replicaset-001" in out
    assert "instance-001" in out
    assert "instance-002" in out

    lines = out.splitlines()

    # Find first non error line (greetings).
    greetings_position = lines.index("   • Active cluster topology")

    topo_lines = "\n".join(lines[greetings_position + 1 :])

    unreachable = [line for line in topo_lines.splitlines() if "instance-003" in line]
    assert len(unreachable) == 1
    assert "not reachable" in unreachable[0]
    replicaset_lines = [line for line in topo_lines.splitlines() if "replicaset-001" in line]
    assert len(replicaset_lines) == 1
    assert "not reachable" not in replicaset_lines[0]
    for instance in ("instance-001", "instance-002", "instance-004", "instance-005"):
        instance_lines = [line for line in topo_lines.splitlines() if instance in line]
        assert len(instance_lines) == 1
        assert "OK" in instance_lines[0]
        assert "not reachable" not in instance_lines[0]

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0
    data = _parse_json_output(out)
    assert _instance_names_from_json(data) == set(INSTANCES)
    instances = {
        instance["instance_name"]: instance
        for replicas in data["replicasets"].values()
        for instance in replicas
    }
    assert instances["instance-003"]["status"] == "not reachable"
    assert instances["instance-003"]["instance_uuid"]
    assert instances["instance-003"]["hostname"] == ""
    assert instances["instance-003"]["mode"] == "unknown"
    for instance in ("instance-001", "instance-002", "instance-004", "instance-005"):
        assert instances[instance]["status"] == "OK"
        assert instances[instance]["instance_uuid"]
        assert instances[instance]["hostname"]
        assert instances[instance]["mode"] == EXPECTED_MODES[instance]


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_unreachable_replicaset_included(tt_cmd, ccluster_app):
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")

    # Stop all instances of replicaset-002.
    rc, out = run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-004"],
        cwd=tmpdir,
    )
    assert rc == 0, out
    rc, out = run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-005"],
        cwd=tmpdir,
    )
    assert rc == 0, out

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0
    lines = out.splitlines()
    greetings_position = lines.index("   • Active cluster topology")
    topology_lines = lines[greetings_position + 1 :]

    assert any("replicaset-001" in line for line in topology_lines)
    reachable_replicaset_lines = [line for line in topology_lines if "replicaset-001" in line]
    assert len(reachable_replicaset_lines) == 1
    assert "not reachable" not in reachable_replicaset_lines[0]
    replicaset_lines = [line for line in topology_lines if "replicaset-002" in line]
    assert len(replicaset_lines) == 1
    assert "not reachable" in replicaset_lines[0]
    for instance in ("instance-004", "instance-005"):
        instance_lines = [line for line in topology_lines if instance in line]
        assert len(instance_lines) == 1
        assert "not reachable" in instance_lines[0]
    for instance in ("instance-001", "instance-002", "instance-003"):
        instance_lines = [line for line in topology_lines if instance in line]
        assert len(instance_lines) == 1
        assert "OK" in instance_lines[0]
        assert "not reachable" not in instance_lines[0]

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0
    data = _parse_json_output(out)
    names = _instance_names_from_json(data)
    assert names == set(INSTANCES)
    assert len(data["replicasets"]) == 2
    assert "replicaset-002" in data["replicasets"]
    assert {
        instance["instance_name"]: (
            instance["instance_uuid"],
            instance["mode"],
            instance["status"],
        )
        for instance in data["replicasets"]["replicaset-002"]
    } == {
        "instance-004": ("", "unknown", "not reachable"),
        "instance-005": ("", "unknown", "not reachable"),
    }
    reachable_rs_ids = [rid for rid in data["replicasets"] if rid != "replicaset-002"]
    assert len(reachable_rs_ids) == 1
    assert reachable_rs_ids[0], "reachable replicaset UUID must be non-empty"
    reachable_instances = {
        instance["instance_name"]: instance
        for replicaset_id, instances in data["replicasets"].items()
        if replicaset_id != "replicaset-002"
        for instance in instances
    }
    assert set(reachable_instances) == {"instance-001", "instance-002", "instance-003"}
    for name, instance in reachable_instances.items():
        assert instance["instance_uuid"]
        assert instance["hostname"]
        assert instance["mode"] == EXPECTED_MODES[name]
        assert instance["status"] == "OK"


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_unreachable_cluster_included(tt_cmd, ccluster_app):
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")

    rc, out = run_command_and_get_output(
        [tt_cmd, "stop", "-y", CCONFIG_APP_NAME],
        cwd=tmpdir,
    )
    assert rc == 0, out

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0, out
    lines = out.splitlines()
    greetings_position = lines.index("   • Active cluster topology")
    topology_lines = lines[greetings_position + 1 :]

    for replicaset in ("replicaset-001", "replicaset-002"):
        replicaset_lines = [line for line in topology_lines if replicaset in line]
        assert len(replicaset_lines) == 1
        assert "not reachable" in replicaset_lines[0]
    for instance in INSTANCES:
        instance_lines = [line for line in topology_lines if instance in line]
        assert len(instance_lines) == 1
        assert "not reachable" in instance_lines[0]

    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0, out
    data = _parse_json_output(out)
    assert set(data["replicasets"]) == {"replicaset-001", "replicaset-002"}
    assert _instance_names_from_json(data) == set(INSTANCES)
    for instances in data["replicasets"].values():
        for instance in instances:
            assert instance["instance_uuid"] == ""
            assert instance["hostname"] == ""
            assert instance["mode"] == "unknown"
            assert instance["status"] == "not reachable"


def test_topology_no_config(tt_cmd, tmpdir_with_cfg):
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "topology"],
        cwd=tmpdir_with_cfg,
    )
    assert rc != 0
    assert "required flag" in out or "config" in out.lower()


def test_topology_bad_format(tt_cmd, tmpdir_with_cfg):
    rc, out = run_command_and_get_output(
        [
            tt_cmd,
            "cluster",
            "topology",
            "-c",
            "/dev/null",
            "--format",
            "yaml",
        ],
        cwd=tmpdir_with_cfg,
    )
    assert rc != 0
    assert "unsupported format" in out
