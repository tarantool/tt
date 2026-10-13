import io
import os
import platform
import subprocess
import tarfile

import pytest

from utils import create_tt_config, run_command_and_get_output

EE_VERSION = "gc64-3.4.0-0-r60"
TCM_VERSION = "1.3.1-0-g074b5ffa"


@pytest.fixture(autouse=True)
def scope_environment(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "user data"))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)


def scope_root(tmp_path, scope=None):
    if scope == "user":
        if platform.system() == "Darwin":
            return tmp_path / "home" / "Library" / "Application Support" / "tarantool"
        return tmp_path / "user data" / "tarantool"
    return tmp_path / ".tarantool"


def make_bundle(
    tmp_path,
    program="tarantool-ee",
    version=EE_VERSION,
    content="original",
    missing=None,
    repository="local bundles",
):
    directory = tmp_path / repository
    directory.mkdir(exist_ok=True)
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    prefix = "tarantool-enterprise/" if program == "tarantool-ee" else ""
    name = (
        f"tarantool-enterprise-sdk-{version}.linux.x86_64.tar.gz"
        if prefix
        else (f"tcm-{version}.linux.amd64.tar.gz")
    )
    archive = directory / name
    with tarfile.open(archive, "w:gz") as bundle:
        files = {executable: f"#!/bin/sh\necho '{content}'\n"}
        if prefix:
            files["include/tarantool/module.h"] = content
        for filename, data in files.items():
            if missing == "binary" and filename == executable:
                continue
            if missing == "headers" and filename.startswith("include/"):
                continue
            entry = tarfile.TarInfo(prefix + filename)
            encoded = data.encode()
            entry.size = len(encoded)
            entry.mode = 0o755 if filename == executable else 0o644
            bundle.addfile(entry, io.BytesIO(encoded))
    return archive


def install(
    tt_cmd,
    tmp_path,
    program="tarantool-ee",
    version=EE_VERSION,
    scope=None,
    reinstall=False,
    repository="local bundles",
):
    flags = [] if scope is None else ["--scope", scope]
    if reinstall:
        flags.append("--reinstall")
    return run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            program,
            version,
            "--force",
            "--local-repo",
            str(repository),
            *flags,
        ],
        cwd=tmp_path,
    )


def assert_active(root, program, version):
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    destination = root / "versions" / program / f"{program}_{version}"
    binary = destination / executable
    assert os.access(binary, os.X_OK)
    assert not (destination / "bin").exists()
    assert (root / "bin" / executable).readlink() == binary
    if program == "tarantool-ee":
        assert (root / "inc" / "tarantool").readlink() == destination / "inc" / "tarantool"
        assert (root / "inc" / "tarantool" / "module.h").read_text()
    return destination


@pytest.mark.parametrize("program,version", [("tarantool-ee", EE_VERSION), ("tcm", TCM_VERSION)])
@pytest.mark.parametrize("scope", [None, "project", "user"])
@pytest.mark.parametrize("absolute", [False, True])
def test_bundle_scope(tt_cmd, tmp_path, program, version, scope, absolute):
    make_bundle(tmp_path, program, version)
    repository = tmp_path / "local bundles" if absolute else "local bundles"
    rc, output = install(tt_cmd, tmp_path, program, version, scope, repository=repository)
    assert rc == 0, output
    root = scope_root(tmp_path, scope)
    destination = assert_active(root, program, version)
    if program == "tcm":
        assert not (root / "inc").exists()
        assert not (destination / "inc").exists()
    if scope == "user":
        assert not scope_root(tmp_path).exists()
    assert not (tmp_path / "tt.yaml").exists()


def test_bundle_ignores_config_sources_and_destinations(tt_cmd, tmp_path, monkeypatch):
    config_dir = tmp_path / "environment"
    config = create_tt_config(
        config_dir,
        {
            "env": {"bin_dir": "legacy", "inc_dir": "headers"},
            "repo": {"distfiles": "custom bundles"},
        },
    )
    monkeypatch.setenv("TT_CLI_CFG", str(config))
    make_bundle(config_dir, repository="custom bundles", content="configured source")
    make_bundle(tmp_path, content="other source")
    rc, output = install(tt_cmd, tmp_path)
    assert rc == 0, output
    assert_active(scope_root(tmp_path), "tarantool-ee", EE_VERSION)
    assert (scope_root(tmp_path) / "inc" / "tarantool" / "module.h").read_text() == "other source"
    assert not (config_dir / "legacy").exists()
    assert not (config_dir / "headers").exists()
    assert not (tmp_path / "tt.yaml").exists()


@pytest.mark.parametrize("dangling", [False, True])
@pytest.mark.parametrize("program,version", [("tarantool-ee", EE_VERSION), ("tcm", TCM_VERSION)])
def test_bundle_existing_version_reactivates_without_extraction(
    tt_cmd,
    tmp_path,
    dangling,
    program,
    version,
):
    archive = make_bundle(tmp_path, program, version)
    rc, output = install(tt_cmd, tmp_path, program, version)
    assert rc == 0, output
    root = scope_root(tmp_path)
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    for directory in ["bin", "inc"] if program == "tarantool-ee" else ["bin"]:
        link = root / directory / executable
        link.unlink()
        if dangling:
            link.symlink_to("missing")
    archive.write_bytes(b"invalid archive")
    rc, output = install(tt_cmd, tmp_path, program, version)
    assert rc == 0, output
    assert "already exists" in output
    assert_active(root, program, version)


