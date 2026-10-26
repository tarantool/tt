"""tt restore apply run per instance: one chain distributed to every member.

`tt restore plan` hands every member of the replicaset its own restore
target: the chain the master was backed up on is applied once per instance,
over the same archives and to the same recovery point, stamping each
instance's ORIGINAL uuid -- the one its name owned in the backed-up cluster's
_cluster, which the plan reads out of the backup's recorded membership --
into the headers. Every member then comes up on the same restored chain,
registered under its own numeric id, and the replicaset is whole with no
empty join anywhere: a member that comes up empty has nothing to serve while
the quorum waits for it, which under synchronous replication never resolves.

The plan document is the orchestrator's whole input: the ordered chain, the
checksums, the trim point, the label, and every member's uuid in
restore_targets.instances. The loop below applies exactly those, and the
live identities captured before the teardown are the verification, not the
source: the uuids the plan hands out must be the ones the backed-up cluster
knew each instance by.

The second test runs the same distribution under synchronous replication:
a synchronous space behind a quorum of 2 out of 2 members, where the one
apply + wiped rejoin flow deadlocks -- an empty replica cannot acknowledge,
the quorum never forms, the queue never unfreezes. The distributed chain
restores every member to an acknowledger, and the promote and the synchronous
commit that follow prove it.
"""

import json
import shutil
import time
from pathlib import Path

