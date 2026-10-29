import os
import platform
import re
import shlex
import shutil
import subprocess
import tarfile
import tempfile
import uuid
from pathlib import Path

import pytest
from test_install_tarantool import install_tarantool, local_tarantool_build  # noqa: F401
from test_install_tt import install_tt, local_tt_build  # noqa: F401

from utils import (
    get_tarantool_commit,
    is_tarantool_ee,
    is_valid_tarantool_installed,
    run_command_and_get_output,
)


@pytest.mark.slow
def test_install_tt_noexist_commit(tt_cmd, tmp_path):
    tmp_dir = tempfile.mkdtemp(dir=tmp_path)

    os.makedirs(tmp_dir + "/tt")

    install_cmd = ["git", "init"]
    subprocess.run(
        install_cmd,
        cwd=tmp_dir + "/tt",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        check=True,
    )

    # Install tt.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "--local-repo",
        tmp_dir,
        "2df3077",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process.communicate()
    assert instance_process.returncode != 0, output

    assert re.search(r"Searching in commits", output)
    assert re.search(r"2df3077: unable to get hash info", output)


@pytest.mark.slow
def test_install_tt(tt_cmd, tmp_path):
    # Install latest tt.
    install_cmd = [tt_cmd, "binary", "--scope", "project", "install", "tt"]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process.communicate()
    assert instance_process.returncode == 0, output

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.run(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        timeout=30,
    )
    assert installed_program_process.returncode == 0, installed_program_process.stdout


@pytest.mark.slow
def test_install_tt_specific_commit(tt_cmd, tmp_path):
    # Install specific tt's commit.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "97c7b73",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process.communicate()
    assert instance_process.returncode == 0, output

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.run(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        timeout=30,
    )
    assert installed_program_process.returncode == 0, installed_program_process.stdout


@pytest.mark.slow
def test_wrong_format_hash(tt_cmd, tmp_path):
    # Install specific tt's commit.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "111",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process.communicate()
    assert instance_process.returncode != 0, output
    assert re.search(r"Searching in versions...", output)
    assert re.search(r"Searching in commits...", output)
    assert re.search(
        r"the hash must contain at least 7 characters",
        output,
    )

    # Install specific tt's commit.
    install_cmd_second = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "zzzzzzz",
    ]
    instance_process_second = subprocess.Popen(
        install_cmd_second,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process_second.communicate()
    assert instance_process_second.returncode != 0, output
    assert re.search(r"Searching in commits...", output)
    assert re.search(r"hash has a wrong format", output)


@pytest.mark.slow
@pytest.mark.parametrize("required_ver", ["2.1.2", "v2.2", "1"])
def test_install_tt_specific_version(tt_cmd, tmp_path, required_ver: str):
    # Install latest tt.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        required_ver,
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdown correctly.
    output, _ = instance_process.communicate()
    assert instance_process.returncode == 0, output

    installed_cmd = [tmp_path / ".tarantool" / "bin" / "tt", "version"]
    installed_program_process = subprocess.run(
        installed_cmd,
        cwd=tmp_path / ".tarantool" / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        timeout=30,
    )
    assert installed_program_process.returncode == 0, installed_program_process.stdout


@pytest.mark.slow
def test_install_tarantool_commit(tt_cmd, tmp_path):
    project_dir = tmp_path / "project"
    project_dir.mkdir()
    root = project_dir / ".tarantool"

    # Install specific tarantool's commit.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        "00a9e59",
    ]
    instance_process = subprocess.Popen(
        install_cmd,
        cwd=project_dir,
        stderr=subprocess.STDOUT,
        # Do not use pipe for stdout, if you are not going to read from it.
        # In case of build failure, logs are printed to stdout. It fills pipe buffer and
        # blocks all subsequent stdout write calls in tt, because there is no pipe reader in test.
        stdout=subprocess.DEVNULL,
        text=True,
    )

    # Check that the process was shutdown correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [root / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.run(
        installed_cmd,
        cwd=root / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        timeout=30,
    )

    assert installed_program_process.returncode == 0, installed_program_process.stdout
    assert (root / "include" / "tarantool").is_dir()

    assert is_valid_tarantool_installed(
        root / "bin",
        root / "include",
        str(root / "versions" / "tarantool" / "tarantool_00a9e59" / "tarantool"),
        str(root / "versions" / "tarantool" / "tarantool_00a9e59" / "include" / "tarantool"),
    )


