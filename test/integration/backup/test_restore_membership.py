"""Restore across replicaset membership changes between backups.

Three scenarios the per-instance restore has to survive, each built on a
replicaset whose composition changed between two backups of one chain:

* a replica ADDED between the full backup and the increment -- the added
  member appears only in the WAL past the full's snapshot, and every member
  of the point's composition, it included, is handed out by the plan and
  restored in place;
* a member's UUID REPLACED under the same name between two increments -- the
  plan of a post-change point carries the new uuid, and a target time between
  the two points is refused: the composition the stitching boundary compares
  is the uuid set, so a 3.x-style re-registration under the same name is a
  new member beside a lost one and splits the chain -- the in-between target
  comes back with a topology boundary bracketed by the two points (a
  name leaving is asserted to refuse the same way in the removal scenario);
* a member REMOVED between two increments -- the post-removal plan does not
  list it, and the remaining set restores and serves.

Beside those, two probes of the membership a backup RECORDS:

* the membership a full backup's fragment records is the fold of the frozen
  snapshot with the journals the backup lists, clamped at the frozen vclock
  -- a registration that landed between the checkpoint and the freeze is
  inside the range and lands in the manifest with it, so a full backup's
  fragment no longer races a live read (an incremental one still falls back
  to the live _cluster, so tt as a whole can still drift from the freeze);
* the raced artifact -- a manifest claiming a member whose registration row
  the backup's data does not carry -- is refused at apply time by the
  patch-uuid check against the restored _cluster.
"""

import json
import shutil
import time
from datetime import datetime
from pathlib import Path

