import json

import pytest
from backup_helpers import backup_gc
from storage_helpers import (
    STORAGE_BACKENDS,
    FileStorage,
    archive_key,
    days_ago,
    manifest_key,
    write_backup,
)

GC_OK = 0


def write_chain(storage, base_id, *ages):
    """Store a full backup plus one increment per extra age, in days."""
    ids = []
    for index, age in enumerate(ages):
        if index == 0:
            write_backup(storage, base_id, created=days_ago(age))
            ids.append(base_id)
            continue

        backup_id = f"{base_id}-inc{index}"
        write_backup(
            storage,
            backup_id,
            previous=ids[-1],
            base=base_id,
            backup_type="incremental",
            vclock_begin=index * 100,
            vclock_end=(index + 1) * 100,
            created=days_ago(age),
        )
        ids.append(backup_id)

    return ids


def gc_json(tt, storage, **kwargs):
    """Run gc, returning the parsed report."""
    rc, out = backup_gc(tt, storage.uri, fmt="json", **kwargs)
    assert rc == GC_OK, f"tt backup gc failed:\n{out}"

    return json.loads(out)


def deleted_backup_ids(report):
    return [deleted["backup_id"] for deleted in report["plan"]["backups"]]


def orphan_keys(report):
    return [orphan["key"] for orphan in report["plan"]["orphans"]]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_without_retention_rule_is_noop(tt, storage):
    write_chain(storage, "2026-01-01-full", 60)
    write_chain(storage, "2026-02-01-full", 2)
    before = storage.keys()

    report = gc_json(tt, storage)

    assert report["plan"]["backups"] == []
    assert report["result"]["backups_deleted"] == 0
    assert storage.keys() == before
    assert any("no retention rule" in note for note in report["plan"]["notes"])


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_keep_full_deletes_older_chains(tt, storage):
    write_chain(storage, "2026-01-01-full", 60)
    write_chain(storage, "2026-02-01-full", 30)
    write_chain(storage, "2026-02-20-full", 3)

    report = gc_json(tt, storage, keep_full=1)

    assert deleted_backup_ids(report) == ["2026-01-01-full", "2026-02-01-full"]
    assert report["result"]["backups_deleted"] == 2
    assert report["result"]["archives_deleted"] == 2
    assert storage.keys() == [
        archive_key("2026-02-20-full"),
        manifest_key("2026-02-20-full"),
    ]


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_cascade_deletes_the_whole_chain(tt, storage):
    write_chain(storage, "2026-01-01-full", 60, 59, 58)
    write_chain(storage, "2026-02-20-full", 3)

    report = gc_json(tt, storage, keep_full=1)

    # Newest first, so an interrupted run leaves a valid chain prefix.
    assert deleted_backup_ids(report) == [
        "2026-01-01-full-inc2",
        "2026-01-01-full-inc1",
        "2026-01-01-full",
    ]
    assert manifest_key("2026-01-01-full") not in storage.keys()
    assert archive_key("2026-01-01-full-inc2") not in storage.keys()


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_keep_days_holds_the_backups_a_kept_increment_needs(tt, storage):
    # Only the last increment is inside the window, but it cannot be recovered
    # without the full backup and the increments before it.
    write_chain(storage, "2026-01-01-full", 60, 59, 2)
    write_chain(storage, "2026-02-20-full", 1)
    before = storage.keys()

    report = gc_json(tt, storage, keep_days=7)

    assert report["plan"]["backups"] == []
    assert storage.keys() == before


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_keep_days_cuts_the_tail_after_the_last_kept_backup(tt, storage):
    # inc1 is inside the window, inc2 and inc3 are not.
    write_chain(storage, "2026-01-01-full", 60, 2, 40, 39)
    write_chain(storage, "2026-02-20-full", 1)

    report = gc_json(tt, storage, keep_days=7)

    assert deleted_backup_ids(report) == [
        "2026-01-01-full-inc3",
        "2026-01-01-full-inc2",
    ]
    assert manifest_key("2026-01-01-full") in storage.keys()
    assert manifest_key("2026-01-01-full-inc1") in storage.keys()


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_never_deletes_the_head_chain(tt, storage):
    # A single chain, far outside every window: gc still refuses to empty the
    # storage, and says so.
    write_chain(storage, "2026-01-01-full", 60, 59)
    before = storage.keys()

    report = gc_json(tt, storage, keep_days=1, keep_full=1)

    assert report["plan"]["backups"] == []
    assert storage.keys() == before
    assert any("newest manifest" in note for note in report["plan"]["notes"])
    assert any("never deletes every chain" in note for note in report["plan"]["notes"])


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_keeps_the_newest_recoverable_chain(tt, storage):
    # The newest manifest belongs to a tail whose full backup is gone. Protecting
    # only that one would delete every backup that can still be restored and keep
    # the one that cannot.
    write_chain(storage, "2026-01-01-full", 60, 59)
    write_backup(
        storage,
        "2026-03-01-orphan",
        previous="2026-02-28-full",
        base="2026-02-28-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
        created=days_ago(1),
    )
    before = storage.keys()

    # A generous --orphan-age keeps the tail out of the stale-tail rule, so the
    # only thing under test is which chain the protections land on.
    report = gc_json(tt, storage, keep_days=7, orphan_age="72h")

    assert report["plan"]["backups"] == []
    assert storage.keys() == before
    assert any("recovered from" in note for note in report["plan"]["notes"])


