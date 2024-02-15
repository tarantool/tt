import glob
import os
import re
import shutil
import stat
import subprocess
import tarfile

import pytest
import yaml

from utils import config_name, run_command_and_get_output

# ##### #
# Tests #
# ##### #


def get_arch():
    process = subprocess.Popen(["uname", "-m"],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    result = process.communicate()
    return result[0][:-1]


def assert_bundle_structure(path):
    assert os.path.isfile(os.path.join(path, config_name))
    assert os.path.isdir(os.path.join(path, "bin"))
    assert os.path.isdir(os.path.join(path, "modules"))


def assert_bundle_structure_compat(path):
    assert not os.path.isfile(os.path.join(path, config_name))
    assert not os.path.isdir(os.path.join(path, "var"))
    assert not os.path.isdir(os.path.join(path, "var/run"))
    assert not os.path.isdir(os.path.join(path, "var/lib"))
    assert not os.path.isdir(os.path.join(path, "var/log"))
    assert not os.path.isdir(os.path.join(path, "bin"))
    assert not os.path.isdir(os.path.join(path, "modules"))


def assert_env(path, artifacts_in_separated_dirs, compat_mode):
    with open(os.path.join(path, config_name)) as f:
        data = yaml.load(f, Loader=yaml.SafeLoader)
        if compat_mode:
            assert data["env"]["instances_enabled"] == "."
            assert data["env"]["bin_dir"] == "."
        else:
            assert data["env"]["instances_enabled"] == "instances.enabled"
            assert data["env"]["bin_dir"] == "bin"
        if artifacts_in_separated_dirs:
            assert data["app"]["wal_dir"] == "var/wal"
            assert data["app"]["vinyl_dir"] == "var/vinyl"
            assert data["app"]["memtx_dir"] == "var/snap"
        else:
            assert data["app"]["wal_dir"] == "var/lib"
            assert data["app"]["vinyl_dir"] == "var/lib"
            assert data["app"]["memtx_dir"] == "var/lib"
        assert data["app"]["log_dir"] == "var/log"
        assert data["app"]["run_dir"] == "var/run"
        assert data["modules"]["directory"] == "modules"
    f.close()
    return True


def prepare_tgz_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "name": "Test --name option.",
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "test_package"],
            "res_file": "test_package-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
                os.path.join("instances.enabled", "app1"),
            ],
            "check_not_exist": [
                os.path.join("app2", "var", "run"),
                os.path.join("app2", "var", "log"),
                os.path.join("app2", "var", "lib"),
                os.path.join("app1"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "name": "Test --version option.",
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--version", "1.0.0"],
            "res_file": "bundle1-1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "name": "Test --version and --name options.",
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--version", "1.0.0", "--name", "test_package"],
            "res_file": "test_package-1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "name": "Test --name option.",
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--with-binaries"],
            "res_file": "bundle1-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--without-modules"],
            "res_file": "bundle1-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--without-binaries"],
            "res_file": "bundle1-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle8",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--cartridge-compat"],
            "app_name": "app_name",
            "res_file": "app_name-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app_name", "VERSION"),
                os.path.join("app_name", "VERSION.lua"),
                os.path.join("app_name", "tt.yaml"),
            ],
            "check_not_exist": [
                os.path.join("bin"),
                os.path.join("instances.enabled"),
                os.path.join("modules"),
                os.path.join("var"),
                os.path.join("tt.yaml"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--cartridge-compat", "--app-list", "app2"],
            "app_name": "app2",
            "res_file": "app2-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "VERSION"),
                os.path.join("app2", "VERSION.lua"),
                os.path.join("app2", "tt.yaml"),
            ],
            "check_not_exist": [
                os.path.join("app1"),
                os.path.join("app"),
                os.path.join("bin"),
                os.path.join("instances_enabled"),
                os.path.join("modules"),
                os.path.join("var"),
                os.path.join("tt.yaml"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle9",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--cartridge-compat"],
            "app_name": "bundle9",
            "res_file": "bundle9-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("bundle9", "init.lua"),
                os.path.join("bundle9", "tt.yaml"),
                os.path.join("bundle9", "VERSION"),
                os.path.join("bundle9", "VERSION.lua"),
            ],
            "check_not_exist": [
                os.path.join("bin"),
                os.path.join("instances_enabled"),
                os.path.join("modules"),
                os.path.join("var"),
                os.path.join("tt.yaml"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--all"],
            "res_file": "bundle1-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
                os.path.join("instances.enabled", "app1", "var", "lib", "app1", "test.xlog"),
                os.path.join("instances.enabled", "app1", "var", "log", "app1", "test.log"),

                os.path.join("instances.enabled", "app2", "var", "lib", "inst1", "test.vylog"),
                os.path.join("instances.enabled", "app2", "var", "lib", "inst1", "test.snap"),
                os.path.join("instances.enabled", "app2", "var", "lib", "inst1", "test.xlog"),

                os.path.join("instances.enabled", "app2", "var", "lib", "inst2", "test.xlog"),
                os.path.join("instances.enabled", "app2", "var", "lib", "inst2", "test.snap"),

                os.path.join("instances.enabled", "app2", "var", "log", "inst1", "test.log"),
                os.path.join("instances.enabled", "app2", "var", "log", "inst2", "test.log"),
            ],
            "check_not_exist": [
                "var",
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle_with_different_data_dirs",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--all"],
            "res_file": "bundle_with_different_data_dirs-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
                os.path.join("instances.enabled", "app1", "var", "vinyl", "app1"),
                os.path.join("instances.enabled", "app1", "var", "snap", "app1"),
                os.path.join("instances.enabled", "app1", "var", "wal", "app1"),
                os.path.join("instances.enabled", "app1", "var", "log", "app1", "tt.log"),

                os.path.join("app2", "var", "vinyl", "app2", "test.vylog"),
                os.path.join("app2", "var", "snap", "app2", "test.snap"),
                os.path.join("app2", "var", "wal", "app2", "test.xlog"),
                os.path.join("app2", "var", "log", "app2", "tt.log"),
            ],
            "check_not_exist": [
                os.path.join("instances.enabled", "app1", "var", "lib"),
                os.path.join("instances.enabled", "app1", "var", "memtx"),
                os.path.join("instances.enabled", "app1", "var", "run"),
                os.path.join("app2", "var", "lib", "memtx"),
                os.path.join("app2", "var", "lib", "wal"),
                os.path.join("var"),
            ],
            "artifacts_in_separated_dir": True,
        },
        {
            "bundle_src": "bundle_with_git_files",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--all"],
            "res_file": "bundle_with_git_files-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),
                os.path.join("app1.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [
                os.path.join(".git"),
                os.path.join("app2", ".git"),
                os.path.join("app2", ".github"),
                os.path.join("app2", ".gitignore"),
                os.path.join("app2", ".gitmodules"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--app-list", "app2"],
            "res_file": "bundle1-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app2", ".rocks"),

                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [
                os.path.join("app.lua"),
            ],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "cartridge_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "cartridge_app", "--version", "v2"],
            "res_file": "cartridge_app-v2." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("cartridge_app"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("cartridge_app", "app", "roles", "custom.lua"),
                os.path.join("cartridge_app", "app", "admin.lua"),
                os.path.join("cartridge_app", "cartridge.post-build"),
                os.path.join("cartridge_app", "cartridge.pre-build"),
                os.path.join("cartridge_app", "init.lua"),
                os.path.join("cartridge_app", "instances.yml"),
                os.path.join("cartridge_app", "replicasets.yml"),
                os.path.join("cartridge_app", "failover.yml"),
                os.path.join("cartridge_app", "myapp-scm-1.rockspec"),
                os.path.join("cartridge_app", ".rocks"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "cartridge_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "cartridge_app", "--version", "v2"],
            "res_file": "cartridge_app-v2." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("cartridge_app"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("cartridge_app", "app", "roles", "custom.lua"),
                os.path.join("cartridge_app", "app", "admin.lua"),
                os.path.join("cartridge_app", "cartridge.post-build"),
                os.path.join("cartridge_app", "cartridge.pre-build"),
                os.path.join("cartridge_app", "init.lua"),
                os.path.join("cartridge_app", "instances.yml"),
                os.path.join("cartridge_app", "replicasets.yml"),
                os.path.join("cartridge_app", "failover.yml"),
                os.path.join("cartridge_app", "myapp-scm-1.rockspec"),
                os.path.join("cartridge_app", ".rocks"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": False,
        },
        {
            "bundle_src": "bundle6",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "bundle6", "--version", "v1", "--all"],
            "res_file": "bundle6-v1." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("instances.enabled", "app.lua"),
                os.path.join("instances.enabled", "app", "var", "wal", "app", "artifact_wal"),
                os.path.join("instances.enabled", "app", "var", "vinyl", "app", "artifact_vinyl"),
                os.path.join("instances.enabled", "app", "var", "snap", "app", "artifact_memtx"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": True,
        },
        {
            "bundle_src": "bundle7",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "bundle7", "--version", "v1", "--all"],
            "res_file": "bundle7-v1." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("instances.enabled", "app.lua"),
                os.path.join("instances.enabled", "app", "var", "wal", "app", "artifact_wal"),
                os.path.join("instances.enabled", "app", "var", "vinyl", "app", "artifact_vinyl"),
                os.path.join("instances.enabled", "app", "var", "snap", "app", "artifact_memtx"),
            ],
            "check_not_exist": [],
            "artifacts_in_separated_dir": True,
        },
    ]


@pytest.mark.slow
def test_pack_tgz_table(tt_cmd, tmpdir):
    test_cases = prepare_tgz_test_cases(tt_cmd)

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    for test_case in test_cases:
        base_dir = os.path.join(tmpdir, test_case["bundle_src"])
        print("BASEDIR: " + base_dir)
        print("ARGS: " + " ".join(test_case["args"]))
        rc, output = run_command_and_get_output(
            [test_case["cmd"], "pack", test_case["pack_type"], *test_case["args"]],
            cwd=base_dir, env=dict(os.environ, PWD=base_dir))

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

        if "--cartridge-compat" in test_case["args"]:
            assert_bundle_structure_compat(extract_path)
            assert_env(os.path.join(extract_path, test_case["app_name"]),
                       test_case["artifacts_in_separated_dir"], True)
        else:
            assert_bundle_structure(extract_path)
            assert_env(extract_path, test_case["artifacts_in_separated_dir"], False)

        if "--without-modules" in test_case["args"]:
            assert not os.listdir(os.path.join(extract_path, "modules"))

        for file_path in test_case["check_exist"]:
            print("Check exist " + file_path + " in  "+extract_path)
            assert glob.glob(os.path.join(extract_path, file_path))

        for file_path in test_case["check_not_exist"]:
            assert not glob.glob(os.path.join(extract_path, file_path))

        shutil.rmtree(extract_path)
        os.remove(package_file)


def test_pack_tgz_missing_app(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle2")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle2"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--app-list", "unexisting-app"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


@pytest.mark.slow
def test_pack_tgz_files_with_compat(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle8")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle8"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 0

    package_file = os.path.join(base_dir, "app_name-0.1.0.0." + get_arch() + ".tar.gz")

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    assert len([name for name in os.listdir(extract_path)]) == 1
    assert os.path.exists(os.path.join(extract_path, "app_name", "tt"))
    assert os.path.exists(os.path.join(extract_path, "app_name", "tarantool"))
    assert os.path.exists(os.path.join(extract_path, "app_name", "tt.yaml"))
    assert os.path.exists(os.path.join(extract_path, "app_name", "init.lua"))
    assert os.path.exists(os.path.join(extract_path, "app_name", "VERSION"))
    assert os.path.exists(os.path.join(extract_path, "app_name", "VERSION.lua"))


@pytest.mark.slow
def test_pack_tgz_git_version_compat(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle9")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle9"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        ["git", "init"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "add", "*"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "config", "user.email", "\"none\""],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0
    rc, output = run_command_and_get_output(
        ["git", "config", "user.name", "\"none\""],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "commit", "-m", "commit"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "tag", "1.2.3"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    package_file = os.path.join(base_dir, "bundle9-1.2.3.0." + get_arch() + ".tar.gz")
    assert os.path.isfile(package_file)


@pytest.mark.slow
def test_pack_tgz_git_version_compat_with_instances(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    app_dir = os.path.join(base_dir, "app2")

    rc, output = run_command_and_get_output(
        ["git", "init"],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "add", "*"],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "config", "user.email", "\"none\""],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0
    rc, output = run_command_and_get_output(
        ["git", "config", "user.name", "\"none\""],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "commit", "-m", "commit"],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        ["git", "tag", "1.2.3"],
        cwd=app_dir, env=dict(os.environ, PWD=app_dir))
    assert rc == 0

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--app-list", "app2", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))
    assert rc == 0

    package_file = os.path.join(base_dir, "app2-1.2.3.0." + get_arch() + ".tar.gz")
    assert os.path.isfile(package_file)


@pytest.mark.slow
def test_pack_tgz_compat_with_binaries(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle8")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle8"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--with-binaries", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 0

    package_file = os.path.join(base_dir, "app_name-0.1.0.0." + get_arch() + ".tar.gz")

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    app_path = os.path.join(extract_path, "app_name")

    assert os.path.isfile(os.path.join(app_path, "tt"))
    assert os.path.isfile(os.path.join(app_path, "tarantool"))

    script = ("cat > tarantool <<EOF\n"
              "#!/bin/bash\n"
              "printf 'Hello World'")
    subprocess.run(script, cwd=app_path, shell=True,
                   env=dict(os.environ, PWD=app_path))
    subprocess.run(["chmod", "+x", "tarantool"], cwd=app_path,
                   env=dict(os.environ, PWD=app_path))

    rc, output = run_command_and_get_output(
            [tt_cmd, "run"],
            cwd=app_path, env=dict(os.environ, PWD=app_path))

    assert rc == 0
    assert output == "Hello World"


def test_pack_tgz_multiple_apps_compat(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_deb_compat(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "dep", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_rpm_compat(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--cartridge-compat"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def prepare_deb_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--name", "test_package"],
            "res_file": "test_package_0.1.0.0-1_" + get_arch() + ".deb",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": [
                "--name", "test_package",
                "--deps", "tarantool>=1.10", "--deps", "tt=2.0"],
            "res_file": "test_package_0.1.0.0-1_" + get_arch() + ".deb",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": [
                "--deps", "tarantool>=1.10,tt=2.0"],
            "res_file": "bundle1_0.1.0.0-1_" + get_arch() + ".deb",
        },
    ]


def prepare_rpm_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--name", "test_package"],
            "res_file": "test_package-0.1.0.0-1." + get_arch() + ".rpm",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": [
                "--name", "test_package",
                "--deps", "tarantool>=1.10", "--deps", "tt=2.0"],
            "res_file": "test_package-0.1.0.0-1." + get_arch() + ".rpm",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "rpm",
            "args": [
                "--deps", "tarantool>=1.10,tt=2.0"],
            "res_file": "bundle1-0.1.0.0-1." + get_arch() + ".rpm",
        },
    ]


@pytest.mark.slow
def test_pack_rpm_deb_table(tt_cmd, tmpdir):
    test_cases = prepare_deb_test_cases(tt_cmd)
    test_cases.extend(prepare_rpm_test_cases(tt_cmd))

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)
    for test_case in test_cases:
        base_dir = os.path.join(tmpdir, test_case["bundle_src"])
        rc, output = run_command_and_get_output(
            [test_case["cmd"], "pack", test_case["pack_type"], *test_case["args"]],
            cwd=base_dir, env=dict(os.environ, PWD=base_dir))

        assert rc == 0

        package_file = os.path.join(base_dir, test_case["res_file"])
        assert os.path.exists(package_file)


