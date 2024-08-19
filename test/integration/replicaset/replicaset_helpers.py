import io
import os
import re
import subprocess

import yaml

from utils import run_command_and_get_output, wait_event, wait_file

run_path = os.path.join("var", "run")


def start_application(tt_cmd, workdir, app_name, instances):
    start_cmd = [tt_cmd, "start", app_name]
    rc, _ = run_command_and_get_output(start_cmd, cwd=workdir)

    assert rc == 0
    for inst in instances:
        file = wait_file(os.path.join(workdir, app_name), f'ready-{inst}', [])
        assert file != ""


def stop_application(tt_cmd, app_name, workdir, instances, force=False):
    stop_cmd = [tt_cmd, "stop", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=workdir)
    assert stop_rc == 0

    if not force:
        for inst in instances:
            assert re.search(rf"The Instance {app_name}:{inst} \(PID = \d+\) has been terminated.",
                             stop_out)
            assert not os.path.exists(os.path.join(workdir, run_path, app_name,
                                                   inst, "tarantool.pid"))


def eval_on_instance(tt_cmd, app_name, inst_name, workdir, eval):
    connect_process = subprocess.Popen(
        [tt_cmd, "connect", f"{app_name}:{inst_name}", "-f-"],
        cwd=workdir,
        stdin=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    connect_process.stdin.write(eval)
    connect_process.stdin.close()
    connect_process.wait()
    return connect_process.stdout.read()


def box_ctl_promote(tt_cmd, app_name, inst_name, workdir):
    eval_on_instance(tt_cmd, app_name, inst_name, workdir, "box.ctl.promote()")

    def is_leader_elected():
        out = eval_on_instance(tt_cmd, app_name, inst_name, workdir, "box.info.ro")
        return out.find("false") != -1
    assert wait_event(10, is_leader_elected)


def parse_status(buf: io.StringIO):
    def next():
        return buf.readline().rstrip("\n")

    def cut(line=None, c=":"):
        if line is None:
            line = next()
        ind = line.find(c)
        return line[:ind].strip(), line[ind+1:].strip()

    _, orchestrator = cut()
    _, state = cut()
    next()

    replicasets = {}
    line = next()
    while line.startswith("• "):
        replicaset_name = line.lstrip("• ")
        replicaset = {}

        line = next()
        while not line.startswith("    "):
            k, v = cut(line)
            replicaset[k.lower()] = v
            line = next()

        instances = {}
        while line.startswith("    "):
            is_leader = line.startswith("    ★")
            line = line[len("    ★ "):]

            name, rest = cut(line, " ")
            listen, mode = cut(rest, " ")
            instances[name] = {
                "is_leader": is_leader,
                "listen": listen,
                "mode": mode,
            }
            line = next()

        replicaset["instances"] = instances
        replicasets[replicaset_name] = replicaset

    return {
        "orchestrator": orchestrator,
        "state": state,
        "replicasets": replicasets,
    }


def parse_yml(input):
    return yaml.safe_load(input)


def get_group_by_replicaset_name(cluster_cfg, replicaset):
    for gk, g in cluster_cfg["groups"].items():
        for r in g["replicasets"].keys():
            if r == replicaset:
                return gk


def get_group_replicaset_by_instance_name(cluster_cfg, instance):
    for gk, g in cluster_cfg["groups"].items():
        for rk, r in g["replicasets"].items():
            for i in r["instances"].keys():
                if i == instance:
                    return gk, rk
    return "", ""
