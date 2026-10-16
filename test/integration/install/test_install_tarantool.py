import os
import platform
import subprocess

import pytest

from utils import create_tt_config, run_command_and_get_output


@pytest.fixture(autouse=True)
def scope_environment(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "user data"))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)


@pytest.fixture
def local_tarantool_build(tmp_path, monkeypatch):
    repository = tmp_path / "distfiles" / "tarantool"
    repository.mkdir(parents=True)
    (repository / "README").write_text("local tarantool sources\n")
    for args in [
        ["init", "--initial-branch=master"],
        ["add", "README"],
        ["-c", "user.name=Scope Test", "-c", "user.email=scope@example.com", "commit", "-m", "ce"],
        ["tag", "3.2.1"],
    ]:
        subprocess.run(["git", *args], cwd=repository, check=True, capture_output=True)

    built_binary = tmp_path / "built-tarantool"
    built_binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1'\n")
    built_binary.chmod(0o755)
    built_header = tmp_path / "module.h"
    built_header.write_text("local tarantool headers\n")
    build_log = tmp_path / "build.log"
    tools = tmp_path / "tools"
    tools.mkdir()
    cmake = tools / "cmake"
    cmake.write_text("#!/bin/sh\nexit 0\n")
    cmake.chmod(0o755)
    make = tools / "make"
    make.write_text(
        "#!/bin/sh\n"
        "set -e\n"
        "mkdir -p tarantool-prefix/bin tarantool-prefix/include/tarantool\n"
        'cp "$TT_CE_SCOPE_TEST_BINARY" tarantool-prefix/bin/tarantool\n'
        'cp "$TT_CE_SCOPE_TEST_HEADER" tarantool-prefix/include/tarantool/module.h\n'
        'echo build >> "$TT_CE_SCOPE_TEST_BUILD_LOG"\n',
    )
    make.chmod(0o755)
    monkeypatch.setenv("PATH", f"{tools}{os.pathsep}{os.environ['PATH']}")
    monkeypatch.setenv("TT_CE_SCOPE_TEST_BINARY", str(built_binary))
    monkeypatch.setenv("TT_CE_SCOPE_TEST_HEADER", str(built_header))
    monkeypatch.setenv("TT_CE_SCOPE_TEST_BUILD_LOG", str(build_log))
    return built_binary, built_header, build_log


def scope_root(tmp_path, scope):
    if scope == "user":
        if platform.system() == "Darwin":
            return tmp_path / "home" / "Library" / "Application Support" / "tarantool"
        return tmp_path / "user data" / "tarantool"
    return tmp_path / ".tarantool"


def install_tarantool(tt_cmd, tmp_path, flags=(), version="3.2.1"):
    return run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tarantool",
            *([] if version is None else [version]),
            "--force",
            "--local-repo",
            "distfiles",
            *flags,
        ],
        cwd=tmp_path,
    )


@pytest.mark.parametrize("scope", [None, "project", "user"])
@pytest.mark.parametrize("dynamic", [False, True])
@pytest.mark.parametrize("reinstall", [False, True])
def test_install_tarantool_scope(
    tt_cmd,
    tmp_path,
    local_tarantool_build,
    scope,
    dynamic,
    reinstall,
):
    built_binary, built_header, build_log = local_tarantool_build
    flags = [] if scope is None else ["--scope", scope]
    if dynamic:
        flags.append("--dynamic")
    if reinstall:
        flags.append("--reinstall")

    rc, output = install_tarantool(tt_cmd, tmp_path, flags)
    assert rc == 0, output
    assert "to PATH to use this as default" not in output
    root = scope_root(tmp_path, scope)
    version = root / "versions" / "tarantool" / "tarantool_3.2.1"
    binary = version / "tarantool"
    headers = version / "include" / "tarantool"
    assert binary.read_bytes() == built_binary.read_bytes()
    assert os.access(binary, os.X_OK)
    assert not (version / "bin").exists()
    assert (headers / "module.h").read_bytes() == built_header.read_bytes()
    assert (root / "bin" / "tarantool").readlink() == binary
    assert (root / "bin" / "tarantool").resolve() == binary
    assert (root / "include" / "tarantool").readlink() == headers
    assert (root / "include" / "tarantool").resolve() == headers
    assert build_log.read_text() == "build\n"
    assert not (tmp_path / "tt.yaml").exists()
    if scope == "user":
        assert not (tmp_path / ".tarantool").exists()
        assert not (tmp_path / ".env").exists()
        assert "source .env/activate.sh" not in output
    else:
        assert (tmp_path / ".env" / "activate.sh").is_file()
        assert (tmp_path / ".env" / "deactivate.sh").is_file()
        assert output.count("source .env/activate.sh") == 1