def test_pack_tgz_empty_app_directory(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle2")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle2"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--app-list", "empty_app"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1

    base_dir = tmpdir
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_tgz_empty_enabled(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle3")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle3"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    os.mkdir(os.path.join(base_dir, "generated_dir"))

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_tgz_links_to_binaries(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle4")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle4"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 0

    package_file = os.path.join(base_dir, "bundle4-0.1.0.0." + get_arch() + ".tar.gz")
    assert os.path.isfile(package_file)

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    assert_bundle_structure(extract_path)
    assert_env(extract_path, False, False)

    tt_is_link = os.path.islink(os.path.join(extract_path, "bin", "tt"))
    tnt_is_link = os.path.islink(os.path.join(extract_path, "bin", "tarantool"))
    assert not tt_is_link
    assert not tnt_is_link

    shutil.rmtree(extract_path)
    os.remove(package_file)


def test_pack_incorrect_pack_type(tt_cmd, tmpdir):
    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    expected_output = "incorrect combination of command parameters: " \
                      "invalid argument \"de\" for \"tt pack\""

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "de"],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))

    assert expected_output in output


def test_pack_nonexistent_modules_directory(tt_cmd, tmpdir):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle5"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))

    assert re.search(r"Failed to copy modules from",
                     output)
    assert rc == 0


