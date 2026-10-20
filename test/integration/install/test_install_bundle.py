import io
import os
import platform
import re
import subprocess
import tarfile

import pytest

from utils import create_tt_config, run_command_and_get_output

EE_VERSION = "gc64-3.4.0-0-r60"
TCM_VERSION = "1.3.1-0-g074b5ffa"

VERSION_SELECTION_BUNDLES = {
    "tarantool-ee": [
        "gc64-2.11.1-0-r8",
        "gc64-2.11.1-0-r9",
        "nogc64-2.11.1-0-r10",
        "gc64-3.4.0-0-r9",
        "gc64-3.4.0-0-r60",
        "gc64-3.4.1-0-r61",
        "gc64-3.8.1-0-r70",
        "gc64-3.9.0-rc2-0-r80",
        "nogc64-3.9.1-0-r90",
        "debug-gc64-3.9.2-0-r99",
        "gc64-4.0.0-0-r1",
        "gc64-4.1.0-rc1-0-r2",
        "nogc64-4.2.0-0-r3",
        "debug-gc64-4.3.0-0-r4",
    ],
    "tcm": [
        "1.3.0-0-g3857712a",
        "1.3.0-2-g074b5ffa",
        "1.3.1-0-g074b5ffa",
        "1.4.0-0-g074b5ffa",
        "1.5.0-rc2-0-g074b5ffa",
        "2.0.0-0-g074b5ffa",
        "3.0.0-rc1-0-g074b5ffa",
    ],
}


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
    if program == "tarantool-ee":
        program = "tarantool"
        if version and version[0].isdigit():
            version += "ee"
    version_args = [] if version is None else [version]
    return run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            program,
            *version_args,
            "--force",
            "--local-dir",
            str(repository),
            *flags,
        ],
        cwd=tmp_path,
    )


def assert_active(root, program, version):
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    if program == "tarantool-ee":
        match = re.match(r"(.*?)(\d+\.\d+\.\d+(?:-(?:entrypoint|alpha|beta|rc)\d*)?)", version)
        build, number = match.groups()
        if int(number.split(".")[0]) >= 3:
            build = build.replace("nogc64-", "").replace("gc64-", "")
        version = build + number
    destination = root / "versions" / program / f"{program}_{version}"
    binary = destination / executable
    assert os.access(binary, os.X_OK)
    assert not (destination / "bin").exists()
    assert (root / "bin" / executable).readlink() == binary
    if program == "tarantool-ee":
        assert (root / "include" / "tarantool").readlink() == destination / "include" / "tarantool"
        assert (root / "include" / "tarantool" / "module.h").read_text()
    return destination


@pytest.mark.parametrize(
    "program,requested,expected",
    [
        ("tarantool-ee", "4", "gc64-4.0.0-0-r1"),
        ("tarantool-ee", "3", "gc64-3.8.1-0-r70"),
        ("tarantool-ee", "3.4", "gc64-3.4.1-0-r61"),
        ("tarantool-ee", "3.4.0", "gc64-3.4.0-0-r60"),
        ("tarantool-ee", "gc64-2.11.1", "gc64-2.11.1-0-r9"),
        ("tarantool-ee", "nogc64-2.11.1", "nogc64-2.11.1-0-r10"),
        ("tarantool-ee", "3.9.0-rc2", "gc64-3.9.0-rc2-0-r80"),
        ("tarantool-ee", "debug-3.9.2", "debug-gc64-3.9.2-0-r99"),
        ("tarantool-ee", "gc64-3.4.0-0-r9", "gc64-3.4.0-0-r9"),
        ("tarantool-ee", "gc64-3.9.0-rc2-0-r80", "gc64-3.9.0-rc2-0-r80"),
        ("tarantool-ee", "nogc64-3.9.1-0-r90", "nogc64-3.9.1-0-r90"),
        ("tcm", None, "2.0.0-0-g074b5ffa"),
        ("tcm", "", "2.0.0-0-g074b5ffa"),
        ("tcm", "1", "1.4.0-0-g074b5ffa"),
        ("tcm", "1.3", "1.3.1-0-g074b5ffa"),
        ("tcm", "1.3.0", "1.3.0-2-g074b5ffa"),
        ("tcm", "1.3.0-0-g3857712a", "1.3.0-0-g3857712a"),
        ("tcm", "1.5.0-rc2-0-g074b5ffa", "1.5.0-rc2-0-g074b5ffa"),
    ],
)
def test_bundle_version_selection(tt_cmd, tmp_path, program, requested, expected):
    for version in VERSION_SELECTION_BUNDLES[program]:
        make_bundle(tmp_path, program, version, content=version)

    rc, output = install(tt_cmd, tmp_path, program, requested)
    assert rc == 0, output
    root = scope_root(tmp_path)
    assert_active(root, program, expected)
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    rc, output = run_command_and_get_output([root / "bin" / executable], cwd=tmp_path)
    assert rc == 0, output
    assert output.strip() == expected
    if program == "tarantool-ee":
        assert (root / "include" / "tarantool" / "module.h").read_text() == expected