@pytest.mark.parametrize("with_symlinks", [False, True])
def test_install_tarantool_existing_version(tt_cmd, tmp_path, local_tarantool_build, with_symlinks):
    built_binary, built_header, build_log = local_tarantool_build
    root = tmp_path / ".tarantool"
    version = root / "versions" / "tarantool" / "tarantool_3.2.1"
    binary = version / "tarantool"
    headers = version / "include" / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_bytes(built_binary.read_bytes())
    binary.chmod(0o755)
    headers.mkdir(parents=True)
    (headers / "module.h").write_bytes(built_header.read_bytes())
    if with_symlinks:
        for directory in ["bin", "include"]:
            (root / directory).mkdir()
            (root / directory / "tarantool").symlink_to("missing-version")

    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    assert "already exists" in output
    assert (root / "bin" / "tarantool").resolve() == binary
    assert (root / "include" / "tarantool").resolve() == headers
    assert not build_log.exists()


@pytest.mark.parametrize("missing", ["binary", "headers"])
def test_install_tarantool_incomplete_version(tt_cmd, tmp_path, local_tarantool_build, missing):
    built_binary, _, build_log = local_tarantool_build
    version = tmp_path / ".tarantool" / "versions" / "tarantool" / "tarantool_3.2.1"
    if missing == "binary":
        (version / "include" / "tarantool").mkdir(parents=True)
    else:
        version.mkdir(parents=True)
        binary = version / "tarantool"
        binary.write_bytes(built_binary.read_bytes())
        binary.chmod(0o755)

    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    assert (version / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (version / "include" / "tarantool" / "module.h").is_file()
    assert build_log.read_text() == "build\n"


@pytest.mark.parametrize("missing_binary", [False, True])
def test_install_tarantool_reinstall(tt_cmd, tmp_path, local_tarantool_build, missing_binary):
    built_binary, built_header, build_log = local_tarantool_build
    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    stale_header = root / "include" / "tarantool" / "stale.h"
    stale_header.write_text("stale header\n")
    other_version = root / "versions" / "tarantool" / "tarantool_0.9.0"
    other_version.mkdir()
    (other_version / "keep").write_text("other version\n")
    built_binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1 rebuilt'\n")
    built_header.write_text("rebuilt headers\n")
    if missing_binary:
        (root / "bin" / "tarantool").resolve().unlink()

    rc, output = install_tarantool(tt_cmd, tmp_path, ["--reinstall"])
    assert rc == 0, output
    assert (root / "bin" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (root / "include" / "tarantool" / "module.h").read_bytes() == built_header.read_bytes()
    assert not stale_header.exists()
    assert (other_version / "keep").read_text() == "other version\n"
    assert build_log.read_text() == "build\nbuild\n"


@pytest.mark.parametrize("failure", ["version", "tmp", "cmake", "make"])
def test_install_tarantool_failure_leaves_no_scope(
    tt_cmd,
    tmp_path,
    local_tarantool_build,
    monkeypatch,
    failure,
):
    _, _, build_log = local_tarantool_build
    version = "3.2.1"
    if failure == "version":
        version = "0" * 40
    elif failure == "tmp":
        monkeypatch.setenv("TMPDIR", str(tmp_path / "missing-tmp"))
    else:
        (tmp_path / "tools" / failure).write_text("#!/bin/sh\nexit 1\n")

    rc, output = install_tarantool(tt_cmd, tmp_path, version=version)
    assert rc != 0, output
    assert not (tmp_path / ".tarantool").exists()
    assert not build_log.exists()


def test_install_tarantool_ignores_config_destinations(tt_cmd, tmp_path, local_tarantool_build):
    built_binary, _, _ = local_tarantool_build
    legacy = tmp_path / "legacy"
    legacy.mkdir()
    (legacy / "tarantool").write_text("#!/bin/sh\necho 'Tarantool 3.0.0'\n")
    (legacy / "tarantool").chmod(0o755)
    create_tt_config(tmp_path, {"env": {"bin_dir": "legacy", "inc_dir": "legacy-headers"}})

    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    assert (tmp_path / ".tarantool" / "bin" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (legacy / "tarantool").read_text() == "#!/bin/sh\necho 'Tarantool 3.0.0'\n"
    assert not (tmp_path / "legacy-headers").exists()


def test_install_tarantool_scopes_are_independent(tt_cmd, tmp_path, local_tarantool_build):
    built_binary, built_header, _ = local_tarantool_build
    original_binary = built_binary.read_bytes()
    original_header = built_header.read_bytes()
    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    built_binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1 user build'\n")
    built_header.write_text("user headers\n")

    rc, output = install_tarantool(tt_cmd, tmp_path, ["--scope", "user"])
    assert rc == 0, output
    project = scope_root(tmp_path, "project")
    user = scope_root(tmp_path, "user")
    assert (project / "bin" / "tarantool").read_bytes() == original_binary
    assert (project / "include" / "tarantool" / "module.h").read_bytes() == original_header
    assert (user / "bin" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (user / "include" / "tarantool" / "module.h").read_bytes() == built_header.read_bytes()


@pytest.mark.parametrize("revision", ["release", "commit", "master", "pull-request"])
@pytest.mark.usefixtures("local_tarantool_build")
def test_install_tarantool_remote_revision(tt_cmd, tmp_path, monkeypatch, revision):
    repository = tmp_path / "distfiles" / "tarantool"

    def git(*args):
        return subprocess.check_output(["git", *args], cwd=repository, text=True).strip()

    release_commit = git("rev-parse", "HEAD")
    commits = {}
    for name in ["master", "pull request"]:
        (repository / "README").write_text(f"{name} sources\n")
        git(
            "-c",
            "user.name=Scope Test",
            "-c",
            "user.email=scope@example.com",
            "commit",
            "-am",
            name,
        )
        commits[name] = git("rev-parse", "HEAD")
    git("update-ref", "refs/pull/42/head", commits["pull request"])
    git("reset", "--hard", commits["master"])

    # Exercise remote downloads through a local repository without using the network.
    git_config = tmp_path / "gitconfig"
    git(
        "config",
        "--file",
        str(git_config),
        f"url.{repository.as_uri()}.insteadOf",
        "https://github.com/tarantool/tarantool.git",
    )
    monkeypatch.setenv("GIT_CONFIG_GLOBAL", str(git_config))
    monkeypatch.setenv("TT_CE_SCOPE_TEST_BINARY", "../../README")
    revisions = {
        "release": ("3.2.1", "local tarantool sources\n", "3.2.1"),
        "commit": (release_commit, "local tarantool sources\n", release_commit[:7]),
        "master": ("master", "master sources\n", "master"),
        "pull-request": ("pr/42", "pull request sources\n", commits["pull request"][:7]),
    }
    requested, expected, name = revisions[revision]
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "install", "tarantool", requested, "--force"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert "Downloading tarantool..." in output
    root = tmp_path / ".tarantool"
    binary = root / "versions" / "tarantool" / f"tarantool_{name}" / "tarantool"
    assert (root / "bin" / "tarantool").readlink() == binary
    assert binary.read_text() == expected
    assert (root / "include" / "tarantool" / "module.h").is_file()


@pytest.mark.usefixtures("local_tarantool_build")
def test_install_tarantool_latest_release(tt_cmd, tmp_path):
    rc, output = install_tarantool(tt_cmd, tmp_path, version=None)
    assert rc == 0, output
    assert (tmp_path / ".tarantool" / "versions" / "tarantool" / "tarantool_3.2.1").is_dir()


@pytest.mark.parametrize("update", [False, True])
def test_install_tarantool_existing_master(tt_cmd, tmp_path, local_tarantool_build, update):
    built_binary, _, build_log = local_tarantool_build
    repository = tmp_path / "distfiles" / "tarantool"
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repository, text=True).strip()
    old_commit = ("0" if head[0] != "0" else "1") + head[1:7]
    root = tmp_path / ".tarantool"
    version = root / "versions" / "tarantool" / "tarantool_master"
    binary = version / "tarantool"
    binary.parent.mkdir(parents=True)
    old_contents = f"#!/bin/sh\necho 'Tarantool 3.0.0-1-g{old_commit}'\n"
    binary.write_text(old_contents)
    binary.chmod(0o755)
    headers = version / "include" / "tarantool"
    headers.mkdir(parents=True)

    args = [tt_cmd, "--self"]
    if not update:
        args.append("--no-prompt")
    args.extend(
        [
            "binary",
            "install",
            "tarantool",
            "master",
            "--local-repo",
            "distfiles",
            "--force",
        ],
    )
    rc, output = run_command_and_get_output(
        args,
        cwd=tmp_path,
        input="y\n" if update else None,
    )
    assert rc == 0, output
    assert (root / "bin" / "tarantool").resolve() == binary
    assert (root / "include" / "tarantool").resolve() == headers
    if update:
        assert "Found newest commit of tarantool in master" in output
        assert binary.read_bytes() == built_binary.read_bytes()
        assert build_log.read_text() == "build\n"
    else:
        assert "already exists" in output
        assert binary.read_text() == old_contents
        assert not build_log.exists()


@pytest.mark.parametrize("scope", ["project", "user"])
@pytest.mark.parametrize(
    "binary_path",
    ["src/tarantool", "tarantool/src/tarantool", "tarantool-prefix/bin/tarantool"],
)
@pytest.mark.parametrize("headers", ["auto", "custom", None])
@pytest.mark.usefixtures("scope_environment")
def test_install_tarantool_from_build(tt_cmd, tmp_path, scope, binary_path, headers):
    build = tmp_path / "build"
    binary = build / binary_path
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1'\n")
    binary.chmod(0o755)
    flags = []
    if headers is not None:
        includes = build / "tarantool-prefix" / "include" / "tarantool"
        includes.mkdir(parents=True)
        if headers == "custom":
            includes = build / "custom-include"
            includes.mkdir()
            flags = ["--include-dir", str(includes.relative_to(tmp_path))]
    root = scope_root(tmp_path, scope)
    old_headers = root / "include" / "tarantool"
    old_headers.mkdir(parents=True)
    (old_headers / "stale.h").touch()

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tarantool",
            "--scope",
            scope,
            "--from-build",
            "build",
            *flags,
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (root / "bin" / "tarantool").readlink() == binary
    if headers is None:
        assert not old_headers.is_symlink()
        assert not old_headers.exists()
        assert "Tarantool headers location was not specified" in output
    else:
        assert old_headers.readlink() == includes
    assert not (root / "versions").exists()
    assert binary.is_file()
    assert not (tmp_path / "tt.yaml").exists()
    assert "to PATH to use this as default" not in output
    if scope == "user":
        assert not (tmp_path / ".tarantool").exists()
        assert not (tmp_path / ".env").exists()
        assert "source .env/activate.sh" not in output
    else:
        assert (tmp_path / ".env" / "activate.sh").is_file()
        assert (tmp_path / ".env" / "deactivate.sh").is_file()
        assert output.count("source .env/activate.sh") == 1


@pytest.mark.parametrize(
    "failure",
    [
        "missing-build",
        "missing-binary",
        "binary-directory",
        "not-executable",
        "missing-headers",
        "headers-file",
    ],
)
@pytest.mark.usefixtures("scope_environment")
def test_install_tarantool_from_build_failure(tt_cmd, tmp_path, failure):
    build = tmp_path / "build"
    if failure != "missing-build":
        (build / "src").mkdir(parents=True)
    binary = build / "src" / "tarantool"
    flags = []
    if failure == "binary-directory":
        binary.mkdir()
    elif failure not in ["missing-build", "missing-binary"]:
        binary.write_text("#!/bin/sh\nexit 0\n")
        binary.chmod(0o644 if failure == "not-executable" else 0o755)
    if failure in ["missing-headers", "headers-file"]:
        includes = tmp_path / "headers"
        if failure == "headers-file":
            includes.touch()
        flags = ["--include-dir", str(includes)]

    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "install", "tarantool", "--from-build", str(build), *flags],
        cwd=tmp_path,
    )
    assert rc != 0, output
    assert not (tmp_path / ".tarantool").exists()


def test_install_tarantool_from_build_invalid_headers_preserves_links(
    tt_cmd,
    tmp_path,
    local_tarantool_build,
):
    built_binary, _, _ = local_tarantool_build
    rc, output = install_tarantool(tt_cmd, tmp_path)
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    original_binary = (root / "bin" / "tarantool").readlink()
    original_headers = (root / "include" / "tarantool").readlink()
    build = tmp_path / "build"
    (build / "src").mkdir(parents=True)
    binary = build / "src" / "tarantool"
    binary.write_bytes(built_binary.read_bytes())
    binary.chmod(0o755)

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tarantool",
            "--from-build",
            str(build),
            "--include-dir",
            str(tmp_path / "missing-headers"),
        ],
        cwd=tmp_path,
    )
    assert rc != 0, output
    assert (root / "bin" / "tarantool").readlink() == original_binary
    assert (root / "include" / "tarantool").readlink() == original_headers


