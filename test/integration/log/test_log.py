import os
import subprocess
import time

import pytest

from utils import config_name


@pytest.fixture(scope="function")
def mock_env_dir(tmpdir):
    with open(os.path.join(tmpdir, config_name), 'w') as f:
        f.write('env:\n  instances_enabled: ie\n')

    for app_n in range(2):
        app = os.path.join(tmpdir, 'ie', f'app{app_n}')
        os.makedirs(app, 0o755)
        with open(os.path.join(app, 'instances.yml'), 'w') as f:
            for i in range(4):
                f.write(f'inst{i}:\n')
                os.makedirs(os.path.join(app, 'var', 'log', f'inst{i}'), 0o755)

        with open(os.path.join(app, 'init.lua'), 'w') as f:
            f.write('')

        for i in range(3):  # Skip log for instance 4.
            with open(os.path.join(app, 'var', 'log', f'inst{i}', 'tt.log'), 'w') as f:
                f.writelines([f'line {j}\n' for j in range(20)])

    return tmpdir


def test_log_output_default_run(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert '\n'.join([f'app0:inst{inst_n}: line {i}' for i in range(10, 20)]) in output
        assert '\n'.join([f'app1:inst{inst_n}: line {i}' for i in range(10, 20)]) in output

    assert 'app0:inst3' not in output
    assert 'app1:inst3' not in output


def test_log_limit_lines_count(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-n', '3']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert '\n'.join([f'app0:inst{inst_n}: line {i}' for i in range(17, 20)]) in output
        assert '\n'.join([f'app1:inst{inst_n}: line {i}' for i in range(17, 20)]) in output


def test_log_more_lines(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-n', '300']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert '\n'.join([f'app0:inst{inst_n}: line {i}' for i in range(0, 20)]) in output
        assert '\n'.join([f'app1:inst{inst_n}: line {i}' for i in range(0, 20)]) in output


def test_log_want_zero(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-n', '0']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.readlines()

    assert len(output) == 0


def test_log_specific_instance(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', 'app0:inst1', '-n', '3']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    assert '\n'.join([f'app0:inst1: line {i}' for i in range(17, 20)]) in output

    assert 'app0:inst0' not in output and 'app0:inst2' not in output
    assert 'app1' not in output


def test_log_specific_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', 'app1']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) == 0
    output = process.stdout.read()

    for inst_n in range(3):
        assert '\n'.join([f'app1:inst{inst_n}: line {i}' for i in range(10, 20)]) in output

    assert 'app0' not in output


def test_log_negative_lines_num(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-n', '-10']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert 'negative' in output


def test_log_no_app(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', 'no_app']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert 'can\'t collect instance information for no_app' in output


def test_log_no_inst(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', 'app0:inst4']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    assert process.wait(10) != 0
    output = process.stdout.read()

    assert 'app0:inst4: instance(s) not found' in output


def wait_for_lines_in_output(stdout, expected_lines):
    output = ''
    retries = 10
    found = 0
    while True:
        line = stdout.readline()
        if line == '':
            if retries == 0:
                break
            time.sleep(0.2)
            retries -= 1
        else:
            retries = 10
            output += line
            for expected in expected_lines:
                if expected in line:
                    found += 1
                    break

            if found == len(expected_lines):
                break

    return output


def test_log_output_default_follow(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-f']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )

    output = wait_for_lines_in_output(process.stdout,
                                      ['app0:inst0: line 19', 'app1:inst2: line 19',
                                       'app0:inst1: line 19', 'app1:inst1: line 19'])

    with open(os.path.join(mock_env_dir, 'ie', 'app0', 'var', 'log', 'inst0', 'tt.log'), 'w') as f:
        f.writelines([f'line {i}\n' for i in range(20, 23)])

    with open(os.path.join(mock_env_dir, 'ie', 'app1', 'var', 'log', 'inst2', 'tt.log'), 'w') as f:
        f.writelines([f'line {i}\n' for i in range(20, 23)])

    output += wait_for_lines_in_output(process.stdout,
                                       ['app1:inst2: line 22', 'app0:inst0: line 22'])

    process.terminate()
    for i in range(10, 23):
        assert f'app0:inst0: line {i}' in output
        assert f'app1:inst2: line {i}' in output

    for i in range(10, 20):
        assert f'app0:inst1: line {i}' in output
        assert f'app1:inst1: line {i}' in output


def test_log_output_default_follow_want_zero_last(tt_cmd, mock_env_dir):
    cmd = [tt_cmd, 'log', '-f', '-n', '0']
    process = subprocess.Popen(
        cmd,
        cwd=mock_env_dir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
        universal_newlines=True,
        bufsize=1
    )

    time.sleep(1)

    with open(os.path.join(mock_env_dir, 'ie', 'app0', 'var', 'log', 'inst0', 'tt.log'), 'w') as f:
        f.writelines([f'line {i}\n' for i in range(20, 23)])

    with open(os.path.join(mock_env_dir, 'ie', 'app1', 'var', 'log', 'inst2', 'tt.log'), 'w') as f:
        f.writelines([f'line {i}\n' for i in range(20, 23)])

    output = wait_for_lines_in_output(process.stdout,
                                      ['app1:inst2: line 22', 'app0:inst0: line 22'])

    process.terminate()
    for i in range(20, 23):
        assert f'app0:inst0: line {i}' in output
        assert f'app1:inst2: line {i}' in output

    assert 'app0:inst1' not in output
    assert 'app0:inst2' not in output
    assert 'app1:inst0' not in output