@pytest.mark.parametrize("program", ["tarantool-ee", "tcm"])
def test_bundle_search_keeps_full_versions(tt_cmd, tmp_path, program):
    versions = VERSION_SELECTION_BUNDLES[program]
    for version in versions:
        make_bundle(tmp_path, program, version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "search", program, "--local-dir", "local bundles"],
        cwd=tmp_path,
        stderr=None,
    )
    assert rc == 0, output
    assert output.splitlines() == versions


@pytest.mark.parametrize(
    "program,full_version,numeric_version,other_version",
    [
        ("tarantool-ee", EE_VERSION, "3.4.0", "gc64-4.0.0-0-r1"),
        ("tcm", TCM_VERSION, "1.3.1", "2.0.0-0-g074b5ffa"),
    ],
)
@pytest.mark.parametrize("components", [1, 2, 3])
def test_bundle_switch_and_uninstall_numeric_version(
    tt_cmd,
    tmp_path,
    program,
    full_version,
    numeric_version,
    other_version,
    components,
):
    for version in (full_version, other_version):
        make_bundle(tmp_path, program, version)
    requested = ".".join(numeric_version.split(".")[:components])
    rc, output = install(tt_cmd, tmp_path, program, requested)
    assert rc == 0, output
    root = scope_root(tmp_path)
    selected = assert_active(root, program, full_version)
    rc, output = install(tt_cmd, tmp_path, program, other_version)
    assert rc == 0, output
    assert_active(root, program, other_version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "switch", program, numeric_version],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert_active(root, program, full_version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "uninstall", program, numeric_version],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert not selected.exists()
    assert_active(root, program, other_version)


@pytest.mark.parametrize("operation", ["switch", "uninstall"])
@pytest.mark.parametrize("components", [1, 2])
@pytest.mark.parametrize(
    "program,full_version,numeric_version",
    [("tarantool-ee", EE_VERSION, "3.4.0"), ("tcm", TCM_VERSION, "1.3.1")],
)
def test_bundle_short_version_preserves_sole_installation(
    tt_cmd,
    tmp_path,
    operation,
    components,
    program,
    full_version,
    numeric_version,
):
    make_bundle(tmp_path, program, full_version)
    requested = ".".join(numeric_version.split(".")[:components])
    rc, output = install(tt_cmd, tmp_path, program, requested)
    assert rc == 0, output
    root = scope_root(tmp_path)
    selected = assert_active(root, program, full_version)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", operation, program, requested],
        cwd=tmp_path,
    )
    assert rc != 0, output
    assert "not installed" in output
    assert list((root / "versions" / program).iterdir()) == [selected]
    assert_active(root, program, full_version)


@pytest.mark.parametrize("operation", ["switch", "uninstall"])
@pytest.mark.parametrize(
    "program,first,second,requested",
    [
        ("tarantool-ee", "gc64-2.11.1-0-r60", "nogc64-2.11.1-0-r60", "2.11.1"),
        ("tcm", TCM_VERSION, "1.3.1-1-g3857712a", "1.3.1"),
        ("tcm", TCM_VERSION, "1.3.1-rc1-0-g074b5ffa", "1.3.1"),
    ],
)
def test_bundle_ambiguous_version_preserves_installations(
    tt_cmd,
    tmp_path,
    operation,
    program,
    first,
    second,
    requested,
):
    root = scope_root(tmp_path)
    installations = []
    for version in (first, second):
        make_bundle(tmp_path, program, version, content=version)
        rc, output = install(tt_cmd, tmp_path, program, version)
        assert rc == 0, output
        installations.append(assert_active(root, program, version))
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    link_dirs = ("bin", "include") if program == "tarantool-ee" else ("bin",)
    links_before = [(root / directory / executable).readlink() for directory in link_dirs]

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", operation, program, requested],
        cwd=tmp_path,
    )
    assert rc != 0, output
    assert "more than one installed version matches" in output
    assert installations[0].name.removeprefix(f"{program}_") in output
    assert installations[1].name.removeprefix(f"{program}_") in output
    assert all(path.is_dir() for path in installations)
    assert [(root / directory / executable).readlink() for directory in link_dirs] == links_before
    assert_active(root, program, second)

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", operation, program, first],
        cwd=tmp_path,
    )
    assert rc == 0, output
    if operation == "switch":
        assert_active(root, program, first)
    else:
        assert not installations[0].exists()
        assert_active(root, program, second)