import pytest
import tt_helper
from backup_helpers import (
    app_config_path,
    app_instance,
    app_name,
    archive_path_from_output,
    backup_dir,
    backup_plan,
    backup_upload,
    eval_yaml_app,
    exec_split,
    finalize_backup,
    instance_data_dir,
    start_backup,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"
STORAGE_1_B = "storage-001-b"
INSTANCES = (STORAGE_1_A, STORAGE_1_B)

# The identities pinned in multi_apply_app/config.yaml: what the live cluster
# is captured holding below, and the only uuids a per-instance apply can stamp
# and still leave a startable member -- each instance's own original one.
REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
LEADER_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
REPLICA_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaab"
ORIGINAL_MEMBERS = {
    STORAGE_1_A: {"id": 1, "uuid": LEADER_UUID},
    STORAGE_1_B: {"id": 2, "uuid": REPLICA_UUID},
}

FULL_ID = "itest-mapl-full"
INC_ID = "itest-mapl-inc"
LABEL = "mapl-1"

SYNC_FULL_ID = "itest-msyn-full"
SYNC_INC_ID = "itest-msyn-inc"
SYNC_LABEL = "msyn-1"

SYNC_SPACE = "restore_test_sync"

SEEDED_ROWS = 10  # 1..10, written by the app's box.once at first boot.
FULL_ROWS = 30  # 11..30 land in the full backup.
POINT_ROWS = 40  # 31..40 precede the recovery point.
AFTER_ROWS = 50  # 41..50 are past it and must not come back.

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore apply requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_multi_apply_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


TT_MULTI_APPLY_APP = dict(
    app_path="multi_apply_app",
    app_name="app",
    instances=list(INSTANCES),
    running_targets=["app:*"],
    post_start=post_start_multi_apply_app,
)

TT_MULTI_SYNC_APP = dict(
    app_path="multi_sync_app",
    app_name="app",
    instances=list(INSTANCES),
    running_targets=["app:*"],
    post_start=post_start_multi_apply_app,
)


# -- Reading the live cluster ------------------------------------------------


def data_dir(tt_app, instance):
    """The instance's data directory: the app configures none of the three
    keys, so Tarantool and a restore agree on the default var/lib/<name>."""
    return Path(instance_data_dir(tt_app, instance))


def restore_marker(tt_app, instance):
    """The marker apply writes beside the snapshot directory, named after it."""
    return data_dir(tt_app, instance).parent / f"{instance}.restore_state.json"


def instance_state(tt, tt_app, instance, space="restore_test"):
    """Everything a restored instance is asserted by, in one round trip.

    The rows and the max id are read out of one space; the identities and the
    vclock belong to the instance and come back the same whichever space is
    named.
    """
    return eval_yaml_app(
        tt,
        app_instance(tt_app, instance),
        f"""
        local cluster = {{}}
        for _, row in box.space._cluster:pairs() do
            table.insert(cluster, {{row[1], row[2]}})
        end
        local vclock = {{}}
        for id, lsn in pairs(box.info.vclock) do
            if lsn > 0 then vclock[id] = lsn end
        end
        local max = box.space.{space}.index.pk:max()
        return {{
            uuid = box.info.uuid,
            name = box.info.name,
            status = box.info.status,
            ro = box.info.ro,
            rows = box.space.{space}:len(),
            max_id = max and max[1] or nil,
            vclock = vclock,
            cluster = cluster,
        }}
        """,
    )


def capture_members(tt, tt_app):
    """The original identities of the replicaset, read off the live cluster:
    the numeric id _cluster gives each instance's uuid, the uuid each instance
    answers to. These are the uuids the per-instance applies have to stamp."""
    rows = eval_yaml_app(
        tt,
        app_instance(tt_app, STORAGE_1_A),
        "return box.space._cluster:select()",
    )
    ids_by_uuid = {row[1]: row[0] for row in rows}

    members = {}
    for name in INSTANCES:
        uuid = eval_yaml_app(tt, app_instance(tt_app, name), "return box.info.uuid")
        members[name] = {"id": ids_by_uuid[uuid], "uuid": uuid}

    return members


def peer_status(tt, target, side):
    """The status of the other member's replication link, seen from either
    side: 'downstream' on the leader feeding it, 'upstream' on the replica."""
    return eval_yaml_app(
        tt,
        target,
        f"""
        for _, peer in pairs(box.info.replication) do
            if peer.id ~= box.info.id then
                return (peer.{side} or {{}}).status or 'none'
            end
        end
        return 'no peer'
        """,
    )


def peer_view(tt, target, peer_id):
    """The other member as box.info().replication names it: its instance name
    and the state of the link, from whichever side has one."""
    return eval_yaml_app(
        tt,
        target,
        f"""
        local peer = box.info.replication[{peer_id}]
        return {{
            name = peer.name,
            upstream = peer.upstream and peer.upstream.status or nil,
            downstream = peer.downstream and peer.downstream.status or nil,
        }}
        """,
    )


def wait_replicaset_connected(tt, tt_app, timeout=60):
    """Wait until the leader feeds the replica and the replica follows it.

    A member appears in box.info.replication only once it subscribes, so this
    is also the wait for the replicaset being whole again after a restart.
    """
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = (
            peer_status(tt, app_instance(tt_app, STORAGE_1_A), "downstream"),
            peer_status(tt, app_instance(tt_app, STORAGE_1_B), "upstream"),
        )
        if last == ("follow", "follow"):
            return
        time.sleep(0.5)
    raise AssertionError(f"the replicaset did not reconnect; last seen: {last}")


def wait_rows(tt, tt_app, instance, rows, timeout=30, space="restore_test"):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = eval_yaml_app(
            tt,
            app_instance(tt_app, instance),
            f"return box.space.{space}:len()",
        )
        if last == rows:
            return
        time.sleep(0.5)
    raise AssertionError(f"{instance} did not reach {rows} rows; last seen: {last}")


# -- Driving the backup stack ------------------------------------------------


def insert_rows(tt, target, begin, end, spaces=("restore_test",)):
    """Replace rows begin..end in each of the spaces.

    A synchronous space blocks in the call until the quorum acknowledges, so
    on a replicaset that is up the rows of every space land together.
    """
    space_list = ", ".join(f"'{space}'" for space in spaces)
    eval_yaml_app(
        tt,
        target,
        f"for _, space in ipairs({{{space_list}}}) do "
        f"for i = {begin}, {end} do box.space[space]:replace{{i, 'row-' .. i}} end "
        "end return true",
    )


def create_recovery_point(tt, target, label):
    return eval_yaml_app(
        tt,
        target,
        f"return box.backup.recovery_point.create({{label = '{label}'}})",
    )


def backup_plan_file(tt, target, storage_uri, app, tmp_path, name):
    """Run tt backup plan and keep its raw output as the file upload reads."""
    rc, out = backup_plan(tt, target, storage_uri, app=app, fmt="json")
    assert rc == 0, f"tt backup plan --target={target} failed:\n{out}"

    path = tmp_path / name
    path.write_text(out.strip(), encoding="utf-8")

    return json.loads(out.strip()), str(path)


def backup_and_upload(tt, target, storage, backup_id, plan_path, from_vclock=None):
    """start -> upload -> finalize, the way the orchestrator drives a node."""
    rc, out = start_backup(tt, target, backup_id, from_vclock=from_vclock)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)
    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=archive.removesuffix(".tar.zst") + ".json",
        plan=plan_path,
        backup_id=backup_id,
    )
    assert rc == 0, f"tt backup upload failed:\n{out}"

    rc, out = finalize_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup finalize failed:\n{out}"
    assert not Path(backup_dir(backup_id)).exists(), "finalize sweeps what upload left"


