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


@pytest.fixture
def ccluster_app(tt_cmd, tmpdir_with_cfg):
    """Start the centralized-config cluster app and tear it down after."""
    tmpdir = tmpdir_with_cfg
    app_path = os.path.join(tmpdir, CCONFIG_APP_NAME)
    shutil.copytree(CCONFIG_APP_DIR, app_path)

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
    cmd = [tt_cmd, "cluster", "topology", "-c", config_path]
    if fmt:
        cmd += ["--format", fmt]
    return run_command_and_get_output(cmd, cwd=tmpdir)


def _parse_json_output(output):
    """Extract and parse JSON from combined stdout+stderr output."""
    idx = output.find("\n{")
    if idx == -1:
        idx = output.find("{")
    raw = output[idx + 1 :] if idx != -1 and output[idx] == "\n" else output[idx:]
    return json.loads(raw)


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
    """tt cluster topology -c <config.yaml> — table output with master markers."""
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0

    # Two replicasets.
    assert "replicaset-001" in out
    assert "replicaset-002" in out

    # Master (rw) is marked with M, replicas (read) with •.
    assert "M instance-001" in out
    assert "instance-002" in out
    assert "instance-003" in out
    assert "M instance-004" in out
    assert "M instance-005" in out

    # Mode is shown.
    lines = out.splitlines()
    inst001 = [line for line in lines if "instance-001" in line and "M" in line]
    assert inst001, "instance-001 master line not found"
    assert "rw" in inst001[0]


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_json(tt_cmd, ccluster_app):
    """tt cluster topology -c <config.yaml> --format json — backup.Topology."""
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


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_unreachable_instance_excluded(tt_cmd, ccluster_app):
    """A stopped instance must not appear in the live topology."""
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")

    # Stop one instance from replicaset-001.
    run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-003"],
        cwd=tmpdir,
    )

    # Table format: instance-003 must not appear as a topology entry.
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0
    assert "replicaset-001" in out
    assert "M instance-001" in out
    assert "instance-002" in out

    lines = out.splitlines()

    # Find first non error line (greetings).
    greetings_position = lines.index("   • Active cluster topology")

    topo_lines = "\n".join(lines[greetings_position + 1 :])

    assert "instance-003" not in topo_lines, "stopped instance must not be in topology"
    assert "instance-001" in topo_lines
    assert "instance-002" in topo_lines

    # JSON format: instance-003 must not be in the output.
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0
    data = _parse_json_output(out)
    names = _instance_names_from_json(data)
    assert "instance-003" not in names, "stopped instance must not be in JSON topology"
    assert "instance-001" in names
    assert "instance-002" in names
    # Replicaset-002 is untouched.
    assert "instance-004" in names
    assert "instance-005" in names


@pytest.mark.skipif(
    tarantool_major_version < 3,
    reason="centralized config requires Tarantool 3.x",
)
def test_topology_unreachable_replicaset_excluded(tt_cmd, ccluster_app):
    """If all instances of a replicaset are stopped, the replicaset disappears."""
    tmpdir = ccluster_app
    config_path = os.path.join(tmpdir, CCONFIG_APP_NAME, "config.yaml")

    # Stop all instances of replicaset-002.
    run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-004"],
        cwd=tmpdir,
    )
    run_command_and_get_output(
        [tt_cmd, "stop", "-y", f"{CCONFIG_APP_NAME}:instance-005"],
        cwd=tmpdir,
    )

    # Table format: replicaset-002 must not appear at all.
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir)
    assert rc == 0
    assert "replicaset-001" in out
    assert "replicaset-002" not in out, "stopped replicaset must not be in topology"

    # JSON format: replicaset-002 instances must not appear.
    rc, out = _topology_cmd(tt_cmd, config_path, tmpdir, fmt="json")
    assert rc == 0
    data = _parse_json_output(out)
    names = _instance_names_from_json(data)
    assert "instance-004" not in names
    assert "instance-005" not in names
    assert "instance-001" in names
    assert "instance-002" in names
    assert "instance-003" in names

    # Only one replicaset remains.
    assert len(data["replicasets"]) == 1


def test_topology_no_config(tt_cmd, tmpdir_with_cfg):
    """Missing -c flag must error."""
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "topology"],
        cwd=tmpdir_with_cfg,
    )
    assert rc != 0
    assert "required flag" in out or "config" in out.lower()


def test_topology_bad_format(tt_cmd, tmpdir_with_cfg):
    """Unsupported --format value must error."""
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
