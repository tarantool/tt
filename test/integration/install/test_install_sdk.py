import io
import json
import os
import platform
import shutil
import tarfile

import pytest

from utils import run_command_and_get_output

SDK_VERSION = "gc64-3.7.1-0-r93"
NEW_SDK_VERSION = "gc64-3.8.0-0-r94"
TT_EE_VERSION = "2.15.0"
NEW_TT_EE_VERSION = "2.16.0"


@pytest.fixture(autouse=True)
def scope_environment(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "user data"))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)


def sdk_root(tmp_path, scope):
    if scope == "user":
        if platform.system() == "Darwin":
            return tmp_path / "home" / "Library" / "Application Support" / "tarantool"
        return tmp_path / "user data" / "tarantool"
    return tmp_path / ".tarantool"


def make_sdk(tmp_path, version=SDK_VERSION, missing=None, content=None):
    directory = tmp_path / "local bundles"
    directory.mkdir(exist_ok=True)
    path = directory / f"tarantool-enterprise-sdk-{version}.linux.x86_64.tar.gz"
    files = {
        "tarantool": "#!/bin/sh\necho tarantool\n",
        "tcm": "#!/bin/sh\necho 1.11.0-0-g4ecac7de8\n",
        "tt": "#!/bin/sh\necho 3.0.0.abcdef0\n",
        "include/tarantool/module.h": version,
        "rocks/manifest": "repository = {}\n",
        "rocks/test-1.0.0-1.all.rock": "rock",
        "env.sh": "# The archive's environment must not be installed.\nreturn 99\n",
        "VERSION": f"TARANTOOL_EE_SDK={version}\n",
    }
    if content is not None:
        for name in ("tarantool", "tcm", "tt"):
            files[name] = f"#!/bin/sh\necho {content}\n"
        files["include/tarantool/module.h"] = content
    with tarfile.open(path, "w:gz") as archive:
        for name, content in files.items():
            if missing and (name == missing or name.startswith(missing + "/")):
                continue
            data = content.encode()
            entry = tarfile.TarInfo(f"tarantool-enterprise/{name}")
            entry.size = len(data)
            entry.mode = 0o755 if name in ("tarantool", "tcm", "tt") else 0o644
            archive.addfile(entry, io.BytesIO(data))
    return path


def binary_command(tt_cmd, tmp_path, *args, scope="project"):
    return run_command_and_get_output(
        [tt_cmd, "binary", "--scope", scope, *args],
        cwd=tmp_path,
    )


def make_tt_ee(tmp_path, version=TT_EE_VERSION, missing=False):
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        pytest.skip("Standalone tt-ee archives are available only on Linux amd64")
    directory = tmp_path / "local bundles"
    directory.mkdir(exist_ok=True)
    path = directory / f"tt-ee-{version}-linux-amd64.tar.gz"
    data = f"#!/bin/sh\necho {version}.abcdef0\n".encode()
    with tarfile.open(path, "w:gz") as archive:
        entry = tarfile.TarInfo("tt")
        entry.size = len(data)
        entry.mode = 0o755
        if not missing:
            archive.addfile(entry, io.BytesIO(data))
    return path


def install_tt_ee(tt_cmd, tmp_path, version=TT_EE_VERSION, scope="project"):
    return binary_command(
        tt_cmd,
        tmp_path,
        "install",
        "tt",
        f"{version}ee",
        "--local-dir",
        "local bundles",
        scope=scope,
    )


def install_sdk(
    tt_cmd,
    tmp_path,
    version=None,
    alias="sdk",
    scope="project",
    reinstall=False,
    dev=False,
):
    args = ["install", alias]
    if version is not None:
        args.append(version)
    args.extend(["--local-dir", "local bundles"])
    if reinstall:
        args.append("--reinstall")
    if dev:
        args.append("--dev")
    return binary_command(tt_cmd, tmp_path, *args, scope=scope)


