import os
import re
import shutil
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
    assert os.path.isdir(os.path.join(path, "var"))
    assert os.path.isdir(os.path.join(path, "var/run"))
    assert os.path.isdir(os.path.join(path, "var/lib"))
    assert os.path.isdir(os.path.join(path, "var/log"))
    assert os.path.isdir(os.path.join(path, "bin"))
    assert os.path.isdir(os.path.join(path, "modules"))


def assert_env(path):
    with open(os.path.join(path, config_name)) as f:
        data = yaml.load(f, Loader=yaml.SafeLoader)
        assert data["tt"]["app"]["instances_enabled"] == "instances.enabled"
        assert data["tt"]["app"]["wal_dir"] == "var/lib"
        assert data["tt"]["app"]["vinyl_dir"] == "var/lib"
        assert data["tt"]["app"]["memtx_dir"] == "var/lib"
        assert data["tt"]["app"]["bin_dir"] == "bin"
        assert data["tt"]["app"]["log_dir"] == "var/log"
        assert data["tt"]["app"]["run_dir"] == "var/run"
        assert data["tt"]["modules"]["directory"] == "modules"
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
            "res_file": "test_package-0.1.0.0." + get_arch() + ".tar.gz",
            "check_exist": [
                os.path.join("app2", "init.lua"),
                os.path.join("app.lua"),
                os.path.join("bin", "tarantool"),
                os.path.join("bin", "tt"),
                os.path.join("modules", "test_module.txt"),
            ],
            "check_not_exist": [],
        },
        {
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
        },
        {
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
        },
        {
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
                os.path.join("var", "lib", "app1", "test.snap"),
                os.path.join("var", "lib", "app1", "test.xlog"),
                os.path.join("var", "lib", "app1", "test.vylog"),
                os.path.join("var", "lib", "app2", "test.snap"),
                os.path.join("var", "lib", "app2", "test.xlog"),
                os.path.join("var", "log", "app1", "test.log"),
                os.path.join("var", "log", "app2", "test.log"),
            ],
            "check_not_exist": [
                os.path.join("var", "lib", "app2", "test.vylog"),
            ],
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
                os.path.join("var", "lib", "app1", "test.snap"),
                os.path.join("var", "lib", "app1", "test.xlog"),
                os.path.join("var", "lib", "app1", "test.vylog"),
                os.path.join("var", "lib", "app2", "test.snap"),
                os.path.join("var", "lib", "app2", "test.xlog"),
                os.path.join("var", "lib", "app2", "test.vylog"),
                os.path.join("var", "log", "app1", "test.log"),
                os.path.join("var", "log", "app2", "test.log"),
            ],
            "check_not_exist": [
                os.path.join("var", "lib", "memtx", "app1", "test.snap"),
                os.path.join("var", "lib", "wal", "app1", "test.xlog"),
                os.path.join("var", "lib", "vinyl", "app1", "test.vylog"),
                os.path.join("var", "lib", "memtx", "app2", "test.snap"),
                os.path.join("var", "lib", "wal", "app2", "test.xlog"),
                os.path.join("var", "lib", "vinyl", "app2", "test.vylog"),
            ],
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

        assert_bundle_structure(extract_path)
        assert_env(extract_path)

        for file_path in test_case["check_exist"]:
            assert os.path.exists(os.path.join(extract_path, file_path))

        for file_path in test_case["check_not_exist"]:
            assert not os.path.exists(os.path.join(extract_path, file_path))

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

    package_file = os.path.join(base_dir, "bundle4-0.1.0.0." + get_arch() + ".tar.gz")
    assert os.path.isfile(package_file)

    extract_path = os.path.join(base_dir, "tmp")
    os.mkdir(extract_path)

    tar = tarfile.open(package_file)
    tar.extractall(extract_path)
    tar.close()

    assert_bundle_structure(extract_path)
    assert_env(extract_path)

    tt_is_link = os.path.islink(os.path.join(extract_path, "bin", "tt"))
    tnt_is_link = os.path.islink(os.path.join(extract_path, "bin", "tarantool"))
    assert not tt_is_link
    assert not tnt_is_link

    shutil.rmtree(extract_path)
    os.remove(package_file)


def test_pack_incorrect_pack_type(tt_cmd, tmpdir):
    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
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


@pytest.mark.slow
def test_pack_deb(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle1")

    cmd = [tt_cmd, "pack", "deb"]

    rc, output = run_command_and_get_output(
        cmd,
        cwd=base_dir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    package_file_name = "bundle1_0.1.0.0-1_" + get_arch() + ".deb"
    package_file = os.path.join(base_dir, package_file_name)
    assert os.path.isfile(package_file)

    rc, output = run_command_and_get_output(['docker', 'run', '--rm', '-v',
                                             '{0}:/usr/src/'.format(base_dir),
                                             '-w', '/usr/src',
                                             'jrei/systemd-ubuntu',
                                             '/bin/bash', '-c',
                                             '/bin/dpkg -i {0} && '
                                             'ls /usr/share/tarantool/bundle1 '
                                             '&& systemctl list-unit-files | grep bundle1'
                                            .format(package_file_name)])

    assert re.search(r'Preparing to unpack {0}'.format(package_file_name), output)
    assert re.search(r'Unpacking bundle \(0\.1\.0\)', output)
    assert re.search(r'Setting up bundle \(0\.1\.0\)', output)

    installed_package_paths = ['app.lua', 'app2', 'instances.enabled',
                               config_name, 'var']
    systemd_units = ['bundle1@.service', 'bundle1.service']

    for path in installed_package_paths:
        assert re.search(path, output)
    for unit in systemd_units:
        assert re.search(unit, output)
    assert rc == 0


@pytest.mark.slow
def test_pack_rpm(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle1")

    cmd = [tt_cmd, "pack", "rpm"]

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
                                             'rpm -i {0} '
                                             '&& ls /usr/share/tarantool/bundle1 '
                                             '&& systemctl list-unit-files | grep bundle1'
                                            .format(package_file_name)])
    installed_package_paths = ['app.lua', 'app2', 'instances.enabled',
                               config_name, 'var']
    systemd_units = ['bundle1@.service', 'bundle1.service']

    for path in installed_package_paths:
        assert re.search(path, output)
    for unit in systemd_units:
        assert re.search(unit, output)

    assert rc == 0


@pytest.mark.slow
def test_pack_rpm_use_docker(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle1")

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
    installed_package_paths = ['app.lua', 'app2', 'instances.enabled',
                               config_name, 'var']
    systemd_paths = ['bundle1%.service', 'bundle1.service']

    for path in installed_package_paths:
        re.search(path, output)
    for path in systemd_paths:
        re.search(path, output)

    assert rc == 0


@pytest.mark.slow
def test_pack_deb_use_docker(tt_cmd, tmpdir):
    if shutil.which('docker') is None:
        pytest.skip("docker is not installed in this system")

    # check if docker daemon is up
    rc, _ = run_command_and_get_output(['docker', 'ps'])
    assert rc == 0

    shutil.copytree(os.path.join(os.path.dirname(__file__), "test_bundles"),
                    tmpdir, symlinks=True, ignore=None,
                    copy_function=shutil.copy2, ignore_dangling_symlinks=True,
                    dirs_exist_ok=True)

    base_dir = os.path.join(tmpdir, "bundle1")

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