import pytest
import tt_helper
from backup_helpers import (
    app_instance,
    archive_path_from_output,
    backup_dir,
    backup_plan,
    backup_upload,
    eval_yaml_app,
    exec_split,
    finalize_backup,
    start_backup,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

STORAGE_A = "storage-001-a"
STORAGE_B = "storage-001-b"
STORAGE_C = "storage-001-c"
LEADER_AND_REPLICA = (STORAGE_A, STORAGE_B)
ALL_INSTANCES = (STORAGE_A, STORAGE_B, STORAGE_C)

# The identities pinned in membership_app/config.yaml. B_REPLACED is the uuid
# a re-registered storage-001-b takes over its name mid-test.
REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
UUID_A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
UUID_B = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaab"
UUID_C = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaac"
UUID_B_REPLACED = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaabb"

SYNC_SPACE = "restore_test_sync"
SPACES = ("restore_test", SYNC_SPACE)

SEEDED_ROWS = 10  # 1..10, written by the app's box.once at first boot.
FULL_ROWS = 30  # 11..30 land in the full backup.
POINT_ROWS = 40  # 31..40 precede the recovery point.
AFTER_ROWS = 50  # 41..50 are past it and must not come back.

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore apply requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_two_members(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


TT_MEMBERSHIP_TWO = dict(
    app_path="membership_app",
    app_name="app",
    instances=list(ALL_INSTANCES),
    running_targets=[f"app:{STORAGE_A}", f"app:{STORAGE_B}"],
    post_start=post_start_two_members,
)

TT_MEMBERSHIP_ALL = dict(
    app_path="membership_app",
    app_name="app",
    instances=list(ALL_INSTANCES),
    running_targets=["app"],
    post_start=post_start_two_members,
)


# -- Reading the live cluster ------------------------------------------------


def data_dir(tt_app, instance):
    """The instance's data directory: the app configures none of the three
    keys, so Tarantool and a restore agree on the default var/lib/<name>."""
    return Path(tt_app.path("var", "lib", instance))


# The app ships with all three members declared; a scenario keeps the
# configured set in step with the running one, because the manager-side
# tt backup plan connects to every instance the config names and refuses
# on one it cannot reach. Dropping the stanza before the scenario's first
# plan and adding it back before the member starts keeps both sides
# honest; the running instances are untouched by the edit either way.
MEMBER_C_STANZA = """          storage-001-c:
            database:
              instance_uuid: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaac'
"""
MEMBER_C_INSTANCE = "storage-001-c:\n"


def drop_member_c(tt_app):
    config = Path(tt_app.path("config.yaml"))
    text = config.read_text(encoding="utf-8")
    assert MEMBER_C_STANZA in text, "storage-001-c is not declared"
    config.write_text(text.replace(MEMBER_C_STANZA, ""), encoding="utf-8")

    instances = Path(tt_app.path("instances.yml"))
    text = instances.read_text(encoding="utf-8")
    instances.write_text(text.replace(MEMBER_C_INSTANCE, ""), encoding="utf-8")


def add_member_c(tt_app):
    config = Path(tt_app.path("config.yaml"))
    text = config.read_text(encoding="utf-8")
    assert MEMBER_C_STANZA not in text, "storage-001-c is already declared"
    config.write_text(text + MEMBER_C_STANZA, encoding="utf-8")

    instances = Path(tt_app.path("instances.yml"))
    text = instances.read_text(encoding="utf-8")
    instances.write_text(text + MEMBER_C_INSTANCE, encoding="utf-8")


def instance_state(tt, tt_app, instance, space="restore_test"):
    """Everything a restored instance is asserted by, in one round trip."""
    return eval_yaml_app(
        tt,
        app_instance(tt_app, instance),
        f"""
        local cluster = {{}}
        for _, row in box.space._cluster:pairs() do
            table.insert(cluster, {{row[1], row[2], row[3] or ''}})
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


def live_members(tt, tt_app):
    """The identities of the replicaset as the leader's _cluster holds them:
    {name: {id, uuid}}. Tarantool 3.x carries the name on every row."""
    rows = eval_yaml_app(
        tt,
        app_instance(tt_app, STORAGE_A),
        "return box.space._cluster:select()",
    )
    return {row[2]: {"id": row[0], "uuid": row[1]} for row in rows}


def upstream_status(tt, tt_app, follower, leader_id):
    """The follower's replication link to the leader, by replica id."""
    return eval_yaml_app(
        tt,
        app_instance(tt_app, follower),
        f"""
        local peer = box.info.replication[{leader_id}]
        if peer == nil then return 'no peer' end
        return (peer.upstream or {{}}).status or 'none'
        """,
    )


def wait_following(tt, tt_app, follower, leader_id, timeout=60):
    """Wait until the follower subscribes to the leader and follows it."""
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = upstream_status(tt, tt_app, follower, leader_id)
        if last == "follow":
            return
        time.sleep(0.5)
    raise AssertionError(f"{follower} did not follow replica {leader_id}; last: {last}")


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


def insert_rows(tt, target, begin, end, spaces=SPACES):
    """Replace rows begin..end in each of the spaces, in lockstep."""
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


def backup_plan_file(tt, target, storage_uri, config_path, tmp_path, name):
    """Run tt backup plan and keep its raw output as the file upload reads."""
    rc, out = backup_plan(tt, target, storage_uri, config=config_path, fmt="json")
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


def manifest_of(storage, backup_id):
    """The stored cluster manifest of a backup, as a document."""
    return json.loads(storage.read(f"manifests/{backup_id}.json"))


def manifest_members(manifest):
    """The membership a manifest recorded: {name: uuid} of its replicaset."""
    entries = manifest["topology"]["replicasets"][REPLICASET_UUID]
    return {entry["instance_name"]: entry["instance_uuid"] for entry in entries}


def unix_time(stamp):
    """A plan timestamp (RFC 3339) as the unix seconds a recovery point carries."""
    return int(datetime.fromisoformat(stamp.replace("Z", "+00:00")).timestamp())


def restore_plan_document(tt, storage_uri, target_time, dest, config):
    """Run tt restore plan and return (exit code, parsed document)."""
    rc, out, err = exec_split(
        tt,
        "restore",
        "plan",
        "--target-time",
        str(target_time),
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


def apply_for_instance(tt, tt_app, items, point, label, instance, uuid, work_dir=None):
    """tt restore apply for one member of the replicaset: the same chain, the
    same point and the same label for everyone, --patch-uuid the only thing
    that differs. Without a work_dir the directories come from the app's own
    config for that instance; a work_dir names a flat throwaway directory."""
    args = [
        "restore",
        "apply",
        "--archives",
        ",".join(item["artifact"] for item in items),
        "--checksums",
        ",".join(item["checksum_sha256"] for item in items),
        "--target-point",
        json.dumps(point),
        "--point-name",
        label,
        "--patch-uuid",
        uuid,
    ]
    if work_dir is None:
        args.extend(
            ["-c", tt_app.path("config.yaml"), "--instance", instance, "--work-dir", tt_app.path()],
        )
    else:
        args.extend(["--work-dir", str(work_dir)])
    return tt.exec(*args)


def stop_instances(tt, tt_app, names):
    for name in names:
        proc = tt.run("stop", app_instance(tt_app, name), "-y")
        assert proc.returncode == 0, f"tt stop {name} failed:\n{proc.stdout}"


def wipe_instances(tt_app, names):
    for name in names:
        shutil.rmtree(data_dir(tt_app, name), ignore_errors=True)


def start_instances(tt, tt_app, names):
    for name in names:
        proc = tt.run("start", app_instance(tt_app, name))
        assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(
        60,
        tt_app,
        [app_instance(tt_app, name) for name in names],
        ["running"],
    )


def synchro_state(tt, tt_app, names):
    """The synchronous queue and every named member's mode, for a failure
    message: what the queue is waiting on when a promote does not return."""
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
        for name in names
    }


def promote_owner(tt, tt_app):
    """box.ctl.promote() on the leader whose uuid owns the restored queue."""
    try:
        return eval_yaml_app(
            tt,
            app_instance(tt_app, STORAGE_A),
            "box.ctl.promote() return true",
        )
    except AssertionError as exc:
        raise AssertionError(
            f"box.ctl.promote() did not confirm; synchro state: "
            f"{synchro_state(tt, tt_app, ALL_INSTANCES)}",
        ) from exc


def commit_sync_row(tt, tt_app, row_id):
    """Replace a row in the synchronous space and return once it commits."""
    try:
        return eval_yaml_app(
            tt,
            app_instance(tt_app, STORAGE_A),
            f"box.space.{SYNC_SPACE}:replace({{{row_id}, 'row-{row_id}'}}) return true",
        )
    except AssertionError as exc:
        raise AssertionError(
            f"the synchronous insert of row {row_id} did not commit; "
            f"synchro state: {synchro_state(tt, tt_app, ALL_INSTANCES)}",
        ) from exc


# -- A1: a replica added between the full backup and the increment ----------


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MEMBERSHIP_TWO)
def test_membership_replica_added_between_full_and_increment(tt, tt_app, tmp_path):
    leader = app_instance(tt_app, STORAGE_A)
    config_path = tt_app.path("config.yaml")

    # -- Two members up, in step, under their pinned identities; the third
    # is out of the config until it joins between the two backups.
    drop_member_c(tt_app)
    wait_following(tt, tt_app, STORAGE_B, 1)
    members = live_members(tt, tt_app)
    assert members == {
        STORAGE_A: {"id": 1, "uuid": UUID_A},
        STORAGE_B: {"id": 2, "uuid": UUID_B},
    }, members

    # -- The full backup is taken on the two-member replicaset.
    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)

    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    assert plan_full["mode"] == "full"
    backup_and_upload(tt, leader, storage, "itest-madd-full", plan_full_path)

    # The full's manifest still records the two members it was taken on.
    assert manifest_members(manifest_of(storage, "itest-madd-full")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
    }

    # A cluster point is recorded to the whole second, while the storage's
    # coverage starts at the full manifest's creation time, fraction and all.
    time.sleep(1.1)

    # -- The third member joins the live replicaset after the full was taken.
    add_member_c(tt_app)
    proc = tt.run("start", app_instance(tt_app, STORAGE_C))
    assert proc.returncode == 0, f"tt start {STORAGE_C} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(
        60,
        tt_app,
        [app_instance(tt_app, STORAGE_C)],
        ["running"],
    )
    wait_following(tt, tt_app, STORAGE_C, 1)

    members = live_members(tt, tt_app)
    assert members == {
        STORAGE_A: {"id": 1, "uuid": UUID_A},
        STORAGE_B: {"id": 2, "uuid": UUID_B},
        STORAGE_C: {"id": 3, "uuid": UUID_C},
    }, members

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    created = create_recovery_point(tt, leader, "madd-1")
    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)

    assert created["replica_id"] == 1, created

    plan_inc, plan_inc_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc.json",
    )
    assert plan_inc["previous_backup_id"] == "itest-madd-full"
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-madd-inc",
        plan_inc_path,
        from_vclock=plan_inc["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # The increment's manifest carries the three members the replicaset had
    # when it was taken: the added one appears in the WAL past the full's
    # snapshot, so the fold at the point knows it even though no snapshot
    # of the chain ever held its row.
    assert manifest_members(manifest_of(storage, "itest-madd-inc")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
        STORAGE_C: UUID_C,
    }

    # -- The plan resolves the point and hands out all three members, the
    # added one included, each with the uuid its name owns in the backup.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        int(created["timestamp"]),
        dest,
        config_path,
    )

    assert rc == 0, doc
    assert doc["status"] == "ok", doc
    assert doc["warnings"] == [], doc
    assert doc["recovery_point"]["label"] == "madd-1"

    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]
    assert point == {"replica_id": created["replica_id"], "lsn": created["lsn"]}, doc

    items = doc["download_plan"][REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [
        "itest-madd-full",
        "itest-madd-inc",
    ], items
    assert items[-1]["trim_xlog"] is True, items

    target = doc["restore_targets"][REPLICASET_UUID]
    assert target["instances"] == [
        {"instance_name": STORAGE_A, "patch_uuid": UUID_A, "role": "master"},
        {"instance_name": STORAGE_B, "patch_uuid": UUID_B, "role": "replica"},
        {"instance_name": STORAGE_C, "patch_uuid": UUID_C, "role": "replica"},
    ], doc
    assert target.get("rejoin", []) == [], doc

    # -- Stop the replicaset, wipe the members, distribute the chain: the
    # third member's uuid is validated against the _cluster the WAL fold
    # ends with, not against any snapshot's.
    stop_instances(tt, tt_app, ALL_INSTANCES)
    wipe_instances(tt_app, ALL_INSTANCES)

    for name in ALL_INSTANCES:
        rc, out = apply_for_instance(
            tt,
            tt_app,
            items,
            point,
            "madd-1",
            name,
            members[name]["uuid"],
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

    # -- The replicaset comes back whole on the distributed chain.
    start_instances(tt, tt_app, ALL_INSTANCES)
    wait_following(tt, tt_app, STORAGE_B, 1)
    wait_following(tt, tt_app, STORAGE_C, 1)

    states = {name: instance_state(tt, tt_app, name) for name in ALL_INSTANCES}

    expected_cluster = [
        [members[name]["id"], members[name]["uuid"], name] for name in ALL_INSTANCES
    ]
    for name, state in states.items():
        assert state["status"] == "running", (name, state)
        assert state["uuid"] == members[name]["uuid"], (name, state)
        for space in SPACES:
            space_state = instance_state(tt, tt_app, name, space=space)
            assert space_state["rows"] == POINT_ROWS, (name, space, space_state)
        assert sorted(map(tuple, state["cluster"])) == sorted(
            map(tuple, expected_cluster),
        ), (name, state)

    # Vclocks equal across the replicaset on the replicated axes, with the
    # backed-up master's axis sitting exactly on the point: every member
    # replayed the same trimmed chain, and none of them arrived at the data
    # over replication from an empty boot. Axis 0 is left out: it counts the
    # local rows each instance writes for itself, which are not part of the
    # replicated stream (box.cc wal_stream_apply_mixed_dml_row maps a
    # replica_id-0 row to its transaction's owner, and each boot writes its
    # own).
    def replicated(state):
        return {axis: lsn for axis, lsn in state["vclock"].items() if axis != 0}

    assert replicated(states[STORAGE_A]) == replicated(states[STORAGE_B]), states
    assert replicated(states[STORAGE_A]) == replicated(states[STORAGE_C]), states
    for name, state in states.items():
        assert state["vclock"][point["replica_id"]] == point["lsn"], (name, state)

    # -- The queue unfreezes under the leader the backup was taken on, and a
    # synchronous write behind the quorum of 2 out of 3 commits and lands on
    # every member, the restored late-joiner included.
    assert promote_owner(tt, tt_app) is True
    assert commit_sync_row(tt, tt_app, POINT_ROWS + 1) is True

    for name in ALL_INSTANCES:
        wait_rows(tt, tt_app, name, POINT_ROWS + 1, space=SYNC_SPACE)


# -- A2: a member's uuid replaced under the same name between increments ----


def replace_member_uuid(tt, tt_app, name, old_uuid, new_uuid):
    """Stop the member, drop its _cluster registration on the leader, and
    start it back with a new uuid pinned under the same name.

    Tarantool 3.x identifies a member by name, but a re-registration needs
    the leader to let the old one go first: the leader's own applier to the
    stopped member counts as a live connection and keeps the registration
    refused ("UUID update when the old replica is still here", "Duplicate
    replica name"), so the peers list is cut back to the leader alone before
    the _cluster row is deleted. The restarted member then joins fresh and
    takes the freed registration under the new uuid: its name with a uuid
    it never held before, which is the membership change the chain records.
    """
    stop_instances(tt, tt_app, [name])
    wipe_instances(tt_app, [name])

    leader = app_instance(tt_app, STORAGE_A)

    # The stopped member out of the leader's peers: the leader's applier to
    # it holds the old registration alive.
    eval_yaml_app(
        tt,
        leader,
        """
        box.cfg({replication = {
            'unix/:./var/run/' .. box.info.name .. '/tarantool.sock'
        }})
        return #box.info.replication
        """,
    )

    row = eval_yaml_app(
        tt,
        leader,
        f"""
        local row
        for _, r in box.space._cluster:pairs() do
            if r[3] == '{name}' then row = r break end
        end
        assert(row ~= nil, 'no _cluster row for {name}')
        assert(row[2] == '{old_uuid}', 'unexpected uuid: ' .. tostring(row[2]))
        return box.space._cluster:delete(row[1])
        """,
    )
    assert row[1] == old_uuid, row

    config_path = Path(tt_app.path("config.yaml"))
    config = config_path.read_text(encoding="utf-8")
    assert config.count(old_uuid) == 1, "the old uuid must name exactly one site"
    config_path.write_text(config.replace(old_uuid, new_uuid), encoding="utf-8")

    proc = tt.run("start", app_instance(tt_app, name))
    assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(
        60,
        tt_app,
        [app_instance(tt_app, name)],
        ["running"],
    )


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MEMBERSHIP_TWO)
def test_membership_uuid_replaced_between_backups(tt, tt_app, tmp_path):
    leader = app_instance(tt_app, STORAGE_A)
    config_path = tt_app.path("config.yaml")

    # -- {A, B} in step; the full and the first increment are taken on it.
    # storage-001-c never joins this scenario, so it stays out of the config.
    drop_member_c(tt_app)
    wait_following(tt, tt_app, STORAGE_B, 1)
    assert live_members(tt, tt_app) == {
        STORAGE_A: {"id": 1, "uuid": UUID_A},
        STORAGE_B: {"id": 2, "uuid": UUID_B},
    }

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)

    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    backup_and_upload(tt, leader, storage, "itest-mrep-full", plan_full_path)

    time.sleep(1.1)

    # A point before the change, listed by the pre-change increment.
    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    before = create_recovery_point(tt, leader, "mrep-before")

    plan_inc1, plan_inc1_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc1.json",
    )
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-mrep-inc1",
        plan_inc1_path,
        from_vclock=plan_inc1["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # -- B's name is re-registered under a fresh uuid between the increments.
    replace_member_uuid(tt, tt_app, STORAGE_B, UUID_B, UUID_B_REPLACED)
    wait_following(tt, tt_app, STORAGE_B, 1)

    members = live_members(tt, tt_app)
    assert members == {
        STORAGE_A: {"id": 1, "uuid": UUID_A},
        STORAGE_B: {"id": 2, "uuid": UUID_B_REPLACED},
    }, members

    # The post-change point needs a second of its own past the pre-change
    # one: a target time the refusal is demonstrated on has to fall between
    # the two, and a replacement this fast leaves nothing in between.
    time.sleep(2.5)

    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)
    after = create_recovery_point(tt, leader, "mrep-after")
    insert_rows(tt, leader, AFTER_ROWS + 1, AFTER_ROWS + 10)

    plan_inc2, plan_inc2_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc2.json",
    )
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-mrep-inc2",
        plan_inc2_path,
        from_vclock=plan_inc2["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # Each manifest records the composition its backup was taken on: the
    # first increment the old uuid under the name, the second the new one.
    assert manifest_members(manifest_of(storage, "itest-mrep-inc1")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
    }
    assert manifest_members(manifest_of(storage, "itest-mrep-inc2")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B_REPLACED,
    }

    # -- The plan of the post-change point carries the new uuid.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        int(after["timestamp"]),
        dest,
        config_path,
    )

    assert rc == 0, doc
    assert doc["status"] == "ok", doc
    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]

    items = doc["download_plan"][REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [
        "itest-mrep-full",
        "itest-mrep-inc1",
        "itest-mrep-inc2",
    ], items

    target = doc["restore_targets"][REPLICASET_UUID]
    assert target["instances"] == [
        {"instance_name": STORAGE_A, "patch_uuid": UUID_A, "role": "master"},
        {
            "instance_name": STORAGE_B,
            "patch_uuid": UUID_B_REPLACED,
            "role": "replica",
        },
    ], doc
    assert target.get("rejoin", []) == [], doc

    # -- A target time between the two points is refused: the composition
    # the stitching boundary compares is the uuid set
    # (cli/backup/chain/chain.go memberIdentities), so a uuid replacement
    # under the same name is a new member beside a lost one -- the chain
    # splits at it, and an in-between moment has no single membership to
    # restore to. (A name leaving the replicaset splits it the same way;
    # that refusal is asserted in
    # test_membership_member_removed_between_backups.)
    gap_time = int(before["timestamp"]) + max(
        1,
        (int(after["timestamp"]) - int(before["timestamp"])) // 2,
    )
    assert gap_time < int(after["timestamp"]), (
        before,
        after,
        "the two points must leave a second between them",
    )
    rc, gap = restore_plan_document(tt, storage.uri, gap_time, dest, config_path)

    assert rc == 2, (rc, gap)
    assert gap["status"] == "topology_boundary", gap
    assert "download_plan" not in gap, gap
    assert "restore_targets" not in gap, gap

    # The bracket the refusal offers is exactly the two points the change
    # sits between: the pre-change one before it, the post-change one after.
    nearest = gap["nearest_safe"]
    assert nearest is not None, gap
    assert unix_time(nearest["before"]) == int(before["timestamp"]), gap
    assert unix_time(nearest["after"]) == int(after["timestamp"]), gap

    # The last pre-change point itself still plans the pre-change way:
    # against the composition its own backup recorded, the old uuid under
    # the name -- the boundary sits after it, not on it.
    rc, old = restore_plan_document(
        tt,
        storage.uri,
        int(before["timestamp"]),
        dest,
        config_path,
    )
    assert rc == 0, old
    assert old["status"] == "ok", old
    expected_old = [
        {"instance_name": STORAGE_A, "patch_uuid": UUID_A, "role": "master"},
        {"instance_name": STORAGE_B, "patch_uuid": UUID_B, "role": "replica"},
    ]
    assert old["restore_targets"][REPLICASET_UUID]["instances"] == expected_old, old

    # -- A chain with the middle increment left out is refused. The
    # archive-level checks cannot see the hole: a real tt backup archive
    # carries no instance_backup.json -- the fragment is written beside the
    # archive (cli/backup/start.go packArchive), not packed into it, and the
    # plan downloads archives alone, so checkFragments never runs -- and
    # journal names stay monotone across the hole (full ends at journal 55,
    # the second increment starts at 149, and the missing increment is the
    # one that covered 55..149). What can tell is the content: the
    # continuity check of the unpacked journals (checkJournalContinuity in
    # cli/restore/apply.go) refuses a journal that does not continue the
    # chain's end. The check runs after the unpack, so the refusal is a
    # plain failure and the rebuilt directories still show the hole --
    # without a marker claiming the restore was finished.
    gap_dir = tmp_path / "gapped"
    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(item["artifact"] for item in [items[0], items[2]]),
        "--checksums",
        ",".join(item["checksum_sha256"] for item in [items[0], items[2]]),
        "--work-dir",
        str(gap_dir),
        "--target-point",
        json.dumps(point),
        "--patch-uuid",
        UUID_A,
    )
    assert rc != 0, f"a chain with a missing increment is applied:\n{out}"
    assert rc == 1, f"unexpected exit {rc}:\n{out}"
    assert "does not continue" in out, out

    # The hole the refusal names is real: the unpacked journals still show it.
    journals = sorted(path.name for path in gap_dir.iterdir() if path.name.endswith(".xlog"))
    signatures = [int(name.split(".")[0]) for name in journals]
    assert signatures[1] - signatures[0] > 1, (
        f"the refused chain has a hole between its journals: {journals}"
    )
    assert not Path(f"{gap_dir}.restore_state.json").exists(), (
        "a refused restore must not leave its marker"
    )

    # -- Stop, wipe and restore the post-change composition: the fold at the
    # point applies the name's re-registration row, so the replacement uuid
    # is the one the restored _cluster holds for the name.
    stop_instances(tt, tt_app, (STORAGE_A, STORAGE_B))
    wipe_instances(tt_app, (STORAGE_A, STORAGE_B))

    for name, uuid in ((STORAGE_A, UUID_A), (STORAGE_B, UUID_B_REPLACED)):
        rc, out = apply_for_instance(
            tt,
            tt_app,
            items,
            point,
            "mrep-after",
            name,
            uuid,
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

    start_instances(tt, tt_app, (STORAGE_A, STORAGE_B))
    wait_following(tt, tt_app, STORAGE_B, 1)

    states = {name: instance_state(tt, tt_app, name) for name in LEADER_AND_REPLICA}
    # The ids the live replicaset carried: which follower joined first at
    # fixture start is a race, so the ids are read, not assumed.
    restored_cluster = sorted(
        map(
            tuple,
            [
                [members[STORAGE_A]["id"], UUID_A, STORAGE_A],
                [members[STORAGE_B]["id"], UUID_B_REPLACED, STORAGE_B],
            ],
        ),
    )
    for name, state in states.items():
        assert state["uuid"] == members[name]["uuid"], (name, state)
        for space in SPACES:
            space_state = instance_state(tt, tt_app, name, space=space)
            assert space_state["rows"] == AFTER_ROWS, (name, space, space_state)
        assert sorted(map(tuple, state["cluster"])) == restored_cluster, (
            name,
            state,
        )

    # -- The replicaset writes: the promote takes a new term with both
    # members acknowledging, and a synchronous row commits and lands.
    assert promote_owner(tt, tt_app) is True
    assert commit_sync_row(tt, tt_app, AFTER_ROWS + 1) is True
    wait_rows(tt, tt_app, STORAGE_B, AFTER_ROWS + 1, space=SYNC_SPACE)


# -- A3: a member removed between increments ---------------------------------


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MEMBERSHIP_ALL)
def test_membership_member_removed_between_backups(tt, tt_app, tmp_path):
    leader = app_instance(tt_app, STORAGE_A)
    config_path = tt_app.path("config.yaml")

    # -- All three members up and following the leader.
    for name in (STORAGE_B, STORAGE_C):
        wait_following(tt, tt_app, name, 1)
    members = live_members(tt, tt_app)
    assert set(members) == set(ALL_INSTANCES), members
    removed_id = members[STORAGE_C]["id"]

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    # -- The full and a first increment on the three-member replicaset.
    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)

    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    backup_and_upload(tt, leader, storage, "itest-mrem-full", plan_full_path)

    time.sleep(1.1)

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    before = create_recovery_point(tt, leader, "mrem-before")

    plan_inc1, plan_inc1_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc1.json",
    )
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-mrem-inc1",
        plan_inc1_path,
        from_vclock=plan_inc1["replicasets"][REPLICASET_UUID]["from_vclock"],
    )
    assert manifest_members(manifest_of(storage, "itest-mrem-inc1")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
        STORAGE_C: UUID_C,
    }

    # -- The third member is stopped and unregistered between the increments:
    # out of the config as well, the way a removal leaves the cluster.
    stop_instances(tt, tt_app, [STORAGE_C])
    drop_member_c(tt_app)
    row = eval_yaml_app(
        tt,
        leader,
        f"return box.space._cluster:delete({removed_id})",
    )
    assert row[2] == STORAGE_C, row

    # The post-removal point needs a second of its own past the pre-removal
    # one, so that a target time can fall strictly between the two (and the
    # post-removal plan resolves to the post-removal point, not to the last
    # one of the pre-removal second).
    time.sleep(2.5)

    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)
    after = create_recovery_point(tt, leader, "mrem-after")
    insert_rows(tt, leader, AFTER_ROWS + 1, AFTER_ROWS + 10)

    plan_inc2, plan_inc2_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc2.json",
    )
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-mrem-inc2",
        plan_inc2_path,
        from_vclock=plan_inc2["replicasets"][REPLICASET_UUID]["from_vclock"],
    )
    assert manifest_members(manifest_of(storage, "itest-mrem-inc2")) == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
    }

    # -- The post-removal plan does not list the removed member, and hands
    # the remaining two their original uuids.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        int(after["timestamp"]),
        dest,
        config_path,
    )

    assert rc == 0, doc
    assert doc["status"] == "ok", doc
    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]

    items = doc["download_plan"][REPLICASET_UUID]
    assert [item["backup_id"] for item in items] == [
        "itest-mrem-full",
        "itest-mrem-inc1",
        "itest-mrem-inc2",
    ], items

    target = doc["restore_targets"][REPLICASET_UUID]
    assert target["instances"] == [
        {"instance_name": STORAGE_A, "patch_uuid": UUID_A, "role": "master"},
        {"instance_name": STORAGE_B, "patch_uuid": UUID_B, "role": "replica"},
    ], doc
    # The removed member left the configuration together with the
    # replicaset, so nothing is left to wipe and bootstrap fresh.
    assert target.get("rejoin", []) == [], doc

    # A target time between the last three-member point and the first
    # two-member one is refused, exactly as a replacement is.
    gap_time = int(before["timestamp"]) + max(
        1,
        (int(after["timestamp"]) - int(before["timestamp"])) // 2,
    )
    rc, gap = restore_plan_document(tt, storage.uri, gap_time, dest, config_path)
    assert rc == 2, (rc, gap)
    assert gap["status"] == "topology_boundary", gap

    # -- The remaining set restores and serves.
    stop_instances(tt, tt_app, (STORAGE_A, STORAGE_B))
    wipe_instances(tt_app, (STORAGE_A, STORAGE_B))

    for name, uuid in ((STORAGE_A, UUID_A), (STORAGE_B, UUID_B)):
        rc, out = apply_for_instance(
            tt,
            tt_app,
            items,
            point,
            "mrem-after",
            name,
            uuid,
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

    start_instances(tt, tt_app, (STORAGE_A, STORAGE_B))
    wait_following(tt, tt_app, STORAGE_B, 1)

    for name in LEADER_AND_REPLICA:
        state = instance_state(tt, tt_app, name)
        assert (
            state["uuid"]
            == {
                STORAGE_A: UUID_A,
                STORAGE_B: UUID_B,
            }[name]
        ), (name, state)
        assert sorted(map(tuple, state["cluster"])) == sorted(
            map(
                tuple,
                [
                    [members[STORAGE_A]["id"], UUID_A, STORAGE_A],
                    [members[STORAGE_B]["id"], UUID_B, STORAGE_B],
                ],
            ),
        ), (name, state)
        for space in SPACES:
            space_state = instance_state(tt, tt_app, name, space=space)
            assert space_state["rows"] == AFTER_ROWS, (name, space, space_state)

    assert promote_owner(tt, tt_app) is True
    assert commit_sync_row(tt, tt_app, AFTER_ROWS + 1) is True
    wait_rows(tt, tt_app, STORAGE_B, AFTER_ROWS + 1, space=SYNC_SPACE)


# -- The membership a backup records: the frozen range, snapshot and WAL -----


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MEMBERSHIP_TWO)
def test_backup_membership_covers_the_frozen_range(tt, tt_app, tmp_path):
    """The fragment's membership covers the frozen range, snapshot and WAL.

    The checkpoint is taken before a third member joins and the backup
    starts after it has: the registration row sits inside the frozen WAL
    range, beside the snapshot the artifact carries. A fragment reading the
    snapshot alone would miss a member its own data restores; the one tt
    records folds the backed-up journals into the snapshot and clamps the
    fold at the frozen vclock, so the member is in the manifest. The master
    entry carries its cluster id, the way every member always has.
    """
    leader = app_instance(tt_app, STORAGE_A)
    config_path = tt_app.path("config.yaml")
    wait_following(tt, tt_app, STORAGE_B, 1)
    drop_member_c(tt_app)

    eval_yaml_app(tt, leader, "return box.snapshot()")

    add_member_c(tt_app)
    proc = tt.run("start", app_instance(tt_app, STORAGE_C))
    assert proc.returncode == 0, f"tt start {STORAGE_C} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(
        60,
        tt_app,
        [app_instance(tt_app, STORAGE_C)],
        ["running"],
    )
    wait_following(tt, tt_app, STORAGE_C, 1)

    live_ids = {
        row[1]: row[0] for row in eval_yaml_app(tt, leader, "return box.space._cluster:select()")
    }

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))
    _, plan_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-frozen.json",
    )
    backup_and_upload(tt, leader, storage, "itest-frozen-full", plan_path)

    entries = manifest_of(storage, "itest-frozen-full")["topology"]["replicasets"][REPLICASET_UUID]
    recorded = {entry["instance_name"]: entry.get("instance_id") for entry in entries}
    assert set(recorded) == {STORAGE_A, STORAGE_B, STORAGE_C}, (
        recorded,
        "the registration is inside the frozen range, so the fold the "
        "fragment records the membership from carries the third member",
    )
    assert recorded[STORAGE_A] == 1, recorded
    assert recorded[STORAGE_B] == live_ids[UUID_B], recorded
    assert recorded[STORAGE_C] == live_ids[UUID_C], recorded