@pytest.mark.parametrize("program,version", [("tarantool-ee", EE_VERSION), ("tcm", TCM_VERSION)])
@pytest.mark.parametrize("scope", [None, "project", "user"])
@pytest.mark.parametrize("absolute", [False, True])
def test_bundle_scope(tt_cmd, tmp_path, program, version, scope, absolute):
    make_bundle(tmp_path, program, version)
    repository = tmp_path / "local bundles" if absolute else "local bundles"
    rc, output = install(tt_cmd, tmp_path, program, version, scope, repository=repository)
    assert rc == 0, output
    assert "to PATH to use this as default" not in output
    root = scope_root(tmp_path, scope)
    destination = assert_active(root, program, version)
    if program == "tcm":
        assert not (root / "include").exists()
        assert not (destination / "include").exists()
    if scope == "user":
        assert not scope_root(tmp_path).exists()
        assert not (tmp_path / ".env").exists()
        assert "source .env/activate.sh" not in output
    else:
        assert (tmp_path / ".env" / "activate.sh").is_file()
        assert (tmp_path / ".env" / "deactivate.sh").is_file()
        assert output.count("source .env/activate.sh") == 1
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
    assert (
        scope_root(tmp_path) / "include" / "tarantool" / "module.h"
    ).read_text() == "other source"
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
    for directory in ["bin", "include"] if program == "tarantool-ee" else ["bin"]:
        link = root / directory / executable
        link.unlink()
        if dangling:
            link.symlink_to("missing")
    archive.write_bytes(b"invalid archive")
    rc, output = install(tt_cmd, tmp_path, program, version)
    assert rc == 0, output
    assert "already exists" in output
    assert_active(root, program, version)


@pytest.mark.parametrize(
    "first,other,identity",
    [
        (EE_VERSION, "gc64-3.4.0-0-r61", "3.4.0"),
        (EE_VERSION, "nogc64-3.4.0-0-r61", "3.4.0"),
        ("gc64-2.11.1-0-r60", "gc64-2.11.1-0-r61", "gc64-2.11.1"),
        ("nogc64-2.11.1-0-r60", "nogc64-2.11.1-0-r61", "nogc64-2.11.1"),
        ("gc64-3.4.0-rc1-0-r60", "gc64-3.4.0-rc1-0-r61", "3.4.0-rc1"),
        ("debug-gc64-3.4.0-0-r60", "debug-gc64-3.4.0-0-r61", "debug-3.4.0"),
    ],
)
def test_ee_revision_identity_scopes_and_reinstall(tt_cmd, tmp_path, first, other, identity):
    make_bundle(tmp_path, version=first)
    make_bundle(tmp_path, version=other, content="other revision")
    for version, scope in [(first, None), (other, None), (other, "user")]:
        rc, output = install(tt_cmd, tmp_path, version=version, scope=scope)
        assert rc == 0, output
        if version == other and scope is None:
            assert "already exists" in output
            assert "Unpacking archive" not in output
    root = scope_root(tmp_path)
    selected = root / "versions" / "tarantool-ee" / f"tarantool-ee_{identity}"
    assert list(selected.parent.iterdir()) == [selected]
    assert "original" in (root / "bin" / "tarantool").read_text()
    assert (root / "include" / "tarantool" / "module.h").read_text() == "original"
    (selected / "include" / "tarantool" / "stale.h").write_text("stale")
    make_bundle(tmp_path, version=other, content="replacement")
    rc, output = install(tt_cmd, tmp_path, version=other, reinstall=True)
    assert rc == 0, output
    assert_active(root, "tarantool-ee", other)
    assert list(selected.parent.iterdir()) == [selected]
    assert "replacement" in (root / "bin" / "tarantool").read_text()
    assert not (selected / "include" / "tarantool" / "stale.h").exists()
    assert (root / "include" / "tarantool" / "module.h").read_text() == "replacement"
    assert (
        scope_root(tmp_path, "user") / "include" / "tarantool" / "module.h"
    ).read_text() == "other revision"