def restore_plan_document(tt, storage_uri, target_time, dest, config):
    """Run tt restore plan and return (exit code, parsed document)."""
    rc, out, err = exec_split(
        tt,
        "restore",
        "plan",
        "--target-time",
        target_time,
        "--backup-storage",
        storage_uri,
        "-d",
        str(dest),
        "-c",
        str(config),
        "--format",
        "json",
    )
    assert out.strip(), f"tt restore plan printed no document (exit {rc}):\n{err}"

    return rc, json.loads(out)


def synchro_state(tt, tt_app):
    """The synchronous queue and every member's mode, for a failure message:
    what the queue is waiting on when a promote or a commit does not return."""
    return {
        name: eval_yaml_app(
            tt,
            app_instance(tt_app, name),
            """
            return {
                ro = box.info.ro,
                status = box.info.status,
                term = box.info.synchro.queue.term,
                busy = box.info.synchro.queue.busy,
                len = box.info.synchro.queue.len,
            }
            """,
        )
        for name in INSTANCES
    }


def promote_queue_owner(tt, tt_app):
    """box.ctl.promote() on the member whose uuid owns the queue.

    The leader the backup was taken on is the queue owner the restored chain
    carries, and taking a new term needs the quorum's acknowledgements -- the
    exact step the one-apply-and-wipe flow deadlocks on: an empty replica
    cannot acknowledge, the quorum of 2 never forms, the promote never
    confirms. On a replicaset restored member by member it returns; on one
    that cannot, the report says what the queue is still waiting for.
    """
    try:
        return eval_yaml_app(
            tt,
            app_instance(tt_app, STORAGE_1_A),
            "box.ctl.promote() return true",
        )
    except AssertionError as exc:
        raise AssertionError(
            f"box.ctl.promote() did not confirm; synchro state: {synchro_state(tt, tt_app)}",
        ) from exc


def commit_sync_row(tt, tt_app, row_id):
    """Replace a row in the synchronous space and return once it commits.

    The call is the assertion: a synchronous replace returns to its client
    only after replication_synchro_quorum members have acknowledged it, so a
    queue that cannot gather them fails the call after
    replication_synchro_timeout instead of returning.
    """
    try:
        return eval_yaml_app(
            tt,
            app_instance(tt_app, STORAGE_1_A),
            f"box.space.{SYNC_SPACE}:replace({{{row_id}, 'row-{row_id}'}}) return true",
        )
    except AssertionError as exc:
        raise AssertionError(
            f"the synchronous insert of row {row_id} did not commit; "
            f"synchro state: {synchro_state(tt, tt_app)}",
        ) from exc


