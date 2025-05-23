import re
import subprocess

from utils import config_name


def test_env_output(tt_cmd, tmp_path):
    configPath = tmp_path / config_name
    # Create test config
    with open(configPath, "w") as f:
        f.write("env:\n  bin_dir:\n  inc_dir:\n")
    binDir = tmp_path / "bin"
    tarantoolDir = "TARANTOOL_DIR=" + (tmp_path / "include" / "include").as_posix()

    env_cmd = [tt_cmd, "env"]
    instance_process = subprocess.Popen(
        env_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0

    # Check output
    output = instance_process.stdout.read()
    assert re.search(tarantoolDir, output)
    assert re.search(binDir.as_posix(), output)
