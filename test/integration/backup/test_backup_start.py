import os
import time
from pathlib import Path

import pytest
from backup_helpers import (
    TT_BACKUP_APP,
    app_instance,
    archive_path_from_output,
    assert_fragment_matches_golden,
    backup_dir,
    eval_yaml_app,
    finalize_backup,
    get_backup_info_app,
    inspect_backup_artifact,
    start_backup,
)

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"
TESTDATA_DIR = Path(__file__).parent / "testdata"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"backup start requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_full_artifact_and_open_backup_lifecycle(tt, tt_app, tmp_path):
    target = app_instance(tt_app, STORAGE_1_A)

    eval_yaml_app(tt, target, "return box.snapshot()")
    eval_yaml_app(tt, target, "box.backup.recovery_point.create({label = 'rp-itest'})")

    backup_id = "itest-full"
    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"tt backup start failed:\n{out}"

    try:
        archive_path = archive_path_from_output(out)
        fragment = inspect_backup_artifact(archive_path, tmp_path / "full", backup_id)
        assert_fragment_matches_golden(fragment, TESTDATA_DIR / "fragment_full.golden.json")

        # Lifecycle: the backup stays open after start.
        assert get_backup_info_app(tt, target) is not None

        with open(archive_path, "rb") as src:
            archive_before = src.read()

        conflicting_id = "itest-already-open"
        conflict_rc, conflict_out = start_backup(tt, target, conflicting_id)
        assert conflict_rc == 2, conflict_out
        assert "backup already in progress" in conflict_out.lower()
        assert "type=full" in conflict_out.lower(), conflict_out
        assert "vclock_begin=" in conflict_out.lower(), conflict_out
        assert not os.path.exists(backup_dir(conflicting_id))
        with open(archive_path, "rb") as src:
            assert src.read() == archive_before
        assert get_backup_info_app(tt, target) is not None
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_incremental_continues_full_artifact(tt, tt_app, tmp_path):
    target = app_instance(tt_app, STORAGE_1_A)

    full_id = "itest-inc-full"
    rc, out = start_backup(tt, target, full_id)
    assert rc == 0, f"full backup start failed:\n{out}"
    try:
        full_fragment = inspect_backup_artifact(
            archive_path_from_output(out),
            tmp_path / "full",
            full_id,
        )
    finally:
        rc, finalize_out = finalize_backup(tt, target, full_id)
        assert rc == 0, f"full backup finalize failed:\n{finalize_out}"

    # Write more data after the full backup.
    eval_yaml_app(
        tt,
        target,
        "for i = 100, 105 do box.space.backup_test:insert({i, 'extra-' .. i}) end",
    )

    inc_id = "itest-inc"
    rc, out = start_backup(
        tt,
        target,
        inc_id,
        from_vclock=full_fragment["vclock_end"],
    )
    assert rc == 0, f"incremental backup start failed:\n{out}"

    try:
        inc_fragment = inspect_backup_artifact(
            archive_path_from_output(out),
            tmp_path / "incremental",
            inc_id,
        )
        assert_fragment_matches_golden(
            inc_fragment,
            TESTDATA_DIR / "fragment_incremental.golden.json",
        )

        # Cross-field consistency: the incremental must continue the full
        # backup's end vclock (the value we passed via --from-vclock).
        assert inc_fragment["vclock_begin"] == full_fragment["vclock_end"]
        assert any(
            inc_fragment["vclock_end"].get(replica_id, 0) > lsn
            for replica_id, lsn in full_fragment["vclock_end"].items()
        )
        assert get_backup_info_app(tt, target) is not None
    finally:
        rc, cleanup_out = finalize_backup(tt, target, inc_id)
        assert rc == 0, f"incremental backup cleanup failed:\n{cleanup_out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
