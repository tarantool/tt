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
        file = wait_file(os.path.join(workdir, app_name), f"ready-{inst}", [])
        assert file != ""


def stop_application(tt_cmd, app_name, workdir, instances, force=False):
    stop_cmd = [tt_cmd, "stop", "-y", app_name]
    stop_rc, stop_out = run_command_and_get_output(stop_cmd, cwd=workdir)
    assert stop_rc == 0

    if not force:
        for inst in instances:
            assert re.search(
                rf"The Instance {app_name}:{inst} \(PID = \d+\) has been terminated.",
                stop_out,
            )
            assert not os.path.exists(
                os.path.join(workdir, run_path, app_name, inst, "tarantool.pid"),
            )


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

    # 30s — election negotiation in a 3-voter replicaset can take longer
    # than the original 10s on slower hosts (and reliably did on macOS).
    assert wait_event(30, is_leader_elected)


def wait_election_leader_known(tt_cmd, app_name, inst_name, workdir):
    """Wait until inst_name has accepted a raft leader (election.leader_id != 0)
    and is in a stable election state (`leader` or `follower`).

    Election failover requires a quorum of voters that agree on the current
    term before `box.ctl.promote()` can win the next election. Tests that
    fire promote immediately after start_application race three separate
    things: the TCP handshake, replication entering `follow` state, and
    raft term propagation. Waiting for `follow` alone is not enough — the
    follower can be in `follow` state with `box.info.election.leader_id == 0`
    if no election round has completed yet, and `box.ctl.promote()` then
    fails with "Not enough peers connected to start elections".

    The lua reports back a status string. The Python side polls until that
    string contains both a stable election state and a non-zero leader id.
    """
    lua = (
        "local info = box.info.election or {} "
        "return string.format('state=%s leader_id=%s', "
        "  tostring(info.state), tostring(info.leader_id or info.leader or 0))"
    )

    def election_settled():
        out = eval_on_instance(tt_cmd, app_name, inst_name, workdir, lua)
        # Look for the result line (starts with '- ' in YAML output).
        for line in out.splitlines():
            line = line.strip()
            if not line.startswith("- "):
                continue
            payload = line[2:].strip("'\" ")
            if "leader_id=0" in payload or "leader_id=nil" in payload:
                return False
            if "state=leader" in payload or "state=follower" in payload:
                return True
        return False

    assert wait_event(15, election_settled), (
        f"{inst_name} did not settle election state within 15s"
    )


def parse_status(buf: io.StringIO):
    def next():
        return buf.readline().rstrip("\n")

    def cut(line=None, c=":"):
        if line is None:
            line = next()
        ind = line.find(c)
        return line[:ind].strip(), line[ind + 1 :].strip()

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
            line = line[len("    ★ ") :]

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
    return ""


def get_group_replicaset_by_instance_name(cluster_cfg, instance):
    for gk, g in cluster_cfg["groups"].items():
        for rk, r in g["replicasets"].items():
            for i in r["instances"].keys():
                if i == instance:
                    return gk, rk
    return "", ""
