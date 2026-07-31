"""Shared storage plumbing for the manager-side backup command tests.

Both tt backup verify and tt backup gc work on a synthetic storage: manifests
under manifests/, their archives under data/. The helpers below build such a
storage on either backend (local filesystem or S3) so a test can state what the
storage contains and assert what the command did to it.
"""

import hashlib
import io
import json
import os
from datetime import datetime, timedelta, timezone

import pytest

DEFAULT_CREATION_TIME = datetime(2026, 1, 1, tzinfo=timezone.utc)

REPLICASET = "11111111-1111-1111-1111-111111111111"
REPLICASET_B = "22222222-2222-2222-2222-222222222222"
INSTANCE = "aaaaaaaa-0000-0000-0000-000000000001"
INSTANCE_B = "bbbbbbbb-0000-0000-0000-000000000001"

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

    def set_age(self, key, days):
        """Backdate an object, for rules that go by storage modification time.

        Only the filesystem backend can do this: S3 stamps LastModified itself,
        so age-driven tests are file-only.
        """
        path = os.path.join(self.root, key)
        timestamp = (datetime.now(timezone.utc) - timedelta(days=days)).timestamp()
        os.utime(path, (timestamp, timestamp))

    def keys(self):
        found = []
        for directory, _, files in os.walk(self.root):
            for name in files:
                path = os.path.join(directory, name)
                found.append(os.path.relpath(path, self.root))
        return sorted(found)

    def read(self, key):
        with open(os.path.join(self.root, key), "rb") as src:
            return src.read()


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

    def read(self, key):
        response = self.garage.client.get_object(self.garage.bucket, self.prefix + key)
        return response.read()


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


def build_manifest(
    backup_id,
    previous,
    base,
    backup_type,
    vclock_begin,
    vclock_end,
    content,
    created=DEFAULT_CREATION_TIME,
):
    return {
        "schema_version": 1,
        "backup_id": backup_id,
        "previous_backup_id": previous,
        "base_full_backup_id": base,
        "status": "OK",
        "creation_time": created.strftime("%Y-%m-%dT%H:%M:%SZ"),
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


def days_ago(days):
    """A creation time N days in the past, for retention rules."""
    return datetime.now(timezone.utc) - timedelta(days=days)


def write_backup(
    storage,
    backup_id,
    previous="",
    base=None,
    backup_type="full",
    vclock_begin=0,
    vclock_end=100,
    created=DEFAULT_CREATION_TIME,
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
        created,
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
