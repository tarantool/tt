"""Per-instance restore of a replicaset whose config names its members only.

Without database.instance_uuid in the config, Tarantool binds an instance to
its name through the snapshot it boots from: before box.cfg it looks the
uuid the snapshot header carries up in that snapshot's _cluster, and refuses
to start when the row there has no name ("Instance name for ... is not set in
snapshot and UUID is missing in the config"). It reads the .snap alone, never
the journals that follow it.

A full backup ships the newest checkpoint, and every member of a restored
replicaset boots from that one .snap. On a freshly deployed replicaset that
checkpoint is the bootstrap snapshot: it holds the leader's _cluster row only,
the others registered into the journals afterwards. Restored from it, the
leader starts and every other member crash-loops, although the journals
replay their rows and every apply succeeds.

tt backup start --snapshot takes a new checkpoint before the full backup
opens. The scenario below starts from the bootstrap checkpoint -- no
box.snapshot() of its own before the full backup -- takes the full with
--snapshot, and expects the whole replicaset to come back on the restored
chain.
"""

import json
import shutil
import time
from pathlib import Path

import pytest
import tt_helper
from backup_helpers import (
    app_instance,
    archive_path_from_output,
    backup_plan,
    backup_upload,
    eval_yaml_app,
    exec_split,
    finalize_backup,
    start_backup,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

LEADER = "storage-001-a"
INSTANCES = (LEADER, "storage-001-b", "storage-001-c")

FULL_ID = "itest-unpin-full"
INC_ID = "itest-unpin-inc"
LABEL = "unpin-1"

SEEDED_ROWS = 10  # 1..10, written by the app's box.once at first boot.
FULL_ROWS = 30  # 11..30 land in the full backup.
POINT_ROWS = 40  # 31..40 precede the recovery point.
AFTER_ROWS = 50  # 41..50 are past it and must not come back.

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore apply requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_unpinned_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(60, tt_app, tt_app.running_instances, ["running"])


TT_UNPINNED_APP = dict(
    app_path="unpinned_app",
    app_name="app",
    instances=list(INSTANCES),
    running_targets=["app"],
    post_start=post_start_unpinned_app,
)


# -- Reading the cluster -----------------------------------------------------


def data_dir(tt_app, instance):
    """The instance's data directory: the app configures none of the three
    keys, so Tarantool and a restore agree on the default var/lib/<name>."""
    return Path(tt_app.path("var", "lib", instance))


def capture_members(tt, tt_app):
    """name -> {id, uuid} as the live replicaset knows its members."""
    rows = eval_yaml_app(
        tt,
        app_instance(tt_app, LEADER),
        "return box.space._cluster:select()",
    )

    return {row[2]: {"id": row[0], "uuid": row[1]} for row in rows}


def checkpoint_names(tt, tt_app):
    """The member names the leader's newest checkpoint records in _cluster,
    read the way Tarantool reads them before box.cfg: the .snap alone."""
    return eval_yaml_app(
        tt,
        app_instance(tt_app, LEADER),
        """
        local fio = require('fio')
        local xlog = require('xlog')
        local checkpoints = box.info.gc().checkpoints
        local path = fio.pathjoin(box.cfg.memtx_dir, string.format('%020d.snap',
            checkpoints[#checkpoints].signature))
        local names = {}
        for _, row in xlog.pairs(path) do
            local space_id = row.BODY.space_id
            if space_id ~= nil and space_id > box.schema.CLUSTER_ID then
                break
            end
            if space_id == box.schema.CLUSTER_ID then
                table.insert(names, row.BODY.tuple[3])
            end
        end
        table.sort(names)
        return names
        """,
    )


def wait_replicaset_connected(tt, tt_app, members, timeout=60):
    """Wait until the leader feeds every other member."""
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = {
            name: eval_yaml_app(
                tt,
                app_instance(tt_app, LEADER),
                f"local peer = box.info.replication[{members[name]['id']}] "
                "return peer and peer.downstream and peer.downstream.status or 'none'",
            )
            for name in INSTANCES
            if name != LEADER
        }
        if all(status == "follow" for status in last.values()):
            return
        time.sleep(0.5)
    raise AssertionError(f"the replicaset did not connect; last seen: {last}")


def instance_state(tt, tt_app, instance):
    return eval_yaml_app(
        tt,
        app_instance(tt_app, instance),
        """
        local cluster = {}
        for _, row in box.space._cluster:pairs() do
            table.insert(cluster, {row[1], row[2], row[3]})
        end
        local max = box.space.restore_test.index.pk:max()
        return {
            uuid = box.info.uuid,
            name = box.info.name,
            status = box.info.status,
            rows = box.space.restore_test:len(),
            max_id = max and max[1] or nil,
            cluster = cluster,
        }
        """,
    )


# -- Driving the backup stack ------------------------------------------------


def insert_rows(tt, target, begin, end):
    eval_yaml_app(
        tt,
        target,
        f"for i = {begin}, {end} do box.space.restore_test:replace{{i, 'row-' .. i}} end "
        "return true",
    )


def backup_and_upload(tt, tt_app, target, storage, backup_id, kind, tmp_path, extra_args=None):
    """plan -> start -> upload -> finalize, the way the orchestrator drives a
    node; extra_args go to start."""
    rc, out = backup_plan(
        tt,
        kind,
        storage.uri,
        config=tt_app.path("config.yaml"),
        fmt="json",
    )
    assert rc == 0, f"tt backup plan --target={kind} failed:\n{out}"
    plan = json.loads(out.strip())
    plan_path = tmp_path / f"plan-{kind}.json"
    plan_path.write_text(out.strip(), encoding="utf-8")

    from_vclock = None
    if kind == "incremental":
        from_vclock = next(iter(plan["replicasets"].values()))["from_vclock"]

    rc, start_out = start_backup(
        tt, target, backup_id, from_vclock=from_vclock, extra_args=extra_args
    )
    assert rc == 0, f"tt backup start failed:\n{start_out}"

    archive = archive_path_from_output(start_out)
    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=archive.removesuffix(".tar.zst") + ".json",
        plan=str(plan_path),
        backup_id=backup_id,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    rc, out = finalize_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup finalize failed:\n{out}"

    return start_out


# -- The scenario ------------------------------------------------------------


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_UNPINNED_APP)
def test_restore_members_named_in_config_only(tt, tt_app, tmp_path):
    leader = app_instance(tt_app, LEADER)

    members = capture_members(tt, tt_app)
    assert sorted(members) == sorted(INSTANCES), members
    wait_replicaset_connected(tt, tt_app, members)

    # The precondition the scenario is about: the newest checkpoint is the
    # bootstrap snapshot, which records the leader alone.
    assert checkpoint_names(tt, tt_app) == [LEADER]

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    # -- The full backup is taken with --snapshot, no checkpoint of the
    # test's own.
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)
    backup_and_upload(
        tt, tt_app, leader, storage, FULL_ID, "full", tmp_path, extra_args=["--snapshot"]
    )

    # tt took the checkpoint the full backup ships.
    assert checkpoint_names(tt, tt_app) == sorted(INSTANCES)

    # A cluster point is recorded to the whole second, while the storage's
    # coverage starts at the full manifest's creation time, fraction and all.
    time.sleep(1.1)

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    created = eval_yaml_app(
        tt,
        leader,
        f"return box.backup.recovery_point.create({{label = '{LABEL}'}})",
    )
    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)

    backup_and_upload(tt, tt_app, leader, storage, INC_ID, "incremental", tmp_path)

    # -- The plan names every member with the uuid the live replicaset knew.
    dest = tmp_path / "restore"
    rc, stdout, stderr = exec_split(
        tt,
        "restore",
        "plan",
        "--target-time",
        str(int(created["timestamp"])),
        "--backup-storage",
        storage.uri,
        "-d",
        str(dest),
        "-c",
        tt_app.path("config.yaml"),
        "--format",
        "json",
    )
    assert rc == 0, f"tt restore plan failed:\n{stdout}\n{stderr}"
    doc = json.loads(stdout)
    assert doc["status"] == "ok", doc

    replicaset_uuid, items = next(iter(doc["download_plan"].items()))
    assert [item["backup_id"] for item in items] == [FULL_ID, INC_ID], items
    point = doc["recovery_point"]["trim_to_by_replicaset"][replicaset_uuid]
    targets = {
        entry["instance_name"]: entry["patch_uuid"]
        for entry in doc["restore_targets"][replicaset_uuid]["instances"]
    }
    assert targets == {name: members[name]["uuid"] for name in INSTANCES}, doc

    # -- Stop, wipe, apply the same chain on every member.
    for name in INSTANCES:
        proc = tt.run("stop", app_instance(tt_app, name), "-y")
        assert proc.returncode == 0, f"tt stop {name} failed:\n{proc.stdout}"

    for name in INSTANCES:
        shutil.rmtree(data_dir(tt_app, name), ignore_errors=True)

    for name in INSTANCES:
        rc, out = tt.exec(
            "restore",
            "apply",
            "--archives",
            ",".join(item["artifact"] for item in items),
            "--checksums",
            ",".join(item["checksum_sha256"] for item in items),
            "-c",
            tt_app.path("config.yaml"),
            "--instance",
            name,
            "--work-dir",
            tt_app.path(),
            "--target-point",
            json.dumps(point),
            "--point-name",
            LABEL,
            "--patch-uuid",
            targets[name],
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

    # -- Every member comes back, under its own name and uuid.
    for name in INSTANCES:
        proc = tt.run("start", app_instance(tt_app, name))
        assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"

    assert tt_helper.wait_box_status(60, tt_app, tt_app.instances, ["running"]), (
        "a restored member did not start; the instance logs name the reason"
    )
    wait_replicaset_connected(tt, tt_app, members)

    expected_cluster = sorted(
        [members[name]["id"], members[name]["uuid"], name] for name in INSTANCES
    )
    for name in INSTANCES:
        state = instance_state(tt, tt_app, name)
        assert state["status"] == "running", (name, state)
        assert state["name"] == name, (name, state)
        assert state["uuid"] == members[name]["uuid"], (name, state)
        # 40 rows: the point's, none of the 41..50 written past it.
        assert state["rows"] == POINT_ROWS, (name, state)
        assert state["max_id"] == POINT_ROWS, (name, state)
        assert sorted(state["cluster"]) == expected_cluster, (name, state)