def verify_rpmdeb_package_content(pkg_dir):
    env_path = os.path.join(pkg_dir, 'usr', 'share', 'tarantool', 'bundle1')

    def prefix(suffix):
        return os.path.join(env_path, suffix)

    check_paths = [
        {
            'path': env_path, 'perms': stat.S_IXOTH & stat.S_IROTH
        },
        {
            'path': prefix('app2'), 'perms': stat.S_IXOTH & stat.S_IROTH
        },
        {
            'path': prefix('app2/init.lua'), 'perms': stat.S_IXOTH & stat.S_IROTH
        },
        {
            'path': prefix('instances.enabled/app1'),
            'perms': stat.S_IXOTH & stat.S_IROTH & stat.S_IFDIR
        },
        {
            'path': prefix('instances.enabled/app2'),
            'perms': stat.S_IXOTH & stat.S_IROTH & stat.S_IFLNK
        },
        {
            'path': prefix('instances.enabled/app1.lua'),
            'perms': stat.S_IXOTH & stat.S_IROTH & stat.S_IFLNK
        },
        {
            'path': prefix('app.lua'),
            'perms': stat.S_IXOTH & stat.S_IROTH & stat.S_IFREG
        },
        {
            'path': prefix('tt.yaml'),
            'perms': stat.S_IXOTH & stat.S_IROTH & stat.S_IFREG
        },
        {
            'path': os.path.join(pkg_dir, 'usr', 'lib', 'systemd', 'system',
                                 'app1.service'),
            'perms': stat.S_IFREG
        },
        {
            'path': os.path.join(pkg_dir, 'usr', 'lib', 'systemd', 'system',
                                 'app2@.service'),
            'perms': stat.S_IFREG
        },
        ]
    for unpacked in check_paths:
        assert os.path.exists(unpacked['path'])
        perms = os.stat(unpacked['path'])
        assert (perms.st_mode & unpacked['perms']) == unpacked['perms']


