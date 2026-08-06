import os
import platform
import re
import shutil
import time

import yaml

from utils import (
    find_ports,
    log_file,
    log_path,
    pid_file,
    run_command_and_get_output,
    run_path,
    var_path,
    wait_file,
)

cartridge_name = "cartridge_app"
cartridge_username = "admin"
cartridge_password = "secret-cluster-cookie"

# The default 10s is not enough for a two-phase commit over the whole cluster
# on a loaded CI runner.
bootstrap_timeout = 60

instances = [
    "router",
    "s1-master",
    "s1-replica",
    "s2-master",
    "s2-replica-1",
    "s2-replica-2",
    "stateboard",
    "s3-master",
]


def get_instances_cfg():
    ports = find_ports(15)
    return {
        f"{cartridge_name}.router": {
            "advertise_uri": f"localhost:{ports[0]}",
            "http_port": ports[1],
        },
        f"{cartridge_name}.s1-master": {
            "advertise_uri": f"localhost:{ports[2]}",
            "http_port": ports[3],
        },
        f"{cartridge_name}.s1-replica": {
            "advertise_uri": f"localhost:{ports[4]}",
            "http_port": ports[5],
        },
        f"{cartridge_name}.s2-master": {
            "advertise_uri": f"localhost:{ports[6]}",
            "http_port": ports[7],
        },
        f"{cartridge_name}.s2-replica-1": {
            "advertise_uri": f"localhost:{ports[8]}",
            "http_port": ports[9],
        },
        f"{cartridge_name}.s2-replica-2": {
            "advertise_uri": f"localhost:{ports[10]}",
            "http_port": ports[11],
        },
        f"{cartridge_name}-stateboard": {
            "listen": f"localhost:{ports[12]}",
            "password": "passwd",
        },
        f"{cartridge_name}.s3-master": {
            "advertise_uri": f"localhost:{ports[13]}",
            "http_port": ports[14],
        },
    }


replicasets_cfg = {
    "router": {
        "instances": ["router"],
        "roles": ["failover-coordinator", "vshard-router", "app.roles.custom"],
        "all_rw": False,
    },
    "s-1": {
        "instances": ["s1-master", "s1-replica"],
        "roles": ["vshard-storage"],
        "weight": 1,
        "all_rw": False,
        "vshard_group": "default",
    },
    "s-2": {
        "instances": ["s2-master", "s2-replica-1", "s2-replica-2"],
        "roles": ["vshard-storage"],
        "weight": 1,
        "all_rw": False,
        "vshard_group": "default",
    },
    "s-3": {
        "instances": ["s3-master"],
        "roles": ["app.roles.custom"],
        "weight": 1,
        "all_rw": False,
        "vshard_group": "default",
    },
}


def wait_inst_files(dir, inst):
    run_dir = os.path.join(dir, cartridge_name, run_path, inst)
    log_dir = os.path.join(dir, cartridge_name, log_path, inst)

    file = wait_file(run_dir, pid_file, [])
    assert file != ""
    file = wait_file(log_dir, log_file, [])
    assert file != ""


# Cartridge logs every confapplier state change, e.g.
# "Instance state changed: ConfiguringRoles -> RolesConfigured".
state_change_re = re.compile(r"Instance state changed: \S+ -> (\S+)")


def wait_inst_roles_configured(dir, inst):
    # The last recorded transition is checked instead of the mere presence of one:
    # every clusterwide config apply drives the instance through ConfiguringRoles
    # again, and a 2PC started before it settles is rejected with a Prepare2pcError.
    log = os.path.join(dir, cartridge_name, log_path, inst, log_file)
    state = None
    trying = 0
    while trying < 200:
        with open(log, "r") as fp:
            states = state_change_re.findall(fp.read())
        if states:
            state = states[-1]
            if state == "RolesConfigured":
                return
        time.sleep(0.05)
        trying = trying + 1
    assert state == "RolesConfigured", f"{inst} is in state {state}, expected RolesConfigured"


def wait_inst_start(dir, inst):
    wait_inst_files(dir, inst)

    log_dir = os.path.join(dir, cartridge_name, log_path, inst)
    started = False
    trying = 0
    while not started and trying < 200:
        if inst == "stateboard":
            started = True
            break
        with open(os.path.join(log_dir, log_file), "r") as fp:
            lines = fp.readlines()
            lines = [line.rstrip() for line in lines]
        for line in lines:
            if re.search("Set default metrics endpoints", line):
                started = True
                break
        time.sleep(0.05)
        trying = trying + 1
    assert started


