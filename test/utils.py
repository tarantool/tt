import os
import re
import subprocess
import time

import yaml


def run_command_and_get_output(
    cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, cwd=None, env=None
):
    process = subprocess.Popen(
        cmd,
        env=env,
        cwd=cwd,
        stderr=stderr,
        stdout=stdout,
    )

    out, _ = process.communicate()
    out = out.decode('utf-8')

    # This print is here to make running tests with -s flag more verbose
    print(out)

    return process.returncode, out


def create_tt_config(config_path, modules_path):
    config_path = os.path.join(config_path, "tarantool.yaml")
    with open(config_path, "w") as f:
        yaml.dump({"tt": {"modules": {"directory": f"{modules_path}"}}}, f)

    return config_path


def create_external_module(module_name, directory):
    module_message = f"\"Hello, I'm {module_name} external module!\""
    with open(os.path.join(directory, f"{module_name}.sh"), "w") as f:
        f.write(f"""#!/bin/sh
            if [ "$1" = "--help" ]; then
                echo Help for external {module_name} module
            elif [ "$1" = "--description" ]; then
                echo Description for external module {module_name}
            else
                echo {module_message}
            fi

            echo List of passed args: $@""")

    os.chmod(os.path.join(directory, f"{module_name}.sh"), 0o777)

    return module_message.strip('"')


def wait_file(dir_name, file_pattern, exclude_list, timeout_sec=1):
    """Wait for "timeout_sec" until a file matching "file_pattern" and not
    included in "exclude_list" is found in the "dir_name" directory.
    Returns the name of the file.

    Alternatively, https://pypi.org/project/watchdog/ may be used,
    but that seems like overkill.
    """
    iter_timeout_sec = 0.01
    iter_count = 0

    while True:
        files = os.listdir(dir_name)
        for file in files:
            if re.match(file_pattern, file) is not None and file not in exclude_list:
                return file

        if (iter_count * iter_timeout_sec) > timeout_sec:
            break

        cur_timeout = timeout_sec if timeout_sec < iter_timeout_sec else iter_timeout_sec
        time.sleep(cur_timeout)

        iter_count = iter_count + 1

    return ""