def assert_sdk_active(root, version):
    enterprise = root / "versions" / "tarantool-ee" / f"tarantool_sdk_{version}"
    tcm = root / "versions" / "tcm" / f"tcm_sdk_{version}"
    tt = root / "versions" / "tt-ee" / f"tt_sdk_{version}"
    for link, target in {
        "bin/tarantool": enterprise / "tarantool",
        "bin/tcm": tcm / "tcm",
        "bin/tt": tt / "tt",
        "include/tarantool": enterprise / "include" / "tarantool",
    }.items():
        assert (root / link).resolve(strict=True) == target
    for directory in (root, enterprise, tcm, tt):
        assert not os.path.lexists(directory / "env.sh")
        assert not os.path.lexists(directory / "rocks")


@pytest.mark.parametrize("scope", ["project", "user"])
@pytest.mark.parametrize("uninstall_active", [False, True])
def test_sdk_dev_coexists_with_release(tt_cmd, tmp_path, scope, uninstall_active):
    make_sdk(tmp_path, content="release")
    rc, output = install_sdk(tt_cmd, tmp_path, scope=scope)
    assert rc == 0, output
    root = sdk_root(tmp_path, scope)
    assert_sdk_active(root, SDK_VERSION)

    make_sdk(tmp_path, content="development")
    dev_version = f"dev_{SDK_VERSION}"
    rc, output = install_sdk(tt_cmd, tmp_path, scope=scope, dev=True)
    assert rc == 0, output
    assert "already exists" not in output
    assert_sdk_active(root, dev_version)
    manifest = root / "versions" / "tarantool-sdk" / f"tarantool-sdk_{dev_version}" / "sdk.json"
    assert json.loads(manifest.read_text())["version"] == dev_version

    for product, executable in [("tarantool-ee", "tarantool"), ("tcm", "tcm"), ("tt-ee", "tt")]:
        directory = root / "versions" / product
        release = directory / f"{executable}_sdk_{SDK_VERSION}" / executable
        development = directory / f"{executable}_sdk_{dev_version}" / executable
        assert "release" in release.read_text()
        assert "development" in development.read_text()
        for selector, target in [
            (f"sdk-{SDK_VERSION}", release),
            (f"sdk-{dev_version}", development),
        ]:
            public_product = "tarantool" if product == "tarantool-ee" else executable
            rc, output = binary_command(
                tt_cmd,
                tmp_path,
                "switch",
                public_product,
                selector,
                scope=scope,
            )
            assert rc == 0, output
            assert (root / "bin" / executable).resolve(strict=True) == target
    assert_sdk_active(root, dev_version)

    for flags, label in [([], "installed"), (["--dev"], "active")]:
        rc, output = binary_command(
            tt_cmd,
            tmp_path,
            "search",
            "sdk",
            "--local-dir",
            "local bundles",
            *flags,
            scope=scope,
        )
        assert rc == 0, output
        selected = dev_version if flags else SDK_VERSION
        assert f"{selected} [{label}]" in output
    rc, output = binary_command(tt_cmd, tmp_path, "list", scope=scope)
    assert rc == 0, output
    assert f"{dev_version} [active]" in output
    assert f"sdk-{dev_version} [active]" in output

    make_sdk(tmp_path, content="replacement")
    rc, output = install_sdk(tt_cmd, tmp_path, scope=scope, dev=True)
    assert rc == 0, output
    assert "already exists" in output
    assert "development" in (root / "bin" / "tarantool").read_text()
    rc, output = install_sdk(tt_cmd, tmp_path, dev_version, scope=scope, reinstall=True)
    assert rc == 0, output
    assert_sdk_active(root, dev_version)
    assert "replacement" in (root / "bin" / "tarantool").read_text()

    rc, output = binary_command(tt_cmd, tmp_path, "switch", "sdk", SDK_VERSION, scope=scope)
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)
    assert "release" in (root / "bin" / "tarantool").read_text()
    if uninstall_active:
        rc, output = binary_command(tt_cmd, tmp_path, "switch", "sdk", dev_version, scope=scope)
        assert rc == 0, output
        assert_sdk_active(root, dev_version)
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "sdk", dev_version, scope=scope)
    assert rc == 0, output
    assert not manifest.parent.exists()
    assert_sdk_active(root, SDK_VERSION)
    for product, executable in [("tarantool-ee", "tarantool"), ("tcm", "tcm"), ("tt-ee", "tt")]:
        assert not (root / "versions" / product / f"{executable}_sdk_{dev_version}").exists()