@pytest.mark.slow
def test_pack_deb(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    cmd = [tt_cmd, "pack", "deb"]

    rc, output = run_command_and_get_output(
        cmd,
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1_0.1.0.0-1_" + get_arch() + ".deb"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    unpacked_pkg_dir = os.path.join(tmpdir, 'unpacked')
    os.mkdir(unpacked_pkg_dir)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-v', '{0}:/tmp/unpack'.format(unpacked_pkg_dir),
                                             '-w', '/usr/src',
                                             'jrei/systemd-ubuntu',
                                             '/bin/bash', '-c',
                                             '/bin/dpkg -i {0} && '
                                             'ls /usr/share/tarantool/bundle1 '
                                             '&& systemctl list-unit-files | grep app'
                                             '&& cat /usr/lib/systemd/system/app1.service'
                                             ' /usr/lib/systemd/system/app2@.service '
                                             ' /usr/share/tarantool/bundle1/tt.yaml '
                                             '&& id tarantool '
                                             ' && dpkg -x {0} /tmp/unpack '
                                             ' && chown {1}:{2} /tmp/unpack -R'.
                                             format(package_file_name, os.getuid(), os.getgid())
                                             ])
    assert rc == 0

    assert re.search(r'Preparing to unpack {0}'.format(package_file_name), output)
    assert re.search(r'Unpacking bundle1 \(0\.1\.0\)', output)
    assert re.search(r'Setting up bundle1 \(0\.1\.0\)', output)
    assert re.search(r'uid=\d+\(tarantool\) gid=\d+\(tarantool\) groups=\d+\(tarantool\)', output)

    installed_package_paths = ['app.lua', 'app2', 'instances.enabled', config_name]
    systemd_units = ['app1.service', 'app2@.service']

    for path in installed_package_paths:
        assert re.search(path, output)
    for unit in systemd_units:
        assert re.search(unit, output)
    assert 'wal_dir: /var/lib/tarantool/bundle1' in output
    assert 'log_dir: /var/log/tarantool/bundle1' in output
    assert 'run_dir: /var/run/tarantool/bundle1' in output

    file = open(os.path.join(os.path.dirname(__file__), 'systemd_unit_template.txt'), mode='r')
    app_systemd_template = file.read()
    file.close()

    assert app_systemd_template.format(app='app1', args='app1') in output
    assert app_systemd_template.format(app='app2@%i', args='app2:%i') in output

    # Verify Deb package content.
    verify_rpmdeb_package_content(unpacked_pkg_dir)