@pytest.mark.usefixtures("scope_environment")
def test_install_tarantool_from_build_uses_current_directory(tt_cmd, tmp_path):
    config_dir = tmp_path / "environment"
    config = create_tt_config(config_dir, {"env": {"bin_dir": "bin", "inc_dir": "inc"}})
    build = tmp_path / "build"
    binary = build / "src" / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\nexit 0\n")
    binary.chmod(0o755)
    legacy = config_dir / "bin"
    legacy.mkdir()
    (legacy / "tarantool").write_text("#!/bin/sh\necho 'Tarantool 3.0.0'\n")
    (legacy / "tarantool").chmod(0o755)
    parent_scope = tmp_path / ".tarantool"
    parent_scope.mkdir()
    project = tmp_path / "project"
    project.mkdir()

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "--cfg",
            str(config),
            "binary",
            "install",
            "tarantool",
            "--from-build",
            str(build),
        ],
        cwd=project,
    )
    assert rc == 0, output
    assert (project / ".tarantool" / "bin" / "tarantool").readlink() == binary
    assert list(parent_scope.iterdir()) == []
    assert (legacy / "tarantool").read_text() == "#!/bin/sh\necho 'Tarantool 3.0.0'\n"
    assert not (config_dir / "inc").exists()


@pytest.fixture
def docker_tarantool_build(tmp_path, local_tarantool_build):
    if platform.system() != "Linux":
        pytest.skip("Docker installation requires a Linux tt executable")

    built_binary, built_header, _ = local_tarantool_build
    repository = tmp_path / "distfiles" / "tarantool"
    source = repository / "src"
    source.mkdir()
    (source / "tarantool").write_bytes(built_binary.read_bytes())
    (source / "tarantool").chmod(0o755)
    (source / "module.h").write_bytes(built_header.read_bytes())
    (repository / "CMakeLists.txt").write_text(
        "cmake_minimum_required(VERSION 3.10)\n"
        "project(TarantoolScopeTest NONE)\n"
        'install(PROGRAMS "${CMAKE_CURRENT_SOURCE_DIR}/src/tarantool" DESTINATION bin)\n'
        'install(FILES "${CMAKE_CURRENT_SOURCE_DIR}/src/module.h" DESTINATION include/tarantool)\n',
    )
    static = repository / "static-build"
    static.mkdir()
    (static / "CMakeLists.txt").write_text(
        "cmake_minimum_required(VERSION 3.10)\n"
        "project(TarantoolScopeTest NONE)\n"
        'set(prefix "${CMAKE_BINARY_DIR}/tarantool-prefix")\n'
        'file(MAKE_DIRECTORY "${prefix}/bin" "${prefix}/include/tarantool")\n'
        'configure_file("../src/tarantool" "${prefix}/bin/tarantool" COPYONLY)\n'
        'configure_file("../src/module.h" "${prefix}/include/tarantool/module.h" COPYONLY)\n'
        "add_custom_target(tarantool ALL)\n",
    )
    for args in [
        ["add", "."],
        [
            "-c",
            "user.name=Scope Test",
            "-c",
            "user.email=scope@example.com",
            "commit",
            "-m",
            "build",
        ],
        ["tag", "-f", "3.2.1"],
    ]:
        subprocess.run(["git", *args], cwd=repository, check=True, capture_output=True)
    return built_binary, built_header


