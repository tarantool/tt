import filecmp
import glob
import itertools
import os
import re
import shutil
import subprocess
import tarfile
from pathlib import Path

import pytest
import yaml

from utils import config_name, run_command_and_get_output

# ##### #
# Tests #
# ##### #

# spell-checker:ignore jrei getgid


def get_arch():
    process = subprocess.Popen(
        ["uname", "-m"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    result = process.communicate()
    return result[0][:-1]


def assert_bundle_structure(path):
    assert not os.path.exists(os.path.join(path, "include"))
    assert not os.path.exists(os.path.join(path, "templates"))


def assert_single_app_env(config):
    assert config["env"]["bin_dir"] == "bin"


def assert_artifacts_env(config):
    assert config["app"]["wal_dir"] == "var/lib"
    assert config["app"]["vinyl_dir"] == "var/lib"
    assert config["app"]["memtx_dir"] == "var/lib"
    assert config["app"]["log_dir"] == "var/log"
    assert config["app"]["run_dir"] == "var/run"


def assert_config(path, checks):
    with open(os.path.join(path, config_name)) as f:
        data = yaml.load(f, Loader=yaml.SafeLoader)
        for check in checks:
            check(data)
    f.close()
    return True


def prepare_tgz_test_cases(tt_cmd) -> list:
    arch = get_arch()
    return [
        {
            "name": "Pack current application",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": [],
            "res_file": f"single_app-0.1.0.0.{arch}.tar.gz",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
                "single_app/bin/tarantool",
                "single_app/bin/tt",
                "single_app/modules/sample.txt",
            ],
            "check_not_exist": [
                "tt.yaml",
                "single_app/tt.yml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Set package name",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "myapp"],
            "res_file": f"myapp-0.1.0.0.{arch}.tar.gz",
            "check_exist": [
                "myapp/config.yaml",
                "myapp/instances.yml",
                "myapp/tt.yaml",
                "myapp/bin/tarantool",
                "myapp/bin/tt",
                "myapp/modules/sample.txt",
            ],
            "check_not_exist": [
                "single_app",
                "myapp/tt.yml",
                "myapp/var/lib/instance001/test.xlog",
                "myapp/var/log/instance001/test.log",
            ],
            "check_env": ["myapp", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Set package version",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--version", "1.2.3"],
            "res_file": f"single_app-1.2.3.{arch}.tar.gz",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
            ],
            "check_not_exist": [
                "single_app/tt.yml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Set output filename",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--filename", "application-package"],
            "res_file": "application-package",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
            ],
            "check_not_exist": [
                "single_app/tt.yml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Exclude binaries",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--without-binaries"],
            "res_file": f"single_app-0.1.0.0.{arch}.tar.gz",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
            ],
            "check_not_exist": [
                "single_app/bin",
                "single_app/tt.yml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Exclude modules",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--without-modules"],
            "res_file": f"single_app-0.1.0.0.{arch}.tar.gz",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
            ],
            "check_not_exist": [
                "single_app/modules",
                "single_app/tt.yml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
        {
            "name": "Include runtime artifacts",
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--all"],
            "res_file": f"single_app-0.1.0.0.{arch}.tar.gz",
            "check_exist": [
                "single_app/config.yaml",
                "single_app/instances.yml",
                "single_app/tt.yaml",
                "single_app/var/lib/instance001/test.xlog",
                "single_app/var/log/instance001/test.log",
            ],
            "check_not_exist": ["single_app/tt.yml", "single_app/var/run"],
            "check_env": ["single_app", assert_single_app_env, assert_artifacts_env],
        },
    ]


@pytest.mark.slow
def test_pack_tgz_table(tt_cmd, tmp_path):
    test_cases = prepare_tgz_test_cases(tt_cmd)

    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        os.path.join(tmp_path, "single_app"),
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    for test_case in test_cases:
        base_dir = os.path.join(tmp_path, test_case["bundle_src"])
        print("BASEDIR: " + base_dir)
        print("ARGS: " + " ".join(test_case["args"]))
        rc, output = run_command_and_get_output(
            [test_case["cmd"], "pack", test_case["pack_type"], *test_case["args"]],
            cwd=base_dir,
            env=dict(os.environ, PWD=base_dir),
        )

        assert rc == 0
        package_file = os.path.join(base_dir, test_case["res_file"])
        print("PACKAGE FILE " + package_file)
        os.system("ls -l " + package_file)
        assert os.path.isfile(package_file)

        # if the bundle was packed with option --filename,
        # it may be packed with no file extension, so rename it
        # for unpacking tar library
        if not package_file.endswith("tar.gz"):
            os.rename(package_file, package_file + ".tar.gz")
            package_file = package_file + ".tar.gz"

        extract_path = os.path.join(base_dir, "tmp")
        os.mkdir(extract_path)

        tar = tarfile.open(package_file)
        tar.extractall(extract_path)
        tar.close()

        assert_bundle_structure(extract_path)

        for file_path in test_case["check_exist"]:
            print("Check exist " + file_path + " in  " + extract_path)
            assert glob.glob(os.path.join(extract_path, file_path))

        for file_path in test_case["check_not_exist"]:
            assert not glob.glob(os.path.join(extract_path, file_path))

        assert_config(
            os.path.join(extract_path, test_case["check_env"][0]),
            test_case["check_env"][1:],
        )

        shutil.rmtree(extract_path)
        os.remove(package_file)


@pytest.mark.slow
def test_pack_tgz_relative_symlinks(tt_cmd, tmp_path):
    tmp_path = os.path.join(tmp_path, "symlinks")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    Path(tmp_path, "file").write_text("content")
    os.symlink("file", os.path.join(tmp_path, "symlink"))

    base_dir = tmp_path
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir,
        env=dict(os.environ, PWD=base_dir),
    )

    assert rc == 0

    package_file = os.path.join(base_dir, "symlinks-0.1.0.0." + get_arch() + ".tar.gz")

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    assert os.path.exists(os.path.join(extract_path, "symlinks", "file"))
    assert os.path.exists(os.path.join(extract_path, "symlinks", "symlink"))


def prepare_deb_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--name", "test_package"],
            "res_file": "test_package_0.1.0.0-1_" + get_arch() + ".deb",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--name", "test_package", "--deps", "tarantool>=1.10", "--deps", "tt=2.0"],
            "res_file": "test_package_0.1.0.0-1_" + get_arch() + ".deb",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--deps", "tarantool>=1.10,tt=2.0"],
            "res_file": "single_app_0.1.0.0-1_" + get_arch() + ".deb",
        },
    ]


