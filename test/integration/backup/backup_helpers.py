import hashlib
import json
import os
import subprocess
import tempfile

import tt_helper
import yaml

# Must resolve to the same directory as Go's os.TempDir() in cli/backup: both
# honour TMPDIR, which on macOS points into /var/folders/... rather than /tmp.
BACKUP_TMP_ROOT = os.path.join(tempfile.gettempdir(), "tt-backup")


def eval_yaml_app(tt, target, lua):
    proc = tt.run("connect", target, "-f-", input=lua, timeout=30)
    assert proc.returncode == 0, proc.stdout
    return parse_yaml(proc.stdout)


def parse_yaml(out):
    marker = out.find("\n---")
    if marker != -1:
        out = out[marker + 1 :]
    elif not out.lstrip().startswith("---"):
        return None
    docs = list(yaml.safe_load_all(out))
    if not docs:
        return None
    val = docs[0]
    if isinstance(val, list) and len(val) == 1:
        return val[0]
    return val


def app_instance(tt_app, instance):
    suffix = f":{instance}"
    targets = [target for target in tt_app.instances if target.endswith(suffix)]
    assert len(targets) == 1, f"expected one {instance!r} instance, got {targets}"
    return targets[0]


def post_start_backup_app(tt_app):
    tt_helper.post_start_base(tt_app)
    assert tt_helper.wait_box_status(
        30,
        tt_app,
        tt_app.running_instances,
        ["running"],
    )


TT_BACKUP_APP = dict(
    app_path="test_app",
    app_name="app",
    instances=["storage-001-a"],
    running_targets=["app"],
    post_start=post_start_backup_app,
)


def start_backup(tt, target, backup_id, from_vclock=None):
    args = ["backup", "start", target, "--backup-id", backup_id]
    if from_vclock is not None:
        args.extend(["--from-vclock", json.dumps({str(k): v for k, v in from_vclock.items()})])
    return tt.exec(*args)


def finalize_backup(tt, target, backup_id):
    return tt.exec("backup", "finalize", target, "--backup-id", backup_id)


def backup_last(tt, storage_uri, fmt=None):
    args = ["backup", "last", "--backup-storage", storage_uri]
    if fmt:
        args.extend(["--format", fmt])
    return tt.exec(*args)


def backup_verify(tt, storage_uri, fmt=None, timeout=None):
    args = ["backup", "verify", "--backup-storage", storage_uri]
    if fmt:
        args.extend(["--format", fmt])
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def archive_path_from_output(output):
    paths = [line.strip() for line in output.splitlines() if line.strip().endswith(".tar.zst")]
    assert len(paths) == 1, f"expected one archive path in output, got {paths}:\n{output}"
    assert os.path.isabs(paths[0]), f"archive path must be absolute: {paths[0]}"
    return paths[0]


def get_backup_info_app(tt, target):
    return eval_yaml_app(tt, target, "return box.backup.info()")


def get_instance_info_app(tt, target):
    return eval_yaml_app(
        tt,
        target,
        """
        return {
            replicaset_uuid = box.info.replicaset.uuid,
            instance_uuid   = box.info.uuid,
            instance_name   = box.info.name,
            hostname        = box.info.hostname,
        }
        """,
    )


def backup_dir(backup_id):
    return os.path.join(BACKUP_TMP_ROOT, backup_id)


def _sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as src:
        for chunk in iter(lambda: src.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _unpack_archive(archive_path, destination):
    os.makedirs(destination, exist_ok=True)
    result = subprocess.run(
        ["tar", "-xf", archive_path, "-C", destination],
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, (
        f"failed to unpack {archive_path}:\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
    )

    extracted = []
    for root, dirs, files in os.walk(destination):
        assert not dirs, f"backup archive is not flat: {dirs} in {root}"
        for name in files:
            path = os.path.join(root, name)
            assert os.path.isfile(path), f"archive entry is not a regular file: {path}"
            assert os.path.getsize(path) > 0, f"archive contains an empty file: {path}"
            extracted.append(os.path.relpath(path, destination))
    return sorted(extracted)


# Fields that cannot be made deterministic in a test environment:
#   hostname         — the OS hostname of the machine running the test.
#   timestamp        — the wall-clock time of a recovery point.
#   checksum_sha256  — the archive checksum.
# Everything else is deterministic: UUIDs are pinned in the test app config,
# vclock/LSNs are fixed by the deterministic operation sequence.
_VOLATILE_FRAGMENT_KEYS = {"hostname", "checksum_sha256"}


def assert_fragment_matches_golden(fragment, golden_path):
    with open(golden_path, "r") as src:
        golden = json.load(src)

    actual = {k: v for k, v in fragment.items() if k not in _VOLATILE_FRAGMENT_KEYS}
    for rp in actual["recovery_points"]:
        rp.pop("timestamp", None)

    assert actual == golden


def inspect_backup_artifact(archive_path, unpack_dir, backup_id):
    expected_dir = backup_dir(backup_id)
    assert os.path.dirname(archive_path) == expected_dir
    assert os.path.isfile(archive_path), f"archive not found: {archive_path}"

    fragment_path = archive_path.removesuffix(".tar.zst") + ".json"
    assert os.path.isfile(fragment_path), f"manifest fragment not found: {fragment_path}"
    with open(fragment_path, "r") as src:
        fragment = json.load(src)

    # The replicaset_uuid in the fragment must match the archive file name.
    assert os.path.basename(archive_path) == (
        f"{backup_id}-{fragment['replicaset_uuid']}.tar.zst"
    ), fragment

    # Checksum is the sha256 of the actual archive bytes.
    checksum = fragment["checksum_sha256"]
    assert len(checksum) == 64 and all(c in "0123456789abcdef" for c in checksum)
    assert _sha256_file(archive_path) == checksum, fragment

    # The archive must contain exactly the files listed in the fragment.
    assert fragment["files"], fragment
    assert len(fragment["files"]) == len(set(fragment["files"])), fragment
    assert all(os.path.basename(name) == name for name in fragment["files"]), fragment

    extracted_files = _unpack_archive(archive_path, unpack_dir)
    assert extracted_files == sorted(fragment["files"]), fragment

    return fragment


def write_manifest_to_storage(storage_dir, manifest):
    manifests_dir = os.path.join(storage_dir, "manifests")
    os.makedirs(manifests_dir, exist_ok=True)
    key = f"{manifest['backup_id']}.json"
    path = os.path.join(manifests_dir, key)
    with open(path, "w") as f:
        json.dump(manifest, f)
