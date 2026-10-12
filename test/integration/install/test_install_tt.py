import os
import platform
import subprocess

import pytest

from utils import create_tt_config, run_command_and_get_output


@pytest.fixture
def local_tt_build(tmp_path, monkeypatch):
    repository = tmp_path / "distfiles" / "tt"
    repository.mkdir(parents=True)
    (repository / "README").write_text("local tt sources\n")
    for args in [
        ["init", "--initial-branch=master"],
        ["add", "README"],
        ["-c", "user.name=Scope Test", "-c", "user.email=scope@example.com", "commit", "-m", "tt"],
        ["tag", "v1.2.3"],
    ]:
        subprocess.run(["git", *args], cwd=repository, check=True, capture_output=True)

    built_binary = tmp_path / "built-tt"
    built_binary.write_text("#!/bin/sh\necho 'local tt build'\n")
    built_binary.chmod(0o755)
    build_log = tmp_path / "build.log"
    tools = tmp_path / "tools"
    tools.mkdir()
    mage = tools / "mage"
    mage.write_text(
        "#!/bin/sh\n"
        "set -e\n"
        'cp "$TT_SCOPE_TEST_BINARY" tt\n'
        "chmod 755 tt\n"
        'echo build >> "$TT_SCOPE_TEST_BUILD_LOG"\n',
    )
    mage.chmod(0o755)
    monkeypatch.setenv("PATH", f"{tools}{os.pathsep}{os.environ['PATH']}")
    monkeypatch.setenv("TT_SCOPE_TEST_BINARY", str(built_binary))
    monkeypatch.setenv("TT_SCOPE_TEST_BUILD_LOG", str(build_log))
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "user data"))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)
    return built_binary, build_log


def install_tt(tt_cmd, tmp_path, flags=(), version="1.2.3"):
    return run_command_and_get_output(
        [
            tt_cmd,
            "--self",
            "binary",
            "install",
            "tt",
            version,
            "--force",
            "--local-repo",
            *flags,
        ],
        cwd=tmp_path,
    )


@pytest.mark.parametrize("scope", [None, "project", "user"])
@pytest.mark.parametrize("reinstall", [False, True])
def test_install_tt_scope(tt_cmd, tmp_path, local_tt_build, scope, reinstall):
    built_binary, build_log = local_tt_build
    flags = [] if scope is None else ["--scope", scope]
    if reinstall:
        flags.append("--reinstall")

    rc, output = install_tt(tt_cmd, tmp_path, flags)
    assert rc == 0, output
    if scope == "user":
        if platform.system() == "Darwin":
            root = tmp_path / "home" / "Library" / "Application Support" / "tarantool"
        else:
            root = tmp_path / "user data" / "tarantool"
        assert not (tmp_path / ".tarantool").exists()
    else:
        root = tmp_path / ".tarantool"

    binary = root / "versions" / "tt" / "tt_v1.2.3" / "tt"
    assert binary.read_bytes() == built_binary.read_bytes()
    assert os.access(binary, os.X_OK)
    assert (root / "bin" / "tt").readlink() == binary
    assert not (binary.parent / "bin").exists()
    assert (root / "bin" / "tt").resolve() == binary
    assert build_log.read_text() == "build\n"
    assert not (root / "inc").exists()
    assert not (tmp_path / "tt.yaml").exists()


@pytest.mark.parametrize("with_symlink", [False, True])
def test_install_tt_existing_version(tt_cmd, tmp_path, local_tt_build, with_symlink):
    built_binary, build_log = local_tt_build
    root = tmp_path / ".tarantool"
    binary = root / "versions" / "tt" / "tt_v1.2.3" / "tt"
    binary.parent.mkdir(parents=True)
    binary.write_bytes(built_binary.read_bytes())
    binary.chmod(0o755)
    if with_symlink:
        (root / "bin").mkdir()
        (root / "bin" / "tt").symlink_to("missing-version")

    rc, output = install_tt(tt_cmd, tmp_path)
    assert rc == 0, output
    assert "already exists" in output
    assert (root / "bin" / "tt").resolve() == binary
    assert not build_log.exists()


def test_install_tt_reinstall(tt_cmd, tmp_path, local_tt_build):
    built_binary, build_log = local_tt_build
    rc, output = install_tt(tt_cmd, tmp_path)
    assert rc == 0, output
    built_binary.write_text("#!/bin/sh\necho 'rebuilt tt'\n")

    rc, output = install_tt(tt_cmd, tmp_path, ["--reinstall"])
    assert rc == 0, output
    active = tmp_path / ".tarantool" / "bin" / "tt"
    assert active.read_bytes() == built_binary.read_bytes()
    assert build_log.read_text() == "build\nbuild\n"