@pytest.mark.parametrize("operation", ["switch", "uninstall"])
@pytest.mark.parametrize("full_request", [False, True])
def test_ee_canonical_stable_version_with_prerelease_and_debug(
    tt_cmd,
    tmp_path,
    operation,
    full_request,
):
    root = scope_root(tmp_path)
    for version in [EE_VERSION, "gc64-3.4.0-rc1-0-r60", "debug-gc64-3.4.0-0-r60"]:
        make_bundle(tmp_path, version=version)
        rc, output = install(tt_cmd, tmp_path, version=version)
        assert rc == 0, output
        assert_active(root, "tarantool-ee", version)

    rc, output = run_command_and_get_output([tt_cmd, "--self", "binary", "list"], cwd=tmp_path)
    assert rc == 0, output
    for identity in ["3.4.0", "3.4.0-rc1", "debug-3.4.0"]:
        assert identity in output
    assert EE_VERSION not in output

    requested = "gc64-3.4.0-0-r61" if full_request else "3.4.0"
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", operation, "tarantool-ee", requested],
        cwd=tmp_path,
    )
    assert rc == 0, output
    if operation == "switch":
        assert_active(root, "tarantool-ee", EE_VERSION)
    else:
        assert not (root / "versions" / "tarantool-ee" / "tarantool-ee_3.4.0").exists()
        assert_active(root, "tarantool-ee", "debug-gc64-3.4.0-0-r60")


@pytest.mark.parametrize(
    "program,version,failure",
    [("tarantool-ee", EE_VERSION, failure) for failure in ("corrupt", "binary", "headers")]
    + [("tcm", TCM_VERSION, failure) for failure in ("corrupt", "binary")],
)
@pytest.mark.parametrize("reinstall", [False, True])
def test_bundle_invalid_archive_preserves_destination(
    tt_cmd,
    tmp_path,
    program,
    version,
    failure,
    reinstall,
):
    root = scope_root(tmp_path)
    executable = "tarantool" if program == "tarantool-ee" else "tcm"
    directories = ["bin", "include"] if program == "tarantool-ee" else ["bin"]
    if reinstall:
        make_bundle(tmp_path, program, version)
        rc, output = install(tt_cmd, tmp_path, program, version)
        assert rc == 0, output
        binary_before = (root / "bin" / executable).read_bytes()
        links_before = [(root / directory / executable).readlink() for directory in directories]
    archive = make_bundle(tmp_path, program, version, missing=failure)
    if failure == "corrupt":
        archive.write_bytes(b"invalid archive")
    rc, output = install(tt_cmd, tmp_path, program, version, reinstall=reinstall)
    assert rc != 0, output
    if reinstall:
        assert (root / "bin" / executable).read_bytes() == binary_before
        if program == "tarantool-ee":
            assert (root / "include/tarantool/module.h").read_text() == "original"
        assert [
            (root / directory / executable).readlink() for directory in directories
        ] == links_before
    else:
        assert not root.exists()
        assert not (tmp_path / ".env").exists()


def test_tcm_preserves_active_tarantool_headers(tt_cmd, tmp_path):
    make_bundle(tmp_path)
    rc, output = install(tt_cmd, tmp_path)
    assert rc == 0, output
    root = scope_root(tmp_path)
    links = [(root / directory / "tarantool").readlink() for directory in ["bin", "include"]]
    make_bundle(tmp_path, "tcm", TCM_VERSION)
    rc, output = install(tt_cmd, tmp_path, "tcm", TCM_VERSION)
    assert rc == 0, output
    destination = assert_active(root, "tcm", TCM_VERSION)
    assert not (destination / "include").exists()
    assert [
        (root / directory / "tarantool").readlink() for directory in ["bin", "include"]
    ] == links


def test_ee_switches_both_ce_active_links(tt_cmd, tmp_path):
    root = scope_root(tmp_path)
    ce = root / "versions" / "tarantool" / "tarantool_3.2.1"
    binary = ce / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1'\n")
    binary.chmod(0o755)
    headers = ce / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "module.h").write_text("CE headers")
    for directory, destination in [("bin", binary), ("include", headers)]:
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
            "--source-build",
            "3.2.1",
            "--force",
            "--local-repo",
            "local bundles",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (root / "bin" / "tarantool").readlink() == binary
    assert (root / "include" / "tarantool").readlink() == headers


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
    assert not (root / "include").exists()


@pytest.mark.parametrize("requested", ["3ee", "3.4ee", "3.4.0ee"])
@pytest.mark.parametrize("extra", [[], ["--dev"], ["--verbose"]])
def test_public_tarantool_enterprise_selector(tt_cmd, tmp_path, requested, extra):
    make_bundle(tmp_path)
    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "--scope",
            "project",
            "install",
            "tarantool",
            requested,
            "--local-dir",
            "local bundles",
            *extra,
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert_active(scope_root(tmp_path), "tarantool-ee", EE_VERSION)
    assert not (scope_root(tmp_path) / "versions/tarantool").exists()
