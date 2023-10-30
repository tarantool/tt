import os
import re
import subprocess

from utils import config_name


def test_env_output(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, config_name)
    # Create test config
    with open(configPath, 'w') as f:
        f.write('env:\n  bin_dir:\n  inc_dir:\n')
    binDir = str(tmpdir + "/bin")
    tarantoolDir = "TARANTOOL_DIR=" + str(tmpdir + "/include/include")

    env_cmd = [tt_cmd, "env"]
    instance_process = subprocess.Popen(
        env_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0

    # Check output
    output = instance_process.stdout.read()
    assert re.search(tarantoolDir, output)
    assert re.search(binDir, output)