@pytest.mark.slow
def test_pack_rpm(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    cmd = [tt_cmd, "pack", "rpm"]

    rc, output = run_command_and_get_output(
        cmd,
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1-0.1.0.0-1." + get_arch() + ".rpm"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    unpacked_pkg_dir = os.path.join(tmpdir, 'unpacked')
    os.mkdir(unpacked_pkg_dir)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-v', '{0}:/tmp/unpack'.format(unpacked_pkg_dir),
                                             '-w', '/usr/src',
                                             'jrei/systemd-fedora',
                                             '/bin/bash', '-c',
                                             'rpm -i {0} '
                                             '&& ls /usr/share/tarantool/bundle1 '
                                             '&& systemctl list-unit-files | grep app'
                                             '&& cat /usr/lib/systemd/system/app1.service'
                                             ' /usr/lib/systemd/system/app2@.service '
                                             ' /usr/share/tarantool/bundle1/tt.yaml '
                                             '&& id tarantool '
                                             '&& rpm2cpio {0} > /tmp/unpack/pkg.cpio'
                                            .format(package_file_name)])
    assert rc == 0
    installed_package_paths = ['app.lua', 'app2', 'instances.enabled', config_name]
    systemd_units = ['app1.service', 'app2@.service']

    assert re.search(r'uid=\d+\(tarantool\) gid=\d+\(tarantool\) groups=\d+\(tarantool\)', output)

    for path in installed_package_paths:
        assert re.search(path, output)
    for unit in systemd_units:
        assert re.search(unit, output)

    file = open(os.path.join(os.path.dirname(__file__), 'systemd_unit_template.txt'), mode='r')
    app_systemd_template = file.read()
    file.close()

    assert app_systemd_template.format(app='app1', args='app1') in output
    assert app_systemd_template.format(app='app2@%i', args='app2:%i') in output

    # Verify Deb package content.
    rc, output = run_command_and_get_output(
        ['cpio',
         '--file', os.path.join(unpacked_pkg_dir, 'pkg.cpio'),
         '-idm'],
        env=dict(os.environ, LANG='en_US.UTF-8', LC_ALL='en_US.UTF-8'),
        cwd=unpacked_pkg_dir)

    assert rc == 0
    verify_rpmdeb_package_content(unpacked_pkg_dir)


