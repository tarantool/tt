from urllib.parse import parse_qs, urlencode, urlsplit, urlunsplit

import pytest
import yaml

from utils import run_command_and_get_output

pytestmark = pytest.mark.tls


def test_cluster_publish_and_show_tls(tt_cmd, tls_cluster):
    config = {"groups": {"g": {"replicasets": {"rs": {"instances": {"i": {}}}}}}}
    source = tls_cluster.directory / "published.yaml"
    source.write_text(yaml.safe_dump(config))
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "publish", tls_cluster.storage_url, str(source), "--force"],
        cwd=tls_cluster.directory,
    )
    assert rc == 0, out
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", tls_cluster.storage_url],
        cwd=tls_cluster.directory,
    )
    assert rc == 0, out
    assert yaml.safe_load(out) == config


def test_cluster_tls_rejects_wrong_ca(tt_cmd, tls_cluster):
    url = urlsplit(tls_cluster.storage_url)
    params = parse_qs(url.query)
    params["ssl_ca_file"] = [str(tls_cluster.directory / "wrong-ca.crt")]
    target = urlunsplit(url._replace(query=urlencode(params, doseq=True)))
    rc, out = run_command_and_get_output(
        [tt_cmd, "cluster", "show", target],
        cwd=tls_cluster.directory,
    )
    assert rc != 0, out
    assert "certificate" in out.lower(), out