# CartridgeApp wraps tt environment with cartridge application.
class CartridgeApp:
    def __init__(self, workdir, tt_cmd) -> None:
        self.workdir = workdir
        self.tt_cmd = tt_cmd
        self.instances = instances
        self.instances_cfg = get_instances_cfg()
        self.replicasets_cfg = replicasets_cfg

        self.uri = {}
        for inst in self.instances:
            found = False
            for inst_name, cfg in self.instances_cfg.items():
                if inst_name.endswith(inst):
                    self.uri[inst] = cfg["advertise_uri"] if inst != "stateboard" else cfg["listen"]
                    found = True
                    break
            assert found

        cmd = [self.tt_cmd, "init", "-f"]
        rc, _ = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0

        self.create()
        self.build()

    def truncate(self, bootstrap_vshard=True):
        self.stop()
        shutil.rmtree(os.path.join(self.workdir, cartridge_name, var_path))
        self.start(bootstrap_vshard=bootstrap_vshard)

    def create(self):
        cmd = [self.tt_cmd, "create", "-s", "cartridge", "--name", cartridge_name, "-f"]
        rc, _ = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0

        # Set instances config.
        with open(os.path.join(self.workdir, cartridge_name, "instances.yml"), "w") as f:
            f.write(yaml.dump(self.instances_cfg))
        # Set replicasets config.
        with open(os.path.join(self.workdir, cartridge_name, "replicasets.yml"), "w") as f:
            f.write(yaml.dump(self.replicasets_cfg))

    def build(self):
        cmd = [self.tt_cmd, "build", cartridge_name]
        rc, out = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0
        assert re.search(r"Application was successfully built", out)

    def start(self, bootstrap_vshard=True):
        start_cmd = [self.tt_cmd, "start", cartridge_name]
        test_env = os.environ.copy()
        # Avoid too long path.
        if platform.system() == "Darwin":
            test_env["TT_LISTEN"] = ""
        rc, _ = run_command_and_get_output(start_cmd, cwd=self.workdir, env=test_env)
        assert rc == 0
        # Wait for the full start of the cartridge.
        for inst in self.instances:
            wait_inst_start(self.workdir, inst)

        # Bootstrap.
        self.bootstrap(bootstrap_vshard=bootstrap_vshard)

    def bootstrap(self, bootstrap_vshard=True):
        cmd = [
            self.tt_cmd,
            "replicaset",
            "bootstrap",
            "--timeout",
            str(bootstrap_timeout),
            cartridge_name,
        ]
        if bootstrap_vshard:
            cmd.append("--bootstrap-vshard")
        rc, out = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0, f"vshard bootstrap output: {out}"
        assert re.search(r"Done.", out)

        # Wait until the instances are configured.
        self.wait_roles_configured()

    def wait_roles_configured(self):
        for inst in self.instances:
            if inst == "stateboard":
                continue
            wait_inst_roles_configured(self.workdir, inst)

    def set_failover(self, data):
        with open(os.path.join(self.workdir, cartridge_name, "failover.yml"), "w") as f:
            f.write(yaml.dump(data))
        cmd = [self.tt_cmd, "cartridge", "failover", "setup", "--name", cartridge_name]
        rc, out = run_command_and_get_output(cmd, cwd=os.path.join(self.workdir, cartridge_name))
        assert rc == 0
        assert re.search(r"Failover configured successfully", out)
        # Failover setup patches the clusterwide config, so the instances reconfigure
        # their roles once more. Without waiting for that, a command issued right away
        # races with the reconfiguration and fails to start its own 2PC.
        self.wait_roles_configured()

    def stop(self):
        cmd = [self.tt_cmd, "stop", "-y", cartridge_name]
        rc, _ = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0

    def stop_inst(self, name):
        assert name in self.instances, "instance is offline"
        cmd = [self.tt_cmd, "stop", "-y", f"{cartridge_name}:{name}"]
        rc, _ = run_command_and_get_output(cmd, cwd=self.workdir)
        self.instances.remove(name)
        assert rc == 0

    def start_inst(self, name):
        assert name not in self.instances, "instance is online"
        cmd = [self.tt_cmd, "start", f"{cartridge_name}:{name}"]
        rc, _ = run_command_and_get_output(cmd, cwd=self.workdir)
        assert rc == 0
        self.instances.append(name)
        wait_inst_files(self.workdir, name)
