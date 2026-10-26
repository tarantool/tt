"""A restore onto a journal cut inside an unfinished synchronous write.

A synchronous write reaches the journal as its DML row first and the CONFIRM
that resolves it only after the quorum acknowledged it -- or the ROLLBACK
that gives up on it after replication_synchro_timeout. The unit twin of this
file's second half pins the trim's shape: a cut between the two keeps the
REQUEST and drops the resolution, because they are separate transactions in
the row stream and the whole-transaction rule cannot span them.

Producing that cut through the supported flow turns out to be impossible,
and the first test below pins why, with the measurement: while the leader's
synchronous queue is stuck (both followers of a quorum-2 replicaset stopped,
one synchronous write accepted and unacknowledged), the recovery point
creation itself blocks behind the queue for the whole synchro timeout and
then fails with ER_SYNC_ROLLBACK -- the point manager writes through the
same leader the stuck queue holds, so no point can land between the REQUEST
and its resolution.

The second test then builds the journal anyway -- the real archives of the
real stall, cut by a hand-set --target-point exactly at the REQUEST row,
which is the position a point created in that gap would have named -- and
boots the whole replicaset on it. The assertions are the safety properties:
nobody sticks in startup, the queue resolves without an operator, all three
members agree on the hung row, and the replicaset promotes and commits a
fresh synchronous write afterwards.
"""

import hashlib
import json
import shutil
import time
from pathlib import Path

import pytest
import tt_helper
from backup_helpers import (
    app_instance,
    app_name,
    archive_path_from_output,
    backup_dir,
    backup_plan,
    backup_upload,
    eval_yaml_app,
    finalize_backup,
    instance_data_dir,
    start_backup,
)
from storage_helpers import FileStorage

from utils import get_tarantool_version, is_tarantool_less

STORAGE_A = "storage-001-a"
STORAGE_B = "storage-001-b"
STORAGE_C = "storage-001-c"
ALL_INSTANCES = (STORAGE_A, STORAGE_B, STORAGE_C)

REPLICASET_UUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
UUID_A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
UUID_B = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaab"
UUID_C = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaac"
UUIDS = {STORAGE_A: UUID_A, STORAGE_B: UUID_B, STORAGE_C: UUID_C}

SYNC_SPACE = "restore_test_sync"

SEEDED_ROWS = 10  # 1..10, written by the app's box.once at first boot.
FULL_ROWS = 30  # 11..30 land in the full backup.
POINT_ROWS = 40  # 31..40 precede the hung write.
HUNG_ROW = 41  # the synchronous row that cannot gather its quorum
AFTER_ROWS = 50  # 42..50 land past the hung write

# membership_app pins synchro_timeout = 10: a hung write is rolled back ten
# seconds after it was written.
SYNCHRO_TIMEOUT = 10

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore apply requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


