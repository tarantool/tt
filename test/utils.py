import ipaddress
import os
import re
import shutil
import socket
import subprocess
import time

import netifaces
import psutil
import tarantool
import yaml

run_path = os.path.join("var", "run")
log_path = os.path.join("var", "log")


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
        try:
            files = os.listdir(dir_name)
        except OSError:
            pass
        else:
            for file in files:
                if re.match(file_pattern, file) is not None and file not in exclude_list:
                    return file

        if (iter_count * iter_timeout_sec) > timeout_sec:
            break

        cur_timeout = timeout_sec if timeout_sec < iter_timeout_sec else iter_timeout_sec
        time.sleep(cur_timeout)

        iter_count = iter_count + 1

    return ""


def kill_child_process(pid=psutil.Process().pid):
    parent = psutil.Process(int(pid))
    procs = parent.children()

    return kill_procs(procs)


def kill_procs(procs):
    for proc in procs:
        proc.terminate()
    _, alive = psutil.wait_procs(procs, timeout=3)

    for proc in alive:
        proc.kill()

    return len(procs)


def wait_instance_start(log_path, timeout_sec=5):
    started = False
    iter_timeout_sec = 0.05

    iter_count = 0
    while not started:
        if (iter_count * iter_timeout_sec) > timeout_sec:
            break

        time.sleep(iter_timeout_sec)

        with open(log_path, "r") as log_file:
            last_line = log_file.readlines()[-1]
            if "started" in last_line:
                started = True

        iter_count = iter_count + 1

    return started


def wait_instance_stop(pid_path, timeout_sec=5):
    stopped = False
    iter_timeout_sec = 0.05

    iter_count = 0
    while not stopped:
        if (iter_count * iter_timeout_sec) > timeout_sec:
            break

        time.sleep(iter_timeout_sec)
        if os.path.isfile(pid_path) is False:
            stopped = True

        iter_count = iter_count + 1

    return stopped


