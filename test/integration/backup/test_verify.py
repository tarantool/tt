import json
import os
import time

import pytest
from backup_helpers import backup_verify
from storage_helpers import (
    PREFIXED_STORAGE_BACKENDS,
    REPLICASET,
    REPLICASET_B,
    STORAGE_BACKENDS,
    FileStorage,
    add_failed_shard,
    add_shard,
    archive_key,
    manifest_key,
    write_backup,
    write_chain,
    write_manifest,
)

VERIFY_OK = 0
VERIFY_PROBLEMS_FOUND = 2


def verify_json(tt, storage):
    """Run verify, asserting the storage was not modified, and return its report."""
    before = storage.keys()

    rc, out = backup_verify(tt, storage.uri, fmt="json")
    assert rc in (VERIFY_OK, VERIFY_PROBLEMS_FOUND), f"tt backup verify failed:\n{out}"

    assert storage.keys() == before, "tt backup verify must not modify the storage"

    return rc, json.loads(out)


def issue_kinds(report):
    return [issue["kind"] for issue in report["issues"]]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_healthy_storage(tt, storage):
    write_chain(storage)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK
    assert report["issues"] == []
    assert report["manifests_checked"] == 2
    assert report["archives_checked"] == 2


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_reports_duplicate_backup_id(tt, storage):
    """A manifest copied to a second key used to abort the whole run with "could
    not check" and a usage dump, saying nothing at all about the storage - even
    though a duplicated backup_id is exactly the defect verify exists to name."""
    full, _ = write_chain(storage)
    storage.put(manifest_key(full["backup_id"]) + ".copy", json.dumps(full).encode())

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    kinds = issue_kinds(report)
    assert kinds.count("unreadable_manifest") == 2, report
    details = " ".join(issue["detail"] for issue in report["issues"])
    assert "duplicate backup_id" in details, report
    # The untouched increment is still checked rather than lost with them.
    assert report["archives_checked"] == 1, report


@pytest.mark.parametrize("storage", [pytest.param(("file", ""), id="file")], indirect=True)
def test_verify_keeps_stale_temp_files(tt, storage):
    """Opening a file storage used to sweep leftover .tt-backup-* files, which
    contradicts verify's promise to delete nothing whatever it finds. Such a file
    is the residue of an interrupted upload - evidence, not garbage."""
    write_chain(storage)

    stale = os.path.join(storage.root, "data", ".tt-backup-interrupted")
    os.makedirs(os.path.dirname(stale), exist_ok=True)
    with open(stale, "wb") as dst:
        dst.write(b"half an upload")
    aged = time.time() - 48 * 60 * 60
    os.utime(stale, (aged, aged))

    # verify_json asserts the key set is unchanged, and keys() walks dotfiles too.
    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK
    assert report["issues"] == []
    assert os.path.exists(stale)


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_empty_storage(tt, storage):
    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK
    assert report["manifests_checked"] == 0
    assert report["issues"] == []


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_missing_archive(tt, storage):
    write_chain(storage)
    storage.delete(archive_key("2026-01-02-inc"))

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["missing_archive"]

    issue = report["issues"][0]
    assert issue["backup_id"] == "2026-01-02-inc"
    assert issue["replicaset_uuid"] == REPLICASET
    assert issue["archive"] == archive_key("2026-01-02-inc")


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_checksum_mismatch(tt, storage):
    write_chain(storage)
    storage.put(archive_key("2026-01-01-full"), b"corrupted archive")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["checksum_mismatch"]
    assert report["issues"][0]["backup_id"] == "2026-01-01-full"


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_broken_chain(tt, storage):
    _, incremental = write_chain(storage)
    incremental["previous_backup_id"] = "2025-12-31-vanished"
    write_manifest(storage, incremental)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["chain_orphan"]
    assert "2025-12-31-vanished" in report["issues"][0]["detail"]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_vclock_mismatch(tt, storage):
    _, incremental = write_chain(storage)
    # The increment starts past the end of the full backup: a hole in the WAL.
    incremental["shards"][REPLICASET]["instance"]["vclock_begin"] = {"1": 150}
    write_manifest(storage, incremental)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["vclock_mismatch"]
    assert report["issues"][0]["backup_id"] == "2026-01-02-inc"


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_dangling_archive(tt, storage):
    write_chain(storage)
    # Older than the newest manifest: the pipeline has moved past this backup id,
    # so nothing can still be uploading it.
    dangling = archive_key("2026-01-01-abandoned")
    storage.put(dangling, b"no manifest refers to me")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["dangling_archive"]
    assert report["issues"][0]["archive"] == dangling


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_reports_an_upload_in_progress_without_failing(tt, storage):
    # An upload writes its archives before the manifest referencing them, so a
    # verify overlapping the nightly backup sees unreferenced archives of a
    # healthy backup. Alerting on that would fire on every backup.
    write_chain(storage)
    uploading = archive_key("2026-01-03-uploading")
    storage.put(uploading, b"still uploading")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK, report
    assert issue_kinds(report) == ["upload_in_progress"]
    assert report["issues"][0]["archive"] == uploading
    assert report["issues"][0]["informational"] is True


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_reports_the_first_upload_of_an_empty_storage(tt, storage):
    # No manifest to compare against: this is a cluster whose first backup has
    # not finished uploading, not a storage full of garbage.
    uploading = archive_key("2026-01-01-first")
    storage.put(uploading, b"still uploading")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK, report
    assert issue_kinds(report) == ["upload_in_progress"]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_upload_in_progress_does_not_mask_a_real_problem(tt, storage):
    write_chain(storage)
    storage.put(archive_key("2026-01-03-uploading"), b"still uploading")
    storage.put(archive_key("2026-01-01-full"), b"corrupted archive")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert sorted(issue_kinds(report)) == ["checksum_mismatch", "upload_in_progress"]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_reports_every_problem_class(tt, storage):
    write_chain(storage)
    storage.put(archive_key("2026-01-01-full"), b"corrupted archive")
    storage.delete(archive_key("2026-01-02-inc"))
    storage.put(archive_key("2026-01-01-abandoned"), b"dangling")
    write_backup(
        storage,
        "2026-01-04-orphan",
        previous="2025-12-31-vanished",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=200,
        vclock_end=300,
    )

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert sorted(issue_kinds(report)) == sorted(
        [
            "checksum_mismatch",
            "missing_archive",
            "chain_orphan",
            "dangling_archive",
        ],
    )