@pytest.mark.slow
def test_pack_rpm_use_docker(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--use-docker"],
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1-0.1.0.0-1." + get_arch() + ".rpm"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-w', '/usr/src',
                                             'centos:7',
                                             '/bin/bash', '-c',
                                             'rpm -i {0} && ls /usr/share/tarantool/bundle1 '
                                             '&& ls /usr/lib/systemd/system'
                                            .format(package_file_name)])
    assert rc == 0
    installed_package_paths = ['app.lua', 'app2', 'instances.enabled', config_name]
    systemd_paths = ['app1.service', 'app2@.service']

    for path in installed_package_paths:
        re.search(path, output)
    for path in systemd_paths:
        re.search(path, output)


@pytest.mark.slow
def test_pack_deb_use_docker_tnt_version(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "deb", "--use-docker", "--tarantool-version", "2.7.3"],
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1_0.1.0.0-1_" + get_arch() + ".deb"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-w', '/usr/src',
                                             'ubuntu',
                                             '/bin/bash', '-c',
                                             '/bin/dpkg -i {0} && '
                                             '/usr/share/tarantool/bundle1/bin/tarantool '
                                             '--version'
                                            .format(package_file_name)])
    assert rc == 0
    assert re.search("Tarantool 2.7.3", output)


@pytest.mark.slow
def test_pack_rpm_use_docker_wrong_version_format(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--use-docker", "--tarantool-version",
            "cool.tarantool.version"],
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))

    assert rc == 1


