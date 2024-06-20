import os
import shutil
import subprocess

import yaml

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


def test_switch_tt(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tt.yaml")
    bin_dir_path = os.path.join(tmpdir, "bin")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": bin_dir_path}}, f)

    fake_tt_path = os.path.join(bin_dir_path, "tt_v7.7.7")
    os.makedirs(bin_dir_path)
    shutil.copyfile(tt_cmd, fake_tt_path)

    switch_cmd = [
        tt_cmd,
        "--cfg", config_path,
        "binaries", "switch", "tt", "7.7.7"
    ]

    switch_process = subprocess.Popen(
        switch_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_process_rc = switch_process.wait()
    output = switch_process.stdout.read()
    assert "Switching to tt 7.7.7" in output
    assert switch_process_rc == 0

    expected_bin = os.path.join(bin_dir_path, "tt_v7.7.7")
    tt_bin = os.path.realpath(os.path.join(bin_dir_path, "tt"))
    assert tt_bin == expected_bin


def test_switch_tt_full_version_name(tt_cmd, tmpdir):
    config_path = os.path.join(tmpdir, "tt.yaml")
    bin_dir_path = os.path.join(tmpdir, "bin")
    with open(config_path, "w") as f:
        yaml.dump({"env": {"bin_dir": bin_dir_path}}, f)

    fake_tt_path = os.path.join(bin_dir_path, "tt_v7.7.7")
    os.makedirs(bin_dir_path)
    shutil.copyfile(tt_cmd, fake_tt_path)

    switch_cmd = [
        tt_cmd,
        "--cfg", config_path,
        "binaries", "switch", "tt", "v7.7.7"
    ]

    switch_process = subprocess.Popen(
        switch_cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_process_rc = switch_process.wait()
    output = switch_process.stdout.read()
    assert "Switching to tt v7.7.7" in output
    assert switch_process_rc == 0

    expected_bin = os.path.join(bin_dir_path, "tt_v7.7.7")
    tt_bin = os.path.realpath(os.path.join(bin_dir_path, "tt"))
    assert tt_bin == expected_bin