# -- The raced artifact: a member the data behind the manifest never saw -----


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_MEMBERSHIP_TWO)
def test_raced_membership_is_refused_at_apply(tt, tt_app, tmp_path):
    """What the freeze race produces, played forward through the restore.

    The manifest below is edited to carry the third member -- what a
    foreign or hand-made manifest would look like. A full backup's fragment
    can no longer produce the shape: its membership folds the frozen
    snapshot with the journals and clamps at the freeze. An incremental one
    still can -- an increment lists no snapshot of its own and reads the
    live _cluster -- so tt as a whole can still record a member the backup's
    data does not carry, through an increment. The plan trusts the manifest
    and lists the ghost member as a restore target with a uuid of its own;
    the apply for it is then refused by the patch-uuid check against the
    _cluster the restored data actually folds into.
    """
    leader = app_instance(tt_app, STORAGE_A)
    config_path = tt_app.path("config.yaml")

    # The chain is taken on the two-member replicaset: storage-001-c stays
    # out of the config until it joins after the chain was taken.
    drop_member_c(tt_app)
    wait_following(tt, tt_app, STORAGE_B, 1)

    storage_root = tmp_path / "backups"
    storage_root.mkdir()
    storage = FileStorage(str(storage_root))

    eval_yaml_app(tt, leader, "return box.snapshot()")
    insert_rows(tt, leader, SEEDED_ROWS + 1, FULL_ROWS)
    plan_full, plan_full_path = backup_plan_file(
        tt,
        "full",
        storage.uri,
        config_path,
        tmp_path,
        "plan-full.json",
    )
    backup_and_upload(tt, leader, storage, "itest-race-full", plan_full_path)

    time.sleep(1.1)

    insert_rows(tt, leader, FULL_ROWS + 1, POINT_ROWS)
    created = create_recovery_point(tt, leader, "race-1")
    insert_rows(tt, leader, POINT_ROWS + 1, AFTER_ROWS)
    plan_inc, plan_inc_path = backup_plan_file(
        tt,
        "incremental",
        storage.uri,
        config_path,
        tmp_path,
        "plan-inc.json",
    )
    backup_and_upload(
        tt,
        leader,
        storage,
        "itest-race-inc",
        plan_inc_path,
        from_vclock=plan_inc["replicasets"][REPLICASET_UUID]["from_vclock"],
    )

    # The third member joins the live replicaset after the chain was taken.
    add_member_c(tt_app)
    proc = tt.run("start", app_instance(tt_app, STORAGE_C))
    assert proc.returncode == 0, f"tt start {STORAGE_C} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(
        60,
        tt_app,
        [app_instance(tt_app, STORAGE_C)],
        ["running"],
    )
    wait_following(tt, tt_app, STORAGE_C, 1)
    assert live_members(tt, tt_app)[STORAGE_C]["uuid"] == UUID_C

    # The raced increment manifest: the membership recorded beside a range
    # that was frozen before the registration.
    manifest = manifest_of(storage, "itest-race-inc")
    entries = manifest["topology"]["replicasets"][REPLICASET_UUID]
    template = next(entry for entry in entries if entry["instance_name"] == STORAGE_B)
    ghost = dict(template)
    ghost.update(
        instance_id=3,
        instance_uuid=UUID_C,
        instance_name=STORAGE_C,
    )
    entries.append(ghost)
    storage.put(
        "manifests/itest-race-inc.json",
        json.dumps(manifest).encode(),
    )

    # -- The plan trusts the manifest: three targets, the ghost included.
    dest = tmp_path / "restore"
    rc, doc = restore_plan_document(
        tt,
        storage.uri,
        int(created["timestamp"]),
        dest,
        config_path,
    )
    assert rc == 0, doc
    assert doc["status"] == "ok", doc

    target = doc["restore_targets"][REPLICASET_UUID]
    assert {entry["instance_name"]: entry["patch_uuid"] for entry in target["instances"]} == {
        STORAGE_A: UUID_A,
        STORAGE_B: UUID_B,
        STORAGE_C: UUID_C,
    }, doc

    items = doc["download_plan"][REPLICASET_UUID]
    point = doc["recovery_point"]["trim_to_by_replicaset"][REPLICASET_UUID]

    # -- The apply for the ghost member is refused: the fold of the restored
    # snapshot and journals holds no row for it, and the refusal dumps the
    # _cluster it does hold. A plain failure (exit 1), not a rejected input:
    # the directories of a run that got this far are already rebuilt.
    ghost_dir = tmp_path / "ghost"
    rc, out = apply_for_instance(
        tt,
        tt_app,
        items,
        point,
        "race-1",
        STORAGE_C,
        UUID_C,
        work_dir=ghost_dir,
    )
    assert rc != 0, out
    assert rc == 1, f"unexpected exit {rc}:\n{out}"
    assert "is not a member of the restored replicaset" in out, out
    assert UUID_A in out and UUID_B in out, out
    assert f'holds [1 {UUID_A} "{STORAGE_A}", 2 {UUID_B} "{STORAGE_B}"]' in out, out

    # The real members restore fine beside the ghost target: the refusal is
    # the ghost's alone.
    leader_dir = tmp_path / "leader"
    rc, out = apply_for_instance(
        tt,
        tt_app,
        items,
        point,
        "race-1",
        STORAGE_A,
        UUID_A,
        work_dir=leader_dir,
    )
    assert rc == 0, f"tt restore apply on {STORAGE_A} failed:\n{out}"