def apply_for_instance(tt, tt_app, items, point, label, instance, uuid):
    """tt restore apply for one member of the replicaset.

    Every member gets the same chain, the same point and the same label; the
    only thing that differs is --patch-uuid, which takes the instance's own
    original uuid, the one its name owned in the backed-up cluster's _cluster.
    The target names the instance, so apply takes its data directories from
    tt, the ones tt starts it with.
    """
    return tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(item["artifact"] for item in items),
        "--checksums",
        ",".join(item["checksum_sha256"] for item in items),
        f"{app_name(tt_app)}:{instance}",
        "--target-point",
        json.dumps(point),
        "--point-name",
        label,
        "--patch-uuid",
        uuid,
    )


# -- The scenario ------------------------------------------------------------


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MULTI_APPLY_APP)
def test_apply_per_instance_bring_up_the_whole_replicaset(tt, tt_app, tmp_path):
    leader = app_instance(tt_app, STORAGE_1_A)
    config_path = app_config_path(tt_app)

    # -- The live replicaset: original identities, both members in step.
    wait_replicaset_connected(tt, tt_app)
    members = capture_members(tt, tt_app)
    assert members == ORIGINAL_MEMBERS, members

    for name in INSTANCES:
        state = instance_state(tt, tt_app, name)
        assert state["rows"] == SEEDED_ROWS, (name, state)

    # -- A real chain in a real storage: rows 11..30 in the full backup,
    # the point at row 40 inside the increment, rows 41..50 past it.
    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)

    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        app_name(tt_app),
        tmp_path,
        "plan-full.json",
    )
    assert plan_full["mode"] == "full"
    backup_and_upload(tt, leader, storage, FULL_ID, plan_full_path)

    # A cluster point is recorded to the whole second, while the storage's
    # coverage starts at the full manifest's creation time, fraction and all:
    # a point taken in the same second names a moment the storage covers
    # nothing of, and every plan against it is out of range.
    time.sleep(1.1)

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    created = create_recovery_point(tt, leader, LABEL)
    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)

    assert created["replica_id"] == members[STORAGE_1_A]["id"], created

    plan_inc, plan_inc_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        app_name(tt_app),
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["previous_backup_id"] == FULL_ID
    backup_and_upload(
        tt,
        leader,
        storage,
        INC_ID,
        plan_inc_path,
        from_vclock=plan_inc["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # -- The plan: what it resolves the moment into, and what it models.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        str(int(created["timestamp"])),
        dest,
        config_path,
    )

    assert rc == 0, doc
    assert doc["status"] == "ok", doc
    assert doc["warnings"] == [], doc
    assert doc["recovery_point"]["label"] == LABEL

    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]
    assert point == {"replica_id": created["replica_id"], "lsn": created["lsn"]}, doc

    items = doc["download_plan"][REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [FULL_ID, INC_ID], items
    assert items[-1]["trim_xlog"] is True, items
    for item in items:
        assert Path(item["artifact"]).is_file(), item

    # What the plan models now: every member is restored onto itself, stamped
    # with its own original uuid. The legacy single-target fields still name
    # the master, and rejoin is empty -- nobody bootstraps fresh, every member
    # was backed up.
    target = doc["restore_targets"][REPLICASET_UUID]
    assert target["instance_name"] == STORAGE_1_A, doc
    assert target["patch_uuid"] == LEADER_UUID, doc
    assert target["instances"] == [
        {"instance_name": STORAGE_1_A, "patch_uuid": LEADER_UUID, "role": "master"},
        {"instance_name": STORAGE_1_B, "patch_uuid": REPLICA_UUID, "role": "replica"},
    ], doc
    assert target.get("rejoin", []) == [], doc

    # The uuids the plan hands out are the ones the live cluster was captured
    # holding: the applies below take them from the document, and the capture
    # verifies it rather than feeding it.
    plan_uuids = {entry["instance_name"]: entry["patch_uuid"] for entry in target["instances"]}
    assert plan_uuids == {name: members[name]["uuid"] for name in INSTANCES}, doc

    # -- Stop the replicaset, wipe the members, distribute the chain.
    for name in INSTANCES:
        proc = tt.run("instance", "stop", app_instance(tt_app, name), "-y")
        assert proc.returncode == 0, f"tt stop {name} failed:\n{proc.stdout}"

    for name in INSTANCES:
        shutil.rmtree(data_dir(tt_app, name), ignore_errors=True)

    for name in INSTANCES:
        rc, out = apply_for_instance(
            tt,
            tt_app,
            items,
            point,
            LABEL,
            name,
            plan_uuids[name],
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

        # Each member holds the restored files before anyone starts: with the
        # data directories wiped, there is nothing else to come up on, and a
        # member apply skipped would have to boot empty.
        restored = sorted(path.name for path in data_dir(tt_app, name).iterdir())
        assert any(file.endswith(".snap") for file in restored), (name, restored)
        assert any(".xlog" in file for file in restored), (name, restored)

        marker_path = restore_marker(tt_app, name)
        assert marker_path.is_file(), f"no restore marker for {name}: {marker_path}"

        marker = json.loads(marker_path.read_text(encoding="utf-8"))
        assert marker["point_name"] == LABEL, (name, marker)
        assert marker["target_point"] == point, (name, marker)
        assert marker["instance_name"] == name, (name, marker)
        assert marker["instance_uuid"] == members[name]["uuid"], (name, marker)
        assert marker["archives"] == [Path(item["artifact"]).name for item in items], (
            name,
            marker,
        )

    # -- The replicaset comes back on the distributed chain.
    for name in INSTANCES:
        proc = tt.run("instance", "start", app_instance(tt_app, name))
        assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"

    assert tt_helper.wait_box_status(30, tt_app, tt_app.instances, ["running"])
    wait_replicaset_connected(tt, tt_app)

    states = {name: instance_state(tt, tt_app, name) for name in INSTANCES}

    for name, state in states.items():
        assert state["status"] == "running", (name, state)
        assert state["uuid"] == members[name]["uuid"], (name, state)
        assert state["name"] == name, (name, state)
        # 40 rows: 1..10 from app start, 11..30 from the full backup, 31..40
        # from the increment, trimmed at the point. 50 would mean the trim
        # never happened, less than 40 that part of the chain never replayed.
        assert state["rows"] == POINT_ROWS, (name, state)
        assert state["max_id"] == POINT_ROWS, (name, state)
        # The original identities came back on every node, not only on the
        # master the backup was taken on.
        assert state["cluster"] == [
            [members[member]["id"], members[member]["uuid"]] for member in INSTANCES
        ], (name, state)

    # Vclocks equal across the replicaset, with the backed-up master's axis
    # sitting exactly on the point: every member replayed the same trimmed
    # chain (system records on other axes come with it), and none of them
    # arrived at the data over replication from an empty boot. The replica's
    # own axis staying absent is part of that: a member that had bootstrapped
    # empty would carry records of its own.
    assert states[STORAGE_1_A]["vclock"] == states[STORAGE_1_B]["vclock"], states
    for name, state in states.items():
        assert state["vclock"][point["replica_id"]] == point["lsn"], (name, state)
    assert states[STORAGE_1_B]["vclock"].get(members[STORAGE_1_B]["id"]) is None, states

    # Everyone connected, under the names the cluster knows them by: the
    # leader feeds the replica and the replica follows it.
    leader_view = peer_view(tt, leader, members[STORAGE_1_B]["id"])
    assert leader_view["name"] == STORAGE_1_B, leader_view
    assert leader_view["downstream"] == "follow", leader_view

    replica_view = peer_view(
        tt,
        app_instance(tt_app, STORAGE_1_B),
        members[STORAGE_1_A]["id"],
    )
    assert replica_view["name"] == STORAGE_1_A, replica_view
    assert replica_view["upstream"] == "follow", replica_view

    # -- The replicaset writes and serves: a fresh row through the leader,
    # read back on both members, vclocks moving together.
    eval_yaml_app(
        tt,
        leader,
        f"box.space.restore_test:replace{{{POINT_ROWS + 1}, 'row-{POINT_ROWS + 1}'}} return true",
    )
    wait_rows(tt, tt_app, STORAGE_1_B, POINT_ROWS + 1)

    for name in INSTANCES:
        row = eval_yaml_app(
            tt,
            app_instance(tt_app, name),
            f"return box.space.restore_test:get({POINT_ROWS + 1})",
        )
        assert row == [POINT_ROWS + 1, f"row-{POINT_ROWS + 1}"], (name, row)

        state = instance_state(tt, tt_app, name)
        assert state["rows"] == POINT_ROWS + 1, (name, state)
        assert state["vclock"][point["replica_id"]] == point["lsn"] + 1, (name, state)

    # The synchronous twin of this scenario -- the same distribution with a
    # synchronous space behind a quorum of 2 out of 2 members, where the
    # promote and the commit are the steps an empty rejoin could never
    # acknowledge -- is test_apply_per_instance_with_synchro_quorum below.


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MULTI_SYNC_APP)
def test_apply_per_instance_with_synchro_quorum(tt, tt_app, tmp_path):
    """The same distribution under synchronous replication.

    Every synchronous write and every term change needs a quorum of 2 out of
    2 members, so this is the deadlock probe end to end: both members come up
    as acknowledgers of the same restored chain rather than one of them
    joining the other empty, the queue unfreezes under the leader the backup
    was taken on, and a synchronous write commits.
    """
    leader = app_instance(tt_app, STORAGE_1_A)
    config_path = app_config_path(tt_app)
    spaces = ("restore_test", SYNC_SPACE)

    def assert_sync_configuration():
        for name in INSTANCES:
            actual = eval_yaml_app(
                tt,
                app_instance(tt_app, name),
                f"""
                return {{
                    is_sync = box.space.{SYNC_SPACE}.is_sync,
                    quorum = box.cfg.replication_synchro_quorum,
                    timeout = box.cfg.replication_synchro_timeout,
                    members = box.space._cluster:count(),
                }}
                """,
            )
            assert actual == {
                "is_sync": True,
                "quorum": 2,
                "timeout": 10,
                "members": len(INSTANCES),
            }, (name, actual)

    # -- The live replicaset: original identities, both spaces seeded.
    wait_replicaset_connected(tt, tt_app)
    members = capture_members(tt, tt_app)
    assert members == ORIGINAL_MEMBERS, members

    for name in INSTANCES:
        for space in spaces:
            state = instance_state(tt, tt_app, name, space=space)
            assert state["rows"] == SEEDED_ROWS, (name, space, state)

    assert_sync_configuration()

    # -- The chain: rows 11..30 in the full backup, the point at row 40 inside
    # the increment, rows 41..50 past it, every space written in lockstep.
    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS, spaces=spaces)

    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        app_name(tt_app),
        tmp_path,
        "plan-full.json",
    )
    assert plan_full["mode"] == "full"
    backup_and_upload(tt, leader, storage, SYNC_FULL_ID, plan_full_path)

    # The same-second trap as in the async scenario: a point taken in the
    # second the full manifest was created in is before the storage covers
    # anything.
    time.sleep(1.1)

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS, spaces=spaces)
    created = create_recovery_point(tt, leader, SYNC_LABEL)
    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS, spaces=spaces)

    assert created["replica_id"] == members[STORAGE_1_A]["id"], created

    plan_inc, plan_inc_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        app_name(tt_app),
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["previous_backup_id"] == SYNC_FULL_ID
    backup_and_upload(
        tt,
        leader,
        storage,
        SYNC_INC_ID,
        plan_inc_path,
        from_vclock=plan_inc["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # -- The plan resolves the moment the same way; its single restore target
    # is replaced by the per-instance loop below.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        str(int(created["timestamp"])),
        dest,
        config_path,
    )

    assert rc == 0, doc
    assert doc["status"] == "ok", doc
    assert doc["warnings"] == [], doc
    assert doc["recovery_point"]["label"] == SYNC_LABEL

    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]
    assert point == {"replica_id": created["replica_id"], "lsn": created["lsn"]}, doc

    items = doc["download_plan"][REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [SYNC_FULL_ID, SYNC_INC_ID], items
    assert items[-1]["trim_xlog"] is True, items

    target = doc["restore_targets"][REPLICASET_UUID]
    assert target["instances"] == [
        {"instance_name": STORAGE_1_A, "patch_uuid": LEADER_UUID, "role": "master"},
        {"instance_name": STORAGE_1_B, "patch_uuid": REPLICA_UUID, "role": "replica"},
    ], doc
    assert target.get("rejoin", []) == [], doc

    plan_uuids = {entry["instance_name"]: entry["patch_uuid"] for entry in target["instances"]}
    assert plan_uuids == {name: members[name]["uuid"] for name in INSTANCES}, doc

    # -- Stop the replicaset, wipe the members, distribute the chain.
    for name in INSTANCES:
        proc = tt.run("instance", "stop", app_instance(tt_app, name), "-y")
        assert proc.returncode == 0, f"tt stop {name} failed:\n{proc.stdout}"

    for name in INSTANCES:
        shutil.rmtree(data_dir(tt_app, name), ignore_errors=True)

    for name in INSTANCES:
        rc, out = apply_for_instance(
            tt,
            tt_app,
            items,
            point,
            SYNC_LABEL,
            name,
            plan_uuids[name],
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

        restored = sorted(path.name for path in data_dir(tt_app, name).iterdir())
        assert any(file.endswith(".snap") for file in restored), (name, restored)
        assert any(".xlog" in file for file in restored), (name, restored)

        marker_path = restore_marker(tt_app, name)
        assert marker_path.is_file(), f"no restore marker for {name}: {marker_path}"

        marker = json.loads(marker_path.read_text(encoding="utf-8"))
        assert marker["point_name"] == SYNC_LABEL, (name, marker)
        assert marker["target_point"] == point, (name, marker)
        assert marker["instance_name"] == name, (name, marker)
        assert marker["instance_uuid"] == members[name]["uuid"], (name, marker)
        assert marker["archives"] == [Path(item["artifact"]).name for item in items], (
            name,
            marker,
        )

    # -- The replicaset comes back on the distributed chain: no member stuck
    # in startup, everyone following everyone.
    for name in INSTANCES:
        proc = tt.run("instance", "start", app_instance(tt_app, name))
        assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"

    assert tt_helper.wait_box_status(30, tt_app, tt_app.instances, ["running"])
    wait_replicaset_connected(tt, tt_app)

    states = {name: instance_state(tt, tt_app, name) for name in INSTANCES}

    for name, state in states.items():
        assert state["status"] == "running", (name, state)
        assert state["uuid"] == members[name]["uuid"], (name, state)
        assert state["cluster"] == [
            [members[member]["id"], members[member]["uuid"]] for member in INSTANCES
        ], (name, state)
        for space in spaces:
            space_state = instance_state(tt, tt_app, name, space=space)
            assert space_state["rows"] == POINT_ROWS, (name, space, space_state)
            assert space_state["max_id"] == POINT_ROWS, (name, space, space_state)

    # Both kinds of write came back to the point and to one vclock: the
    # synchronous rows are there with the CONFIRM records they were committed
    # by, all of them written before the point was taken.
    assert states[STORAGE_1_A]["vclock"] == states[STORAGE_1_B]["vclock"], states
    for name, state in states.items():
        assert state["vclock"][point["replica_id"]] == point["lsn"], (name, state)
    assert states[STORAGE_1_B]["vclock"].get(members[STORAGE_1_B]["id"]) is None, states

    assert_sync_configuration()

    # -- The queue unfreezes under the leader the backup was taken on, and the
    # leader is the queue owner the restored chain carries: the promote takes
    # a new term with both members acknowledging it.
    assert promote_queue_owner(tt, tt_app) is True

    leader_state = instance_state(tt, tt_app, STORAGE_1_A)
    assert leader_state["ro"] is False, (leader_state, synchro_state(tt, tt_app))
    replica_state = instance_state(tt, tt_app, STORAGE_1_B)
    assert replica_state["ro"] is True, (replica_state, synchro_state(tt, tt_app))

    # -- A synchronous write commits: the call returns only after the quorum
    # acknowledged it, and the row reaches the other member.
    assert commit_sync_row(tt, tt_app, POINT_ROWS + 1) is True
    wait_rows(tt, tt_app, STORAGE_1_B, POINT_ROWS + 1, space=SYNC_SPACE)

    for name in INSTANCES:
        row = eval_yaml_app(
            tt,
            app_instance(tt_app, name),
            f"return box.space.{SYNC_SPACE}:get({POINT_ROWS + 1})",
        )
        assert row == [POINT_ROWS + 1, f"row-{POINT_ROWS + 1}"], (name, row)

    final = {name: instance_state(tt, tt_app, name) for name in INSTANCES}
    assert final[STORAGE_1_A]["vclock"] == final[STORAGE_1_B]["vclock"], final
    for name, state in final.items():
        # The vclock moved past the point together: the promote and the
        # acknowledged write are on every member. The async space keeps the
        # rows of the point, untouched by both.
        assert state["vclock"][point["replica_id"]] > point["lsn"], (name, state)
        assert state["rows"] == POINT_ROWS, (name, state)

    replica = app_instance(tt_app, STORAGE_1_B)
    proc = tt.run("instance", "stop", replica, "-y")
    assert proc.returncode == 0, proc.stdout

    deadline = time.monotonic() + 30
    while peer_status(tt, leader, "downstream") == "follow":
        assert time.monotonic() < deadline, "stopped replica is still following"
        time.sleep(0.1)

    rejected_id = AFTER_ROWS + 1
    failed_write = eval_yaml_app(
        tt,
        leader,
        f"""
        local ok, err = pcall(function()
            box.space.{SYNC_SPACE}:insert({{{rejected_id}, 'must-rollback'}})
        end)
        return {{
            ok = ok,
            code = not ok and err.code or 0,
            expected_code = box.error.SYNC_QUORUM_TIMEOUT,
            absent = box.space.{SYNC_SPACE}:get({rejected_id}) == nil,
            ro = box.info.ro,
        }}
        """,
    )
    assert failed_write["ok"] is False, failed_write
    assert failed_write["code"] == failed_write["expected_code"], failed_write
    assert failed_write["absent"] is True, failed_write
    assert failed_write["ro"] is False, failed_write

    proc = tt.run("instance", "start", replica)
    assert proc.returncode == 0, proc.stdout
    assert tt_helper.wait_box_status(30, tt_app, tt_app.instances, ["running"])
    wait_replicaset_connected(tt, tt_app)
    assert_sync_configuration()

    committed_id = rejected_id + 1
    assert commit_sync_row(tt, tt_app, committed_id) is True
    wait_rows(tt, tt_app, STORAGE_1_B, POINT_ROWS + 2, space=SYNC_SPACE)
    for name in INSTANCES:
        actual = eval_yaml_app(
            tt,
            app_instance(tt_app, name),
            f"""
            return {{
                rejected_absent = box.space.{SYNC_SPACE}:get({rejected_id}) == nil,
                committed = box.space.{SYNC_SPACE}:get({committed_id}),
            }}
            """,
        )
        assert actual == {
            "rejected_absent": True,
            "committed": [committed_id, f"row-{committed_id}"],
        }, (name, actual)
