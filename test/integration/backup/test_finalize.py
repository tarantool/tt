import os
import shutil
from pathlib import Path

import pytest
import tt_helper
from backup_helpers import (
    TT_BACKUP_APP,
    app_instance,
    archive_path_from_output,
    backup_dir,
    finalize_backup,
    get_backup_info_app,
    get_instance_info_app,
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


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_unknown_backup_id_is_a_noop(tt, tt_app):
    """Finalize is idempotent by contract: an id that was never started, on
    an instance with nothing open, succeeds and changes nothing -- so an
    orchestrator can always finish with finalize, whatever happened before."""
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-never-started"

    assert get_backup_info_app(tt, target) is None

    rc, out = finalize_backup(tt, target, backup_id)

    assert rc == 0, f"tt backup finalize failed:\n{out}"
    assert get_backup_info_app(tt, target) is None
    assert not Path(backup_dir(backup_id)).exists()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_wrong_id_closes_the_lease_but_keeps_other_artifacts(tt, tt_app):
    """The instance holds one backup at a time, so finalize closes whatever
    is open regardless of the id -- but it removes only the named id's local
    artifacts. A finalize racing a newer backup can thus drop the lease, yet
    never eats an archive that is not its own."""
    target = app_instance(tt_app, STORAGE_1_A)
    open_id = "itest-open"
    wrong_id = "itest-wrong"

    rc, out = start_backup(tt, target, open_id)
    assert rc == 0, f"backup start failed:\n{out}"
    archive_path = Path(archive_path_from_output(out))

    try:
        rc, out = finalize_backup(tt, target, wrong_id)
        assert rc == 0, f"tt backup finalize failed:\n{out}"

        assert get_backup_info_app(tt, target) is None, "the open lease must be closed"
        assert archive_path.is_file(), "the other backup's archive must survive"
    finally:
        rc, out = finalize_backup(tt, target, open_id)
        assert rc == 0, f"backup cleanup failed:\n{out}"
        assert not archive_path.exists()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_rejects_empty_backup_id(tt, tt_app):
    """An orchestrator template that expands an unset $BACKUP_ID must not be
    able to end the backup: releasing the lease while removing nothing leaks a
    full archive per shard into a directory tt backup gc never looks at, and
    the backup it was told to finish is reported as done."""
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-fin-empty"

    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"backup start failed:\n{out}"
    archive_path = Path(archive_path_from_output(out))
    fragment_path = Path(str(archive_path).removesuffix(".tar.zst") + ".json")

    try:
        rc, out = finalize_backup(tt, target, "")

        assert rc != 0, f"an empty backup id was accepted:\n{out}"
        assert get_backup_info_app(tt, target) is not None, "the lease must stay open"
        assert archive_path.is_file(), "the archive must survive a malformed finalize"
        assert fragment_path.is_file(), "the fragment must survive a malformed finalize"
    finally:
        rc, out = finalize_backup(tt, target, backup_id)
        assert rc == 0, f"backup cleanup failed:\n{out}"

    # The refusal costs nothing: the backup still finishes the ordinary way.
    assert get_backup_info_app(tt, target) is None
    assert not Path(backup_dir(backup_id)).exists()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_rejects_traversing_backup_id(tt, tt_app, tmp_path):
    """Backup ids come from job names, k8s annotations and date templates, so
    an id resolving outside the backup root turns finalize into an unlink and
    an rmdir driven by an external string -- both of which it used to perform
    with exit 0 and no output."""
    target = app_instance(tt_app, STORAGE_1_A)
    replicaset_uuid = get_instance_info_app(tt, target)["replicaset_uuid"]

    # A private temp root so the sentinels can sit right beside it: with
    # <root> = <tmp_path>/tt-backup, the id "../victim" names <tmp_path>/victim
    # as the directory to rmdir and <tmp_path>/victim-<uuid>.tar.zst as the
    # artifact to unlink -- the two primitives the raw id used to hand out.
    env = dict(os.environ, TMPDIR=str(tmp_path))
    victim_dir = tmp_path / "victim"
    victim_dir.mkdir()
    victim_file = tmp_path / f"victim-{replicaset_uuid}.tar.zst"
    victim_file.write_text("not a backup artifact")

    rc, out = finalize_backup(tt, target, "../victim", env=env)

    assert rc != 0, f"a traversing backup id was accepted:\n{out}"
    assert victim_dir.is_dir(), "a directory outside the backup root must not be removed"
    assert victim_file.is_file(), "a file outside the backup root must not be removed"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_with_config(tt, tt_app):
    """The orchestrator finalizes from wherever its job runs, not from the
    environment directory; --config is the only thing that makes the target
    resolvable there, and the sweep must be as complete as a local one."""
    target = app_instance(tt_app, STORAGE_1_A)
    config = Path(tt.work_dir) / "tt.yaml"
    backup_id = "itest-fin-cfg"

    # "/" is the one cwd guaranteed to be outside the environment: tt walks up
    # from the cwd looking for tt.yaml, so any directory below it would find
    # the config on its own and the flag would prove nothing.
    rc, out = start_backup(tt, target, backup_id, config=config, cwd="/")
    assert rc == 0, f"tt backup start -c failed:\n{out}"
    archive_path = Path(archive_path_from_output(out))

    rc, out = finalize_backup(tt, target, backup_id, cwd="/")
    assert rc != 0, f"without --config the environment must not resolve:\n{out}"
    assert archive_path.is_file(), "a finalize that never reached the instance removes nothing"

    rc, out = finalize_backup(tt, target, backup_id, config=config, cwd="/")

    assert rc == 0, f"tt backup finalize -c failed:\n{out}"
    assert get_backup_info_app(tt, target) is None
    assert not Path(backup_dir(backup_id)).exists()


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_bad_config_names_the_path(tt, tt_app, tmp_path):
    """A --config that does not exist has to be named in the error. The
    generic "tt.yaml not found" sends the operator looking for a config in the
    cwd, while the job passed an explicit path that was silently ignored."""
    target = app_instance(tt_app, STORAGE_1_A)
    missing = tmp_path / "nowhere" / "tt.yaml"

    rc, out = finalize_backup(tt, target, "itest-fin-nocfg", config=missing, cwd="/")

    assert rc != 0, f"a missing --config must fail the command:\n{out}"
    assert str(missing) in out, out


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_force_closes_open_backup_and_keeps_local_artifacts(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-finalize-force"

    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"backup start failed:\n{out}"
    archive_path = Path(archive_path_from_output(out))
    fragment_path = Path(str(archive_path).removesuffix(".tar.zst") + ".json")

    try:
        rc, out = finalize_backup(tt, target, None, force=True)
        assert rc == 0, f"tt backup finalize --force failed:\n{out}"

        assert get_backup_info_app(tt, target) is None
        assert archive_path.is_file(), "force must remove no local artifact"
        assert fragment_path.is_file(), "force must remove no local artifact"
    finally:
        shutil.rmtree(backup_dir(backup_id), ignore_errors=True)


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_force_with_no_backup_open_is_a_noop(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)

    assert get_backup_info_app(tt, target) is None

    rc, out = finalize_backup(tt, target, None, force=True)

    assert rc == 0, f"tt backup finalize --force failed:\n{out}"
    assert get_backup_info_app(tt, target) is None


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_force_and_backup_id_are_mutually_exclusive(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)

    rc, out = finalize_backup(tt, target, "itest-fin-both", force=True)

    assert rc != 0, f"--force and --backup-id together must be rejected:\n{out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_requires_backup_id_or_force(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)

    rc, out = finalize_backup(tt, target, None)

    assert rc != 0, f"finalize with neither --backup-id nor --force must be rejected:\n{out}"


@pytest.mark.skipif(not BACKUP_SUPPORTED, reason=skip_reason)
@pytest.mark.tt_app(**TT_BACKUP_APP)
def test_finalize_after_instance_restart(tt, tt_app):
    target = app_instance(tt_app, STORAGE_1_A)
    backup_id = "itest-fin-restart"

    rc, out = start_backup(tt, target, backup_id)
    assert rc == 0, f"backup start failed:\n{out}"
    archive_path = Path(archive_path_from_output(out))
    fragment_path = Path(str(archive_path).removesuffix(".tar.zst") + ".json")

    try:
        rc, out = tt.exec("restart", "app", "-y")
        assert rc == 0, f"restarting the app failed:\n{out}"
        assert tt_helper.wait_box_status(30, tt_app, tt_app.running_instances, ["running"])

        assert get_backup_info_app(tt, target) is None, "the lease dies with the process"
        assert archive_path.is_file(), "the archive must outlive the restart"
        assert fragment_path.is_file(), "the fragment must outlive the restart"

        rc, out = finalize_backup(tt, target, backup_id)

        assert rc == 0, f"tt backup finalize failed:\n{out}"
        assert not Path(backup_dir(backup_id)).exists()
    finally:
        shutil.rmtree(backup_dir(backup_id), ignore_errors=True)
