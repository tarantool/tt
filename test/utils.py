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
from retry import retry

var_path = "var"
lib_path = os.path.join(var_path, "lib")
run_path = os.path.join(var_path, "run")
log_path = os.path.join(var_path, "log")
config_name = "tt.yaml"
control_socket = "tarantool.control"
pid_file = "tt.pid"
log_file = "tt.log"
initial_snap = "00000000000000000000.snap"
initial_xlog = "00000000000000000000.xlog"


def get_fixture_tcs_params(path_to_cfg, connection_test=True,
                           connection_test_user="client",
                           connection_test_password="secret",
                           instance_name="instance-001",
                           instance_host="localhost",
                           instance_port="3303"):
    return {
        "path_to_cfg_dir": path_to_cfg,
        "connection_test": connection_test,
        "connection_test_user": connection_test_user,
        "connection_test_password": connection_test_password,
        "instance_name": instance_name,
        "instance_host": instance_host,
        "instance_port": instance_port,
    }


def run_command_and_get_output(
    cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, input=None, cwd=None, env=None
):
    process = subprocess.run(
        cmd,
        env=env,
        cwd=cwd,
        stderr=stderr,
        stdout=stdout,
        text=True,
        input=input
    )

    # This print is here to make running tests with -s flag more verbose
    print(process.stdout)

    return process.returncode, process.stdout


def create_tt_config(config_path, modules_path):
    config_path = os.path.join(config_path, config_name)
    with open(config_path, "w") as f:
        yaml.dump({"modules": {"directory": f"{modules_path}"}}, f)

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


def wait_file(dir_name, file_pattern, exclude_list, timeout_sec=10):
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


def wait_instance_start(log_path, timeout_sec=10):
    started = False
    iter_timeout_sec = 0.05

    iter_count = 0
    while not started:
        if (iter_count * iter_timeout_sec) > timeout_sec:
            break

        time.sleep(iter_timeout_sec)

        with open(log_path, "r") as log_file:
            lines = log_file.readlines()
            for line in lines:
                if "started" in line:
                    started = True
                    break

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


def wait_event(timeout, event_func, interval=0.1):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if event_func():
            return True
        time.sleep(interval)
    return False


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
        shutil.copytree(instance_cfg_file_dir, tmpdir, dirs_exist_ok=True)
        # Copy the lua module with the auxiliary functions required by the instance config file.
        if os.path.isfile(os.path.join(path_to_lua_utils, "utils.lua")):
            shutil.copy(os.path.join(path_to_lua_utils, "utils.lua"), tmpdir)

        self._tmpdir = tmpdir
        self._instance_cfg_file_name = instance_cfg_file_name

    def start(
        self,
        connection_test=True,
        connection_test_user="guest",
        connection_test_password=None,
        instance_name=None,
        instance_port="",
        instance_host="localhost",
        use_lua=False,
    ):
        """Starts tarantool test instance and init self.port attribute.

        Args:
        connection_test (bool): if this flag is set, then after bound the port, an attempt will be
            made to connect to the test instance within a three second deadline. (default is True)
        connection_test_user (str): username for the connection attempt. (default is 'guest')
        connection_test_password (str): password for the connection attempt. (default is None)
        instance_name (str): name of instance will be started. (default is None)
        instance_port (str): port which will try to connect to instance. If check is successful,
            set it into class field. (default is empty string)
        instance_host (str): host which will try to connect to instance. If check is successful,
            set it into class field. (default is empty string)
        use_lua (bool): needs to specify if we want to start tarantool with lua configuration
            even if tarantool major version more than 3. (default is false)

        Raises:
            RuntimeError:
                If could not find a file with an instance bound port during 3 seconds deadline.
                You may have forgotten to use require('utils').bind_free_port(arg[0])
                inside your cfg instance file.
                Also, this exception will occur if it is impossible to connect to a started
                instance within three seconds deadline after port bound (an attempt to connect is
                made if there is an option connection_test=True that is present by default).
        """
        tnt_start_cmd = []
        major_version, _ = get_tarantool_version()
        if major_version < 3 or use_lua:
            if instance_name != "" and (".yml" in self._instance_cfg_file_name or
                                        ".yaml" in self._instance_cfg_file_name):
                raise Exception("instance_name cannot be used with Tarantool version < 3")
            tnt_start_cmd = ["tarantool", self._instance_cfg_file_name]
        else:
            tnt_start_cmd = [
                "tarantool",
                "--name",
                instance_name,
                "--config",
                self._instance_cfg_file_name,
            ]
        popen_obj = subprocess.Popen(tnt_start_cmd, cwd=self._tmpdir)

        # Search for file with port only if port directly not provided.
        if len(instance_port) == 0:
            file_with_port_path = (
                str(self._tmpdir) + "/" + self._instance_cfg_file_name + ".port"
            )
            # Waiting 3 seconds for instance configure itself and dump bound port to temp file.
            deadline = time.time() + 3
            while time.time() < deadline:
                if (
                    os.path.exists(file_with_port_path)
                    and os.path.getsize(file_with_port_path) > 0
                ):
                    break
                time.sleep(0.1)
            else:
                raise RuntimeError(
                    "Could not find a file with an instance bound port or empty file"
                )

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
        self.host = instance_host
        self.port = instance_port
        self.connection_username = connection_test_user
        self.connection_password = connection_test_password
        self.endpoint = f"http://{self.host}:{self.port}"

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

    def conn(self):
        """Connects to self instance set in class."""
        conn = tarantool.Connection
        try:
            conn = tarantool.connect(
                self.host,
                int(self.port),
                user=self.connection_username,
                password=self.connection_password,
            )
        except Exception:
            raise Exception("cannot connect to instance with provided params")
        return conn


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


