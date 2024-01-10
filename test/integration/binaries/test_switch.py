import os
import shutil
import subprocess

from utils import config_name


def test_switch(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_tarantool"
    )
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")

    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "binaries", "switch", "tarantool", "2.10.3"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "Switching to tarantool 2.10.3" in output
    assert install_process_rc == 0

    bin_path = os.path.join(tt_dir, "bin")
    expected_bin = os.path.join(bin_path, "tarantool_2.10.3")
    tarantool_bin = os.path.realpath(os.path.join(bin_path, "tarantool"))
    inc_path = os.path.join(tt_dir, "inc/include")
    expected_inc = os.path.join(inc_path, "tarantool_2.10.3")
    tarantool_inc = os.path.realpath(os.path.join(inc_path, "tarantool"))
    assert tarantool_bin == expected_bin
    assert tarantool_inc == expected_inc


def test_switch_with_link(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_tarantool_link"
    )
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")

    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "binaries", "switch", "tarantool", "2.10.3"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "Switching to tarantool 2.10.3" in output
    assert install_process_rc == 0

    bin_path = os.path.join(tt_dir, "bin")
    expected_bin = os.path.join(bin_path, "tarantool_2.10.3")
    tarantool_bin = os.path.realpath(os.path.join(bin_path, "tarantool"))
    inc_path = os.path.join(tt_dir, "inc/include")
    expected_inc = os.path.join(inc_path, "tarantool_2.10.3")
    tarantool_inc = os.path.realpath(os.path.join(inc_path, "tarantool"))
    assert tarantool_bin == expected_bin
    assert tarantool_inc == expected_inc


def test_switch_invalid_program(tt_cmd, tmpdir):
    # Copy test files.
    testdata_path = os.path.join(
        os.path.dirname(__file__),
        "testdata/test_tarantool"
    )
    shutil.copytree(testdata_path, os.path.join(tmpdir, "testdata"), True)
    testdata_path = os.path.join(tmpdir, "testdata")

    tt_dir = os.path.join(testdata_path, "tt")

    install_cmd = [
        tt_cmd,
        "--cfg", os.path.join(tt_dir, config_name),
        "binaries", "switch", "nodejs", "2.10.3"
    ]

    install_process = subprocess.Popen(
        install_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    install_process_rc = install_process.wait()
    output = install_process.stdout.read()
    assert "not supported program: nodejs" in output
    assert install_process_rc != 0