def test_start_unreachable_instance(tt):
    """An instance that cannot be reached fails the command cleanly and
    leaves no half-made backup directory behind."""
    backup_id = "itest-unreachable"

    rc, out = start_backup(tt, "127.0.0.1:1", backup_id)

    assert rc != 0
    assert "failed" in out.lower()
    assert not os.path.exists(backup_dir(backup_id))


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_rejects_backup_id(tt, tt_app, tmp_path):
    """The id names both the directory under the backup root and the artifact
    base name below it, so one that addresses anything else has to be refused
    before box.backup is opened: a rejection that costs a lease is a stuck
    backup, and an id that escapes the root writes archives nothing collects."""
    target = app_instance(tt_app, STORAGE_1_A)

    # A private temp root: everything the command would create lands in here,
    # so "created nothing" is an exact statement about the whole tree it owns.
    tmp_root = tmp_path / "tmproot"
    tmp_root.mkdir()
    env = dict(os.environ, TMPDIR=str(tmp_root))

    for backup_id in ("../escape", "2026/08/02-full", ""):
        rc, out = start_backup(tt, target, backup_id, env=env)

        assert rc != 0, f"backup id {backup_id!r} was accepted:\n{out}"
        assert list(tmp_root.iterdir()) == [], f"backup id {backup_id!r} created files"
        assert get_backup_info_app(tt, target) is None, f"backup id {backup_id!r} left a lease open"

    # The id is checked before the target is dialed: an instance nobody can
    # reach still answers with the malformed id, not with a dial failure.
    rc, out = start_backup(tt, "127.0.0.1:1", "../escape", env=env)
    assert rc != 0
    assert "invalid backup id" in out, out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_concurrent_losers_exit_two(tt, tt_app):
    """Exit 2 is the code an orchestrator keys on to route to its stuck-backup
    branch, and a retry storm is exactly the situation that leaves a backup
    stuck. A start that loses the race inside the instance must therefore be as
    recognizable as one that loses it sequentially."""
    target = app_instance(tt_app, STORAGE_1_A)
    backup_ids = ["itest-c1", "itest-c2", "itest-c3"]

    procs = [tt.popen("backup", "start", target, "--backup-id", i) for i in backup_ids]
    results = {}
    for backup_id, proc in zip(backup_ids, procs):
        out, _ = proc.communicate(timeout=120)
        results[backup_id] = (proc.returncode, out)

    try:
        winners = [won_id for won_id, (rc, _) in results.items() if rc == 0]
        assert len(winners) == 1, f"exactly one start may win the race: {results}"

        for backup_id, (rc, out) in results.items():
            if backup_id in winners:
                continue
            assert rc == 2, f"{backup_id} lost with rc {rc}, not the stuck-backup code:\n{out}"
            assert "already in progress" in out.lower(), out
            assert not os.path.exists(backup_dir(backup_id)), "a loser must create no directory"
    finally:
        for backup_id in backup_ids:
            finalize_backup(tt, target, backup_id)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.skipif(os.geteuid() == 0, reason="root writes into a read-only directory anyway")
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_failure_after_open_closes_the_backup(tt, tt_app, tmp_path):
    """An open backup pins the instance's WAL and checkpoint gc until the ttl
    runs out and blocks every later start, so a run that cannot deliver an
    archive -- here because the backup root is not writable -- has to close it
    again instead of leaving the lease for an operator to find."""
    target = app_instance(tt_app, STORAGE_1_A)

    backup_root = tmp_path / "tmproot" / "tt-backup"
    backup_root.mkdir(parents=True)
    backup_root.chmod(0o500)
    try:
        rc, out = start_backup(
            tt,
            target,
            "itest-readonly-root",
            env=dict(os.environ, TMPDIR=str(backup_root.parent)),
        )
    finally:
        backup_root.chmod(0o755)

    assert rc != 0, f"a read-only backup root must fail the start:\n{out}"
    # Names the directory it could not create, so the run really did get past
    # box.backup.start() -- a failure before it would prove nothing here.
    assert str(backup_root) in out, out
    assert get_backup_info_app(tt, target) is None, "a failed start must not keep the lease"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_with_explicit_config(tt, tt_app, tmp_path):
    """--config is how the orchestrator points a start at an environment its
    job does not stand in. The same command without it cannot even resolve the
    target, so the flag has to carry the whole environment, not just the dial."""
    target = app_instance(tt_app, STORAGE_1_A)
    config = Path(tt.work_dir) / "tt.yaml"
    backup_id = "itest-cfg"

    # "/" is the one cwd guaranteed to be outside the environment: tt searches
    # parent directories for tt.yaml, so any dir below it would find the config
    # on its own and the flag would prove nothing.
    rc, out = start_backup(tt, target, backup_id, cwd="/")
    assert rc != 0, f"without --config the environment must not resolve:\n{out}"
    assert not os.path.exists(backup_dir(backup_id))

    rc, out = start_backup(tt, target, backup_id, config=config, cwd="/")
    assert rc == 0, f"tt backup start -c failed:\n{out}"

    try:
        inspect_backup_artifact(archive_path_from_output(out), tmp_path / "cfg", backup_id)
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id, config=config, cwd="/")
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_start_ttl_expiry_releases_the_lease(tt, tt_app):
    """The --ttl lease expires on the instance side, so an orchestrator that
    died between start and finalize does not pin the instance's gc forever.
    Expiry releases only the lease: the local archive stays for the pipeline
    to pick up, and finalize still cleans it like after any closed backup."""
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-ttl"

    rc, out = start_backup(tt, target, backup_id, ttl="2s")
    assert rc == 0, f"tt backup start failed:\n{out}"

    try:
        archive_path = archive_path_from_output(out)

        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if get_backup_info_app(tt, target) is None:
                break
            time.sleep(1)
        else:
            pytest.fail("the backup lease did not expire within 30s of a 2s ttl")

        assert os.path.isfile(archive_path), "expiry must not touch the local archive"
    finally:
        rc, cleanup_out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{cleanup_out}"
        assert not os.path.exists(backup_dir(backup_id))
