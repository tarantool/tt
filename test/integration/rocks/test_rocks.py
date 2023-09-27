import os
import platform
import re
import shutil
import subprocess
import tempfile

import pytest

from utils import config_name, create_tt_config, run_command_and_get_output

# ##### #
# Tests #
# ##### #


def test_rocks_module(tt_cmd, tmpdir):
    create_tt_config(tmpdir, tmpdir)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "help"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert re.search("^Usage: tt rocks", output)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "search", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Rockspecs and source rocks:\n" in output

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert os.path.isfile(f'{tmpdir}/.rocks/share/tarantool/queue/init.lua')

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "doc", "queue", "--list"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Documentation files for queue" in output

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "pack", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert re.search("Packed: .*queue-.*[.]rock", output)
    rock_file = output.split("Packed: ")[1].strip()
    assert os.path.isfile(rock_file)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "unpack", rock_file],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0

    rock_dir = ""
    rock_dir_items = rock_file.split('.')
    for i in range((len(rock_dir_items) - 2)):
        rock_dir += rock_dir_items[i] + "."
    rock_dir = rock_dir[:-1]

    assert os.path.isdir(rock_dir)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "remove", "queue"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Removal successful.\n" in output

    test_app_path = os.path.join(os.path.dirname(__file__), "files", "testapp-scm-1.rockspec")
    shutil.copy(test_app_path, tmpdir)
    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "make", "testapp-scm-1.rockspec"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "testapp scm-1 is now installed" in output


def test_rocks_admin_module(tt_cmd, tmpdir):
    repo_path = os.path.join(tmpdir, "rocks_repo")
    os.mkdir(repo_path)

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "admin", "make_manifest", repo_path],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert os.path.isfile(f'{tmpdir}/rocks_repo/index.html')
    assert os.path.isfile(f'{tmpdir}/rocks_repo/manifest')

    test_app_path = os.path.join(os.path.dirname(__file__), "files", "testapp-scm-1.rockspec")
    shutil.copy(test_app_path, tmpdir)
    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "admin", "add", "testapp-scm-1.rockspec", "--server", repo_path],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert os.path.isfile(f'{tmpdir}/rocks_repo/testapp-scm-1.rockspec')

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "admin", "remove", "testapp-scm-1.rockspec", "--server", repo_path],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert not os.path.exists(f'{tmpdir}/rocks_repo/testapp-scm-1.rockspec')