def prepare_rpm_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--name", "test_package"],
            "res_file": "test_package-0.1.0.0-1." + get_arch() + ".rpm",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--name", "test_package", "--deps", "tarantool>=1.10", "--deps", "tt=2.0"],
            "res_file": "test_package-0.1.0.0-1." + get_arch() + ".rpm",
        },
        {
            "bundle_src": "single_app",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--deps", "tarantool>=1.10,tt=2.0"],
            "res_file": "single_app-0.1.0.0-1." + get_arch() + ".rpm",
        },
    ]


@pytest.mark.slow
def test_pack_rpm_deb_table(tt_cmd, tmp_path):
    test_cases = prepare_deb_test_cases(tt_cmd)
    test_cases.extend(prepare_rpm_test_cases(tt_cmd))

    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        os.path.join(tmp_path, "single_app"),
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )
    for test_case in test_cases:
        base_dir = os.path.join(tmp_path, test_case["bundle_src"])
        rc, output = run_command_and_get_output(
            [test_case["cmd"], "pack", test_case["pack_type"], *test_case["args"]],
            cwd=base_dir,
            env=dict(os.environ, PWD=base_dir),
        )

        assert rc == 0

        package_file = os.path.join(base_dir, test_case["res_file"])
        assert os.path.exists(package_file)


def test_pack_incorrect_pack_type(tt_cmd, tmp_path):
    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    expected_output = 'invalid argument "de" for "tt pack"'

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "de"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert expected_output in output


def test_pack_nonexistent_modules_directory(tt_cmd, tmp_path):
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "bundle5"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    rc, output = run_command_and_get_output(
        [tt_cmd, "-V", "pack", "tgz"],
        cwd=tmp_path,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert "Skip copying modules from" in output
    assert rc == 0


def prepare_pack_deb_single_app_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "name": "clean",
            "command": ["pack", "deb"],
            "paths": ["tt.yaml", "instances.yml", "config.yaml", "bin/tt", "bin/tarantool"],
        },
    ]