@pytest.mark.skipif(
    is_tarantool_ee(),
    reason="Tarantool CE required",
)
@pytest.mark.slow
@pytest.mark.docker
def test_install_tarantool_in_docker(tt_cmd, tmp_path):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    project_dir = tmp_path / "project"
    project_dir.mkdir()
    root = project_dir / ".tarantool"

    commit = get_tarantool_commit()

    # Install latest tarantool.
    install_cmd = [
        tt_cmd,
        "-V",
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        commit,
        "--use-docker",
    ]
    tt_process = subprocess.run(
        install_cmd,
        cwd=project_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    assert tt_process.returncode == 0

    installed_cmd = [root / "bin" / "tarantool", "-v"]
    installed_program_process = subprocess.run(
        installed_cmd,
        cwd=root / "bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        timeout=30,
    )

    assert installed_program_process.returncode == 0, installed_program_process.stdout

    # spell-checker:ignore objdump
    # Check tarantool glibc version.
    out = subprocess.getoutput(
        "objdump -T "
        + str(root / "bin" / "tarantool")
        + " | grep -o -E 'GLIBC_[.0-9]+' | sort -V | tail -n1",
    )
    assert out == "GLIBC_2.29"

    assert (root / "include" / "tarantool").is_dir()


@pytest.mark.parametrize(
    "args, error",
    [
        (
            ["--local-repo", "distfiles", "tarantool", "--from-build", "build"],
            "unknown flag: --local-repo",
        ),
        (
            ["tarantool", "--from-build", "build", "--local-repo", "distfiles"],
            "group [from-build local-repo]",
        ),
        (
            ["tarantool", "unexpected", "--from-build", "build"],
            "accepts 0 arg(s), received 1",
        ),
        (
            ["tarantool", "--include-dir", "headers"],
            "--include-dir requires --from-build",
        ),
        (
            ["tarantool", "--from-build="],
            "--from-build requires a non-empty directory",
        ),
    ],
)
def test_install_tarantool_from_build_invalid_options(tt_cmd, tmp_path, args, error):
    testdata = os.path.join(os.path.dirname(__file__), "testdata", "tt_basic")
    shutil.copytree(testdata, tmp_path, symlinks=True, dirs_exist_ok=True)

    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "--scope", "project", "install", *args],
        cwd=tmp_path,
    )

    assert rc != 0
    assert error in output
    assert is_valid_tarantool_installed(
        tmp_path / "bin",
        tmp_path / "inc" / "include",
        str(tmp_path / "bin" / "tarantool_2.10.8"),
        str(tmp_path / "inc" / "include" / "tarantool_2.10.8"),
    )


