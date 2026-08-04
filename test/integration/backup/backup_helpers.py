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


def _tt_binary_and_cwd(tt):
    """The binary and the cwd this tt object's own exec() would use.

    tt_helper.Tt name-mangles both attributes and publishes only work_dir;
    sharded_helpers.ShardedApp keeps them as tt_cmd/env_dir. Reading them here
    is what lets exec_split accept exactly the objects tt.exec accepts.
    """
    if hasattr(tt, "tt_cmd"):
        return tt.tt_cmd, tt.env_dir
    return tt._Tt__tt_cmd, tt.work_dir


def exec_split(tt, *args, **kwargs):
    """Run tt with stdout and stderr on separate pipes: (rc, stdout, stderr).

    tt.exec merges the two streams (stderr=subprocess.STDOUT), so a test built
    on it cannot tell which stream carried a report - and `tt backup ... --format
    json | jq` only works if the document is on stdout by itself. Argument
    filtering and cwd match tt.exec, so this is a drop-in wherever the split is
    the property under test.

    Keyword arguments go to subprocess.run and override the defaults, exactly as
    tt.exec handles them - including env, which replaces the environment instead
    of extending it, so pass dict(os.environ, VAR=...).
    """
    binary, cwd = _tt_binary_and_cwd(tt)
    cmd = [binary, *(arg for arg in args if arg is not None)]
    run_kwargs = dict(cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    run_kwargs.update(kwargs)
    proc = subprocess.run(cmd, **run_kwargs)

    # Same reason as utils.run_command_and_get_output: keeps `pytest -s` useful.
    print(proc.stdout)
    print(proc.stderr)

    return proc.returncode, proc.stdout, proc.stderr


def start_backup(tt, target, backup_id, from_vclock=None, ttl=None, config=None, **kwargs):
    """Run tt backup start.

    backup_id reaches the command line verbatim: "", "../escape" and
    "2026/08/02-full" are passed through unsanitised, because rejecting them is
    the command's job, not the helper's. Pass None to leave --backup-id off the
    command line entirely. Extra keyword arguments go to tt.exec (cwd, env,
    input); note that env there replaces the environment rather than extending
    it, so pass dict(os.environ, VAR=...).
    """
    args = ["backup", "start", target]
    if backup_id is not None:
        args.extend(["--backup-id", backup_id])
    if from_vclock is not None:
        args.extend(["--from-vclock", json.dumps({str(k): v for k, v in from_vclock.items()})])
    if ttl is not None:
        args.extend(["--ttl", ttl])
    if config is not None:
        args.extend(["-c", str(config)])
    return tt.exec(*args, **kwargs)


def finalize_backup(tt, target, backup_id, config=None, **kwargs):
    """Run tt backup finalize. Same backup_id and kwargs contract as start_backup."""
    args = ["backup", "finalize", target]
    if backup_id is not None:
        args.extend(["--backup-id", backup_id])
    if config is not None:
        args.extend(["-c", str(config)])
    return tt.exec(*args, **kwargs)


def scope_args(cluster_name=None, environment=None):
    """The --cluster-name / --environment pair every storage command takes."""
    args = []
    if cluster_name:
        args.extend(["--cluster-name", cluster_name])
    if environment:
        args.extend(["--environment", environment])
    return args


def backup_last(tt, storage_uri, fmt=None, timeout=None, cluster_name=None, environment=None):
    args = ["backup", "last", "--backup-storage", storage_uri]
    args.extend(scope_args(cluster_name, environment))
    if fmt:
        args.extend(["--format", fmt])
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def backup_verify(tt, storage_uri, fmt=None, timeout=None, cluster_name=None, environment=None):
    args = ["backup", "verify", "--backup-storage", storage_uri]
    args.extend(scope_args(cluster_name, environment))
    if fmt:
        args.extend(["--format", fmt])
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def backup_gc(
    tt,
    storage_uri,
    keep_full=None,
    keep_days=None,
    orphan_age=None,
    dry_run=False,
    fmt=None,
    timeout=None,
    cluster_name=None,
    environment=None,
):
    args = ["backup", "gc", "--backup-storage", storage_uri]
    args.extend(scope_args(cluster_name, environment))
    if keep_full is not None:
        args.extend(["--keep-full", str(keep_full)])
    if keep_days is not None:
        args.extend(["--keep-days", str(keep_days)])
    if orphan_age is not None:
        args.extend(["--orphan-age", orphan_age])
    if dry_run:
        args.append("--dry-run")
    if fmt:
        args.extend(["--format", fmt])
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def backup_upload(
    tt,
    storage_uri,
    archives,
    fragments=None,
    plan=None,
    backup_id=None,
    cluster_name=None,
    environment=None,
    keep_local=False,
    timeout=None,
):
    args = ["backup", "upload", "--backup-storage", storage_uri]
    args.extend(scope_args(cluster_name, environment))
    if archives:
        args.extend(["--archives", archives])
    if fragments:
        args.extend(["--fragments", fragments])
    if plan:
        args.extend(["--plan", plan])
    if backup_id:
        args.extend(["--backup-id", backup_id])
    if keep_local:
        args.append("--keep-local")
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def backup_plan(
    tt,
    target,
    storage_uri,
    config=None,
    fmt=None,
    timeout=None,
    cluster_name=None,
    environment=None,
):
    args = ["backup", "plan", "--target", target, "--backup-storage", storage_uri]
    args.extend(scope_args(cluster_name, environment))
    if config:
        args.extend(["-c", config])
    if fmt:
        args.extend(["--format", fmt])
    if timeout:
        args.extend(["--timeout", timeout])
    return tt.exec(*args)


def restore_plan(
    tt,
    storage_uri,
    target_time,
    dest,
    config=None,
    fmt="json",
    cluster_name=None,
    environment=None,
):
    args = [
        "restore",
        "plan",
        "--target-time",
        target_time,
        "--backup-storage",
        storage_uri,
        "-d",
        str(dest),
    ]
    args.extend(scope_args(cluster_name, environment))
    if config is not None:
        args.extend(["-c", str(config)])
    if fmt is not None:
        args.extend(["--format", fmt])
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
    for root, _dirs, files in os.walk(destination):
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

    # The archive must contain exactly the files listed in the fragment. A name
    # may carry a vinyl <space_id>/<index_id>/ prefix, but never an absolute
    # path or a ".." component -- those would escape the destination on extract.
    assert fragment["files"], fragment
    assert len(fragment["files"]) == len(set(fragment["files"])), fragment
    assert all(
        not os.path.isabs(name) and ".." not in name.split("/") for name in fragment["files"]
    ), fragment

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