@pytest.mark.parametrize("failure", ["version", "tmp", "build"])
def test_install_tt_failure_leaves_no_scope(tt_cmd, tmp_path, local_tt_build, monkeypatch, failure):
    _, build_log = local_tt_build
    version = "1.2.3"
    if failure == "version":
        version = "0" * 40
    elif failure == "tmp":
        monkeypatch.setenv("TMPDIR", str(tmp_path / "missing-tmp"))
    else:
        (tmp_path / "tools" / "mage").write_text("#!/bin/sh\nexit 1\n")

    rc, output = install_tt(tt_cmd, tmp_path, version=version)
    assert rc != 0, output
    assert not (tmp_path / ".tarantool").exists()
    assert not build_log.exists()


def test_install_tt_ignores_config_destinations(tt_cmd, tmp_path, local_tt_build):
    built_binary, _ = local_tt_build
    legacy = tmp_path / "legacy"
    legacy.mkdir()
    legacy_binary = legacy / "tt"
    legacy_binary.write_text("legacy binary\n")
    create_tt_config(tmp_path, {"env": {"bin_dir": "legacy", "inc_dir": "legacy-headers"}})

    rc, output = install_tt(tt_cmd, tmp_path)
    assert rc == 0, output
    assert (tmp_path / ".tarantool" / "bin" / "tt").read_bytes() == built_binary.read_bytes()
    assert legacy_binary.read_text() == "legacy binary\n"
    assert not (tmp_path / "legacy-headers").exists()


@pytest.mark.parametrize("revision", ["release", "commit", "master", "pull-request"])
@pytest.mark.usefixtures("local_tt_build")
def test_install_tt_remote_revision(tt_cmd, tmp_path, monkeypatch, revision):
    repository = tmp_path / "distfiles" / "tt"

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

    # Exercise remote downloads against a local Git repository without using the network.
    git_config = tmp_path / "gitconfig"
    git(
        "config",
        "--file",
        str(git_config),
        f"url.{repository.as_uri()}.insteadOf",
        "https://github.com/tarantool/tt.git",
    )
    monkeypatch.setenv("GIT_CONFIG_GLOBAL", str(git_config))
    (tmp_path / "tools" / "mage").write_text("#!/bin/sh\nset -e\ncp README tt\nchmod 755 tt\n")

    revisions = {
        "release": ("1.2.3", "local tt sources\n"),
        "commit": (release_commit, "local tt sources\n"),
        "master": ("master", "master sources\n"),
        "pull-request": ("pr/42", "pull request sources\n"),
    }
    requested, expected = revisions[revision]
    rc, output = run_command_and_get_output(
        [tt_cmd, "--self", "binary", "install", "tt", requested, "--force"],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert "Downloading tt..." in output
    assert (tmp_path / ".tarantool" / "bin" / "tt").read_text() == expected


@pytest.mark.parametrize("update", [False, True])
def test_install_tt_existing_master(tt_cmd, tmp_path, local_tt_build, update):
    built_binary, build_log = local_tt_build
    repository = tmp_path / "distfiles" / "tt"
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repository, text=True).strip()
    old_commit = ("0" if head[0] != "0" else "1") + head[1:7]
    binary = tmp_path / ".tarantool" / "versions" / "tt" / "tt_master" / "tt"
    binary.parent.mkdir(parents=True)
    old_contents = f"#!/bin/sh\necho '2.0.0.{old_commit}'\n"
    binary.write_text(old_contents)
    binary.chmod(0o755)

    args = [tt_cmd, "--self"]
    if not update:
        args.append("--no-prompt")
    args.extend(["binary", "install", "tt", "master", "--local-repo", "--force"])
    rc, output = run_command_and_get_output(
        args,
        cwd=tmp_path,
        input="y\n" if update else None,
    )
    assert rc == 0, output
    assert (tmp_path / ".tarantool" / "bin" / "tt").resolve() == binary
    if update:
        assert "Found newest commit of tt in master" in output
        assert binary.read_bytes() == built_binary.read_bytes()
        assert build_log.read_text() == "build\n"
    else:
        assert "already exists" in output
        assert binary.read_text() == old_contents
        assert not build_log.exists()