@pytest.mark.parametrize("alias", ["tarantool-sdk", "sdk", "SDK"])
@pytest.mark.parametrize("scope", ["project", "user"])
def test_sdk_install_scope(tt_cmd, tmp_path, alias, scope):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path, "3.7", alias, scope)
    assert rc == 0, output
    assert "to PATH to use this as default" not in output
    root = sdk_root(tmp_path, scope)
    assert_sdk_active(root, SDK_VERSION)
    assert not (root / "versions" / "tt").exists()
    assert not (root / "versions" / "tarantool").exists()
    if scope == "project":
        assert (tmp_path / ".env" / "activate.sh").is_file()
        assert (tmp_path / ".env" / "deactivate.sh").is_file()
        assert output.count("source .env/activate.sh") == 1
    else:
        assert not (tmp_path / ".env").exists()
        assert "source .env/activate.sh" not in output

    rc, output = binary_command(tt_cmd, tmp_path, "list", scope=scope)
    assert rc == 0, output
    assert f"sdk-{SDK_VERSION} [active]" in output
    assert f"{SDK_VERSION} [active]" in output
    assert "tarantool ->" not in output


@pytest.mark.parametrize("scope", ["project", "user"])
def test_tt_ee_install_scope_and_lifecycle(tt_cmd, tmp_path, scope):
    make_sdk(tmp_path, missing="tarantool")
    make_tt_ee(tmp_path)
    rc, output = install_tt_ee(tt_cmd, tmp_path, "2.15", scope=scope)
    assert rc == 0, output
    root = sdk_root(tmp_path, scope)
    first = root / "versions" / "tt-ee" / f"tt-ee_{TT_EE_VERSION}" / "tt"
    assert (root / "bin" / "tt").resolve(strict=True) == first
    for product in ("tt", "tarantool-ee", "tcm", "tarantool-sdk"):
        assert not (root / "versions" / product).exists()
    for name in ("include", "rocks", "env.sh"):
        assert not (first.parent / name).exists()
    assert (tmp_path / ".env" / "activate.sh").is_file() == (scope == "project")

    rc, output = run_command_and_get_output([first, "version", "--commit"])
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "list", scope=scope)
    assert rc == 0, output
    assert "tt-ee:" in output
    assert f"{TT_EE_VERSION} [active]" in output
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "search",
        "tt-ee",
        "--local-dir",
        "local bundles",
        scope=scope,
    )
    assert rc == 0, output
    assert f"{TT_EE_VERSION} [active]" in output
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "search",
        "sdk",
        "--local-dir",
        "local bundles",
        scope=scope,
    )
    assert rc == 0, output
    assert "[installed]" not in output
    assert "[active]" not in output

    make_tt_ee(tmp_path, NEW_TT_EE_VERSION)
    rc, output = install_tt_ee(tt_cmd, tmp_path, NEW_TT_EE_VERSION, scope=scope)
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tt-ee", TT_EE_VERSION, scope=scope)
    assert rc == 0, output
    assert (root / "bin" / "tt").resolve(strict=True) == first

    ordinary = root / "versions" / "tt" / "tt_v4.0.0" / "tt"
    ordinary.parent.mkdir(parents=True)
    ordinary.write_text("#!/bin/sh\necho 4.0.0.abcdef0\n")
    ordinary.chmod(0o755)
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tt", "4.0.0", scope=scope)
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "tt-ee", TT_EE_VERSION, scope=scope)
    assert rc == 0, output
    assert not first.parent.exists()
    assert (root / "bin" / "tt").resolve(strict=True) == ordinary
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tt-ee", NEW_TT_EE_VERSION, scope=scope)
    assert rc == 0, output
    second = root / "versions" / "tt-ee" / f"tt-ee_{NEW_TT_EE_VERSION}" / "tt"
    assert (root / "bin" / "tt").resolve(strict=True) == second
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "tt-ee", scope=scope)
    assert rc == 0, output
    assert not second.parent.exists()
    assert (root / "bin" / "tt").resolve(strict=True) == ordinary


