import hashlib
import io
import json
import os
import time

import pytest
from backup_helpers import backup_verify

REPLICASET = "11111111-1111-1111-1111-111111111111"
REPLICASET_B = "22222222-2222-2222-2222-222222222222"
INSTANCE = "aaaaaaaa-0000-0000-0000-000000000001"
INSTANCE_B = "bbbbbbbb-0000-0000-0000-000000000001"

VERIFY_OK = 0
VERIFY_PROBLEMS_FOUND = 2

STORAGE_BACKENDS = [
    pytest.param(("file", ""), id="file"),
    pytest.param(("s3", ""), marks=pytest.mark.docker, id="s3"),
]
PREFIXED_STORAGE_BACKENDS = [
    pytest.param(("file", "mycluster"), id="file"),
    pytest.param(("s3", "mycluster"), marks=pytest.mark.docker, id="s3"),
]


class FileStorage:
    """Backup storage on the local filesystem."""

    def __init__(self, root, prefix=""):
        self.root = os.path.join(root, prefix) if prefix else root
        self.uri = f"file://{root}" + (f"?Prefix={prefix}" if prefix else "")

    def put(self, key, data):
        path = os.path.join(self.root, key)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as dst:
            dst.write(data)

    def delete(self, key):
        os.remove(os.path.join(self.root, key))

    def keys(self):
        found = []
        for directory, _, files in os.walk(self.root):
            for name in files:
                path = os.path.join(directory, name)
                found.append(os.path.relpath(path, self.root))
        return sorted(found)


class S3Storage:
    """Backup storage in an S3 bucket."""

    def __init__(self, garage, prefix=""):
        self.garage = garage
        self.prefix = f"{prefix}/" if prefix else ""
        self.uri = (
            f"s3+http://{garage.endpoint}/{garage.bucket}"
            f"{'/' + prefix if prefix else ''}"
            f"?Region={garage.region}&AccessKeyID={garage.access_key}"
            f"&SecretAccessKey={garage.secret_key}"
        )

    def put(self, key, data):
        self.garage.client.put_object(
            self.garage.bucket,
            self.prefix + key,
            io.BytesIO(data),
            len(data),
        )

    def delete(self, key):
        self.garage.client.remove_object(self.garage.bucket, self.prefix + key)

    def keys(self):
        return sorted(
            obj.object_name.removeprefix(self.prefix)
            for obj in self.garage.client.list_objects(self.garage.bucket, recursive=True)
        )


@pytest.fixture
def storage(request, tmp_path):
    backend, prefix = request.param
    if backend == "file":
        root = tmp_path / "backups"
        root.mkdir()
        yield FileStorage(str(root), prefix)
        return

    garage = request.getfixturevalue("garage")
    s3 = S3Storage(garage, prefix)
    try:
        yield s3
    finally:
        for key in s3.keys():
            s3.delete(key)


def archive_key(backup_id, replicaset=REPLICASET):
    return f"data/{backup_id}-{replicaset}.tar.zst"


def manifest_key(backup_id):
    return f"manifests/{backup_id}.json"


def topology_instance(instance_uuid, instance_name):
    return {
        "instance_uuid": instance_uuid,
        "instance_name": instance_name,
        "hostname": "localhost",
    }


def shard_instance(
    backup_id,
    replicaset,
    instance_uuid,
    instance_name,
    backup_type,
    vclock_begin,
    vclock_end,
    content,
):
    return {
        "instance_uuid": instance_uuid,
        "instance_name": instance_name,
        "hostname": "localhost",
        # Only an incremental starts from a vclock: a full backup starts from
        # nothing, and box.backup.info() reports no prev_vclock for it.
        "vclock_begin": {"1": vclock_begin} if backup_type == "incremental" else None,
        "vclock_end": {"1": vclock_end},
        "artifact": {
            "path": archive_key(backup_id, replicaset),
            "size_bytes": len(content),
            "checksum_sha256": hashlib.sha256(content).hexdigest(),
            "compression": "zstd",
            "files": ["00000000000000000000.snap"],
            "recovery_points": [],
            "type": backup_type,
        },
    }


def build_manifest(backup_id, previous, base, backup_type, vclock_begin, vclock_end, content):
    return {
        "schema_version": 1,
        "backup_id": backup_id,
        "previous_backup_id": previous,
        "base_full_backup_id": base,
        "status": "OK",
        "creation_time": "2026-01-01T00:00:00Z",
        "creation_duration": 1000000000,
        "shards": {
            REPLICASET: {
                "instance": shard_instance(
                    backup_id,
                    REPLICASET,
                    INSTANCE,
                    "storage-001-a",
                    backup_type,
                    vclock_begin,
                    vclock_end,
                    content,
                ),
            },
        },
        "topology": {
            "replicasets": {
                REPLICASET: [topology_instance(INSTANCE, "storage-001-a")],
            },
        },
        "warnings": [],
    }


def write_backup(
    storage,
    backup_id,
    previous="",
    base=None,
    backup_type="full",
    vclock_begin=0,
    vclock_end=100,
):
    """Store a healthy backup: an archive plus the manifest referring to it."""
    content = f"archive of {backup_id}".encode()
    manifest = build_manifest(
        backup_id,
        previous,
        base if base is not None else backup_id,
        backup_type,
        vclock_begin,
        vclock_end,
        content,
    )
    storage.put(archive_key(backup_id), content)
    write_manifest(storage, manifest)
    return manifest


def add_shard(storage, manifest, backup_type="full", vclock_begin=0, vclock_end=100):
    """Add a second successfully backed up replicaset to a stored backup."""
    backup_id = manifest["backup_id"]
    content = f"archive of {backup_id} {REPLICASET_B}".encode()

    storage.put(archive_key(backup_id, REPLICASET_B), content)
    manifest["topology"]["replicasets"][REPLICASET_B] = [
        topology_instance(INSTANCE_B, "storage-002-a"),
    ]
    manifest["shards"][REPLICASET_B] = {
        "instance": shard_instance(
            backup_id,
            REPLICASET_B,
            INSTANCE_B,
            "storage-002-a",
            backup_type,
            vclock_begin,
            vclock_end,
            content,
        ),
    }
    write_manifest(storage, manifest)

    return manifest


def add_failed_shard(storage, manifest, error="instance unreachable"):
    """Add a replicaset that failed to back up: an error instead of an artifact."""
    manifest["topology"]["replicasets"][REPLICASET_B] = [
        topology_instance(INSTANCE_B, "storage-002-a"),
    ]
    manifest["shards"][REPLICASET_B] = {"error": error}
    manifest["status"] = "degraded"
    write_manifest(storage, manifest)

    return manifest


def write_manifest(storage, manifest):
    storage.put(manifest_key(manifest["backup_id"]), json.dumps(manifest).encode())


def write_chain(storage):
    """Store a healthy full backup followed by an increment."""
    full = write_backup(storage, "2026-01-01-full", vclock_begin=0, vclock_end=100)
    incremental = write_backup(
        storage,
        "2026-01-02-inc",
        previous="2026-01-01-full",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
    )
    return full, incremental


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


def test_verify_timeout_expires(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage)

    rc, out = backup_verify(tt, storage.uri, fmt="json", timeout="1ns")

    # A storage that could not be read within the timeout is exit code 1, not a
    # clean bill of health.
    assert rc == 1
    assert "context deadline exceeded" in out.lower()