@pytest.mark.docker
@pytest.mark.parametrize("scope", ["project", "user"])
@pytest.mark.parametrize("dynamic", [False, True])
def test_install_tarantool_docker_scope(tt_cmd, tmp_path, docker_tarantool_build, scope, dynamic):
    built_binary, built_header = docker_tarantool_build
    flags = ["--use-docker", "--scope", scope]
    if dynamic:
        flags.append("--dynamic")
    rc, output = install_tarantool(tt_cmd, tmp_path, flags)
    assert rc == 0, output
    root = scope_root(tmp_path, scope)
    version = root / "versions" / "tarantool" / "tarantool_3.2.1"
    assert (root / "bin" / "tarantool").readlink() == version / "tarantool"
    assert (root / "include" / "tarantool").readlink() == version / "include" / "tarantool"
    assert (root / "bin" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (root / "include" / "tarantool" / "module.h").read_bytes() == built_header.read_bytes()
    assert not (tmp_path / "tt.yaml").exists()
    if scope == "user":
        assert not (tmp_path / ".tarantool").exists()


@pytest.mark.docker
def test_install_tarantool_docker_existing_master(tt_cmd, tmp_path, docker_tarantool_build):
    built_binary, _ = docker_tarantool_build
    root = tmp_path / ".tarantool"
    version = root / "versions" / "tarantool" / "tarantool_master"
    binary = version / "tarantool"
    binary.parent.mkdir(parents=True)
    binary.write_text("#!/bin/sh\necho 'Tarantool 3.2.1-1-g0000000'\n")
    binary.chmod(0o755)
    headers = version / "include" / "tarantool"
    headers.mkdir(parents=True)
    (headers / "stale.h").touch()

    rc, output = run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tarantool",
            "master",
            "--force",
            "--local-repo",
            "distfiles",
            "--use-docker",
        ],
        cwd=tmp_path,
        input="y\n",
    )
    assert rc == 0, output
    assert "Found newest commit of tarantool in master" in output
    assert (root / "bin" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (root / "include" / "tarantool").readlink() == headers
    assert not (headers / "stale.h").exists()


@pytest.mark.docker
def test_install_tarantool_system_scope_in_docker(tt_cmd, tmp_path, docker_tarantool_build):
    built_binary, built_header = docker_tarantool_build
    target = tmp_path / "system"
    target.mkdir()
    repository = tmp_path / "distfiles" / "tarantool"
    rc, output = run_command_and_get_output(
        [
            "docker",
            "run",
            "--rm",
            "--user",
            f"{os.getuid()}:{os.getgid()}",
            "--workdir",
            "/",
            "--mount",
            f"type=bind,source={tt_cmd},target=/tt,readonly",
            "--mount",
            f"type=bind,source={repository},target=/build,readonly",
            "--mount",
            f"type=bind,source={target},target=/opt/tarantool",
            "ubuntu:20.04",
            "/tt",
            "--self",
            "binary",
            "install",
            "tarantool",
            "--scope",
            "system",
            "--from-build",
            "/build",
            "--include-dir",
            "/build/src",
        ],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert str((target / "bin" / "tarantool").readlink()) == "/build/src/tarantool"
    assert str((target / "include" / "tarantool").readlink()) == "/build/src"
    assert (repository / "src" / "tarantool").read_bytes() == built_binary.read_bytes()
    assert (repository / "src" / "module.h").read_bytes() == built_header.read_bytes()
    assert not (target / "versions").exists()