def post_start_all(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


TT_SYNCHRO_BOUNDARY_APP = dict(
    app_path="membership_app",
    app_name="app",
    instances=list(ALL_INSTANCES),
    running_targets=["app:*"],
    post_start=post_start_all,
)


def data_dir(tt_app, instance):
    return Path(instance_data_dir(tt_app, instance))


def eval_on(tt, tt_app, instance, lua):
    return eval_yaml_app(tt, app_instance(tt_app, instance), lua)


def wait_upstream_following(tt, tt_app, follower, leader_id, timeout=60):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = eval_on(
            tt,
            tt_app,
            follower,
            f"""
            local peer = box.info.replication[{leader_id}]
            if peer == nil then return 'no peer' end
            return (peer.upstream or {{}}).status or 'none'
            """,
        )
        if last == "follow":
            return
        time.sleep(0.5)
    raise AssertionError(f"{follower} did not follow replica {leader_id}; last: {last}")


def queue_len(tt, tt_app):
    """The number of entries waiting in the leader's synchronous queue.

    box.info.synchro.queue.busy is the limbo latch, not pending-ness; len is
    the one that counts the unconfirmed requests.
    """
    return eval_on(tt, tt_app, STORAGE_A, "return box.info.synchro.queue.len")


def sync_has_row(tt, tt_app, instance, row_id):
    return eval_on(tt, tt_app, instance, f"return box.space.{SYNC_SPACE}:get({row_id}) ~= nil")


def fire_hung_sync_write(tt, tt_app):
    """A synchronous write with no quorum behind it: the row is accepted
    into the WAL (the REQUEST) and the call blocks on the limbo inside a
    fiber of its own. Returns once the row is visible, and leaves the queue
    holding one unconfirmed entry."""
    eval_on(
        tt,
        tt_app,
        STORAGE_A,
        f"""
        local fiber = require('fiber')
        fiber.create(function()
            pcall(function()
                box.space.{SYNC_SPACE}:replace(
                    {{{HUNG_ROW}, 'row-{HUNG_ROW}'}})
            end)
        end)
        return true
        """,
    )

    deadline = time.monotonic() + 10
    while not sync_has_row(tt, tt_app, STORAGE_A, HUNG_ROW):
        assert time.monotonic() < deadline, "the hung sync row never became visible"
        time.sleep(0.1)
    assert queue_len(tt, tt_app) >= 1, "the hung write must be pending in the queue"


def stop_followers(tt, tt_app):
    for name in (STORAGE_B, STORAGE_C):
        proc = tt.run("instance", "stop", app_instance(tt_app, name), "-y")
        assert proc.returncode == 0, f"tt stop {name} failed:\n{proc.stdout}"


def wait_queue_empty(tt, tt_app, timeout):
    deadline = time.monotonic() + timeout
    while queue_len(tt, tt_app) > 0:
        assert time.monotonic() < deadline, "the queue never resolved after the timeout"
        time.sleep(0.5)


def build_full_backup(tt, tt_app, tmp_path):
    """A full backup of the healthy replicaset plus the increment's plan
    (taken while every member is still reachable -- tt backup plan connects
    to the whole configured cluster -- whose from_vclock stays valid once
    the followers have left)."""
    leader = app_instance(tt_app, STORAGE_A)
    storage = FileStorage(str(tmp_path / "backups"))
    (tmp_path / "backups").mkdir()

    eval_on(tt, tt_app, STORAGE_A, "return box.snapshot()")
    eval_on(
        tt,
        tt_app,
        STORAGE_A,
        f"for i = {SEEDED_ROWS + 1}, {FULL_ROWS} do "
        f"box.space.restore_test:replace({{i, 'row-' .. i}}) end return true",
    )

    rc, out = backup_plan(tt, "full", storage.uri, app=app_name(tt_app), fmt="json")
    assert rc == 0, out
    plan_full = tmp_path / "plan-full.json"
    plan_full.write_text(out.strip(), encoding="utf-8")

    rc, out = start_backup(tt, leader, "itest-sbnd-full")
    assert rc == 0, out
    archive = archive_path_from_output(out)
    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=archive.removesuffix(".tar.zst") + ".json",
        plan=str(plan_full),
        backup_id="itest-sbnd-full",
    )
    assert rc == 0, out
    rc, out = finalize_backup(tt, leader, "itest-sbnd-full")
    assert rc == 0, out
    assert not Path(backup_dir("itest-sbnd-full")).exists()

    rc, out = backup_plan(
        tt,
        "incremental",
        storage.uri,
        app=app_name(tt_app),
        fmt="json",
    )
    assert rc == 0, out
    plan_inc = tmp_path / "plan-inc.json"
    plan_inc.write_text(out.strip(), encoding="utf-8")
    from_vclock = json.loads(out.strip())["replicasets"][REPLICASET_UUID]["from_vclock"]

    return storage, leader, from_vclock, str(plan_inc)


def build_increment_backup(tt, storage, leader, from_vclock, plan_path):
    rc, out = start_backup(tt, leader, "itest-sbnd-inc", from_vclock=from_vclock)
    assert rc == 0, out
    archive = archive_path_from_output(out)
    rc, out = backup_upload(
        tt,
        storage.uri,
        archives=archive,
        fragments=archive.removesuffix(".tar.zst") + ".json",
        plan=plan_path,
        backup_id="itest-sbnd-inc",
    )
    assert rc == 0, out
    rc, out = finalize_backup(tt, leader, "itest-sbnd-inc")
    assert rc == 0, out
    return archive