@pytest.mark.parametrize("package_to_exclude", [pytest.param("mage"), pytest.param("git")])
def test_install_tt_fail_exit_code_dependency_check(tt_cmd, tmp_path, package_to_exclude):
    # Create new PATH with excluded packages.
    original_path = os.environ["PATH"]
    new_path = os.pathsep.join(
        filter(
            lambda dir: os.path.isdir(dir) and package_to_exclude not in os.listdir(dir),
            original_path.split(os.pathsep),
        ),
    )

    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "tt",
        "--source-build",
        "master",
    ]
    install_process = subprocess.Popen(
        install_cmd,
        cwd=tmp_path,
        env={"PATH": new_path},
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output, _ = install_process.communicate()
    assert install_process.returncode == 1, output
    assert re.search(rf"Missing packages:.*\b{package_to_exclude}\b", output)


@pytest.mark.slow
@pytest.mark.parametrize(
    "old_tarantool_version, expected_install_msg, is_interactive",
    [
        pytest.param("7192bf6", "Found newest commit of tarantool in master", True),
        pytest.param("master", "tarantool_master version of tarantool already exists", False),
        pytest.param("7192bf6", "tarantool_master version of tarantool already exists", False),
    ],
)
def test_install_tarantool_fetch_latest_version(
    tt_cmd,
    tmp_path,
    old_tarantool_version,
    expected_install_msg,
    is_interactive,
):
    project_dir = tmp_path / "project"
    project_dir.mkdir()
    root = project_dir / ".tarantool"

    # Install not latest version of tarantool by commit hash.
    install_cmd = [
        tt_cmd,
        "binary",
        "--scope",
        "project",
        "install",
        "-f",
        "tarantool",
        "--source-build",
        old_tarantool_version,
        "--dynamic",
    ]
    instance_process_rc, _ = run_command_and_get_output(
        install_cmd,
        cwd=project_dir,
        stdout=subprocess.DEVNULL,
    )
    assert instance_process_rc == 0

    # Rename the entire installation to master to simulate an older master build.
    versions = root / "versions" / "tarantool"
    master = versions / "tarantool_master"
    (versions / ("tarantool_" + old_tarantool_version)).rename(master)
    for link, source in [
        (root / "bin" / "tarantool", master / "tarantool"),
        (root / "include" / "tarantool", master / "include" / "tarantool"),
    ]:
        link.unlink()
        link.symlink_to(source)

    # Installing newest version.
    install_cmd = [tt_cmd]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(
        [
            "binary",
            "--scope",
            "project",
            "install",
            "-f",
            "tarantool",
            "--source-build",
            "master",
            "--dynamic",
        ],
    )

    instance_process_rc, output = run_command_and_get_output(
        install_cmd,
        cwd=project_dir,
        input="y\n" if is_interactive is True else None,
    )
    assert instance_process_rc == 0
    assert expected_install_msg in output


@pytest.mark.slow
@pytest.mark.parametrize(
    "active_symlink, expected_install_msg, is_interactive",
    [
        ("tt_master", "Found newest commit of tt in master", True),
        ("tt_master", "tt_master version of tt already exists.", False),
        ("tt_v1.2.3", "Found newest commit of tt in master", True),
    ],
)
def test_install_tt_fetch_latest_version(
    active_symlink,
    expected_install_msg,
    is_interactive,
    tt_cmd,
    tmp_path,
):
    # Create executable file with 'tt version --commit'
    # functionality to emulate real work of tt. We need
    # the following to avoid a situation where the last
    # git commit in the master branch makes it impossible
    # to detect the latest version which contradicts the test logic.
    root = tmp_path / ".tarantool"
    versions = root / "versions" / "tt"
    for directory, version in [("tt_master", "2.0.0.677234a"), ("tt_v1.2.3", "1.2.3.087ac72")]:
        binary = versions / directory / "tt"
        binary.parent.mkdir(parents=True)
        binary.write_text(f'#!/bin/sh\necho "{version}"\n')
        binary.chmod(0o755)
    (root / "bin").mkdir()
    (root / "bin" / "tt").symlink_to(versions / active_symlink / "tt")

    install_cmd = [tt_cmd]
    if not is_interactive:
        install_cmd.append("--no-prompt")
    install_cmd.extend(["binary", "--scope", "project", "install", "tt", "master"])
    instance_process_rc, install_output = run_command_and_get_output(
        install_cmd,
        cwd=tmp_path,
        input="y\n" if is_interactive is True else None,
    )
    assert instance_process_rc == 0
    assert expected_install_msg in install_output


@pytest.fixture(params=["tt", "tarantool"])
def community_build(request, tt_cmd, tmp_path):
    product = request.param
    artifacts = request.getfixturevalue(f"local_{product}_build")
    install = install_tt if product == "tt" else install_tarantool
    return product, artifacts, lambda **kwargs: install(tt_cmd, tmp_path, **kwargs)


def retained_paths(output):
    paths = re.findall(r"Artifacts can be found at: ([^\n]+)", output)
    return [Path(path.strip()) for path in paths]


@pytest.mark.parametrize("keep_temp", [False, True], ids=["clean", "no-clean"])
@pytest.mark.parametrize("product,version", [("tt", "2.15.0"), ("tarantool", "3.7.1")])
def test_install_community_prebuilt_release(
    tt_cmd,
    tmp_path,
    monkeypatch,
    product,
    version,
    keep_temp,
):
    system, arch = platform.system(), platform.machine().lower()
    supported = arch in ("x86_64", "amd64", "aarch64", "arm64")
    if product == "tt":
        supported = supported and (
            system == "Linux"
            or (
                system == "Darwin"
                and arch
                in (
                    "x86_64",
                    "amd64",
                )
            )
        )
    else:
        supported = supported and system == "Linux" and platform.libc_ver()[0] == "glibc"
    if not supported:
        pytest.skip(f"No prebuilt {product} release for this platform")
    monkeypatch.setenv("HOME", str(tmp_path / "home"))
    monkeypatch.setenv("TMPDIR", str(tmp_path))
    monkeypatch.delenv("TT_CLI_CFG", raising=False)
    flags = ["--no-clean"] if keep_temp else []
    rc, output = run_command_and_get_output(
        [tt_cmd, "binary", "install", product, version, "--scope", "project", *flags],
        cwd=tmp_path,
    )
    assert rc == 0, output
    assert (
        "Downloading tt binary..."
        if product == "tt"
        else "Downloading Tarantool binary and headers..."
    ) in output
    assert f"Building {product}..." not in output
    root = tmp_path / ".tarantool"
    executable = root / "bin" / product
    directory = f"tt_v{version}" if product == "tt" else f"tarantool_{version}"
    installed = root / "versions" / product / directory
    assert executable.resolve(strict=True) == installed / product
    if product == "tt":
        assert not (root / "include").exists()
    else:
        assert (root / "include" / product).resolve(strict=True) == installed / "include" / product
        assert (root / "include" / product / "module.h").is_file()
    paths = retained_paths(output)
    if keep_temp:
        assert len(paths) == 1, output
        assert paths[0].is_dir() and paths[0].parent == tmp_path
        if product == "tt":
            archives = list(paths[0].glob("tt-package-*/package-*.tar.gz"))
            assert len(archives) == 1
            with tarfile.open(archives[0]) as archive:
                assert archive.extractfile("tt").read() == executable.read_bytes()
        else:
            archives = list(paths[0].glob("tarantool-package-*/*/package-*.rpm"))
            assert len(archives) == 2
            assert all(archive.read_bytes().startswith(b"\xed\xab\xee\xdb") for archive in archives)
            extracted = list(paths[0].glob("tarantool-package-*/runtime/usr/bin/tarantool"))
            assert len(extracted) == 1
            assert extracted[0].read_bytes() == executable.read_bytes()
    else:
        assert paths == []
        assert not list(tmp_path.glob(f"{product}_install*"))
    rc, output = run_command_and_get_output(
        [executable, "version" if product == "tt" else "--version"],
        cwd=tmp_path,
    )
    assert rc == 0, output


@pytest.mark.parametrize("keep_temp", [False, True], ids=["clean", "no-clean"])
@pytest.mark.parametrize("verbose", [False, True], ids=["quiet", "verbose"])
def test_install_community_source_artifacts(
    community_build,
    tmp_path,
    monkeypatch,
    keep_temp,
    verbose,
):
    product, artifacts, install = community_build
    monkeypatch.setenv("TMPDIR", str(tmp_path))
    builder = tmp_path / "tools" / ("mage" if product == "tt" else "make")
    builder.write_text(builder.read_text() + "echo community-build-output-marker\n")
    flags = (["--no-clean"] if keep_temp else []) + (["--verbose"] if verbose else [])
    rc, output = install(flags=flags)
    assert rc == 0, output
    assert ("community-build-output-marker" in output) == verbose
    assert (tmp_path / ".tarantool" / "bin" / product).read_bytes() == artifacts[0].read_bytes()
    paths = retained_paths(output)
    if keep_temp:
        assert len(paths) == 1, output
        source = paths[0]
        assert source.parent == tmp_path
        assert (source / ".git").is_dir()
        assert (source / "README").read_text() == f"local {product} sources\n"
        built = (
            source / "tt"
            if product == "tt"
            else (source / "static-build/build/tarantool-prefix/bin/tarantool")
        )
        assert built.read_bytes() == artifacts[0].read_bytes()
        if product == "tarantool":
            assert (built.parent.parent / "include/tarantool/module.h").read_bytes() == (
                artifacts[1].read_bytes()
            )
    else:
        assert paths == []
        assert not list(tmp_path.glob(f"{product}_install*"))


@pytest.mark.parametrize("failure", ["version", "tmp", "checkout", "build"])
def test_install_community_failed_reinstall_preserves_installation(
    community_build,
    tmp_path,
    monkeypatch,
    failure,
):
    product, artifacts, install = community_build
    rc, output = install()
    assert rc == 0, output
    root = tmp_path / ".tarantool"
    active = root / "bin" / product
    original_link, original_binary = active.readlink(), active.read_bytes()
    if product == "tarantool":
        headers = root / "include" / product
        original_headers_link = headers.readlink()
        original_header = (headers / "module.h").read_bytes()
    artifacts[0].write_text("replacement executable must not be installed\n")
    if product == "tarantool":
        artifacts[1].write_text("replacement headers must not be installed\n")
    flags = ["--reinstall"]
    if failure == "tmp":
        monkeypatch.setenv("TMPDIR", str(tmp_path / "missing-tmp"))
    elif failure == "checkout":
        git = tmp_path / "tools/git"
        git.write_text(
            '#!/bin/sh\n[ "$1" != checkout ] || exit 1\n'
            f'exec {shlex.quote(shutil.which("git"))} "$@"\n',
        )
        git.chmod(0o755)
    elif failure == "build":
        builder = "mage" if product == "tt" else "make"
        (tmp_path / "tools" / builder).write_text("#!/bin/sh\nexit 1\n")
    rc, output = install(flags=flags, **({"version": "0" * 40} if failure == "version" else {}))
    assert rc != 0, output
    assert active.readlink() == original_link
    assert active.resolve(strict=True) == original_link
    assert active.read_bytes() == original_binary
    assert os.access(active, os.X_OK)
    if product == "tarantool":
        assert headers.readlink() == original_headers_link
        assert (headers / "module.h").read_bytes() == original_header
    assert "source .env/activate.sh" not in output


@pytest.mark.skipif(platform.system() != "Linux", reason="Controlled Linux dependency check")
@pytest.mark.parametrize("force", [False, True])
def test_install_tarantool_force_missing_dependencies(
    tt_cmd,
    tmp_path,
    request,
    monkeypatch,
    force,
):
    distro = platform.freedesktop_os_release().get("PRETTY_NAME", "")
    if not any(name in distro for name in ["Ubuntu", "Debian"]):
        pytest.skip("Dependency test requires Debian/Ubuntu package checks")
    built_binary, _, build_log = request.getfixturevalue("local_tarantool_build")
    tools = tmp_path / "tools"
    # gcc and package managers are intentionally unavailable, while the fake build
    # needs only these tools. The same PATH must succeed when --force skips checks.
    for name in ["git", "cp", "chmod", "mkdir", "basename", "sed", "uname", "cat"]:
        (tools / name).symlink_to(shutil.which(name))
    monkeypatch.setenv("PATH", str(tools))
    rc, output = install_tarantool(tt_cmd, tmp_path, force=force)
    if force:
        assert rc == 0, output
        assert "Checking dependencies..." not in output
        assert (tmp_path / ".tarantool/bin/tarantool").read_bytes() == built_binary.read_bytes()
        assert build_log.read_text() == "build\n"
    else:
        assert rc != 0, output
        assert "Missing packages:" in output
        assert not (tmp_path / ".tarantool").exists()
        assert not build_log.exists()


@pytest.fixture(scope="module")
def scope_install_image():
    if platform.system() != "Linux" or platform.machine().lower() not in (
        "x86_64",
        "amd64",
        "aarch64",
        "arm64",
    ):
        pytest.skip("Container scope installation requires a Linux amd64/arm64 tt binary")
    if shutil.which("docker") is None:
        pytest.skip("Docker is not installed")
    probe = subprocess.run(["docker", "info"], capture_output=True, timeout=30)
    if probe.returncode != 0:
        pytest.skip("Docker daemon is not available")

    image = f"tt-install-scope:{uuid.uuid4().hex}"
    dockerfile = (
        "FROM ubuntu:20.04\n"
        "RUN apt-get update && apt-get install -y --no-install-recommends "
        "ca-certificates git && rm -rf /var/lib/apt/lists/*\n"
        "RUN mkdir -p /work /home/tt-test && chown 1000:1000 /work /home/tt-test\n"
        "ENV HOME=/home/tt-test\n"
        "WORKDIR /work\n"
    )
    try:
        built = subprocess.run(
            ["docker", "build", "--tag", image, "-"],
            input=dockerfile,
            capture_output=True,
            text=True,
            timeout=600,
        )
        assert built.returncode == 0, built.stdout + built.stderr
        yield image
    finally:
        subprocess.run(["docker", "image", "rm", image], capture_output=True, timeout=30)


@pytest.mark.docker
@pytest.mark.parametrize(
    "scope,root,uid",
    [
        ("project", "/work/.tarantool", "1000"),
        ("user", "/home/tt-test/.local/share/tarantool", "1000"),
        ("system", "/opt/tarantool", "0"),
    ],
)
def test_install_scope_lifecycle_in_docker(tt_cmd, scope_install_image, scope, root, uid):
    # All installation directories belong to the disposable container. Downloads
    # use the real GitHub tag list and RWS packages, as a normal install does.
    script = r"""
set -o pipefail
scope=$1
root=$2
test "$(id -u)" = "$3"
binary() { /tt binary --scope "$scope" "$@"; }
assert_active() {
    expected="$root/versions/tarantool/tarantool_$1"
    test "$(readlink -f "$root/bin/tarantool")" = "$expected/tarantool"
    test "$(readlink -f "$root/include/tarantool")" = "$expected/include/tarantool"
    test -s "$root/include/tarantool/module.h"
    "$root/bin/tarantool" --version
}
assert_scope_isolated() {
    for path in /work/.tarantool /home/tt-test/.local/share/tarantool /opt/tarantool; do
        if [ "$path" != "$root" ]; then test ! -e "$path"; fi
    done
}

for version in 3.6.0 3.7.1; do
    output=$(binary install tarantool "$version" 2>&1)
    printf '%s\n' "$output"
    printf '%s\n' "$output" | grep -F 'Downloading Tarantool binary and headers...'
    assert_active "$version"
    assert_scope_isolated
done
listing=$(binary list)
printf '%s\n' "$listing" | grep -F '3.6.0'
printf '%s\n' "$listing" | grep -F '3.7.1 [active]'

if [ "$scope" = project ]; then
    printf '%s\n' "$output" | grep -F 'source .env/activate.sh'
    source .env/activate.sh
else
    test ! -e .env
    printf '%s\n' "$output" | grep -F 'add this directory to your PATH:'
    printf '%s\n' "$output" | grep -F "$root/bin"
    export PATH="$root/bin:$PATH"
fi
test "$(command -v tarantool)" = "$root/bin/tarantool"
tarantool --version

binary switch tarantool 3.6.0
assert_active 3.6.0
binary list | grep -F '3.6.0 [active]'

binary uninstall tarantool 3.6.0
test ! -e "$root/versions/tarantool/tarantool_3.6.0"
assert_active 3.7.1
listing=$(binary list)
printf '%s\n' "$listing" | grep -F '3.7.1 [active]'
if printf '%s\n' "$listing" | grep -F '3.6.0'; then exit 1; fi

binary uninstall tarantool
test ! -e "$root/versions/tarantool/tarantool_3.7.1"
test ! -e "$root/bin/tarantool" && test ! -L "$root/bin/tarantool"
test ! -e "$root/include/tarantool" && test ! -L "$root/include/tarantool"
if listing=$(binary list 2>&1); then
    printf 'Expected an empty scope, got: %s\n' "$listing" >&2
    exit 1
fi
printf '%s\n' "$listing" | grep -F 'there are no binaries installed in the selected scope'
assert_scope_isolated
"""
    container = f"tt-install-{scope}-{uuid.uuid4().hex}"
    try:
        result = subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "--name",
                container,
                "--user",
                f"{uid}:{uid}",
                "--mount",
                f"type=bind,source={tt_cmd},target=/tt,readonly",
                "--env",
                "NO_COLOR=1",
                "--env",
                "XDG_DATA_HOME=",
                scope_install_image,
                "bash",
                "-euxc",
                script,
                "scope-test",
                scope,
                root,
                uid,
            ],
            capture_output=True,
            text=True,
            timeout=300,
        )
        assert result.returncode == 0, result.stdout + result.stderr
    finally:
        subprocess.run(["docker", "rm", "--force", container], capture_output=True, timeout=30)