@pytest.mark.docker
def test_pack_deb_single_app(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    test_cases = prepare_pack_deb_single_app_test_cases(tt_cmd)

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    unpacked_dir = os.path.join(base_dir, "unpacked")
    os.mkdir(unpacked_dir)

    for test_case in test_cases:
        cmd = [tt_cmd, *test_case["command"]]

        rc, output = run_command_and_get_output(
            cmd,
            cwd=base_dir,
            env=dict(os.environ, PWD=tmp_path),
        )
        assert rc == 0

        package_file_name = "single_app_0.1.0.0-1_" + get_arch() + ".deb"
        package_file = os.path.join(base_dir, package_file_name)
        assert os.path.isfile(package_file)

        unpacked_pkg_dir = os.path.join(unpacked_dir, test_case["name"])
        os.mkdir(unpacked_pkg_dir)

        rc, output = run_command_and_get_output(
            [
                "docker",
                "run",
                "--rm",
                "--privileged",
                "-t",
                "-v",
                "{0}:/usr/src/".format(base_dir),
                "-v",
                "{0}:/tmp/unpack".format(unpacked_pkg_dir),
                "-w",
                "/usr/src",
                "heywoodlh/systemd:ubuntu-22.04",
                "/bin/bash",
                "-c",
                "/bin/dpkg -i {0}"
                "&& id tarantool "
                " && dpkg -x {0} /tmp/unpack "
                " && chown {1}:{2} /tmp/unpack -R".format(
                    package_file_name,
                    os.getuid(),
                    os.getgid(),
                ),
            ],
        )
        assert rc == 0

        assert re.search(
            r"uid=\d+\(tarantool\) gid=\d+\(tarantool\) groups=\d+\(tarantool\)",
            output,
        )

        with open(
            os.path.join(os.path.dirname(__file__), "systemd_unit_template.txt"),
            mode="r",
        ) as file:
            app_systemd_template = file.read()

        with open(os.path.join(tmp_path, "instantiated_unit.txt"), "w") as f:
            f.write(
                app_systemd_template.format(
                    app="single_app@%i",
                    args="single_app:%i",
                    bundle="single_app",
                ),
            )

        assert filecmp.cmp(
            os.path.join(tmp_path, "instantiated_unit.txt"),
            os.path.join(unpacked_pkg_dir, "usr/lib/systemd/system/single_app@.service"),
            False,
        )

        # Verify Deb package content.
        env_path = os.path.join(unpacked_pkg_dir, "usr", "share", "tarantool", "single_app")
        for path in test_case["paths"]:
            assert os.path.exists(os.path.join(env_path, path))

        for path in ["include", "templates", "distfiles", "modules", "tt.yml"]:
            assert not os.path.exists(os.path.join(env_path, path))


@pytest.mark.docker
def test_pack_rpm_single_app(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    cmd = [tt_cmd, "pack", "rpm"]

    rc, output = run_command_and_get_output(cmd, cwd=base_dir, env=dict(os.environ, PWD=tmp_path))
    assert rc == 0

    package_file_name = "single_app-0.1.0.0-1." + get_arch() + ".rpm"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    unpacked_pkg_dir = os.path.join(tmp_path, "unpacked")
    os.mkdir(unpacked_pkg_dir)

    rc, output = run_command_and_get_output(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            "{0}:/usr/src/".format(base_dir),
            "-v",
            "{0}:/tmp/unpack".format(unpacked_pkg_dir),
            "-w",
            "/usr/src",
            "redhat/ubi9-init:9.7",
            "/bin/bash",
            "-c",
            "rpm -i {0} && id tarantool && rpm2cpio {0} > /tmp/unpack/pkg.cpio".format(
                package_file_name,
            ),
        ],
    )
    assert rc == 0

    assert re.search(r"uid=\d+\(tarantool\) gid=\d+\(tarantool\) groups=\d+\(tarantool\)", output)

    rc, output = run_command_and_get_output(
        ["cpio", "--file", os.path.join(unpacked_pkg_dir, "pkg.cpio"), "-idm"],
        env=dict(os.environ, LANG="en_US.UTF-8", LC_ALL="en_US.UTF-8"),
        cwd=unpacked_pkg_dir,
    )

    assert rc == 0

    with open(
        os.path.join(os.path.dirname(__file__), "systemd_unit_template.txt"),
        mode="r",
    ) as file:
        app_systemd_template = file.read()

    with open(os.path.join(tmp_path, "instantiated_unit.txt"), "w") as f:
        f.write(
            app_systemd_template.format(
                app="single_app@%i",
                args="single_app:%i",
                bundle="single_app",
            ),
        )

    assert filecmp.cmp(
        os.path.join(tmp_path, "instantiated_unit.txt"),
        os.path.join(unpacked_pkg_dir, "usr/lib/systemd/system/single_app@.service"),
        False,
    )

    # Verify Deb package content.
    env_path = os.path.join(unpacked_pkg_dir, "usr", "share", "tarantool", "single_app")
    for path in ["tt.yaml", "instances.yml", "config.yaml", "bin/tt", "bin/tarantool"]:
        assert os.path.exists(os.path.join(env_path, path))

    for path in ["include", "templates", "distfiles", "modules", "tt.yml"]:
        assert not os.path.exists(os.path.join(env_path, path))


@pytest.mark.slow
@pytest.mark.docker
def test_pack_deb_use_docker_tnt_version(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "deb", "--use-docker", "--tarantool-version", "2.7.3"],
        cwd=base_dir,
        env=dict(os.environ, PWD=tmp_path),
    )
    assert rc == 0

    package_file_name = "single_app_0.1.0.0-1_" + get_arch() + ".deb"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            "{0}:/usr/src/".format(base_dir),
            "-w",
            "/usr/src",
            "ubuntu",
            "/bin/bash",
            "-c",
            "/bin/dpkg -i {0} && /usr/share/tarantool/single_app/bin/tarantool --version".format(
                package_file_name,
            ),
        ],
    )
    assert rc == 0
    assert re.search("Tarantool 2.7.3", output)


