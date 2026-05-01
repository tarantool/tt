import os
import subprocess

import pytest

worker_cfg = """type: nontarantool
instrumentation:
  url: host1:8080
  metrics_url: /metrics
  metrics_format: prometheus
config:
  addr: host1:9080
"""

worker_cfg_updated = """type: nontarantool
instrumentation:
  url: host1:8080
  metrics_url: /metrics
  metrics_format: prometheus
config:
  addr: host1:9081
"""


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


def test_cluster_worker_publish_missing_file(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "http://localhost:2379/prefix/host1/worker1",
        "nonexistent.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "failed to read file" in output


def test_cluster_worker_publish_invalid_url(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "not-a-valid-url",
        "worker.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "invalid URL" in output


def test_cluster_worker_publish_invalid_path(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "http://localhost:2379/onlyhost?timeout=0.1",
        "worker.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert output == (
        "   ⨯ failed to parse URL path:"
        " URL path must contain at least a host-name and a worker-name,"
        ' got: "/onlyhost"\n'
    )


def test_cluster_worker_publish_connection_failed(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "https://localhost:12344/prefix/host1/worker1?timeout=0.1",
        "worker.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "failed to connect to storage: failed to connect to etcd or tarantool" in output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5",
        "worker.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    content = ""
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        content, _ = conn.get(storage_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", storage_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert worker_cfg == content


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_nested_prefix(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    publish_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "publish",
        "http://"
        + creds
        + f"{instance.host}:{instance.port}/tdb-workers/cluster1/host1/worker1?timeout=5",
        "worker.yaml",
    ]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert "" == publish_output

    content = ""
    storage_key = "/tdb-workers/cluster1/instances/host1/worker1"
    if instance_name == "etcd":
        content, _ = conn.get(storage_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", storage_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert worker_cfg == content


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_exists_no_force(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg_updated)

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()

    assert publish_output == (
        "   ⨯ failed to publish worker configuration:"
        " worker configuration already exists at"
        ' "/prefix/instances/host1/worker1",'
        " use --force to overwrite\n"
    )

    content = ""
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        content, _ = conn.get(storage_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", storage_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert worker_cfg == content


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_force_overwrite(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg_updated)

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", "--force", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    content = ""
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        content, _ = conn.get(storage_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", storage_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert worker_cfg_updated == content


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_force_new_key(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    conn = instance.conn()
    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", "--force", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    content = ""
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        content, _ = conn.get(storage_key)
        content = content.decode("utf-8")
    else:
        content = conn.call("config.storage.get", storage_key)
        if len(content) > 0:
            content = content[0]["data"][0]["value"]

    assert worker_cfg == content


@pytest.mark.parametrize(
    "auth, instance_name",
    [
        pytest.param("url", "etcd"),
        pytest.param("flag", "etcd"),
        pytest.param("env", "etcd"),
        pytest.param("url", "tcs"),
        pytest.param("flag", "tcs"),
        pytest.param("env", "tcs"),
    ],
)
def test_cluster_worker_publish_auth(tt_cmd, tmpdir_with_cfg, auth, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        if auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"
            )
            publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
            publish_cmd = [
                tt_cmd,
                "cluster",
                "worker",
                "publish",
                url,
                "worker.yaml",
                "-u",
                instance.connection_username,
                "-p",
                instance.connection_password,
            ]
        elif auth == "env":
            env = {
                (
                    "TT_CLI_ETCD_USERNAME" if instance_name == "etcd" else "TT_CLI_USERNAME"
                ): instance.connection_username,
                (
                    "TT_CLI_ETCD_PASSWORD" if instance_name == "etcd" else "TT_CLI_PASSWORD"
                ): instance.connection_password,
            }
            url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
            publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]

        instance_process = subprocess.Popen(
            publish_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()

        assert "" == publish_output

        if instance_name == "etcd":
            instance.disable_auth()

        conn = instance.conn()
        content = ""
        storage_key = "/prefix/instances/host1/worker1"
        if instance_name == "etcd":
            content, _ = conn.get(storage_key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", storage_key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]

        assert worker_cfg == content
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_auth_priority_url_over_flag(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        url = (
            f"http://{instance.connection_username}:{instance.connection_password}@"
            f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"
        )
        publish_cmd = [
            tt_cmd,
            "cluster",
            "worker",
            "publish",
            url,
            "worker.yaml",
            "-u",
            "invalid_user",
            "-p",
            "invalid_pass",
        ]
        instance_process = subprocess.Popen(
            publish_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()

        assert "" == publish_output

        if instance_name == "etcd":
            instance.disable_auth()

        conn = instance.conn()
        content = ""
        storage_key = "/prefix/instances/host1/worker1"
        if instance_name == "etcd":
            content, _ = conn.get(storage_key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", storage_key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]

        assert worker_cfg == content
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_auth_priority_flag_over_env(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        env = {
            "TT_CLI_ETCD_USERNAME"
            if instance_name == "etcd"
            else "TT_CLI_USERNAME": "invalid_env_user",
            "TT_CLI_ETCD_PASSWORD"
            if instance_name == "etcd"
            else "TT_CLI_PASSWORD": "invalid_env_pass",
        }
        url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
        publish_cmd = [
            tt_cmd,
            "cluster",
            "worker",
            "publish",
            url,
            "worker.yaml",
            "-u",
            instance.connection_username,
            "-p",
            instance.connection_password,
        ]
        instance_process = subprocess.Popen(
            publish_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()

        assert "" == publish_output

        if instance_name == "etcd":
            instance.disable_auth()

        conn = instance.conn()
        content = ""
        storage_key = "/prefix/instances/host1/worker1"
        if instance_name == "etcd":
            content, _ = conn.get(storage_key)
            content = content.decode("utf-8")
        else:
            content = conn.call("config.storage.get", storage_key)
            if len(content) > 0:
                content = content[0]["data"][0]["value"]

        assert worker_cfg == content
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_auth_bad_credentials(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        url = f"http://invalid_user:invalid_pass@{instance.host}:{instance.port}/prefix/host1/worker1?timeout=1"
        publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
        instance_process = subprocess.Popen(
            publish_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        publish_output = instance_process.stdout.read()

        assert (
            "failed to connect to storage: failed to connect to etcd or tarantool" in publish_output
        )
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


def test_cluster_worker_show_invalid_url(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "not-a-valid-url",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "invalid URL" in output


def test_cluster_worker_show_invalid_path(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "http://localhost:2379/onlyhost?timeout=0.1",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert output == (
        "   ⨯ failed to parse URL path:"
        " URL path must contain at least a host-name and a worker-name,"
        ' got: "/onlyhost"\n'
    )


def test_cluster_worker_show_connection_failed(tt_cmd, tmpdir_with_cfg):
    tmpdir = tmpdir_with_cfg
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "https://localhost:12344/prefix/host1/worker1?timeout=0.1",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    output = instance_process.stdout.read()

    assert "failed to connect to storage: failed to connect to etcd or tarantool" in output


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert show_output.strip() == worker_cfg.strip()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show_nested_prefix(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/tdb-workers/cluster1/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "http://"
        + creds
        + f"{instance.host}:{instance.port}/tdb-workers/cluster1/host1/worker1?timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert show_output.strip() == worker_cfg.strip()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show_not_found(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    show_cmd = [
        tt_cmd,
        "cluster",
        "worker",
        "show",
        "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5",
    ]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert "worker configuration not found" in show_output


@pytest.mark.parametrize(
    "auth, instance_name",
    [
        pytest.param("url", "etcd"),
        pytest.param("flag", "etcd"),
        pytest.param("env", "etcd"),
        pytest.param("url", "tcs"),
        pytest.param("flag", "tcs"),
        pytest.param("env", "tcs"),
    ],
)
def test_cluster_worker_show_auth(tt_cmd, tmpdir_with_cfg, auth, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        if auth == "url":
            env = None
            url = (
                f"http://{instance.connection_username}:{instance.connection_password}@"
                f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"
            )
            show_cmd = [tt_cmd, "cluster", "worker", "show", url]
        elif auth == "flag":
            env = None
            url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
            show_cmd = [
                tt_cmd,
                "cluster",
                "worker",
                "show",
                url,
                "-u",
                instance.connection_username,
                "-p",
                instance.connection_password,
            ]
        elif auth == "env":
            env = {
                (
                    "TT_CLI_ETCD_USERNAME" if instance_name == "etcd" else "TT_CLI_USERNAME"
                ): instance.connection_username,
                (
                    "TT_CLI_ETCD_PASSWORD" if instance_name == "etcd" else "TT_CLI_PASSWORD"
                ): instance.connection_password,
            }
            url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
            show_cmd = [tt_cmd, "cluster", "worker", "show", url]

        instance_process = subprocess.Popen(
            show_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()

        assert show_output.strip() == worker_cfg.strip()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show_auth_priority_url_over_flag(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        url = (
            f"http://{instance.connection_username}:{instance.connection_password}@"
            f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"
        )
        show_cmd = [
            tt_cmd,
            "cluster",
            "worker",
            "show",
            url,
            "-u",
            "invalid_user",
            "-p",
            "invalid_pass",
        ]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()

        assert show_output.strip() == worker_cfg.strip()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show_auth_priority_flag_over_env(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        env = {
            "TT_CLI_ETCD_USERNAME"
            if instance_name == "etcd"
            else "TT_CLI_USERNAME": "invalid_env_user",
            "TT_CLI_ETCD_PASSWORD"
            if instance_name == "etcd"
            else "TT_CLI_PASSWORD": "invalid_env_pass",
        }
        url = f"{instance.endpoint}/prefix/host1/worker1?timeout=5"
        show_cmd = [
            tt_cmd,
            "cluster",
            "worker",
            "show",
            url,
            "-u",
            instance.connection_username,
            "-p",
            instance.connection_password,
        ]
        instance_process = subprocess.Popen(
            show_cmd,
            env=env,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()

        assert show_output.strip() == worker_cfg.strip()
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_show_auth_bad_credentials(
    tt_cmd,
    tmpdir_with_cfg,
    instance_name,
    request,
):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg

    conn = instance.conn()
    storage_key = "/prefix/instances/host1/worker1"
    if instance_name == "etcd":
        conn.put(storage_key, worker_cfg)
    else:
        conn.call("config.storage.put", storage_key, worker_cfg)

    if instance_name == "etcd":
        instance.enable_auth()

    try:
        url = f"http://invalid_user:invalid_pass@{instance.host}:{instance.port}/prefix/host1/worker1?timeout=1"
        show_cmd = [tt_cmd, "cluster", "worker", "show", url]
        instance_process = subprocess.Popen(
            show_cmd,
            cwd=tmpdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        show_output = instance_process.stdout.read()

        assert "failed to connect to storage: failed to connect to etcd or tarantool" in show_output
    finally:
        if instance_name == "etcd":
            instance.disable_auth()


@pytest.mark.parametrize("instance_name", ["etcd", "tcs"])
def test_cluster_worker_publish_then_show(tt_cmd, tmpdir_with_cfg, instance_name, request):
    instance = request.getfixturevalue(instance_name)
    tmpdir = tmpdir_with_cfg
    worker_cfg_path = os.path.join(tmpdir, "worker.yaml")
    with open(worker_cfg_path, "w") as f:
        f.write(worker_cfg)

    creds = (
        f"{instance.connection_username}:{instance.connection_password}@"
        if instance_name == "tcs"
        else ""
    )
    url = "http://" + creds + f"{instance.host}:{instance.port}/prefix/host1/worker1?timeout=5"

    publish_cmd = [tt_cmd, "cluster", "worker", "publish", url, "worker.yaml"]
    instance_process = subprocess.Popen(
        publish_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    publish_output = instance_process.stdout.read()
    assert "" == publish_output

    show_cmd = [tt_cmd, "cluster", "worker", "show", url]
    instance_process = subprocess.Popen(
        show_cmd,
        cwd=tmpdir,
        stderr=subprocess.STDOUT,
        stdout=subprocess.PIPE,
        text=True,
    )
    show_output = instance_process.stdout.read()

    assert show_output.strip() == worker_cfg.strip()