@pytest.mark.parametrize("storage", PREFIXED_STORAGE_BACKENDS, indirect=True)
def test_verify_with_prefix(tt, storage):
    # Manifest paths and listed keys are both relative to the configured prefix,
    # so a healthy prefixed storage must not look like one full of orphans.
    write_chain(storage)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK, report
    assert report["manifests_checked"] == 2
    assert report["archives_checked"] == 2


@pytest.mark.parametrize("storage", PREFIXED_STORAGE_BACKENDS, indirect=True)
def test_verify_with_prefix_finds_problems(tt, storage):
    write_chain(storage)
    storage.delete(archive_key("2026-01-02-inc"))

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["missing_archive"]
    assert report["issues"][0]["archive"] == archive_key("2026-01-02-inc")


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_unreadable_manifest_does_not_hide_the_rest(tt, storage):
    write_chain(storage)
    storage.put(manifest_key("2026-01-03-broken"), b"{")
    storage.put(archive_key("2026-01-01-full"), b"corrupted archive")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["unreadable_manifest", "checksum_mismatch"]
    assert report["issues"][0]["backup_id"] == "2026-01-03-broken"
    assert report["manifests_checked"] == 3


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_checks_every_shard_of_a_manifest(tt, storage):
    full = write_backup(storage, "2026-01-01-full")
    add_shard(storage, full)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK, report
    assert report["archives_checked"] == 2

    # Corrupting one shard must point at that shard, and only at it.
    storage.put(archive_key("2026-01-01-full", REPLICASET_B), b"corrupted archive")

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["checksum_mismatch"]
    assert report["issues"][0]["replicaset_uuid"] == REPLICASET_B


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_skips_shard_that_failed_to_back_up(tt, storage):
    # A replicaset that was unreachable carries an error, not an artifact:
    # the manifest already records it, there is nothing to check in storage.
    full = write_backup(storage, "2026-01-01-full")
    add_failed_shard(storage, full)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_OK, report
    assert report["archives_checked"] == 1


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_checksum_missing(tt, storage):
    full = write_backup(storage, "2026-01-01-full")
    full["shards"][REPLICASET]["instance"]["artifact"]["checksum_sha256"] = ""
    write_manifest(storage, full)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["checksum_missing"]
    assert report["issues"][0]["archive"] == archive_key("2026-01-01-full")


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_inherited_chain_problem(tt, storage):
    write_backup(storage, "2026-01-01-full")
    # The parent of this increment is gone, and the next one inherits the break.
    write_backup(
        storage,
        "2026-01-02-orphan",
        previous="2025-12-31-vanished",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
    )
    write_backup(
        storage,
        "2026-01-03-tail",
        previous="2026-01-02-orphan",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=200,
        vclock_end=300,
    )

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["chain_orphan", "chain_orphan"]

    inherited = [issue for issue in report["issues"] if issue.get("inherited")]
    assert len(inherited) == 1
    assert inherited[0]["backup_id"] == "2026-01-03-tail"


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_chain_fork(tt, storage):
    write_chain(storage)
    # A second increment on the same parent: the chain forks.
    write_backup(
        storage,
        "2026-01-03-inc",
        previous="2026-01-01-full",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=300,
    )

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["chain_fork", "chain_fork"]
    assert {issue["backup_id"] for issue in report["issues"]} == {
        "2026-01-02-inc",
        "2026-01-03-inc",
    }


