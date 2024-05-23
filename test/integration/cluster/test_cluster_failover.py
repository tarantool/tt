import re
import subprocess

switch_cmd_yml = r"""command: switch
new_master: some_instance
timeout: 30
"""

switch_cmd_timeout_yml = r"""command: switch
new_master: some_instance
timeout: 42
"""


def test_cluster_failover_switch_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    switch_cmd = [tt_cmd, "cluster", "failover", "switch",
                  f"{etcd.endpoint}/prefix", "some_instance"]
    ps_switch = subprocess.Popen(
        switch_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_output = ps_switch.stdout.read()
    assert re.search(r"To check the switching status, run", switch_output)

    task_id = switch_output.split(" ")[-1]
    task_id = "/prefix/failover/command/" + task_id.strip()

    etcd_content, _ = conn.get(task_id)
    assert etcd_content.decode("utf-8") == switch_cmd_yml


def test_cluster_failover_switch_timeout_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    conn = etcd.conn()
    switch_cmd = [tt_cmd, "cluster", "failover", "switch",
                  f"{etcd.endpoint}/prefix", "some_instance", "--timeout", "42"]
    ps_switch = subprocess.Popen(
        switch_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_output = ps_switch.stdout.read()
    assert re.search(r"To check the switching status, run", switch_output)

    task_id = switch_output.split(" ")[-1]
    task_id = "/prefix/failover/command/" + task_id.strip()

    etcd_content, _ = conn.get(task_id)
    assert etcd_content.decode("utf-8") == switch_cmd_timeout_yml


def test_cluster_failover_switch_status_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    _ = etcd.conn()
    switch_cmd = [tt_cmd, "cluster", "failover", "switch",
                  f"{etcd.endpoint}/prefix", "some_instance"]
    ps_switch = subprocess.Popen(
        switch_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_output = ps_switch.stdout.read()
    assert re.search(r"To check the switching status, run", switch_output)

    task_id = switch_output.split(" ")[-1].strip()

    status_cmd = [tt_cmd, "cluster", "failover", "switch-status",
                  f"{etcd.endpoint}/prefix", task_id]
    ps_status = subprocess.Popen(
        status_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )

    status_output = ps_status.stdout.read()
    assert status_output == switch_cmd_yml


def test_cluster_failover_switch_timeout_wait_etcd(tt_cmd, tmpdir_with_cfg, etcd):
    tmpdir = tmpdir_with_cfg

    _ = etcd.conn()
    switch_cmd = [tt_cmd, "cluster", "failover", "switch",
                  f"{etcd.endpoint}/prefix", "some_instance", "--timeout", "1", "--wait"]
    ps_switch = subprocess.Popen(
        switch_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True
    )
    switch_output = ps_switch.stdout.read()
    assert re.search(r"Timeout for command execution reached", switch_output)