class TarantoolTestInstance:
    """Create test tarantool instance via subprocess.Popen with given cfg file.

    Performs this steps for it:
    1) Copy the instance config files to the run pytest tmp directory.
       This cfg file should be in /test/integration/foo-module/test_file/instance_cfg_file_name.
       But you can specify different path by instance_cfg_file_dir arg.

    2) Also copy to pytest tmpdir the lua module utils.lua with the auxiliary functions.
       This functions is required for using with your instance.
       As a result, you can use require('utils') inside your instance config file.
       Arg path_to_lua_utils should specify dir with utils.lua file.

    3) Run tarantool via subprocess.Popen with given cfg file.
       Gets bound port from tmpdir.
       Init subprocess object and instance's port as attributes.

    NOTE: Demand require('utils').bind_free_port(arg[0]) inside your instance cfg file.

    Args:
        instance_cfg_file_name (str): file name of your test instance cfg.
        instance_cfg_file_dir (str): path to dir that contains instance_cfg_file_name.
        path_to_lua_utils (str): path to dir that contains utils.lua file.
        tmpdir (str): expected result of fixture get_tmpdir from conftest.

    Attributes:
        popen_obj (Popen[bytes]): subprocess.Popen object with tarantool test instance.
        port (str): port of tarantool test instance.

    Methods:
        start():
            Starts tarantool test instance and init self.port attribute.
        stop():
            Stops tarantool test instance by SIGKILL signal.
    """

    def __init__(self, instance_cfg_file_name, instance_cfg_file_dir, path_to_lua_utils, tmpdir):
        # Copy the instance config files to the run pytest tmpdir directory.
        shutil.copy(instance_cfg_file_dir + "/" + instance_cfg_file_name, tmpdir)
        # Copy the lua module with the auxiliary functions required by the instance config file.
        shutil.copy(path_to_lua_utils + "/" + "utils.lua", tmpdir)

        self._tmpdir = tmpdir
        self._instance_cfg_file_name = instance_cfg_file_name

    def start(self, connection_test=True,
              connection_test_user='guest',
              connection_test_password=None):
        """Starts tarantool test instance and init self.port attribute.

        Args:
        connection_test (bool): if this flag is set, then after bound the port, an attempt will be
            made to connect to the test instance within a three second deadline. (default is True)
        connection_test_user (str): username for the connection attempt. (default is 'guest')
        connection_test_password (str): password for the connection attempt. (default is None)

        Raises:
            RuntimeError:
                If could not find a file with an instance bound port during 3 seconds deadline.
                You may have forgotten to use require('utils').bind_free_port(arg[0])
                inside your cfg instance file.
                Also, this exception will occur if it is impossible to connect to a started
                instance within three seconds deadline after port bound (an attempt to connect is
                made if there is an option connection_test=True that is present by default).
        """
        popen_obj = subprocess.Popen(["tarantool", self._instance_cfg_file_name], cwd=self._tmpdir)
        file_with_port_path = str(self._tmpdir) + '/' + self._instance_cfg_file_name + '.port'

        # Waiting 3 seconds for instance configure itself and dump bound port to temp file.
        deadline = time.time() + 3
        while time.time() < deadline:
            if os.path.exists(file_with_port_path) and os.path.getsize(file_with_port_path) > 0:
                break
            time.sleep(0.1)
        else:
            raise RuntimeError('Could not find a file with an instance bound port or empty file')

        # Read bound port of test instance from file in temp pytest directory.
        with open(file_with_port_path) as file_with_port:
            instance_port = file_with_port.read()

        # Tries connect to the started instance during 3 seconds deadline with bound port.
        if connection_test:
            deadline = time.time() + 3
            while time.time() < deadline:
                try:
                    conn = tarantool.connect("localhost", int(instance_port),
                                             user=connection_test_user,
                                             password=connection_test_password)
                    conn.close()
                    break
                except tarantool.NetworkError:
                    time.sleep(0.1)
            else:
                raise RuntimeError('Could not connect to the started instance with bound port')

        self.popen_obj = popen_obj
        self.port = instance_port

    def stop(self):
        """Stops tarantool test instance by SIGKILL signal.

        Raises:
            RuntimeError:
                If could not stop instance after receiving SIGKILL during 3 seconds deadline.
        """
        self.popen_obj.kill()
        instance = psutil.Process(self.popen_obj.pid)
        # Waiting for the completion of the process with 3 second timeout.
        deadline = time.time() + 3
        while time.time() < deadline:
            if not psutil.pid_exists(instance.pid) or instance.status() == 'zombie':
                # There is no more instance process or it is zombie.
                break
            else:
                time.sleep(0.1)
        else:
            raise RuntimeError("PID {} couldn't stop after receiving SIGKILL".format(instance.pid))


def is_ipv4_type(address):
    try:
        ip = ipaddress.ip_address(address)

        if isinstance(ip, ipaddress.IPv4Address):
            return True
    except ValueError:
        return False

    return False


def get_test_iface():
    ifaces = netifaces.interfaces()

    for iface in ifaces[1:]:
        addrs = netifaces.ifaddresses(iface)
        for _, addr in addrs.items():
            if is_ipv4_type(addr[0]['addr']):
                return iface

    # loopback
    return netifaces.interfaces()[0]


def proc_by_pidfile(filename):
    try:
        with open(filename, "r") as f:
            pid = int(f.read())
        return psutil.Process(pid)
    except psutil.NoSuchProcess:
        return None


def get_process_conn(pidfile, port):
    proc = proc_by_pidfile(pidfile)
    for conn in proc.connections():
        if conn.status == 'LISTEN' and conn.laddr.port == port \
                and is_ipv4_type(conn.laddr.ip):
            return conn

    return None


def find_port(port=8000):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        if s.connect_ex(("localhost", port)) == 0:
            return find_port(port=port + 1)
        else:
            return port
