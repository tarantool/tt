import shutil
from pathlib import Path

import pytest
from backup_helpers import (
    TT_BACKUP_APP,
    app_instance,
    archive_path_from_output,
    backup_dir,
    finalize_backup,
    get_backup_info_app,
    start_backup,
)

from utils import get_tarantool_version, is_tarantool_less

STORAGE_1_A = "storage-001-a"

tarantool_major, tarantool_minor = get_tarantool_version()
BACKUP_SUPPORTED = not is_tarantool_less(3, 8)

skip_reason = (
    f"backup finalize requires Tarantool 3.8.0+ (running {tarantool_major}.{tarantool_minor})"
)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_removes_only_own_artifacts_and_keeps_instance_intact(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-finalize"
    target_dir = Path(backup_dir(backup_id))
    neighbour_dir = Path(backup_dir("must-survive"))

    try:
        rc, out = start_backup(tt, target, backup_id)
        assert rc == 0, f"backup start failed:\n{out}"
        assert get_backup_info_app(tt, target) is not None

        archive_path = Path(archive_path_from_output(out))
        fragment_path = Path(str(archive_path).removesuffix(".tar.zst") + ".json")
        assert archive_path.is_file()
        assert fragment_path.is_file()

        other_archive = target_dir / f"{backup_id}-other-replicaset.tar.zst"
        other_fragment = target_dir / f"{backup_id}-other-replicaset.json"
        other_archive.write_text("other archive")
        other_fragment.write_text("other fragment")

        neighbour_dir.mkdir()
        neighbour_file = neighbour_dir / "sentinel"
        neighbour_file.write_text("other backup")

        rc, out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"tt backup finalize failed:\n{out}"

        assert get_backup_info_app(tt, target) is None
        assert not archive_path.exists()
        assert not fragment_path.exists()
        assert other_archive.read_text() == "other archive"
        assert other_fragment.read_text() == "other fragment"
        assert neighbour_file.read_text() == "other backup"

        # The backup is already closed, but stale artifacts of this replicaset
        # must still be removed without touching anything else.
        archive_path.write_text("stale archive")
        fragment_path.write_text("stale fragment")

        rc, out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"second finalize failed:\n{out}"
        assert get_backup_info_app(tt, target) is None
        assert not archive_path.exists()
        assert not fragment_path.exists()
        assert other_archive.read_text() == "other archive"
        assert other_fragment.read_text() == "other fragment"
        assert neighbour_file.read_text() == "other backup"
    finally:
        shutil.rmtree(target_dir, ignore_errors=True)
        shutil.rmtree(neighbour_dir, ignore_errors=True)