def test_tt_ee_sdk_component_and_standalone_coexist(tt_cmd, tmp_path):
    make_tt_ee(tmp_path)
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    assert_sdk_active(root, SDK_VERSION)
    rc, output = install_tt_ee(tt_cmd, tmp_path)
    assert rc == 0, output
    standalone = root / "versions" / "tt-ee" / f"tt-ee_{TT_EE_VERSION}" / "tt"
    assert (root / "bin" / "tt").resolve(strict=True) == standalone
    rc, output = binary_command(tt_cmd, tmp_path, "search", "sdk", "--local-dir", "local bundles")
    assert rc == 0, output
    assert f"{SDK_VERSION} [installed]" in output
    assert "[active]" not in output
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tt", "sdk-3.7.1")
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "tt", "sdk-3.7.1")
    assert rc == 0, output
    assert (root / "bin" / "tt").resolve(strict=True) == standalone
    assert (root / "bin" / "tarantool").is_file()
    assert (root / "bin" / "tcm").is_file()
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "sdk")
    assert rc == 0, output
    assert (root / "bin" / "tt").resolve(strict=True) == standalone
    assert not (root / "versions" / "tarantool-sdk" / f"tarantool-sdk_{SDK_VERSION}").exists()


def test_tt_ee_ignores_sdk_archives(tt_cmd, tmp_path):
    make_sdk(tmp_path)
    rc, output = install_tt_ee(tt_cmd, tmp_path)

    assert rc != 0, output
    if platform.system() != "Linux":
        assert "unsupported OS" in output
    elif platform.machine() != "x86_64":
        assert "unsupported architecture" in output
    else:
        assert "not matched with" in output
    assert not (tmp_path / ".tarantool").exists()


@pytest.mark.parametrize("requested", ["2ee", "2.15ee", "2.15.0ee"])
def test_install_tt_enterprise_selector(tt_cmd, tmp_path, requested):
    make_tt_ee(tmp_path)
    make_sdk(tmp_path)
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "install",
        "tt",
        requested,
        "--local-dir",
        "local bundles",
    )

    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    installed = root / "versions" / "tt-ee" / f"tt-ee_{TT_EE_VERSION}" / "tt"
    assert (root / "bin" / "tt").resolve(strict=True) == installed
    assert not (root / "versions" / "tt").exists()
    assert not (root / "versions" / "tarantool-sdk").exists()


def test_tt_ee_search_filters_local_release(tt_cmd, tmp_path):
    make_tt_ee(tmp_path, "2.14.0")
    make_tt_ee(tmp_path)
    make_tt_ee(tmp_path, NEW_TT_EE_VERSION)
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "search",
        "tt-ee",
        "--local-dir",
        "local bundles",
        "--version",
        "2.15",
    )

    assert rc == 0, output
    assert TT_EE_VERSION in output
    assert "2.14.0" not in output
    assert NEW_TT_EE_VERSION not in output


@pytest.mark.parametrize("community", ["2.9.0", "3.1.0"])
def test_tt_ee_fallback_uses_product_version(tt_cmd, tmp_path, community):
    make_tt_ee(tmp_path)
    make_sdk(tmp_path)
    make_sdk(tmp_path, NEW_SDK_VERSION)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    ordinary = root / "versions" / "tt" / f"tt_v{community}" / "tt"
    ordinary.parent.mkdir(parents=True)
    ordinary.write_text(f"#!/bin/sh\necho {community}.abcdef0\n")
    ordinary.chmod(0o755)
    rc, output = install_tt_ee(tt_cmd, tmp_path)
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "tt-ee", TT_EE_VERSION)
    assert rc == 0, output
    expected = ordinary
    if community == "2.9.0":
        expected = root / "versions" / "tt-ee" / f"tt_sdk_{NEW_SDK_VERSION}" / "tt"
    assert (root / "bin" / "tt").resolve(strict=True) == expected


