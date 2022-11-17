import os
import re
import subprocess


def test_remove_tt(tt_cmd, tmpdir):

    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')

    # Install latest tt.
    start_cmd = [tt_cmd, "install", "tt=master", "--cfg", configPath]
    instance_process = subprocess.Popen(
        start_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    # Check that the process was shutdowned correctly.
    instance_process_rc = instance_process.wait()
    assert instance_process_rc == 0
    installed_cmd = [tmpdir + "/bin/tt", "run", "-v"]
    installed_program_process = subprocess.Popen(
        installed_cmd,
        cwd=tmpdir + "/bin",
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    run_output = installed_program_process.stdout.readline()
    assert re.search(r"Tarantool", run_output)

    remove_cmd = [tt_cmd, "remove", "tt=master", "--cfg", configPath]
    remove_process = subprocess.Popen(
        remove_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    remove_process.wait()
    remove_output = remove_process.stdout.readline()
    assert re.search(r"Removing binary...", remove_output)
    remove_output = remove_process.stdout.readline()
    assert re.search(r"tt=master found, removing", remove_output)
    remove_output = remove_process.stdout.readline()
    assert re.search(r"tt=master was removed", remove_output)


def test_remove_missing(tt_cmd, tmpdir):
    configPath = os.path.join(tmpdir, "tarantool.yaml")
    # Create test config.
    with open(configPath, 'w') as f:
        f.write('tt:\n  app:\n    bin_dir:\n    inc_dir:\n')
    # Create bin directory.
    os.mkdir(os.path.join(tmpdir, "bin"))
    os.mkdir(os.path.join(tmpdir, "include"))
    # Remove not installed program.
    remove_cmd = [tt_cmd, "remove", "tt=1.2.3"]
    remove_process = subprocess.Popen(
        remove_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    remove_process.wait()
    remove_output = remove_process.stdout.readline()
    assert re.search(r"Removing binary...", remove_output)
    remove_output = remove_process.stdout.readline()
    assert re.search(r"There is no", remove_output)


def test_remove_foreign_program(tt_cmd, tmpdir):
    # Remove bash.
    remove_cmd = [tt_cmd, "remove", "bash=123"]
    remove_process = subprocess.Popen(
        remove_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    remove_process.wait()
    remove_output = remove_process.stdout.readline()
    assert re.search(r"Removing binary...", remove_output)
    remove_output = remove_process.stdout.readline()
    assert re.search(r"Unknown program:", remove_output)