def test_gc_keeps_archives_when_no_manifest_is_stored_yet(tt, tmp_path):
    # The first backup of a new cluster: archives are uploaded before the
    # manifest, so an empty manifests/ is not a storage full of garbage.
    storage = FileStorage(str(tmp_path / "backups"))
    first = archive_key("2026-02-28-first")
    storage.put(first, b"uploading")
    storage.set_age(first, 30)

    report = gc_json(tt, storage, keep_full=1, orphan_age="1h")

    assert orphan_keys(report) == []
    assert storage.keys() == [first]


def test_gc_rejects_negative_orphan_age(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-01-01-full", 1)

    rc, out = backup_gc(tt, storage.uri, keep_full=1, orphan_age="-1h")

    assert rc != GC_OK
    assert "negative" in out.lower()


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_keeps_a_forked_chain(tt, storage):
    write_chain(storage, "2026-01-01-full", 60, 59)
    # A second increment on the same parent: diagnostic material for verify.
    write_backup(
        storage,
        "2026-01-01-full-fork",
        previous="2026-01-01-full",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
        created=days_ago(58),
    )
    write_chain(storage, "2026-02-20-full", 1)
    before = storage.keys()

    report = gc_json(tt, storage, keep_full=1, keep_days=7)

    assert report["plan"]["backups"] == []
    assert storage.keys() == before
    assert any("chain problems" in note for note in report["plan"]["notes"])


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_dry_run_deletes_nothing(tt, storage):
    write_chain(storage, "2026-01-01-full", 60, 59)
    write_chain(storage, "2026-02-20-full", 1)
    before = storage.keys()

    report = gc_json(tt, storage, keep_full=1, dry_run=True)

    assert report["dry_run"] is True
    assert "result" not in report or report["result"] is None
    assert deleted_backup_ids(report) == ["2026-01-01-full-inc1", "2026-01-01-full"]
    assert storage.keys() == before


@pytest.mark.parametrize("storage", STORAGE_BACKENDS, indirect=True)
def test_gc_deletes_a_stale_chain_without_its_full_backup(tt, storage):
    write_chain(storage, "2026-02-20-full", 1)
    # An increment whose full backup is gone: nothing can recover from it.
    write_backup(
        storage,
        "2026-01-05-orphan",
        previous="2026-01-01-full",
        base="2026-01-01-full",
        backup_type="incremental",
        vclock_begin=100,
        vclock_end=200,
        created=days_ago(40),
    )

    report = gc_json(tt, storage, keep_full=1, orphan_age="24h")

    assert deleted_backup_ids(report) == ["2026-01-05-orphan"]
    assert manifest_key("2026-01-05-orphan") not in storage.keys()


def test_gc_deletes_old_dangling_archive(tt, tmp_path):
    # Storage modification times can only be forged on the filesystem backend.
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-02-20-full", 1)
    old = archive_key("2026-01-10-abandoned")
    young = archive_key("2026-02-19-abandoned")
    storage.put(old, b"dangling")
    storage.put(young, b"dangling")
    storage.set_age(old, 30)

    report = gc_json(tt, storage, keep_full=1, orphan_age="24h")

    assert orphan_keys(report) == [old]
    assert report["result"]["orphans_deleted"] == 1
    assert old not in storage.keys()
    assert young in storage.keys()


def test_gc_keeps_archive_newer_than_every_manifest(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-01-01-full", 60)
    # A long-running upload: its archives are old, but its backup id is newer
    # than every manifest, so they are not dangling.
    in_flight = archive_key("2026-02-28-uploading")
    storage.put(in_flight, b"uploading")
    storage.set_age(in_flight, 30)

    report = gc_json(tt, storage, keep_full=1, orphan_age="1h")

    assert orphan_keys(report) == []
    assert in_flight in storage.keys()
    assert any("newer than the newest manifest" in note for note in report["plan"]["notes"])


def test_gc_table_format(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-01-01-full", 60)
    write_chain(storage, "2026-02-20-full", 1)

    rc, out = backup_gc(tt, storage.uri, keep_full=1, dry_run=True)

    assert rc == GC_OK, out
    assert not out.lstrip().startswith("{")
    assert "dry run" in out
    assert "2026-01-01-full" in out


def test_gc_invalid_format(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-01-01-full", 1)

    rc, out = backup_gc(tt, storage.uri, keep_full=1, fmt="xml")

    assert rc != GC_OK
    assert "unsupported format" in out.lower()


def test_gc_rejects_negative_retention(tt, tmp_path):
    storage = FileStorage(str(tmp_path / "backups"))
    write_chain(storage, "2026-01-01-full", 1)

    rc, out = backup_gc(tt, storage.uri, keep_full=-1)

    assert rc != GC_OK
    assert "negative" in out.lower()


def test_gc_missing_backup_storage_flag(tt):
    rc, out = tt.exec("backup", "gc")

    assert rc != GC_OK
    assert "required" in out.lower()
