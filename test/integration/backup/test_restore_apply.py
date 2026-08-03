import json
import os
import shutil
import subprocess

import pytest
import tt_helper
from backup_helpers import (
    TT_BACKUP_APP,
    app_instance,
    archive_path_from_output,
    eval_yaml_app,
    finalize_backup,
    start_backup,
)

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"

# The UUID the test app pins for its only instance. A restored node has to be
# stamped with the UUID recorded in _cluster at backup time -- which is what
# `tt restore plan` hands out as restore_targets[<rs>].patch_uuid -- or the
# instance comes up unknown to its own snapshot and fails to register.
APP_INSTANCE_UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

# A UUID that is *not* in _cluster, used where the point is to observe the
# patch in the file headers rather than to boot the result.
FOREIGN_UUID = "cccccccc-cccc-cccc-cccc-cccccccccccc"


def post_start_restore_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])


# Deliberately not TT_BACKUP_APP: that app has a vinyl space, and a backup
# archive stores files flat, so vinyl run/index files lose the
# <space_id>/<index_id> directories Tarantool looks for them in. A restored
# instance with vinyl data cannot start at all.
TT_RESTORE_APP = dict(
    app_path="restore_app",
    app_name="app",
    instances=[STORAGE_1_A],
    running_targets=["app"],
    post_start=post_start_restore_app,
)

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"restore apply requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)

INSPECT_LUA = """
local dir = os.getenv('TT_TEST_WORK_DIR')
box.cfg{work_dir = dir, memtx_dir = dir, wal_dir = dir, vinyl_dir = dir}
print('TT_TEST_RESULT ' .. require('json').encode({
    uuid = box.info.uuid,
    lsn = box.info.vclock[1],
    rows = box.space.restore_test:len(),
    max_id = box.space.restore_test.index.pk:max()[1],
}))
os.exit(0)
"""


def boot_restored(work_dir, tmp_path):
    """Start Tarantool on a restored work directory and report its state."""
    script = tmp_path / "inspect.lua"
    script.write_text(INSPECT_LUA, encoding="utf-8")

    proc = subprocess.run(
        ["tarantool", str(script)],
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(os.environ, TT_TEST_WORK_DIR=str(work_dir)),
    )

    for line in proc.stdout.splitlines():
        if line.startswith("TT_TEST_RESULT "):
            return json.loads(line.removeprefix("TT_TEST_RESULT "))

    raise AssertionError(
        f"Tarantool did not start from {work_dir}:\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}",
    )


def restore_apply(tt, archive, work_dir, checksum=None, point=None, patch_uuid=None, name=None):
    args = ["restore", "apply", "--archives", archive, "--work-dir", str(work_dir)]
    if checksum is not None:
        args.extend(["--checksums", checksum])
    if point is not None:
        args.extend(["--target-point", json.dumps(point)])
    if name is not None:
        args.extend(["--point-name", name])
    if patch_uuid is not None:
        args.extend(["--patch-uuid", patch_uuid])
    return tt.exec(*args)


def read_fragment(archive_path):
    with open(archive_path.removesuffix(".tar.zst") + ".json", "r") as src:
        return json.load(src)


def header_instance_uuid(path):
    """Read the Instance line out of a journal file's meta header."""
    with open(path, "rb") as src:
        header = src.read(512).split(b"\n\n", 1)[0]

    for line in header.split(b"\n"):
        if line.startswith(b"Instance: "):
            return line.removeprefix(b"Instance: ").decode()

    raise AssertionError(f"no Instance line in the header of {path}")


@pytest.fixture
def backup_archive(tt, tt_app):
    """A real backup of the app, with a point in the middle of its range.

    Rows 11..30 are written, the position is recorded, then rows 31..50 go on
    top: restoring to that position must bring back exactly the first 30.
    """
    target = app_instance(tt_app, STORAGE_1_A)

    eval_yaml_app(tt, target, "return box.snapshot()")
    eval_yaml_app(
        tt,
        target,
        "for i = 11, 30 do box.space.restore_test:replace{i, 'row-' .. i} end return true",
    )
    point = eval_yaml_app(tt, target, "return {replica_id = 1, lsn = box.info.vclock[1]}")
    eval_yaml_app(
        tt,
        target,
        "for i = 31, 50 do box.space.restore_test:replace{i, 'row-' .. i} end return true",
    )

    backup_id = "itest-restore"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)

    yield archive, point, read_fragment(archive)

    finalize_backup(tt, target, backup_id)


