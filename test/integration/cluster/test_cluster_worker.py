import subprocess


def test_cluster_worker_help(tt_cmd, tmp_path):
    help_cmd = [tt_cmd, "cluster", "worker", "--help"]
    instance_process = subprocess.Popen(
        help_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    help_output = instance_process.stdout.read()

    assert "Manage worker configuration" in help_output
    assert "publish" in help_output
    assert "show" in help_output
    assert "delete" in help_output


def test_cluster_worker_publish_help(tt_cmd, tmp_path):
    help_cmd = [tt_cmd, "cluster", "worker", "publish", "--help"]
    instance_process = subprocess.Popen(
        help_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    help_output = instance_process.stdout.read()

    assert "Publish a worker configuration" in help_output
    assert "http(s)://[username:password@]host:port/prefix/host-name/worker-name" in help_output
    assert "* prefix - a base path to the worker configuration." in help_output
    assert "* host-name - a name of the host." in help_output
    assert "* worker-name - a name of the worker." in help_output
    assert "TT_CLI_USERNAME" in help_output
    assert "TT_CLI_PASSWORD" in help_output
    assert "TT_CLI_ETCD_USERNAME" in help_output
    assert "TT_CLI_ETCD_PASSWORD" in help_output
    assert "environment variables < command flags < URL credentials" in help_output
    assert "--force" in help_output
    assert "-u" in help_output or "--username" in help_output
    assert "-p" in help_output or "--password" in help_output


def test_cluster_worker_show_help(tt_cmd, tmp_path):
    help_cmd = [tt_cmd, "cluster", "worker", "show", "--help"]
    instance_process = subprocess.Popen(
        help_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    help_output = instance_process.stdout.read()

    assert "Show a worker configuration" in help_output
    assert "http(s)://[username:password@]host:port/prefix/host-name/worker-name" in help_output
    assert "* prefix - a base path to the worker configuration." in help_output
    assert "* host-name - a name of the host." in help_output
    assert "* worker-name - a name of the worker." in help_output
    assert "TT_CLI_USERNAME" in help_output
    assert "TT_CLI_PASSWORD" in help_output
    assert "TT_CLI_ETCD_USERNAME" in help_output
    assert "TT_CLI_ETCD_PASSWORD" in help_output
    assert "environment variables < command flags < URL credentials" in help_output
    assert "-u" in help_output or "--username" in help_output
    assert "-p" in help_output or "--password" in help_output


def test_cluster_worker_delete_help(tt_cmd, tmp_path):
    help_cmd = [tt_cmd, "cluster", "worker", "delete", "--help"]
    instance_process = subprocess.Popen(
        help_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    help_output = instance_process.stdout.read()

    assert "Delete a worker configuration" in help_output
    assert "http(s)://[username:password@]host:port/prefix/host-name/worker-name" in help_output
    assert "* prefix - a base path to the worker configuration." in help_output
    assert "* host-name - a name of the host." in help_output
    assert "* worker-name - a name of the worker." in help_output
    assert "TT_CLI_USERNAME" in help_output
    assert "TT_CLI_PASSWORD" in help_output
    assert "TT_CLI_ETCD_USERNAME" in help_output
    assert "TT_CLI_ETCD_PASSWORD" in help_output
    assert "environment variables < command flags < URL credentials" in help_output
    assert "--force" in help_output
    assert "-u" in help_output or "--username" in help_output
    assert "-p" in help_output or "--password" in help_output


def test_cluster_worker_publish_unimplemented(tt_cmd, tmp_path):
    worker_cfg = tmp_path / "worker.yaml"
    worker_cfg.write_text("type: nontarantool\n")

    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "https://localhost:2379/prefix/host/worker",
        str(worker_cfg),
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "unimplemented" in output


def test_cluster_worker_show_unimplemented(tt_cmd, tmp_path):
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "https://localhost:2379/prefix/host/worker",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "unimplemented" in output


def test_cluster_worker_delete_unimplemented(tt_cmd, tmp_path):
    delete_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "delete",
        "https://localhost:2379/prefix/host/worker",
    ]
    instance_process = subprocess.Popen(
        delete_cmd,
        cwd=tmp_path,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "unimplemented" in output