def find_ports(n=1, port=8000):
    ports = []
    while len(ports) < n:
        busy = False
        for proto in [socket.AF_INET, socket.AF_INET6]:
            with socket.socket(proto, socket.SOCK_STREAM) as s:
                if s.connect_ex(("localhost", port)) == 0:
                    busy = True
                    break
        if not busy:
            ports.append(port)
        port += 1
    return ports


def find_port(port=8000):
    return find_ports(1, port)[0]


def extract_status(status_output):
    result = {}
    statuses = status_output.split("\n")
    for i in range(1, len(statuses)-1):
        summary = statuses[i]
        fields = summary.split()
        instance = fields[0]
        info = {}
        if fields[1] == "RUNNING":
            info["STATUS"] = fields[1]
            info["PID"] = int(fields[2])
            if len(fields) == 4:
                info["MODE"] = fields[3]
        else:
            info["STATUS"] = " ".join(fields[1:])
        result[instance] = info
    return result


def is_valid_tarantool_installed(
        bin_path,
        inc_path,
        expected_bin=None,
        expected_inc=None
):
    tarantool_binary_symlink = os.path.join(bin_path, "tarantool")
    tarantool_include_symlink = os.path.join(inc_path, "tarantool")

    if expected_bin is None:
        if os.path.exists(tarantool_binary_symlink):
            tarantool_bin = os.path.realpath(
                os.path.join(bin_path, "tarantool"))
            print(f"tarantool binary {tarantool_bin} is unexpected")
            return False
    else:
        tarantool_bin = os.path.realpath(os.path.join(bin_path, "tarantool"))
        if tarantool_bin != expected_bin:
            print(f"tarantool binary {tarantool_bin} is unexpected,"
                  f" expected: {expected_bin}")
            return False

    if expected_inc is not None:
        tarantool_inc = os.path.realpath(tarantool_include_symlink)
        if tarantool_inc != expected_inc:
            print(f"tarantool include {tarantool_inc} is unexpected,"
                  f" expected: {expected_bin}")
            return False
    else:
        if os.path.exists(tarantool_include_symlink):
            tarantool_inc = os.path.realpath(
                os.path.join(inc_path, "tarantool"))
            print(f"tarantool include {tarantool_inc} is unexpected")
            return False

    return True


def get_tarantool_version():
    try:
        tt_process = subprocess.Popen(
            ["tarantool", "--version"],
            stderr=subprocess.STDOUT, stdout=subprocess.PIPE, text=True
        )
    except FileNotFoundError:
        return 0, 0

    tt_process.wait()
    assert tt_process.returncode == 0
    version_line = tt_process.stdout.readline()

    match = re.match(r"Tarantool\s+(Enterprise\s+)?(?P<major>\d+)\.(?P<minor>\d+)", version_line)

    assert match is not None

    return int(match.group('major')), int(match.group('minor'))


def read_kv(dirname):
    kvs = {}
    for filename in os.listdir(dirname):
        if not os.path.isfile(os.path.join(dirname, filename)):
            continue
        key, _ = os.path.splitext(filename)
        with open(os.path.join(dirname, filename), "r") as f:
            kvs[key] = f.read()
    return kvs


def is_tarantool_less_3():
    major_versoin, _ = get_tarantool_version()
    return True if major_versoin < 3 else False


def is_tarantool_ee():
    cmd = ["tarantool", "--version"]
    instance_process = subprocess.run(
        cmd,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    if instance_process.returncode == 0:
        return "Tarantool Enterprise" in instance_process.stdout
    return False


@retry(Exception, tries=40, delay=0.5)
def wait_string_in_file(file, text):
    with open(file, "r") as fp:
        lines = fp.readlines(100)
        found = False
        while len(lines) != 0 and not found:
            for line in lines:
                if text in line:
                    found = True
                    break
            lines = fp.readlines(100)
        assert found


def wait_for_lines_in_output(stdout, expected_lines: list):
    output = ''
    retries = 10
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
                    expected_lines.remove(expected)
                    break

            if len(expected_lines) == 0:
                break

    return output
