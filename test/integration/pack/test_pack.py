import os
import shutil
import tarfile

import yaml

from utils import run_command_and_get_output

# ##### #
# Tests #
# ##### #


def assert_bundle_structure(path):
    assert os.path.isfile(os.path.join(path, "tarantool.yaml"))
    assert os.path.isdir(os.path.join(path, "var"))
    assert os.path.isdir(os.path.join(path, "var/run"))
    assert os.path.isdir(os.path.join(path, "var/lib"))
    assert os.path.isdir(os.path.join(path, "var/log"))
    assert os.path.isdir(os.path.join(path, "env/bin"))
    assert os.path.isdir(os.path.join(path, "env/modules"))


def assert_env(path):
    with open(os.path.join(path, 'tarantool.yaml')) as f:
        data = yaml.load(f, Loader=yaml.SafeLoader)
        assert data["tt"]["app"]["instances_enabled"] == "instances_enabled"
        assert data["tt"]["app"]["data_dir"] == "var/lib"
        assert data["tt"]["app"]["bin_dir"] == "env/bin"
        assert data["tt"]["app"]["log_dir"] == "var/log"
        assert data["tt"]["app"]["run_dir"] == "var/run"
        assert data["tt"]["modules"]["directory"] == "env/modules"
    f.close()
    return True


def prepare_tgz_test_cases(tt_cmd) -> list:
    tt_cmd = tt_cmd
    return [
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "test_package"],
            "res_file": "test_package_0.1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--version", "1.0.0"],
            "res_file": "bundle1_1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--version", "1.0.0", "--name", "test_package"],
            "res_file": "test_package_1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--filename", "test_package"],
            "res_file": "test_package",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--with-binaries"],
            "res_file": "bundle1_0.1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--without-binaries"],
            "res_file": "bundle1_0.1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: not os.path.exists(os.path.join(path, "env", "bin",
                                                             "tarantool_bin")),
                lambda path: not os.path.exists(os.path.join(path, "env", "bin",
                                                             "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--all"],
            "res_file": "bundle1_0.1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                               "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                               "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
                lambda path: os.path.exists(os.path.join(path, "var", "lib", "app1",
                                                         "test.snap")),
                lambda path: os.path.exists(os.path.join(path, "var", "lib", "app1",
                                                         "test.xlog")),
                lambda path: os.path.exists(os.path.join(path, "var", "lib", "app2",
                                                         "test.snap")),
                lambda path: os.path.exists(os.path.join(path, "var", "lib", "app2",
                                                         "test.xlog")),
                lambda path: os.path.exists(os.path.join(path, "var", "log", "app1",
                                                         "test.log")),
                lambda path: os.path.exists(os.path.join(path, "var", "log", "app2",
                                                         "test.log")),
            ]
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--app-list", "app2"],
            "res_file": "bundle1_0.1.0.0.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "app2", "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "app2", ".rocks")),
                lambda path: not os.path.exists(os.path.join(path, "app.lua")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "modules",
                                                         "test_module.txt")),
            ]
        },
        {
            "bundle_src": "cartridge_app",
            "cmd": tt_cmd,
            "pack_type": "tgz",
            "args": ["--name", "cartridge_app", "--version", "v2"],
            "res_file": "cartridge_app_v2.tar.gz",
            "check_func": [
                lambda path: os.path.exists(os.path.join(path, "cartridge_app")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tarantool_bin")),
                lambda path: os.path.exists(os.path.join(path, "env", "bin",
                                                         "tt_bin")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "app", "roles", "custom.lua")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "app", "admin.lua")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "cartridge.post-build")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "cartridge.pre-build")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "init.lua")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "instances.yml")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "replicasets.yml")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "failover.yml")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         "myapp-scm-1.rockspec")),
                lambda path: os.path.exists(os.path.join(path, "cartridge_app",
                                                         ".rocks")),
            ]
        },
    ]


def test_pack_tgz_table(tt_cmd, tmpdir):
    test_cases = prepare_tgz_test_cases(tt_cmd)

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
        assert_env(extract_path)

        for check_f in test_case["check_func"]:
            assert check_f(extract_path), test_case["args"]

        shutil.rmtree(extract_path)
        os.remove(package_file)


def test_pack_tgz_missing_app(tt_cmd, tmpdir):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle2")
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--app-list", "unexisting-app"],
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
            "res_file": "test_package.deb",
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
            "res_file": "test_package.deb",
        },
        {
            "bundle_src": "bundle1",
            "cmd": tt_cmd,
            "pack_type": "deb",
            "args": [
                "--deps", "tarantool>=1.10,tt=2.0"],
            "res_file": "bundle.deb",
        },
    ]


def test_pack_deb_table(tt_cmd, tmpdir):
    test_cases = prepare_deb_test_cases(tt_cmd)

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
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle2")
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz", "--app-list", "empty_app"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1

    base_dir = os.path.join(tmpdir, "bundle2")
    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_tgz_empty_enabled(tt_cmd, tmpdir):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle3")

    os.mkdir(os.path.join(base_dir, "generated_dir"))

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 1


def test_pack_tgz_links_to_binaries(tt_cmd, tmpdir):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle4")

    rc, output = run_command_and_get_output(
        [tt_cmd, "pack", "tgz"],
        cwd=base_dir, env=dict(os.environ, PWD=base_dir))

    assert rc == 0

    package_file = os.path.join(base_dir, "bundle4_0.1.0.0.tar.gz")
    assert os.path.isfile(package_file)

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    assert_bundle_structure(extract_path)
    assert_env(extract_path)

    tt_is_link = os.path.islink(os.path.join(extract_path, "env", "bin", "tt"))
    tnt_is_link = os.path.islink(os.path.join(extract_path, "env", "bin", "tarantool"))
    assert not tt_is_link
    assert not tnt_is_link

    shutil.rmtree(extract_path)
    os.remove(package_file)