def test_sdk_switch_components_and_uninstall(tt_cmd, tmp_path):
    make_sdk(tmp_path)
    make_sdk(tmp_path, NEW_SDK_VERSION)
    root = sdk_root(tmp_path, "project")
    for selected in (SDK_VERSION, NEW_SDK_VERSION):
        rc, output = install_sdk(tt_cmd, tmp_path, selected)
        assert rc == 0, output

    scripts = {
        name: (tmp_path / ".env" / name).read_bytes() for name in ("activate.sh", "deactivate.sh")
    }

    rc, output = binary_command(tt_cmd, tmp_path, "switch", "SDK", "3.7.1")
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tcm", "sdk-3.8.0")
    assert rc == 0, output
    assert NEW_SDK_VERSION in str((root / "bin" / "tcm").readlink())

    rc, output = binary_command(tt_cmd, tmp_path, "search", "sdk", "--local-dir", "local bundles")
    assert rc == 0, output
    assert f"{SDK_VERSION} [installed]" in output
    assert "[active]" not in output

    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "sdk", "3.8.0")
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)

    standalone = root / "versions" / "tcm" / "tcm_1.3.1" / "tcm"
    standalone.parent.mkdir(parents=True)
    standalone.write_text("#!/bin/sh\necho standalone\n")
    standalone.chmod(0o755)
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tcm", "1.3.1")
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "tarantool-sdk")
    assert rc == 0, output
    assert (root / "bin" / "tcm").resolve(strict=True) == standalone
    assert not os.path.lexists(root / "bin" / "tarantool")
    assert not os.path.lexists(root / "include" / "tarantool")
    assert not (root / "rocks").exists()
    assert not (root / "env.sh").exists()
    assert not (root / "versions" / "tarantool-ee" / f"tarantool_sdk_{SDK_VERSION}").exists()
    assert not (root / "versions" / "tcm" / f"tcm_sdk_{SDK_VERSION}").exists()
    assert not (root / "versions" / "tt-ee" / f"tt_sdk_{SDK_VERSION}").exists()
    assert not os.path.lexists(root / "bin" / "tt")
    for name, content in scripts.items():
        assert (tmp_path / ".env" / name).read_bytes() == content


@pytest.mark.parametrize(
    "missing",
    ["corrupt", "tarantool", "tcm", "tt", "include/tarantool/module.h"],
)
def test_sdk_invalid_reinstall_preserves_complete_version(tt_cmd, tmp_path, missing):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    before = {
        path.relative_to(root): path.read_bytes()
        for path in root.rglob("*")
        if path.is_file() and not path.is_symlink()
    }
    archive = make_sdk(tmp_path, missing=missing)
    if missing == "corrupt":
        archive.write_bytes(b"invalid archive")
    rc, output = install_sdk(tt_cmd, tmp_path, reinstall=True)
    assert rc != 0, output
    assert_sdk_active(root, SDK_VERSION)
    assert {
        path.relative_to(root): path.read_bytes()
        for path in root.rglob("*")
        if path.is_file() and not path.is_symlink()
    } == before


@pytest.mark.parametrize("dangling", [False, True])
def test_sdk_reactivation_skips_extraction(tt_cmd, tmp_path, dangling):
    archive = make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    for name in ("bin/tarantool", "bin/tcm", "bin/tt", "include/tarantool"):
        link = root / name
        link.unlink()
        if dangling:
            link.symlink_to("missing")
    archive.write_bytes(b"corrupt archive")
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    assert "Unpacking archive" not in output
    assert_sdk_active(root, SDK_VERSION)
    make_sdk(tmp_path, NEW_SDK_VERSION)
    rc, output = install_sdk(tt_cmd, tmp_path, NEW_SDK_VERSION)
    assert rc == 0, output
    assert_sdk_active(root, NEW_SDK_VERSION)