def collect_archive(archive, dest_dir):
    """Copy an archive and its fragment off the node, as the orchestrator does.

    tt backup finalize deletes the local archive, so anything meant to outlive
    the backup has to be taken first.
    """
    os.makedirs(dest_dir, exist_ok=True)
    fragment = read_fragment(archive)
    collected = os.path.join(dest_dir, os.path.basename(archive))
    shutil.copy2(archive, collected)

    return collected, fragment


@pytest.fixture
def backup_chain(tt, tt_app, tmp_path):
    """A real full backup plus a real increment continuing it.

    Everything the unit tests know about chains they know from journal files
    written by hand, with vclocks set by the test - and that is exactly the
    shape of fixture that hid two bugs in the xlog adapter until an instance
    was booted off the result. This builds the chain the way the pipeline
    does: tt backup start, more writes, tt backup start --from-vclock.

    Rows 11..30 land in the full backup, 31..50 in the increment, and the
    point sits inside the increment, at row 40.
    """
    target = app_instance(tt_app, STORAGE_1_A)

    eval_yaml_app(tt, target, "return box.snapshot()")
    eval_yaml_app(
        tt,
        target,
        "for i = 11, 30 do box.space.restore_test:replace{i, 'row-' .. i} end return true",
    )

    staged = tmp_path / "collected"

    full_id = "itest-chain-full"
    rc, out = start_backup(tt, target, full_id)
    assert rc == 0, f"full backup start failed:\n{out}"
    full_archive, full_fragment = collect_archive(archive_path_from_output(out), staged)
    rc, out = finalize_backup(tt, target, full_id)
    assert rc == 0, f"full backup finalize failed:\n{out}"

    # The increment has to carry rows the full backup does not, and the point
    # has to sit inside it - otherwise the test would pass without the
    # increment ever being applied.
    eval_yaml_app(
        tt,
        target,
        "for i = 31, 40 do box.space.restore_test:replace{i, 'row-' .. i} end return true",
    )
    point = eval_yaml_app(tt, target, "return {replica_id = 1, lsn = box.info.vclock[1]}")
    eval_yaml_app(
        tt,
        target,
        "for i = 41, 50 do box.space.restore_test:replace{i, 'row-' .. i} end return true",
    )

    inc_id = "itest-chain-inc"
    rc, out = start_backup(tt, target, inc_id, from_vclock=full_fragment["vclock_end"])
    assert rc == 0, f"incremental backup start failed:\n{out}"
    inc_archive, inc_fragment = collect_archive(archive_path_from_output(out), staged)

    assert inc_fragment["vclock_begin"] == full_fragment["vclock_end"], (
        "the increment must continue the full backup"
    )

    yield [full_archive, inc_archive], point, [full_fragment, inc_fragment]

    finalize_backup(tt, target, inc_id)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_boots_a_real_chain_on_the_point(tt, tmp_path, backup_chain):
    archives, point, fragments = backup_chain
    work_dir = tmp_path / "restored"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(archives),
        "--checksums",
        ",".join(f["checksum_sha256"] for f in fragments),
        "--work-dir",
        str(work_dir),
        "--target-point",
        json.dumps(point),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == point["lsn"], "the instance must come up on the point"
    # 40 rows: 1..10 from app start, 11..30 from the full backup, 31..40 from
    # the increment. Anything short of 40 means the increment never replayed.
    assert state["rows"] == 40
    assert state["max_id"] == 40


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_replays_a_real_chain_whole_without_a_point(tt, tmp_path, backup_chain):
    archives, _, fragments = backup_chain
    work_dir = tmp_path / "restored"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        ",".join(archives),
        "--checksums",
        ",".join(f["checksum_sha256"] for f in fragments),
        "--work-dir",
        str(work_dir),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == fragments[-1]["vclock_end"]["1"]
    assert state["rows"] == 50


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_rejects_a_malformed_patch_uuid(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, out
    prepared = sorted(os.listdir(work_dir))

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        point=point,
        patch_uuid="not-a-uuid",
    )
    assert rc == 3, f"a malformed --patch-uuid must exit 3:\n{out}"

    assert sorted(os.listdir(work_dir)) == prepared, (
        "a rejected input must leave the previous attempt alone"
    )


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_boots_on_the_recovery_point(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    # The fragment describes the backup, not the instance; it stays out.
    assert "instance_backup.json" not in os.listdir(work_dir)

    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == point["lsn"], "the instance must come up on the point"
    assert state["rows"] == 30, "rows written past the point must not come back"
    assert state["max_id"] == 30


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_without_a_point_replays_the_whole_chain(tt, tmp_path, backup_archive):
    archive, _, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    state = boot_restored(work_dir, tmp_path)

    assert state["lsn"] == fragment["vclock_end"]["1"]
    assert state["rows"] == 50


# The stamp has to reach every header in the archive, not only the snapshot:
# a .vylog left carrying another UUID fails recovery outright with "invalid
# instance UUID", before any of the data is replayed.
@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_patches_every_header(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        patch_uuid=FOREIGN_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    landed = os.listdir(work_dir)
    assert landed, "the work directory must not be empty"

    for name in landed:
        assert header_instance_uuid(work_dir / name) == FOREIGN_UUID, name


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_without_patch_uuid_keeps_the_headers(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    for name in os.listdir(work_dir):
        assert header_instance_uuid(work_dir / name) == APP_INSTANCE_UUID, name


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_writes_a_marker_beside_the_work_dir(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    point_name = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        name=point_name,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, out

    marker = str(work_dir) + ".restore_state.json"
    assert os.path.isfile(marker), "the marker belongs beside the work directory"
    assert not os.path.exists(work_dir / "restore_state.json"), "and not inside it"

    with open(marker, "r") as src:
        state = json.load(src)

    assert state["work_dir"] == str(work_dir)
    assert state["point_name"] == point_name
    assert state["target_point"] == point
    assert state["instance_uuid"] == APP_INSTANCE_UUID
    assert state["archives"] == [os.path.basename(archive)]


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_is_idempotent(tt, tmp_path, backup_archive):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    def apply():
        rc, out = restore_apply(
            tt,
            archive,
            work_dir,
            checksum=fragment["checksum_sha256"],
            point=point,
            patch_uuid=APP_INSTANCE_UUID,
        )
        assert rc == 0, out
        return sorted(os.listdir(work_dir))

    first = apply()
    first_state = boot_restored(work_dir, tmp_path)

    # Booting the instance wrote a fresh xlog into the directory; the second
    # run has to clear that as well as its own output.
    second = apply()

    assert first == second
    assert boot_restored(work_dir, tmp_path) == first_state


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_rejects_a_bad_checksum_without_touching_the_work_dir(
    tt,
    tmp_path,
    backup_archive,
):
    archive, point, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, out
    prepared = sorted(os.listdir(work_dir))

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum="0" * 64,
        point=point,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 3, f"a rejected input must exit 3:\n{out}"

    assert sorted(os.listdir(work_dir)) == prepared, "the previous attempt must survive"
    assert os.path.isfile(str(work_dir) + ".restore_state.json")


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_rejects_a_reversed_chain_without_touching_the_work_dir(
    tt,
    tmp_path,
    backup_chain,
):
    """A refused chain is a rejected input: exit 3 leaves the directory alone.

    The chain is only known to be one once the archives have been read, so the
    refusal used to arrive after the work directory had been cleared and
    rebuilt -- exit 3 promising an untouched directory that was in fact the
    unfinished result of the run that failed.
    """
    archives, point, fragments = backup_chain
    work_dir = tmp_path / "restored"

    def apply(ordered_archives, ordered_fragments):
        return tt.exec(
            "restore",
            "apply",
            "--archives",
            ",".join(ordered_archives),
            "--checksums",
            ",".join(f["checksum_sha256"] for f in ordered_fragments),
            "--work-dir",
            str(work_dir),
            "--target-point",
            json.dumps(point),
            "--patch-uuid",
            APP_INSTANCE_UUID,
        )

    rc, out = apply(archives, fragments)
    assert rc == 0, f"tt restore apply failed:\n{out}"

    marker = str(work_dir) + ".restore_state.json"
    prepared = {name: os.path.getsize(work_dir / name) for name in os.listdir(work_dir)}
    marker_content = open(marker).read()

    rc, out = apply(archives[::-1], fragments[::-1])
    assert rc == 3, f"a refused chain must exit 3:\n{out}"

    # Sizes, not just names: the increment applied first would put the
    # untrimmed copy of the boundary journal back.
    survived = {name: os.path.getsize(work_dir / name) for name in os.listdir(work_dir)}
    assert survived == prepared, "the previous attempt must survive"
    assert open(marker).read() == marker_content

    state = boot_restored(work_dir, tmp_path)
    assert state["lsn"] == point["lsn"]


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_reports_a_point_no_xlog_covers(tt, tmp_path, backup_archive):
    archive, _, fragment = backup_archive
    work_dir = tmp_path / "restored"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point={"replica_id": 1, "lsn": 1},
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 2, f"a point below the chain must exit 2:\n{out}"
    assert "no xlog covers the recovery point" in out


def test_apply_rejects_a_misspelled_target_point_key(tt, tmp_path):
    """--target-point rejects unknown keys instead of decoding a misspelled
    replica_id to zero and trimming on the wrong axis. The reject fires
    before any input is opened, so even the archive does not have to exist."""
    work_dir = tmp_path / "restored"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        str(tmp_path / "missing.tar.zst"),
        "--work-dir",
        str(work_dir),
        "--target-point",
        '{"replicaid": 1, "lsn": 5}',
    )

    assert rc == 3, f"a rejected input must exit 3:\n{out}"
    assert not work_dir.exists()


def test_apply_rejects_a_mismatched_checksum_count(tt, tmp_path):
    """--checksums must pair with --archives one to one; a mismatch is
    rejected before anything is opened or written."""
    work_dir = tmp_path / "restored"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        str(tmp_path / "missing.tar.zst"),
        "--checksums",
        "0" * 64 + "," + "1" * 64,
        "--work-dir",
        str(work_dir),
    )

    assert rc == 3, f"a rejected input must exit 3:\n{out}"
    assert "2 checksums for 1 archives" in out
    assert not work_dir.exists()


# The flat archive layout suits memtx, whose snapshots and xlogs live at the
# top of the work directory. Vinyl run/index files belong under
# <space_id>/<index_id>/ and Tarantool looks for them only there.
VINYL_BOOT_LUA = """
local dir = os.getenv('TT_TEST_WORK_DIR')
box.cfg{work_dir = dir, memtx_dir = dir, wal_dir = dir, vinyl_dir = dir}
print('TT_TEST_BOOTED')
os.exit(0)
"""


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_vinyl_backup_is_not_restorable_yet(tt, tt_app, tmp_path):
    """Pin of a known limitation, not of desired behavior.

    A backup archive stores files flat, so vinyl run/index files lose the
    <space_id>/<index_id> directories Tarantool looks for them in -- which
    is why every other test in this file runs on a memtx-only app. Apply
    itself succeeds and the vinyl files land, flat, in the work directory;
    the restored instance then cannot start. When the archive layout learns
    to preserve the directories, flip this into a boots-and-serves test.
    """
    target = app_instance(tt_app, STORAGE_1_A)

    # A checkpoint dumps the vinyl memory level, so the backup carries run
    # files and not just the vylog.
    eval_yaml_app(tt, target, "return box.snapshot()")

    backup_id = "itest-vinyl"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    try:
        archive = archive_path_from_output(out)
        work_dir = tmp_path / "restored"

        rc, out = restore_apply(tt, archive, work_dir)
        assert rc == 0, f"tt restore apply failed:\n{out}"

        landed = os.listdir(work_dir)
        vinyl_files = [name for name in landed if name.endswith((".run", ".index"))]
        assert vinyl_files, "the backup must carry vinyl run/index files"
        assert all(os.path.isfile(work_dir / name) for name in landed), (
            "the archive layout is flat: every file lands at the top level"
        )

        script = tmp_path / "boot.lua"
        script.write_text(VINYL_BOOT_LUA, encoding="utf-8")
        proc = subprocess.run(
            ["tarantool", str(script)],
            capture_output=True,
            text=True,
            timeout=60,
            env=dict(os.environ, TT_TEST_WORK_DIR=str(work_dir)),
        )

        assert "TT_TEST_BOOTED" not in proc.stdout, (
            "a restored vinyl instance booted: the layout limitation is "
            "gone, turn this test into boots-and-serves assertions"
        )
        assert proc.returncode != 0
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"