@pytest.mark.slow
@pytest.mark.docker
def test_pack_rpm_use_docker_wrong_version_format(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--use-docker", "--tarantool-version", "cool.tarantool.version"],
        cwd=base_dir,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert rc == 1


@pytest.mark.slow
@pytest.mark.docker
def test_pack_rpm_use_docker_wrong_version(tt_cmd, tmp_path):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--use-docker", "--tarantool-version", "1.239.239"],
        cwd=base_dir,
        env=dict(os.environ, PWD=tmp_path),
    )

    assert rc == 1


@pytest.mark.slow
@pytest.mark.docker
@pytest.mark.parametrize(
    "preinst, postinst",
    [
        pytest.param(True, False, id="preinst_only"),
        pytest.param(False, True, id="postinst_only"),
        pytest.param(True, True, id="both"),
    ],
)
def test_pack_rpm_with_pre_and_post_inst(tt_cmd, tmp_path, preinst, postinst):
    if shutil.which("docker") is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(["docker", "ps"])
    assert rc == 0

    tmp_path = os.path.join(tmp_path, "single_app")
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    base_dir = tmp_path

    # Compose command line.
    cmd = [tt_cmd, "pack", "rpm"]
    if preinst:
        script = os.path.join(tmp_path, "preinst.sh")
        cmd += ["--preinst", script]
        with open(script, "w") as f:
            f.write("echo 'hello'")
    if postinst:
        script = os.path.join(tmp_path, "postinst.sh")
        cmd += ["--postinst", script]
        with open(script, "w") as f:
            f.write("echo 'bye'")

    rc, output = run_command_and_get_output(cmd, cwd=base_dir, env=dict(os.environ, PWD=tmp_path))
    assert rc == 0

    package_file_name = "single_app-0.1.0.0-1." + get_arch() + ".rpm"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            "{0}:/usr/src/".format(base_dir),
            "-w",
            "/usr/src",
            "redhat/ubi9-init:9.7",
            "/bin/bash",
            "-c",
            "rpm -qp --scripts {0} ".format(package_file_name),
        ],
    )
    assert rc == 0

    if preinst:
        assert (
            """preinstall scriptlet (using /bin/sh):
SYSUSER=tarantool
"""
            in output
        )
        assert "echo 'hello'" in output

    if postinst:
        assert (
            """postinstall scriptlet (using /bin/sh):

echo 'bye'
"""
            in output
        )