@pytest.mark.parametrize("missing", ["env.sh", "rocks", "rocks/manifest"])
def test_sdk_install_without_archive_extras(tt_cmd, tmp_path, missing):
    make_sdk(tmp_path, missing=missing)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    assert_sdk_active(root, SDK_VERSION)
    rc, output = binary_command(tt_cmd, tmp_path, "list")
    assert rc == 0, output
    assert f"{SDK_VERSION} [active]" in output
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "sdk", SDK_VERSION)
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "sdk")
    assert rc == 0, output


@pytest.mark.parametrize("operation", ["switch", "uninstall"])
def test_sdk_ambiguous_version_changes_nothing(tt_cmd, tmp_path, operation):
    for selected in (SDK_VERSION, "gc64-3.7.1-0-r94"):
        make_sdk(tmp_path, selected)
        rc, output = install_sdk(tt_cmd, tmp_path, selected)
        assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    rc, output = binary_command(tt_cmd, tmp_path, operation, "sdk", "3.7.1")
    assert rc != 0, output
    assert "more than one installed version matches" in output
    assert_sdk_active(root, "gc64-3.7.1-0-r94")
    assert (root / "versions" / "tarantool-ee" / f"tarantool_sdk_{SDK_VERSION}").is_dir()


def test_switch_from_sdk_to_community_and_back(tt_cmd, tmp_path):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    community = root / "versions" / "tarantool" / "tarantool_v3.7.1"
    (community / "include" / "tarantool").mkdir(parents=True)
    (community / "tarantool").write_text("#!/bin/sh\necho community\n")
    (community / "tarantool").chmod(0o755)
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "tarantool", "3.7.1")
    assert rc == 0, output
    assert (root / "bin" / "tarantool").resolve(strict=True) == community / "tarantool"
    assert not (root / "rocks").exists()
    assert not (root / "env.sh").exists()
    rc, output = binary_command(tt_cmd, tmp_path, "switch", "sdk", "3.7.1")
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)


def test_sdk_uninstall_cleans_missing_component_directory(tt_cmd, tmp_path):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    shutil.rmtree(root / "versions" / "tcm" / f"tcm_sdk_{SDK_VERSION}")
    rc, output = binary_command(tt_cmd, tmp_path, "uninstall", "sdk")
    assert rc == 0, output
    assert not os.path.lexists(root / "bin" / "tcm")
    assert not os.path.lexists(root / "bin" / "tarantool")
    assert not (root / "env.sh").exists()


@pytest.mark.parametrize("operation", ["switch", "uninstall"])
def test_sdk_component_requires_its_own_version_namespace(tt_cmd, tmp_path, operation):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, operation, "tcm", "3.7.1")
    assert rc != 0, output
    assert_sdk_active(sdk_root(tmp_path, "project"), SDK_VERSION)


def test_sdk_search_and_list_recognize_scope_alias(tt_cmd, tmp_path):
    real_root = tmp_path / "sdk data"
    real_root.mkdir()
    (tmp_path / ".tarantool").symlink_to(real_root, target_is_directory=True)
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    rc, output = binary_command(tt_cmd, tmp_path, "search", "sdk", "--local-dir", "local bundles")
    assert rc == 0, output
    assert f"{SDK_VERSION} [active]" in output
    rc, output = binary_command(tt_cmd, tmp_path, "list")
    assert rc == 0, output
    assert f"sdk-{SDK_VERSION} [active]" in output
    assert "tarantool ->" not in output


@pytest.mark.parametrize(
    "failure",
    ["corrupt", "tarantool", "tcm", "tt", "include/tarantool/module.h"],
)
def test_sdk_invalid_fresh_archive_leaves_no_installation(tt_cmd, tmp_path, failure):
    archive = make_sdk(tmp_path, missing=failure)
    if failure == "corrupt":
        archive.write_bytes(b"invalid archive")
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc != 0, output
    assert not (tmp_path / ".tarantool").exists()
    assert not (tmp_path / ".env").exists()