def test_verify_table_format(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)
    storage.delete(archive_key("2026-01-02-inc"))

    rc, out = backup_verify(tt, storage.uri)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert not out.lstrip().startswith("{")
    assert "missing_archive" in out
    assert "2026-01-02-inc" in out


def test_verify_default_format_is_table(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)

    rc, out = backup_verify(tt, storage.uri)

    assert rc == VERIFY_OK
    assert "Manifests checked" in out
    assert not out.lstrip().startswith("{")


def test_verify_invalid_format(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)

    rc, out = backup_verify(tt, storage.uri, fmt="xml")

    assert rc != VERIFY_OK
    assert "unsupported format" in out.lower()


def test_verify_missing_backup_storage_flag(tt):
    rc, out = tt.exec("backup", "verify")

    assert rc != VERIFY_OK
    assert "required" in out.lower()


@pytest.mark.docker
def test_verify_s3_auth_error_does_not_expose_secret(tt, garage):
    secret = "garage-invalid-secret"
    uri = (
        f"s3+http://{garage.endpoint}/{garage.bucket}"
        f"?Region={garage.region}&AccessKeyID={garage.access_key}"
        f"&SecretAccessKey={secret}"
    )

    rc, out = backup_verify(tt, uri, fmt="json")

    assert rc == 1
    assert secret not in out


@pytest.mark.skipif(
    os.getuid() == 0,
    reason="Skipping the test, it shouldn't run as root",
)
def test_verify_unreadable_storage(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)
    os.chmod(os.path.join(storage.root, "manifests"), 0o000)

    try:
        rc, out = backup_verify(tt, storage.uri, fmt="json")
    finally:
        os.chmod(os.path.join(storage.root, "manifests"), 0o755)

    # An unreadable storage is an operational failure, not a health verdict.
    assert rc == 1
    assert "verify" in out.lower()


@pytest.mark.skipif(
    os.getuid() == 0,
    reason="Skipping the test, it shouldn't run as root",
)
def test_verify_unreadable_archive(tt, tmp_path):
    # The archive is listed and referenced, but its bytes cannot be read: that
    # is not the same finding as a missing archive.
    storage = FileStorage(str(tmp_path / "backups"))
    write_backup(storage, "2026-01-01-full")
    archive = os.path.join(storage.root, archive_key("2026-01-01-full"))
    os.chmod(archive, 0o000)

    try:
        rc, report = verify_json(tt, storage)
    finally:
        os.chmod(archive, 0o644)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["unreadable_archive"]
    assert report["issues"][0]["archive"] == archive_key("2026-01-01-full")


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_verify_reports_an_unsupported_schema_version(tt, storage):
    """A manifest from a newer tt has to be reported as one unusable manifest and
    nothing else. Once tt starts writing schema_version 2, every older tt reading
    that storage produces this report: if the version were ignored the fields
    would be read under the wrong meaning, and if it turned into a second finding
    every backup of the storage would also look corrupt or abandoned."""
    write_backup(storage, "2026-01-01-full", schema_version=2)

    rc, report = verify_json(tt, storage)

    assert rc == VERIFY_PROBLEMS_FOUND
    assert issue_kinds(report) == ["invalid_manifest"]
    assert "schema_version" in report["issues"][0]["detail"]
    # The archive is still checked: the manifest is beyond this tt, the storage
    # is not.
    assert report["archives_checked"] == 1


@pytest.mark.skipif(
    os.getuid() == 0,
    reason="Skipping the test, it shouldn't run as root",
)
def test_verify_failure_does_not_dump_the_flag_list(tt, tmp_path):
    """A failed run used to print the whole flag list after the error, so the one
    line saying what went wrong ended up thirty lines above the end of a cron
    log."""
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)
    manifests = os.path.join(storage.root, "manifests")
    os.chmod(manifests, 0o000)

    try:
        rc, out = backup_verify(tt, storage.uri)
    finally:
        os.chmod(manifests, 0o755)

    assert rc == 1
    assert "--format string" not in out
    assert "help for verify" not in out

    last_line = [line for line in out.splitlines() if line.strip()][-1]
    assert "Error:" in last_line, out


def test_verify_timeout_expires(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)

    rc, out = backup_verify(tt, storage.uri, fmt="json", timeout="1ns")

    # A storage that could not be read within the timeout is exit code 1, not a
    # clean bill of health.
    assert rc == 1
    assert "context deadline exceeded" in out.lower()