def test_bundle_versions_scopes_and_reinstall(tt_cmd, tmp_path):
    other = "gc64-3.4.0-0-r61"
    make_bundle(tmp_path)
    make_bundle(tmp_path, version=other, content="other version")
    for version, scope in [(EE_VERSION, None), (other, None), (EE_VERSION, "user")]:
        rc, output = install(tt_cmd, tmp_path, version=version, scope=scope)
        assert rc == 0, output
    root = scope_root(tmp_path)
    selected = root / "versions" / "tarantool-ee" / f"tarantool-ee_{EE_VERSION}"
    (selected / "inc" / "tarantool" / "stale.h").write_text("stale")
    make_bundle(tmp_path, content="replacement")
    rc, output = install(tt_cmd, tmp_path, reinstall=True)
    assert rc == 0, output
    assert_active(root, "tarantool-ee", EE_VERSION)
    assert not (selected / "inc" / "tarantool" / "stale.h").exists()
    assert (root / "inc" / "tarantool" / "module.h").read_text() == "replacement"
    other_root = root / "versions" / "tarantool-ee" / f"tarantool-ee_{other}"
    assert (other_root / "inc" / "tarantool" / "module.h").read_text() == "other version"
    assert (
        scope_root(tmp_path, "user") / "inc" / "tarantool" / "module.h"
    ).read_text() == "original"


@pytest.mark.parametrize("failure", ["corrupt", "binary", "headers"])
@pytest.mark.parametrize("reinstall", [False, True])
def test_bundle_invalid_archive_preserves_destination(tt_cmd, tmp_path, failure, reinstall):
    root = scope_root(tmp_path)
    if reinstall:
        make_bundle(tmp_path)
        rc, output = install(tt_cmd, tmp_path)
        assert rc == 0, output
        binary_before = (root / "bin" / "tarantool").read_bytes()
        links_before = [(root / directory / "tarantool").readlink() for directory in ["bin", "inc"]]
    archive = make_bundle(tmp_path, missing=failure)
    if failure == "corrupt":
        archive.write_bytes(b"invalid archive")
    rc, output = install(tt_cmd, tmp_path, reinstall=reinstall)
    assert rc != 0, output
    if reinstall:
        assert (root / "bin" / "tarantool").read_bytes() == binary_before
        assert (root / "inc" / "tarantool" / "module.h").read_text() == "original"
        assert [
            (root / directory / "tarantool").readlink() for directory in ["bin", "inc"]
        ] == links_before
    else:
        assert not root.exists()


def test_tcm_preserves_active_tarantool_headers(tt_cmd, tmp_path):
    make_bundle(tmp_path)
    rc, output = install(tt_cmd, tmp_path)
    assert rc == 0, output
    root = scope_root(tmp_path)
    links = [(root / directory / "tarantool").readlink() for directory in ["bin", "inc"]]
    make_bundle(tmp_path, "tcm", TCM_VERSION)
    rc, output = install(tt_cmd, tmp_path, "tcm", TCM_VERSION)
    assert rc == 0, output
    destination = assert_active(root, "tcm", TCM_VERSION)
    assert not (destination / "inc").exists()
    assert [(root / directory / "tarantool").readlink() for directory in ["bin", "inc"]] == links


def test_ee_switches_both_ce_active_links(tt_cmd, tmp_path):
    root = scope_root(tmp_path)
    ce = root / "versions" / "tarantool" / "tarantool_3.2.1"
    binary = ce / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1'\n")
    binary.chmod(0o755)
    headers = ce / "inc" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("CE headers")
    for directory, destination in [("bin", binary), ("inc", headers)]:
        (root / directory).mkdir()
        (root / directory / "tarantool").symlink_to(destination)
    make_bundle(tmp_path)
    rc, output = install(tt_cmd, tmp_path)
    assert rc == 0, output
    assert_active(root, "tarantool-ee", EE_VERSION)
    assert binary.read_text() == "#!/bin/sh\necho 'Tarantool 3.2.1'\n"
    assert (headers / "module.h").read_text() == "CE headers"
    repository = tmp_path / "local bundles" / "tarantool"
    repository.mkdir()
    (repository / "README").write_text("CE source")
    for args in [
        ["init", "--initial-branch=master"],
        ["add", "README"],
        ["-c", "user.name=Scope Test", "-c", "user.email=scope@example.com", "commit", "-m", "ce"],
        ["tag", "3.2.1"],
    ]:
        subprocess.run(["git", *args], cwd=repository, check=True, capture_output=True)
    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tarantool",
            "3.2.1",
            "--force",
            "--local-repo",
            "local bundles",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (root / "bin" / "tarantool").readlink() == binary
    assert (root / "inc" / "tarantool").readlink() == headers


def test_tcm_reinstall_replaces_only_selected_version(tt_cmd, tmp_path):
    other = "1.3.2-0-g074b5ffa"
    for version in [TCM_VERSION, other]:
        make_bundle(tmp_path, "tcm", version)
        rc, output = install(tt_cmd, tmp_path, "tcm", version)
        assert rc == 0, output
    make_bundle(tmp_path, "tcm", TCM_VERSION, content="replacement")
    rc, output = install(tt_cmd, tmp_path, "tcm", TCM_VERSION, reinstall=True)
    assert rc == 0, output
    root = scope_root(tmp_path)
    assert_active(root, "tcm", TCM_VERSION)
    assert "replacement" in (root / "bin" / "tcm").read_text()
    other_binary = root / "versions" / "tcm" / f"tcm_{other}" / "tcm"
    assert "original" in other_binary.read_text()
    assert not (root / "inc").exists()
