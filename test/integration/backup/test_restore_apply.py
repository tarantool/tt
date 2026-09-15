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
    get_backup_info_app,
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


# Deliberately not TT_BACKUP_APP: that app has a vinyl space, which is
# incidental to what the tests in this file exercise (trimming, chain order,
# UUID patching) and would only add an extra engine's files to every fixture.
# See test_apply_vinyl_backup_boots_and_serves for the vinyl-specific path.
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

    A chain is only known to be one once the archives have been read, which is
    why the reading has to come before the work directory is cleared: exit 3
    promises an untouched directory, and a directory left as the unfinished
    result of the run that failed is not one.
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


# The archive preserves each file's path relative to its data directory, so
# vinyl run/index files land under <space_id>/<index_id>/, exactly where
# Tarantool looks for them, and a restored instance boots normally.
VINYL_BOOT_LUA = """
local dir = os.getenv('TT_TEST_WORK_DIR')
box.cfg{work_dir = dir, memtx_dir = dir, wal_dir = dir, vinyl_dir = dir}
print('TT_TEST_RESULT ' .. require('json').encode({
    memtx_rows = box.space.backup_test:len(),
    vinyl_rows = box.space.backup_test_vinyl:len(),
}))
os.exit(0)
"""


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_vinyl_backup_boots_and_serves(tt, tt_app, tmp_path):
    """Vinyl restore round-trips.

    A vinyl run or index is read out of <space_id>/<index_id>/ under
    vinyl_dir, and an instance whose files were flattened into vinyl_dir
    itself does not start. The archive names each file relative to the data
    directory of its own kind, which is what carries those two directories
    through, so a restored vinyl instance boots and serves the data it was
    backed up with, like any memtx-only instance.
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

        vinyl_files = [
            path
            for path in work_dir.rglob("*")
            if path.is_file() and path.suffix in (".run", ".index")
        ]
        assert vinyl_files, "the backup must carry vinyl run/index files"
        assert all(path.parent != work_dir for path in vinyl_files), (
            "vinyl run/index files must land under <space_id>/<index_id>/, not flat"
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

        result = None
        for line in proc.stdout.splitlines():
            if line.startswith("TT_TEST_RESULT "):
                result = json.loads(line.removeprefix("TT_TEST_RESULT "))

        assert result is not None, (
            f"restored vinyl instance did not boot:\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}"
        )
        assert result["memtx_rows"] == 10
        assert result["vinyl_rows"] == 10
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"


# A Tarantool 3.x instance keeps snapshots, journals and vinyl data in three
# directories it configures separately, and the configuration is where a
# restore should read them from rather than having an operator retype them.
# The fixture config puts all three under a process.work_dir and tells its
# instances apart with {{ instance_name }}.
SPLIT_DIRS_DIR = os.path.join(os.path.dirname(__file__), "testdata", "split_dirs")
SPLIT_DIRS_CONFIG = os.path.join(SPLIT_DIRS_DIR, "config.yaml")
SPLIT_DIRS_INSPECT = os.path.join(SPLIT_DIRS_DIR, "inspect.lua")

# What the config resolves to, relative to the directory Tarantool is launched
# from: process.work_dir "base", then one directory per kind, then the
# instance name.
SPLIT_DIRS_RELATIVE = {
    "snapshot": ("base", "memtx", STORAGE_1_A),
    "wal": ("base", "wal", STORAGE_1_A),
    "vinyl": ("base", "vinyl", STORAGE_1_A),
}

# Booting on three directories named directly, for the case where they were
# given as flags and no cluster config exists to boot from.
EXPLICIT_DIRS_BOOT_LUA = """
box.cfg{
    memtx_dir = os.getenv('TT_TEST_SNAPSHOT_DIR'),
    wal_dir = os.getenv('TT_TEST_WAL_DIR'),
    vinyl_dir = os.getenv('TT_TEST_VINYL_DIR'),
}
print('TT_TEST_RESULT ' .. require('json').encode({
    memtx_rows = box.space.backup_test:len(),
    vinyl_rows = box.space.backup_test_vinyl:len(),
}))
os.exit(0)
"""


def split_dirs(launch_dir):
    """The three directories the fixture config resolves to under launch_dir."""
    return {kind: launch_dir.joinpath(*parts) for kind, parts in SPLIT_DIRS_RELATIVE.items()}


def read_tt_test_result(proc, what):
    for line in proc.stdout.splitlines():
        if line.startswith("TT_TEST_RESULT "):
            return json.loads(line.removeprefix("TT_TEST_RESULT "))

    raise AssertionError(f"{what}:\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}")


def boot_from_cluster_config(launch_dir):
    """Boot Tarantool on the restored data, using the config it was restored for.

    The config is the only thing that says where the data is, so booting
    through it is what proves the files landed where the instance looks for
    them. TT_APP_FILE is the config's app.file: a script run after box.cfg,
    which reports and exits.
    """
    proc = subprocess.run(
        ["tarantool", "--name", STORAGE_1_A, "--config", SPLIT_DIRS_CONFIG],
        cwd=str(launch_dir),
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(os.environ, TT_APP_FILE=SPLIT_DIRS_INSPECT),
    )

    return read_tt_test_result(proc, f"the restored instance did not boot in {launch_dir}")


def boot_on_explicit_dirs(dirs, tmp_path):
    """Boot Tarantool on three directories named one by one."""
    script = tmp_path / "boot_explicit.lua"
    script.write_text(EXPLICIT_DIRS_BOOT_LUA, encoding="utf-8")

    proc = subprocess.run(
        ["tarantool", str(script)],
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(
            os.environ,
            TT_TEST_SNAPSHOT_DIR=str(dirs["snapshot"]),
            TT_TEST_WAL_DIR=str(dirs["wal"]),
            TT_TEST_VINYL_DIR=str(dirs["vinyl"]),
        ),
    )

    return read_tt_test_result(proc, "the restored instance did not boot")


def assert_kinds_are_apart(dirs):
    """Every file is in the directory Tarantool will look for its kind in."""
    snapshots = sorted(os.listdir(dirs["snapshot"]))
    journals = sorted(os.listdir(dirs["wal"]))
    vinyl = sorted(path.name for path in dirs["vinyl"].rglob("*") if path.is_file())

    assert snapshots and all(name.endswith(".snap") for name in snapshots), snapshots
    assert journals and all(".xlog" in name for name in journals), journals
    assert vinyl, "the backup must carry vinyl files"
    assert all(name.endswith((".vylog", ".run", ".index")) for name in vinyl), vinyl

    runs = [path for path in dirs["vinyl"].rglob("*") if path.suffix in (".run", ".index")]
    assert runs, "the backup must carry vinyl run/index files"
    assert all(path.parent != dirs["vinyl"] for path in runs), (
        "vinyl run/index files must land under <space_id>/<index_id>/, not flat"
    )


@pytest.fixture
def vinyl_archive(tt, tt_app):
    """A real backup of the app that has both a memtx and a vinyl space.

    A checkpoint dumps the vinyl memory level first, so the archive carries
    run and index files and not only the vylog -- without them nothing in the
    backup would need the vinyl directory at all. The rows written after it
    are in the journal and nowhere else, so the twenty a restored instance
    must serve are twenty only if the journal came back too.
    """
    target = app_instance(tt_app, STORAGE_1_A)

    eval_yaml_app(tt, target, "return box.snapshot()")
    eval_yaml_app(
        tt,
        target,
        "for i = 11, 20 do "
        "box.space.backup_test:replace{i, 'row-' .. i} "
        "box.space.backup_test_vinyl:replace{i, 'vinyl-row-' .. i} "
        "end return true",
    )

    backup_id = "itest-split-dirs"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)

    yield archive, read_fragment(archive)

    finalize_backup(tt, target, backup_id)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_into_the_dirs_the_config_names(tt, tmp_path, vinyl_archive):
    archive, fragment = vinyl_archive
    launch_dir = tmp_path / "launch"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        archive,
        "--checksums",
        fragment["checksum_sha256"],
        "-c",
        SPLIT_DIRS_CONFIG,
        "--instance",
        STORAGE_1_A,
        "--work-dir",
        str(launch_dir),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    dirs = split_dirs(launch_dir)
    assert_kinds_are_apart(dirs)

    # Where each file went is what decides whether the instance finds it, and
    # on a split layout its name says nothing about that.
    for path in dirs.values():
        assert f" in {path}" in out, out
    assert f"data directories ready, marker written to {dirs['snapshot']}" in out, out

    # The fragment describes the backup, not the instance; it stays out of
    # every one of the three directories.
    assert not list(launch_dir.rglob("instance_backup.json"))

    marker = str(dirs["snapshot"]) + ".restore_state.json"
    assert os.path.isfile(marker), "the marker belongs beside the snapshot directory"
    assert not os.path.exists(str(launch_dir) + ".restore_state.json"), (
        "the launch directory is shared, so it is not where the marker goes"
    )

    with open(marker, "r") as src:
        state = json.load(src)

    assert state["work_dir"] == str(launch_dir)
    assert state["snapshot_dir"] == str(dirs["snapshot"])
    assert state["wal_dir"] == str(dirs["wal"])
    assert state["vinyl_dir"] == str(dirs["vinyl"])
    assert state["instance_name"] == STORAGE_1_A
    assert state["instance_uuid"] == APP_INSTANCE_UUID

    result = boot_from_cluster_config(launch_dir)

    assert result["uuid"] == APP_INSTANCE_UUID
    # Ten rows of each space are in the snapshot and the vinyl run files, and
    # ten more only in the journal: twenty means all three directories came
    # back, and which ten are missing says which one did not.
    assert result["memtx_rows"] == 20
    assert result["vinyl_rows"] == 20


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_into_dirs_named_one_by_one(tt, tmp_path, vinyl_archive):
    """The three directories can be named directly, with no config to read."""
    archive, fragment = vinyl_archive
    dirs = {kind: tmp_path / kind for kind in ("snapshot", "wal", "vinyl")}

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        archive,
        "--checksums",
        fragment["checksum_sha256"],
        "--snapshot-dir",
        str(dirs["snapshot"]),
        "--wal-dir",
        str(dirs["wal"]),
        "--vinyl-dir",
        str(dirs["vinyl"]),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    assert_kinds_are_apart(dirs)

    marker = str(dirs["snapshot"]) + ".restore_state.json"
    assert os.path.isfile(marker)

    with open(marker, "r") as src:
        state = json.load(src)

    assert state["wal_dir"] == str(dirs["wal"])
    # No --work-dir was given, so the marker falls back on the directory it
    # is named after rather than recording an empty one.
    assert state["work_dir"] == str(dirs["snapshot"])

    result = boot_on_explicit_dirs(dirs, tmp_path)

    assert result["memtx_rows"] == 20
    assert result["vinyl_rows"] == 20


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_is_idempotent_on_split_dirs(tt, tmp_path, vinyl_archive):
    """A re-run clears what the last one left in each of the three directories.

    The files of an aborted attempt are as replayable as the real ones, and on
    a split layout each directory is a separate place for them to survive in.
    """
    archive, fragment = vinyl_archive
    launch_dir = tmp_path / "launch"

    def apply():
        rc, out = tt.exec(
            "restore",
            "apply",
            "--archives",
            archive,
            "--checksums",
            fragment["checksum_sha256"],
            "-c",
            SPLIT_DIRS_CONFIG,
            "--instance",
            STORAGE_1_A,
            "--work-dir",
            str(launch_dir),
            "--patch-uuid",
            APP_INSTANCE_UUID,
        )
        assert rc == 0, f"tt restore apply failed:\n{out}"

    apply()

    dirs = split_dirs(launch_dir)
    first = {kind: sorted(os.listdir(path)) for kind, path in dirs.items()}
    first_state = boot_from_cluster_config(launch_dir)

    stale = [
        dirs["snapshot"] / "00000000000000000900.snap",
        dirs["wal"] / "00000000000000000900.xlog",
        dirs["vinyl"] / "512" / "0" / "00000000000000000900.run",
    ]
    foreign = [dirs[kind] / "operator-notes.txt" for kind in dirs]

    for path in stale + foreign:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as dst:
            dst.write("x")

    apply()

    for path in stale:
        assert not os.path.exists(path), f"a stale restore artifact must be cleared: {path}"

    for path in foreign:
        assert os.path.isfile(path), f"a file a restore does not own must survive: {path}"

    for kind, path in dirs.items():
        landed = sorted(name for name in os.listdir(path) if name != "operator-notes.txt")
        assert landed == first[kind], kind

    assert boot_from_cluster_config(launch_dir) == first_state


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_apply_rejects_a_layout_it_cannot_complete(tt, tmp_path, vinyl_archive):
    """A call that does not add up to three directories is refused, and what
    a previous run left is left alone.

    The archive is a real one and the target directories are seeded, so the
    exit code can only come from the layout: a missing archive would produce
    the same 3 whatever the directories said.
    """
    archive, fragment = vinyl_archive
    snapshot_dir = tmp_path / "memtx"
    launch_dir = tmp_path / "launch"

    seeded = {}
    for path in (snapshot_dir, tmp_path / "wal", launch_dir):
        os.makedirs(path, exist_ok=True)
        sentinel = path / "00000000000000000900.snap"
        sentinel.write_text("sentinel", encoding="utf-8")
        marker = str(path) + ".restore_state.json"
        with open(marker, "w") as dst:
            dst.write('{"schema_version": 1}')
        seeded[path] = (sentinel, marker)

    calls = {
        "nothing at all": ([], "no snapshot directory given"),
        "a config with nobody to read it for": (
            ["-c", SPLIT_DIRS_CONFIG],
            "--config needs --instance",
        ),
        "an instance with no config to read it from": (
            ["--instance", STORAGE_1_A],
            "--instance needs --config",
        ),
        "two of the three directories": (
            [
                "--snapshot-dir",
                str(snapshot_dir),
                "--wal-dir",
                str(tmp_path / "wal"),
            ],
            "no vinyl directory given",
        ),
        "an instance the config does not declare": (
            [
                "-c",
                SPLIT_DIRS_CONFIG,
                "--instance",
                "storage-002-a",
                "--work-dir",
                str(launch_dir),
            ],
            "declares no instance",
        ),
        "a relative config with nothing to resolve it against": (
            ["-c", SPLIT_DIRS_CONFIG, "--instance", STORAGE_1_A],
            "snapshot.dir",
        ),
        "a config file that is not there": (
            [
                "-c",
                str(tmp_path / "absent.yaml"),
                "--instance",
                STORAGE_1_A,
                "--work-dir",
                str(launch_dir),
            ],
            "failed to load the cluster config",
        ),
        "a config file that does not parse": (
            [
                "-c",
                str(write_malformed_config(tmp_path)),
                "--instance",
                STORAGE_1_A,
                "--work-dir",
                str(launch_dir),
            ],
            "failed to load the cluster config",
        ),
    }

    for name, (args, reported) in calls.items():
        rc, out = tt.exec(
            "restore",
            "apply",
            "--archives",
            archive,
            "--checksums",
            fragment["checksum_sha256"],
            *args,
        )

        assert rc == 3, f"{name} must exit 3:\n{out}"
        assert reported in out, f"{name} must say why:\n{out}"

        for path, (sentinel, marker) in seeded.items():
            assert sentinel.read_text(encoding="utf-8") == "sentinel", (
                f"{name} must leave {path} alone"
            )
            assert open(marker).read() == '{"schema_version": 1}', (
                f"{name} must leave the marker beside {path} alone"
            )

        assert not (tmp_path / "vinyl").exists(), f"{name} must create nothing"


def write_malformed_config(tmp_path):
    """A file that is a cluster config by name and nothing else by content."""
    path = tmp_path / "malformed.yaml"
    path.write_text("groups: [this is not a mapping\n", encoding="utf-8")

    return path


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_reports_a_flat_layout_as_one_directory(tt, tmp_path, backup_archive):
    """One directory is reported as one directory.

    An instance that configures none of the three keys has no split to be told
    about, and a per-directory breakdown would name the same place three
    times.
    """
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

    assert f"into {work_dir}:" in out, out
    assert f"work directory ready, marker written to {work_dir}.restore_state.json" in out, out
    assert "data directories ready" not in out, out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_reports_a_relative_work_dir_as_it_was_given(tt, backup_archive):
    """A relative --work-dir is named in the report the way it was typed.

    The line is read against the command it came from: an operator who named a
    directory relative to the one they are standing in is told about that
    directory. The marker is the other half of it -- it outlives the process,
    so it is named in full.
    """
    archive, point, fragment = backup_archive
    work_dir = "restored-rel"

    rc, out = restore_apply(
        tt,
        archive,
        work_dir,
        checksum=fragment["checksum_sha256"],
        point=point,
        patch_uuid=APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    assert f"into {work_dir}:" in out, out
    assert f"marker written to {tt.path(work_dir)}.restore_state.json" in out, out


def write_default_dirs_config(tmp_path):
    """A cluster config that names none of the three directory keys.

    Every kind of file then goes to the default var/lib/{{ instance_name }},
    which makes one directory for the three -- and one that --work-dir is the
    launch directory of rather than the place the files land in.
    """
    path = tmp_path / "defaults.yaml"
    path.write_text(
        "credentials:\n"
        "  users:\n"
        "    guest:\n"
        "      roles: [super]\n"
        "groups:\n"
        "  storages:\n"
        "    replicasets:\n"
        "      storage-001:\n"
        "        instances:\n"
        f"          {STORAGE_1_A}: {{}}\n",
        encoding="utf-8",
    )

    return path


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_RESTORE_APP)
def test_apply_reports_the_directory_the_config_defaults_to(tt, tmp_path, backup_archive):
    """One directory that is not --work-dir is named as the directory it is.

    A configuration that sets none of the three keys puts every kind of file
    in var/lib/<instance>, so the layout is one directory and --work-dir is
    the directory it sits under. Naming the flag there would point an operator
    at a place the restore wrote nothing into.
    """
    archive, point, fragment = backup_archive
    launch_dir = tmp_path / "launch"
    data_dir = launch_dir / "var" / "lib" / STORAGE_1_A

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        archive,
        "--checksums",
        fragment["checksum_sha256"],
        "-c",
        str(write_default_dirs_config(tmp_path)),
        "--instance",
        STORAGE_1_A,
        "--work-dir",
        str(launch_dir),
        "--target-point",
        json.dumps(point),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    assert f"into {data_dir}:" in out, out
    assert f"into {launch_dir}:" not in out, out
    assert f"work directory ready, marker written to {data_dir}.restore_state.json" in out, out
    assert sorted(os.listdir(launch_dir)) == ["var"], out


# An instance configured with memtx.use_sort_data writes a <signature>.sortdata
# beside every snapshot it takes, and recovery reads the memtx indexes out of
# it instead of rebuilding them. It is a snapshot's file: it belongs in
# memtx_dir, next to the snapshot it was written for, and an instance that
# finds it anywhere else silently rebuilds instead.
SORTDATA_DIR = os.path.join(os.path.dirname(__file__), "testdata", "sortdata_dirs")
SORTDATA_CONFIG = os.path.join(SORTDATA_DIR, "config.yaml")
SORTDATA_INSPECT = os.path.join(SORTDATA_DIR, "inspect.lua")

TT_SORTDATA_APP = dict(
    app_path="sortdata_app",
    app_name="app",
    instances=[STORAGE_1_A],
    running_targets=["app"],
    post_start=post_start_restore_app,
)

# The sort data config puts its three directories where the split dirs one
# does, under the same process.work_dir.
SORTDATA_WORK_DIR = "base"

# A sort data file whose Instance header does not match the instance reading it
# is refused with this, and the file is then ignored: the indexes are rebuilt
# from the snapshot and recovery carries on.
SORTDATA_REFUSED = "unmatched UUID"
SORTDATA_IGNORED = "ignored"
SORTDATA_USED = "using the memtx sort data from"

# An instance whose UUID is not in the _cluster it recovers -- which is what a
# foreign --patch-uuid leaves behind -- cannot register, so it is booted as an
# anonymous read-only replica instead. That is the only part of the boot the
# foreign UUID changes: recovery reads the same files in the same order, and
# the sort data beside the snapshot is still offered to it.
ANON_BOOT_LUA = """
box.cfg{
    memtx_dir = os.getenv('TT_TEST_SNAPSHOT_DIR'),
    wal_dir = os.getenv('TT_TEST_WAL_DIR'),
    vinyl_dir = os.getenv('TT_TEST_VINYL_DIR'),
    memtx_use_sort_data = true,
    replication_anon = true,
    read_only = true,
}
print('TT_TEST_RESULT ' .. require('json').encode({
    uuid = box.info.uuid,
    memtx_rows = box.space.sortdata_test:len(),
}))
os.exit(0)
"""


def archive_entry_names(archive):
    """The names an archive stores its entries under."""
    proc = subprocess.run(
        ["tar", "-tf", archive],
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert proc.returncode == 0, f"failed to list {archive}:\n{proc.stderr}"

    return sorted(line.strip() for line in proc.stdout.splitlines() if line.strip())


def sort_data_name(fragment):
    """The one sort data entry the archive carries, by the name it carries it under."""
    names = [name for name in fragment["files"] if name.endswith(".sortdata")]
    assert len(names) == 1, f"expected one sort data file in the backup, got {fragment['files']}"

    return names[0]


def logged_path(line, cwd):
    """The path a Tarantool log line quotes, resolved the way the instance saw it.

    A directory the configuration names relative to process.work_dir is logged
    relative to it too, so a line is only about a file once it has been
    resolved against the directory the instance was running in.
    """
    quoted = line.rsplit("`", 1)[-1].rstrip("'")

    return os.path.realpath(os.path.join(cwd, quoted))


def boot_from_sortdata_config(launch_dir):
    """Boot on the restored data through the sort data config: (result, log).

    The log is what says whether the sort data was read, so unlike the other
    boot helpers this one hands it back: a file the instance ignored leaves the
    reported state exactly as a file it used does.
    """
    proc = subprocess.run(
        ["tarantool", "--name", STORAGE_1_A, "--config", SORTDATA_CONFIG],
        cwd=str(launch_dir),
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(os.environ, TT_APP_FILE=SORTDATA_INSPECT),
    )

    return (
        read_tt_test_result(proc, f"the restored instance did not boot in {launch_dir}"),
        proc.stderr,
    )


def boot_anonymous(dirs, tmp_path):
    """Boot on three directories as an anonymous replica: (result, log)."""
    script = tmp_path / "boot_anon.lua"
    script.write_text(ANON_BOOT_LUA, encoding="utf-8")

    proc = subprocess.run(
        ["tarantool", str(script)],
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(
            os.environ,
            TT_TEST_SNAPSHOT_DIR=str(dirs["snapshot"]),
            TT_TEST_WAL_DIR=str(dirs["wal"]),
            TT_TEST_VINYL_DIR=str(dirs["vinyl"]),
        ),
    )

    return (
        read_tt_test_result(proc, "the restored instance did not boot"),
        proc.stderr,
    )


def assert_sort_data_is_beside_the_snapshot(dirs, name):
    """The sort data sits in the snapshot directory, and in no other one."""
    snapshot_dir = dirs["snapshot"]
    assert (snapshot_dir / name).is_file(), sorted(os.listdir(snapshot_dir))
    assert (snapshot_dir / (name.removesuffix(".sortdata") + ".snap")).is_file(), (
        "the sort data belongs next to the snapshot it was written for"
    )

    for kind in ("wal", "vinyl"):
        strays = [path for path in dirs[kind].rglob("*.sortdata")]
        assert not strays, f"the sort data must not land in the {kind} directory: {strays}"


@pytest.fixture
def sortdata_archive(tt, tt_app):
    """A real backup of an instance that keeps memtx sort data.

    The file is written by a checkpoint, so one is taken here with the option
    already on, and its presence on disk is checked before the backup runs:
    an archive carrying no sort data would let every test below pass for
    having nothing to place. The rows written after the checkpoint are in the
    journal alone, so the twenty a restored instance serves are twenty only if
    the journal came back as well.
    """
    target = app_instance(tt_app, STORAGE_1_A)

    memtx_dir = eval_yaml_app(tt, target, "return require('fio').abspath(box.cfg.memtx_dir)")
    eval_yaml_app(tt, target, "return box.snapshot()")
    eval_yaml_app(
        tt,
        target,
        "for i = 11, 20 do box.space.sortdata_test:replace{i, 'row-' .. i} end return true",
    )

    on_disk = sorted(name for name in os.listdir(memtx_dir) if name.endswith(".sortdata"))
    assert on_disk, (
        f"a checkpoint must write memtx sort data into {memtx_dir}, "
        f"which holds {sorted(os.listdir(memtx_dir))}"
    )

    backup_id = "itest-sortdata"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    archive = archive_path_from_output(out)
    # Read while the backup is open: box.backup.info() is what the archive was
    # packed from, and it stops reporting once the backup is finalized.
    backup_files = get_backup_info_app(tt, target)["files"]

    yield archive, read_fragment(archive), backup_files

    finalize_backup(tt, target, backup_id)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_SORTDATA_APP)
def test_backup_carries_the_memtx_sort_data(sortdata_archive):
    """The sort data file is backed up, under a name that places it by kind.

    Tarantool lists it among the files of the checkpoint, so a backup that
    packs what box.backup.info() reports carries it. It lives directly in
    memtx_dir and is named against memtx_dir, which leaves the bare name: a
    name carrying a directory would be one a restore has to recreate
    underneath the target snapshot directory rather than in it.
    """
    archive, fragment, backup_files = sortdata_archive

    listed = [path for path in backup_files if path.endswith(".sortdata")]
    assert len(listed) == 1, f"box.backup.info() must report the sort data: {backup_files}"

    name = sort_data_name(fragment)
    assert name == os.path.basename(listed[0]), listed
    assert "/" not in name, f"the sort data is stored under a bare name, not {name!r}"

    entries = archive_entry_names(archive)
    assert name in entries, entries
    assert name.removesuffix(".sortdata") + ".snap" in entries, (
        "the snapshot the sort data was written for must be in the same archive"
    )


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_SORTDATA_APP)
def test_apply_puts_the_sort_data_beside_its_snapshot(tt, tmp_path, sortdata_archive):
    """A restored instance reads the sort data back.

    The file is only ever read from the directory the snapshot it belongs to
    was read from, and on a split layout that is one of three the restore
    writes into. The log line is the only thing that tells a file that was used
    from one that was silently rebuilt around: the rows come back either way.
    """
    archive, fragment, _ = sortdata_archive
    launch_dir = tmp_path / "launch"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        archive,
        "--checksums",
        fragment["checksum_sha256"],
        "-c",
        SORTDATA_CONFIG,
        "--instance",
        STORAGE_1_A,
        "--work-dir",
        str(launch_dir),
        "--patch-uuid",
        APP_INSTANCE_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    dirs = split_dirs(launch_dir)
    name = sort_data_name(fragment)
    assert_sort_data_is_beside_the_snapshot(dirs, name)

    result, log = boot_from_sortdata_config(launch_dir)

    used = [line for line in log.splitlines() if SORTDATA_USED in line]
    assert len(used) == 1, f"the instance must report the sort data it read:\n{log}"
    assert logged_path(used[0], launch_dir / SORTDATA_WORK_DIR) == os.path.realpath(
        dirs["snapshot"] / name,
    ), used[0]

    assert result["uuid"] == APP_INSTANCE_UUID
    # Ten rows are in the snapshot the sort data describes and ten more only in
    # the journal: twenty means both directories came back.
    assert result["memtx_rows"] == 20


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_SORTDATA_APP)
def test_apply_leaves_the_sort_data_uuid_for_the_instance_to_judge(
    tt,
    tmp_path,
    sortdata_archive,
):
    """A stamped restore does not stamp the sort data, and need not.

    --patch-uuid rewrites the headers of the files recovery refuses outright on
    a mismatch; the sort data is not one of them. Its header keeps the UUID it
    was written with, the instance sees that it is not its own, says so and
    rebuilds the indexes from the snapshot instead -- which is the same data,
    read the slower way. Placing it correctly still matters: a file in the
    wrong directory is never read, so it is never judged either.
    """
    archive, fragment, _ = sortdata_archive
    launch_dir = tmp_path / "launch"

    rc, out = tt.exec(
        "restore",
        "apply",
        "--archives",
        archive,
        "--checksums",
        fragment["checksum_sha256"],
        "-c",
        SORTDATA_CONFIG,
        "--instance",
        STORAGE_1_A,
        "--work-dir",
        str(launch_dir),
        "--patch-uuid",
        FOREIGN_UUID,
    )
    assert rc == 0, f"tt restore apply failed:\n{out}"

    dirs = split_dirs(launch_dir)
    name = sort_data_name(fragment)
    assert_sort_data_is_beside_the_snapshot(dirs, name)

    assert header_instance_uuid(dirs["snapshot"] / name) == APP_INSTANCE_UUID, (
        "the sort data keeps the UUID it was written with"
    )
    for snapshot in dirs["snapshot"].glob("*.snap"):
        assert header_instance_uuid(snapshot) == FOREIGN_UUID, snapshot

    result, log = boot_anonymous(dirs, tmp_path)

    refused = [line for line in log.splitlines() if SORTDATA_REFUSED in line]
    assert len(refused) == 1, f"the instance must report the UUID it refused:\n{log}"
    assert APP_INSTANCE_UUID in refused[0], refused[0]
    assert str(dirs["snapshot"] / name) in refused[0], refused[0]

    ignored = [line for line in log.splitlines() if SORTDATA_IGNORED in line and name in line]
    assert len(ignored) == 1, f"the refused sort data must be reported as ignored:\n{log}"
    assert SORTDATA_USED not in log, f"a refused sort data file must not be used:\n{log}"

    assert result["uuid"] == FOREIGN_UUID
    assert result["memtx_rows"] == 20, "the indexes are rebuilt, not lost"