@pytest.mark.notarantool
@pytest.mark.slow
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_pack_app_local_tarantool(tt_cmd, tmpdir_with_tarantool, tmp_path):
    shutil.copytree(
        tmpdir_with_tarantool,
        tmp_path,
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        dirs_exist_ok=True,
    )

    build_cmd = [tt_cmd, "create", "single_instance", "--name", "app", "--non-interactive"]
    tt_process = subprocess.Popen(
        build_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    assert tt_process.returncode == 0

    app_dir = os.path.join(tmp_path, "app")
    assert os.path.exists(app_dir)

    build_cmd = [tt_cmd, "pack", "tgz"]
    tt_process = subprocess.Popen(
        build_cmd,
        cwd=app_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    tt_process.wait()
    build_output = tt_process.stdout.read()

    print(build_output)

    assert tt_process.returncode == 0
    assert "Bundle is packed successfully" in build_output


@pytest.mark.slow
def test_pack_ignore(tt_cmd, tmp_path):
    shutil.copytree(
        os.path.join(os.path.dirname(__file__), "test_bundles", "single_app"),
        os.path.join(tmp_path, "single_app"),
        symlinks=True,
        ignore=None,
        copy_function=shutil.copy2,
        ignore_dangling_symlinks=True,
        dirs_exist_ok=True,
    )

    bundle_src = "single_app"
    base_dir = tmp_path / bundle_src

    files_to_ignore = [
        " ",
        "#",
        "#hash_name1",
        "name1",
        "subdir/name1",
        "deep/nested/subdir/name1",
        "subdir2/name1/file",
        "name2",
        "subdir/name3_blabla",  # spell-checker:ignore blabla
        "dir1/file",
        "subdir/dir1/file",
        "dir2/file",
        "subdir/dir3_blabla/file",
        "name11",
        "subdir/name11",
        "deep/nested/subdir/name11",
        "dir/name11/file_bla",
        "dir/name11/file_blabla",
        "dir12/name12",
        "subdir/dir12/name12",
        "deep/nested/subdir/dir12/name12",
    ]

    files_to_pack = [
        "# ",
        "#comment",
        "#hash_name_reincluded",
        "mismatched_name1",
        ".mismatched_name2",
        "subdir/mismatched_name3",
        "deep/nested/subdir/mismatched_name4",
        "name2_reincluded",
        "subdir/name3_reincluded1",
        "dir4/mismatched_dir_name",
        "subdir/as_file/dir1",
        "subdir/mismatched_dir2/file",
        "dir2_reincluded/file",
        "subdir/dir3_reincluded1/filename12",
        "mismatched_parent_dir/name12",
        "deep/nested/mismatched_parent_dir/name12",
    ]

    ignore_patterns = [
        " ",
        "\\  ",
        "#comment",
        "\\#",
        "\\#hash_name*",
        "!#hash_name_reincluded",
        "name1",
        "name[2-3]*",
        "!name2_reincluded",
        "!name3_reincluded[1-9]",
        "dir1/",
        "dir[2-3]*/",
        "!dir2_reincluded/",
        "!dir3_reincluded[1-9]/",
        "**/name11",
        "**/dir12/name12",
    ]

    # Prepare .packignore layout.
    (base_dir / ".packignore").write_text("\n".join(ignore_patterns) + "\n")
    for f in itertools.chain(files_to_ignore, files_to_pack):
        fpath = Path(base_dir, f)
        fpath.parent.mkdir(parents=True, exist_ok=True)
        fpath.write_text("")

    packages_wildcard = os.path.join(base_dir, "*.tar.gz")
    packages = set(glob.glob(packages_wildcard))

    rc, _ = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir,
        env=dict(os.environ, PWD=base_dir),
    )
    assert rc == 0

    # Find the newly generated package.
    new_packages = set(glob.glob(packages_wildcard)) - packages
    assert len(new_packages) == 1
    package_file = Path(next(iter(new_packages)))

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    extract_base_dir = os.path.join(extract_path, bundle_src)
    for file_path in [".packignore", *files_to_ignore]:
        assert not os.path.exists(os.path.join(extract_base_dir, file_path)), (
            f"'{os.path.join(extract_base_dir, file_path)}' unexpectedly exists"
        )
    for file_path in files_to_pack:
        assert os.path.exists(os.path.join(extract_base_dir, file_path)), (
            f"'{os.path.join(extract_base_dir, file_path)}' doesn't exist"
        )