def test_sdk_reinstall_replaces_all_components_and_ownership(tt_cmd, tmp_path):
    make_sdk(tmp_path)
    rc, output = install_sdk(tt_cmd, tmp_path)
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    ownership = root / "versions" / "tarantool-sdk" / f"tarantool-sdk_{SDK_VERSION}"
    for link in ("bin/tarantool", "bin/tcm", "bin/tt", "include/tarantool"):
        destination = (root / link).resolve(strict=True)
        (destination if destination.is_dir() else destination.parent).joinpath("stale").write_text(
            "old",
        )
    (ownership / "stale").write_text("old")
    make_sdk(tmp_path, content="replacement")
    rc, output = install_sdk(tt_cmd, tmp_path, reinstall=True)
    assert rc == 0, output
    assert_sdk_active(root, SDK_VERSION)
    for name in ("tarantool", "tcm", "tt"):
        binary = (root / "bin" / name).resolve(strict=True)
        assert "replacement" in binary.read_text()
        assert not (binary.parent / "stale").exists()
    assert (root / "include/tarantool/module.h").read_text() == "replacement"
    assert not (root / "include/tarantool/stale").exists()
    assert not (ownership / "stale").exists()
    assert json.loads((ownership / "sdk.json").read_text()) == {
        "version": SDK_VERSION,
        "products": ["tarantool-ee", "tcm", "tt-ee"],
    }


@pytest.mark.parametrize("failure", ["corrupt", "missing"])
@pytest.mark.parametrize("reinstall", [False, True])
def test_tt_ee_invalid_archive_preserves_active_installation(tt_cmd, tmp_path, failure, reinstall):
    root = sdk_root(tmp_path, "project")
    if reinstall:
        make_tt_ee(tmp_path)
        rc, output = install_tt_ee(tt_cmd, tmp_path)
        assert rc == 0, output
        binary = (root / "bin/tt").resolve(strict=True)
        before = binary.read_bytes()
    archive = make_tt_ee(tmp_path, missing=failure == "missing")
    if failure == "corrupt":
        archive.write_bytes(b"invalid archive")
    args = ["install", "tt", f"{TT_EE_VERSION}ee", "--local-dir", "local bundles"]
    if reinstall:
        args.append("--reinstall")
    rc, output = binary_command(tt_cmd, tmp_path, *args)
    assert rc != 0, output
    if reinstall:
        assert (root / "bin/tt").resolve(strict=True) == binary
        assert binary.read_bytes() == before
    else:
        assert not root.exists()
        assert not (tmp_path / ".env").exists()


@pytest.mark.parametrize("dangling", [False, True])
def test_tt_ee_reactivation_and_reinstall(tt_cmd, tmp_path, dangling):
    make_tt_ee(tmp_path)
    make_tt_ee(tmp_path, NEW_TT_EE_VERSION)
    make_tt_ee(tmp_path, "2.17.0-rc1")
    # The major-version selector must pick the latest stable Enterprise archive.
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "install",
        "tt",
        "2ee",
        "--local-dir",
        "local bundles",
    )
    assert rc == 0, output
    root = sdk_root(tmp_path, "project")
    binary = root / "versions/tt-ee" / f"tt-ee_{NEW_TT_EE_VERSION}" / "tt"
    link = root / "bin/tt"
    assert link.resolve(strict=True) == binary
    link.unlink()
    if dangling:
        link.symlink_to("missing")
    archive = make_tt_ee(tmp_path, NEW_TT_EE_VERSION)
    archive.write_bytes(b"invalid archive")
    rc, output = install_tt_ee(tt_cmd, tmp_path, NEW_TT_EE_VERSION)
    assert rc == 0, output
    assert "Unpacking archive" not in output
    assert link.resolve(strict=True) == binary
    (binary.parent / "stale").write_text("old")
    binary.write_text("old binary")
    make_tt_ee(tmp_path, NEW_TT_EE_VERSION)
    rc, output = binary_command(
        tt_cmd,
        tmp_path,
        "install",
        "tt",
        f"{NEW_TT_EE_VERSION}ee",
        "--local-dir",
        "local bundles",
        "--reinstall",
    )
    assert rc == 0, output
    assert "Unpacking archive" in output
    assert link.resolve(strict=True) == binary
    assert NEW_TT_EE_VERSION in binary.read_text()
    assert not (binary.parent / "stale").exists()