def archive_checksum(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_SYNCHRO_BOUNDARY_APP)
def test_no_point_can_be_created_inside_a_hung_sync_write(tt, tt_app, tmp_path):
    """The supported flow cannot cut a chain between a synchronous REQUEST
    and its resolution: the point creation writes through the leader the
    stuck queue holds.

    With both followers of the quorum-2 replicaset stopped and one
    synchronous write accepted but unacknowledged, the recovery point
    creation blocks for the whole replication_synchro_timeout and then
    fails with ER_SYNC_ROLLBACK -- the rollback of the hung write fails the
    point's own wait, because the point's transaction queues behind the
    limbo. No point lands between the REQUEST and the ROLLBACK, so the
    trim-with-an-unconfirmed-REQUEST shape cannot be produced this way.
    """
    for name in (STORAGE_B, STORAGE_C):
        wait_upstream_following(tt, tt_app, name, 1)

    build_full_backup(tt, tt_app, tmp_path)

    stop_followers(tt, tt_app)

    eval_on(
        tt,
        tt_app,
        STORAGE_A,
        f"for i = {FULL_ROWS + 1}, {POINT_ROWS} do "
        f"box.space.restore_test:replace({{i, 'row-' .. i}}) end return true",
    )

    fire_hung_sync_write(tt, tt_app)

    # The point creation does not refuse -- it disappears into the same wait
    # the hung write is stuck in, and comes back with the rollback's error
    # a full synchro_timeout later.
    started = time.monotonic()
    created = eval_on(
        tt,
        tt_app,
        STORAGE_A,
        "return box.backup.recovery_point.create({label = 'sbnd-inside'})",
    )
    took = time.monotonic() - started

    assert "error" in created, created
    assert "A rollback for a synchronous transaction is received" in str(created), created
    assert took >= SYNCHRO_TIMEOUT * 0.9, (
        f"the point creation was held for {took:.1f}s, less than the "
        f"{SYNCHRO_TIMEOUT}s synchro timeout -- it did not queue behind the limbo"
    )

    # The hung write was rolled back: the leader ends up without the row and
    # with an empty queue, and a point created after that lands past the
    # ROLLBACK -- the gap has closed for good.
    wait_queue_empty(tt, tt_app, SYNCHRO_TIMEOUT + 15)
    assert sync_has_row(tt, tt_app, STORAGE_A, HUNG_ROW) is False

    after = eval_on(
        tt,
        tt_app,
        STORAGE_A,
        "return box.backup.recovery_point.create({label = 'sbnd-after'})",
    )
    assert "error" not in after, after
    assert after["lsn"] >= 1, after


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_SYNCHRO_BOUNDARY_APP)
def test_apply_boots_a_replicaset_on_an_unconfirmed_request(tt, tt_app, tmp_path):
    """The journal a point inside the gap would have produced, for real.

    The chain is the real one -- a full backup of the healthy replicaset and
    an increment that carries the hung REQUEST, its ROLLBACK and the rows
    past them -- and the cut is a hand-set --target-point exactly at the
    REQUEST row: the position a point created in the gap would have named.
    Every member is restored onto the same trimmed journal, so every member
    replays the same unconfirmed REQUEST with no resolution record behind
    it.
    """
    for name in (STORAGE_B, STORAGE_C):
        wait_upstream_following(tt, tt_app, name, 1)

    storage, leader, from_vclock, plan_inc = build_full_backup(tt, tt_app, tmp_path)

    time.sleep(1.1)  # the same-second trap: storage coverage is whole-second

    stop_followers(tt, tt_app)

    eval_on(
        tt,
        tt_app,
        STORAGE_A,
        f"for i = {FULL_ROWS + 1}, {POINT_ROWS} do "
        f"box.space.restore_test:replace({{i, 'row-' .. i}}) end return true",
    )
    lsn_before = eval_on(tt, tt_app, STORAGE_A, "return box.info.lsn")

    fire_hung_sync_write(tt, tt_app)

    # The REQUEST's own lsn: the hung row is the only write that happened.
    request_lsn = eval_on(tt, tt_app, STORAGE_A, "return box.info.lsn")
    assert request_lsn == lsn_before + 1, (lsn_before, request_lsn)

    # Let the timeout roll the hung write back, then write past it all and
    # take the increment: REQUEST, point-side gap, ROLLBACK, rows, one
    # archive covering it all.
    wait_queue_empty(tt, tt_app, SYNCHRO_TIMEOUT + 15)
    assert sync_has_row(tt, tt_app, STORAGE_A, HUNG_ROW) is False

    eval_on(
        tt,
        tt_app,
        STORAGE_A,
        f"for i = {POINT_ROWS + 1}, {AFTER_ROWS} do "
        f"box.space.restore_test:replace({{i, 'row-' .. i}}) end return true",
    )

    build_increment_backup(tt, storage, leader, from_vclock, plan_inc)
    # Finalize swept the local copies; the storage holds both archives under
    # the names upload gave them.
    archives = [
        str(Path(storage.root) / "data" / f"{name}-{REPLICASET_UUID}.tar.zst")
        for name in ("itest-sbnd-full", "itest-sbnd-inc")
    ]
    for path in archives:
        assert Path(path).is_file(), path
    checksums = [archive_checksum(path) for path in archives]

    # -- Everyone is stopped, wiped and restored onto the trimmed chain: the
    # same unconfirmed REQUEST on every member, the ROLLBACK and the rows
    # past it dropped by the cut at the REQUEST's own lsn.
    proc = tt.run("instance", "stop", leader, "-y")
    assert proc.returncode == 0, proc.stdout

    for name in ALL_INSTANCES:
        shutil.rmtree(data_dir(tt_app, name), ignore_errors=True)

    point = {"replica_id": 1, "lsn": request_lsn}
    for name in ALL_INSTANCES:
        rc, out = tt.exec(
            "restore",
            "apply",
            "--archives",
            ",".join(archives),
            "--checksums",
            ",".join(checksums),
            f"{app_name(tt_app)}:{name}",
            "--target-point",
            json.dumps(point),
            "--point-name",
            "sbnd-at-the-request",
            "--patch-uuid",
            UUIDS[name],
        )
        assert rc == 0, f"tt restore apply on {name} failed:\n{out}"

    # -- The replicaset comes back on the distributed chain: nobody stuck in
    # startup, everyone following the leader.
    for name in ALL_INSTANCES:
        proc = tt.run("instance", "start", app_instance(tt_app, name))
        assert proc.returncode == 0, f"tt start {name} failed:\n{proc.stdout}"
    assert tt_helper.wait_box_status(60, tt_app, tt_app.instances, ["running"])

    for name in (STORAGE_B, STORAGE_C):
        wait_upstream_following(tt, tt_app, name, 1)

    # The recovered REQUEST does not resolve on its own: the synchro
    # timeout governs live writes, not the entries recovery replays, so the
    # queue keeps holding the unconfirmed row well past the timeout. The
    # promote below is what collects it -- a new term rolls the old term's
    # pending entries back -- and the report of the stuck state is part of
    # the assertion message.
    deadline = time.monotonic() + SYNCHRO_TIMEOUT + 5
    last_states = None
    while time.monotonic() < deadline:
        last_states = (
            {name: sync_has_row(tt, tt_app, name, HUNG_ROW) for name in ALL_INSTANCES},
            queue_len(tt, tt_app) == 0,
        )
        if last_states[1]:
            break
        time.sleep(0.5)
    states, resolved = last_states
    assert not resolved, f"the recovered REQUEST resolved without an operator: {states}"
    assert len(set(states.values())) == 1, (
        f"the members disagree on the hung row {HUNG_ROW} while the queue is stuck: {states}"
    )

    def synchro_dump():
        return {
            name: eval_on(
                tt,
                tt_app,
                name,
                """
                return {
                    term = box.info.synchro.queue.term,
                    len = box.info.synchro.queue.len,
                }
                """,
            )
            for name in ALL_INSTANCES
        }

    # The promote takes the queue out of the stuck term and the replicaset
    # serves again.
    assert eval_on(tt, tt_app, STORAGE_A, "box.ctl.promote() return true") is True
    wait_queue_empty(tt, tt_app, 30)
    assert queue_len(tt, tt_app) == 0, synchro_dump()

    # The rows around the cut: the async rows up to the point on everyone,
    # nothing past the cut.
    for name in ALL_INSTANCES:
        assert eval_on(tt, tt_app, name, "return box.space.restore_test:len()") == POINT_ROWS, name

    # The hung row's fate after the promote: the new term's PROMOTE
    # delimiter confirms everything the queue was holding — the row is
    # PRESENT on every member, asserted, not observed, so a change of fate
    # would fail the test instead of passing unnoticed.
    for name in ALL_INSTANCES:
        assert sync_has_row(tt, tt_app, name, HUNG_ROW) is True, (
            name,
            synchro_dump(),
        )
        seeded = eval_on(tt, tt_app, name, f"return box.space.{SYNC_SPACE}:len()")
        assert seeded == SEEDED_ROWS + 1, (
            name,
            seeded,
            synchro_dump(),
        )

    # A fresh synchronous write commits and lands on everyone.
    assert (
        eval_on(
            tt,
            tt_app,
            STORAGE_A,
            f"box.space.{SYNC_SPACE}:replace({{{AFTER_ROWS + 1}, 'row-{AFTER_ROWS + 1}'}})"
            " return true",
        )
        is True
    )

    expected = SEEDED_ROWS + 1 + 1
    deadline = time.monotonic() + 30
    for name in ALL_INSTANCES:
        while eval_on(tt, tt_app, name, f"return box.space.{SYNC_SPACE}:len()") != expected:
            assert time.monotonic() < deadline, (
                f"{name} never got the fresh sync row",
                synchro_dump(),
            )
            time.sleep(0.5)