@pytest.mark.slow
def test_pack_rpm_use_docker_wrong_version(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "rpm", "--use-docker", "--tarantool-version",
            "1.239.239"],
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))

    assert rc == 1


@pytest.mark.slow
def test_pack_deb_use_docker(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "deb", "--use-docker"],
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1_0.1.0.0-1_" + get_arch() + ".deb"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-w', '/usr/src',
                                             'ubuntu',
                                             '/bin/bash', '-c',
                                             '/bin/dpkg -i {0} && '
                                             'ls /usr/share/tarantool/bundle1 '
                                             '&& ls /usr/lib/systemd/system'
                                            .format(package_file_name)])
    installed_package_paths = ['app.lua', 'app2', 'instances.enabled',
                               config_name, 'var']
    systemd_paths = ['bundle1%.service', 'bundle1.service']

    for path in installed_package_paths:
        re.search(path, output)
    for path in systemd_paths:
        re.search(path, output)

    assert rc == 0


@pytest.mark.slow
def test_pack_rpm_with_pre_and_post_inst(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    tmpdir = os.path.join(tmpdir, "bundle1")
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles", "bundle1"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = tmpdir

    with open(os.path.join(tmpdir, "preinst.sh"), "w") as pre_inst:
        pre_inst.write("echo 'hello'")
    with open(os.path.join(tmpdir, "postinst.sh"), "w") as post_inst:
        post_inst.write("echo 'bye'")

    cmd = [tt_cmd, "pack", "rpm", "--preinst", os.path.join(tmpdir, "preinst.sh"),
           "--postinst", os.path.join(tmpdir, "postinst.sh")]

    rc, output = run_command_and_get_output(
        cmd,
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1-0.1.0.0-1." + get_arch() + ".rpm"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-w', '/usr/src',
                                             'jrei/systemd-fedora',
                                             '/bin/bash', '-c',
                                             'rpm -qp --scripts {0} '
                                            .format(package_file_name)])
    assert rc == 0

    assert """preinstall scriptlet (using /bin/sh):
SYSUSER=tarantool
""" in output
    assert "echo 'hello'" in output
    assert """postinstall scriptlet (using /bin/sh):

echo 'bye'
""" in output
