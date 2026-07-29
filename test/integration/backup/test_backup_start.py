import os
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