def test_rocks_install_remote(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  rocks: "repo"''')
    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "stat"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Installing http://rocks.tarantool.org/stat" in output


def test_rocks_install_local(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    # Disable network with unshare.
    rc, output = run_command_and_get_output(
            ["unshare", "-r", "-n", tt_cmd, "rocks", "install", "stat"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert f"Installing {tmpdir}/repo/stat-0.3.2-1.all.rock" in output


def test_rocks_install_local_if_network_is_up(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    rc, output = run_command_and_get_output(
        [tt_cmd, "rocks", "--only-server=repo", "install", "stat"],
        cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert "Installing repo/stat-0.3.2-1.all.rock" in output
    assert "stat 0.3.2-1 is now installed in " + os.path.join(tmpdir, ".rocks") in output


def test_rocks_install_local_specific_version(tt_cmd, tmpdir):
    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    rc, output = run_command_and_get_output(
            [tt_cmd, "rocks", "install", "stat", "0.3.1-1"],
            cwd=tmpdir, env=dict(os.environ, PWD=tmpdir))
    assert rc == 0
    assert f"Installing {tmpdir}/repo/stat-0.3.1-1.all.rock" in output


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_rock_install_without_system_tarantool(tt_cmd, tmpdir_with_tarantool):
    rocks_cmd = [tt_cmd, "rocks", "install", "mysql", "2.1.3-1"]
    pwd = os.environ.get("PWD")
    os.environ["PWD"] = tmpdir_with_tarantool
    tt_process = subprocess.Popen(
        rocks_cmd,
        cwd=tmpdir_with_tarantool,
        stderr=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        text=True
    )
    tt_process.wait()
    os.environ["PWD"] = pwd
    assert tt_process.returncode == 0

    assert os.path.exists(os.path.join(tmpdir_with_tarantool,
                                       ".rocks", "lib", "tarantool", "mysql"))


def test_rocks_install_from_dir_with_no_repo(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  rocks: "repo"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    os.mkdir(os.path.join(tmpdir, "subdir"))

    # Disable network with unshare.
    rc, output = run_command_and_get_output(
            ["unshare", "-r", "-n", tt_cmd, "-c", "../tt.yaml", "rocks", "install", "stat"],
            cwd=os.path.join(tmpdir, "subdir"),
            env=dict(os.environ, PWD=os.path.join(tmpdir, "subdir")))
    assert rc == 0
    print(output)
    assert f"Installing {tmpdir}/repo/stat-0.3.2-1.all.rock" in output
    assert "stat 0.3.2-1 is now installed in " + os.path.join(tmpdir, "subdir", ".rocks") in output
    assert os.path.exists(os.path.join(tmpdir, "subdir", ".rocks"))


def test_rocks_install_from_env_var_repo(tt_cmd, tmpdir):
    if platform.system() == "Darwin":
        pytest.skip("/set platform is unsupported")

    with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
        tnt_env_file.write('''repo:
  distfiles: "distfiles"''')

    shutil.copytree(os.path.join(os.path.dirname(__file__), "repo"),
                    os.path.join(tmpdir, "repo"))

    os.mkdir(os.path.join(tmpdir, "subdir"))

    # Without env and network. Must fail.
    rc, output = run_command_and_get_output(
        ["unshare", "-r", "-n", tt_cmd, "-c", "../tt.yaml", "rocks", "install", "stat"],
        cwd=os.path.join(tmpdir, "subdir"),
        env=dict(
            os.environ,
            PWD=os.path.join(tmpdir, "subdir")))

    assert rc == 1
    assert "Error: No results matching query" in output

    # Tets with env set, no network.
    rc, output = run_command_and_get_output(
            ["unshare", "-r", "-n", tt_cmd, "-c", "../tt.yaml", "rocks", "install", "stat"],
            cwd=os.path.join(tmpdir, "subdir"),
            env=dict(
                os.environ,
                PWD=os.path.join(tmpdir, "subdir"),
                TT_CLI_REPO_ROCKS=f'{tmpdir}/repo'))  # Env var for rock repo directory.
    assert rc == 0
    print(output)
    assert f"Installing {tmpdir}/repo/stat-0.3.2-1.all.rock" in output
    assert "stat 0.3.2-1 is now installed in " + os.path.join(tmpdir, "subdir", ".rocks") in output
    assert os.path.exists(os.path.join(tmpdir, "subdir", ".rocks"))


@pytest.mark.notarantool
@pytest.mark.skipif(shutil.which("tarantool") is not None, reason="tarantool found in PATH")
def test_rock_install_with_non_system_tarantool_in_path(tt_cmd, tmpdir_with_tarantool):
    with tempfile.TemporaryDirectory() as tmpdir:
        with open(os.path.join(tmpdir, config_name), "w") as tnt_env_file:
            tnt_env_file.write('''repo:
  distfiles: "distfiles"''')

        # Rocks install must fail due to not found tarantool headers.
        rocks_cmd = [tt_cmd, "rocks", "install", "crud", "1.1.1-1"]
        rc, output = run_command_and_get_output(
            rocks_cmd,
            cwd=tmpdir,
            env=dict(
                os.environ,
                PWD=tmpdir,
                PATH=os.path.join(tmpdir_with_tarantool, 'bin') + ':' + os.environ['PATH']))

        assert rc == 1  # Tarantool headers are not found.
        assert re.search("Error: Failed finding Lua header files", output)

        # Set env var to find tarantool headers.
        rocks_cmd = [tt_cmd, "rocks", "install", "crud", "1.1.1-1"]
        rc, output = run_command_and_get_output(
            rocks_cmd,
            cwd=tmpdir,
            env=dict(
                os.environ,
                PWD=tmpdir,
                PATH=os.path.join(tmpdir_with_tarantool, 'bin') + ':' + os.environ['PATH'],
                TT_CLI_TARANTOOL_PREFIX=os.path.join(tmpdir_with_tarantool, 'include')))

        assert rc == 0
        assert 'crud 1.1.1-1 is now installed' in output

        assert os.path.exists(os.path.join(tmpdir, ".rocks", "share", "tarantool", "crud"))
